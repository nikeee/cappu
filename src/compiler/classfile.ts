// A complete class-file reader (JVMS chapter 4): every constant-pool tag is
// retained and attribute bodies are kept as raw bytes, decoded on demand. This
// is the input side of `cappu decompile` (nikeee/cappu#43).
//
// Distinct from classfileReader.ts, which parses the same format down to the
// bare minimum a resolution stub needs (Utf8 + Class entries, every attribute
// body skipped) and is load-bearing for the LSP classpath.

export class ClassFileError extends Error {}

/**
 * Modified UTF-8 (JVMS 4.4.7): like UTF-8, except U+0000 is encoded as the
 * two bytes c0 80 and supplementary characters appear as a surrogate pair of
 * three-byte sequences. Decoded to UTF-16 code units, so both fall out.
 */
function decodeModifiedUtf8(bytes: Uint8Array): string {
  let out = "";
  for (let i = 0; i < bytes.length;) {
    const b = bytes[i]!;
    if (b < 0x80) {
      out += String.fromCharCode(b);
      i += 1;
    } else if ((b & 0xe0) === 0xc0) {
      out += String.fromCharCode(((b & 0x1f) << 6) | (bytes[i + 1]! & 0x3f));
      i += 2;
    } else {
      out += String.fromCharCode(
        ((b & 0x0f) << 12) | ((bytes[i + 1]! & 0x3f) << 6) | (bytes[i + 2]! & 0x3f),
      );
      i += 3;
    }
  }
  return out;
}

export type Constant =
  | { tag: "utf8"; value: string }
  | { tag: "int"; value: number }
  | { tag: "float"; value: number }
  | { tag: "long"; value: bigint }
  | { tag: "double"; value: number }
  | { tag: "class"; nameIndex: number }
  | { tag: "string"; valueIndex: number }
  | {
      tag: "fieldref" | "methodref" | "interfaceMethodref";
      classIndex: number;
      nameAndTypeIndex: number;
    }
  | { tag: "nameAndType"; nameIndex: number; descriptorIndex: number }
  | { tag: "methodHandle"; referenceKind: number; referenceIndex: number }
  | { tag: "methodType"; descriptorIndex: number }
  | { tag: "dynamic" | "invokeDynamic"; bootstrapMethodIndex: number; nameAndTypeIndex: number }
  | { tag: "module" | "package"; nameIndex: number };

export interface Attribute {
  readonly name: string;
  readonly bytes: Uint8Array;
}

export interface Member {
  readonly flags: number;
  readonly name: string;
  readonly descriptor: string;
  readonly attributes: Attribute[];
}

export interface ClassFile {
  readonly minor: number;
  readonly major: number;
  /** 1-based; index 0 and the second slot of long/double entries are undefined. */
  readonly pool: readonly (Constant | undefined)[];
  readonly flags: number;
  readonly thisClass: string;
  readonly superClass: string | undefined;
  readonly interfaces: string[];
  readonly fields: Member[];
  readonly methods: Member[];
  readonly attributes: Attribute[];
}

export interface ExceptionEntry {
  readonly startPc: number;
  readonly endPc: number;
  readonly handlerPc: number;
  /** The caught class' internal name, or undefined for a catch-all ("any"). */
  readonly catchType: string | undefined;
}

export interface Code {
  readonly maxStack: number;
  readonly maxLocals: number;
  readonly code: Uint8Array;
  readonly exceptions: ExceptionEntry[];
  readonly attributes: Attribute[];
}

/** A cursor over big-endian class-file bytes. */
class Cursor {
  at = 0;
  readonly view: DataView;
  constructor(readonly bytes: Uint8Array) {
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }
  u1(): number {
    this.require(1);
    return this.view.getUint8(this.at++);
  }
  u2(): number {
    this.require(2);
    const v = this.view.getUint16(this.at);
    this.at += 2;
    return v;
  }
  u4(): number {
    this.require(4);
    const v = this.view.getUint32(this.at);
    this.at += 4;
    return v;
  }
  i4(): number {
    this.require(4);
    const v = this.view.getInt32(this.at);
    this.at += 4;
    return v;
  }
  f4(): number {
    this.require(4);
    const v = this.view.getFloat32(this.at);
    this.at += 4;
    return v;
  }
  i8(): bigint {
    this.require(8);
    const v = this.view.getBigInt64(this.at);
    this.at += 8;
    return v;
  }
  f8(): number {
    this.require(8);
    const v = this.view.getFloat64(this.at);
    this.at += 8;
    return v;
  }
  slice(length: number): Uint8Array {
    this.require(length);
    const out = this.bytes.subarray(this.at, this.at + length);
    this.at += length;
    return out;
  }
  private require(n: number): void {
    if (this.at + n > this.bytes.length) throw new ClassFileError("truncated class file");
  }
}

function readConstantPool(c: Cursor): (Constant | undefined)[] {
  const count = c.u2();
  const pool: (Constant | undefined)[] = new Array(count);
  for (let i = 1; i < count; i++) {
    const tag = c.u1();
    switch (tag) {
      case 1:
        pool[i] = { tag: "utf8", value: decodeModifiedUtf8(c.slice(c.u2())) };
        break;
      case 3:
        pool[i] = { tag: "int", value: c.i4() };
        break;
      case 4:
        pool[i] = { tag: "float", value: c.f4() };
        break;
      case 5:
        pool[i] = { tag: "long", value: c.i8() };
        i++; // longs and doubles take two pool slots
        break;
      case 6:
        pool[i] = { tag: "double", value: c.f8() };
        i++;
        break;
      case 7:
        pool[i] = { tag: "class", nameIndex: c.u2() };
        break;
      case 8:
        pool[i] = { tag: "string", valueIndex: c.u2() };
        break;
      case 9:
      case 10:
      case 11:
        pool[i] = {
          tag: tag === 9 ? "fieldref" : tag === 10 ? "methodref" : "interfaceMethodref",
          classIndex: c.u2(),
          nameAndTypeIndex: c.u2(),
        };
        break;
      case 12:
        pool[i] = { tag: "nameAndType", nameIndex: c.u2(), descriptorIndex: c.u2() };
        break;
      case 15:
        pool[i] = { tag: "methodHandle", referenceKind: c.u1(), referenceIndex: c.u2() };
        break;
      case 16:
        pool[i] = { tag: "methodType", descriptorIndex: c.u2() };
        break;
      case 17:
      case 18:
        pool[i] = {
          tag: tag === 17 ? "dynamic" : "invokeDynamic",
          bootstrapMethodIndex: c.u2(),
          nameAndTypeIndex: c.u2(),
        };
        break;
      case 19:
      case 20:
        pool[i] = { tag: tag === 19 ? "module" : "package", nameIndex: c.u2() };
        break;
      default:
        throw new ClassFileError(`unknown constant pool tag ${tag}`);
    }
  }
  return pool;
}

function readAttributes(c: Cursor, pool: readonly (Constant | undefined)[]): Attribute[] {
  const count = c.u2();
  const attributes: Attribute[] = [];
  for (let i = 0; i < count; i++) {
    const nameIndex = c.u2();
    const length = c.u4();
    const entry = pool[nameIndex];
    attributes.push({
      name: entry?.tag === "utf8" ? entry.value : "",
      bytes: c.slice(length),
    });
  }
  return attributes;
}

function readMembers(c: Cursor, pool: readonly (Constant | undefined)[]): Member[] {
  const count = c.u2();
  const members: Member[] = [];
  for (let i = 0; i < count; i++) {
    const flags = c.u2();
    const name = utf8(pool, c.u2()) ?? "";
    const descriptor = utf8(pool, c.u2()) ?? "";
    members.push({ flags, name, descriptor, attributes: readAttributes(c, pool) });
  }
  return members;
}

export function readClassFile(bytes: Uint8Array): ClassFile {
  const c = new Cursor(bytes);
  if (c.u4() !== 0xcafebabe) throw new ClassFileError("not a class file");
  const minor = c.u2();
  const major = c.u2();
  const pool = readConstantPool(c);
  const flags = c.u2();
  const thisClass = className(pool, c.u2());
  if (!thisClass) throw new ClassFileError("missing this_class");
  const superIndex = c.u2();
  const superClass = superIndex === 0 ? undefined : className(pool, superIndex);
  const interfaces: string[] = [];
  const interfaceCount = c.u2();
  for (let i = 0; i < interfaceCount; i++) {
    const name = className(pool, c.u2());
    if (name) interfaces.push(name);
  }
  const fields = readMembers(c, pool);
  const methods = readMembers(c, pool);
  const attributes = readAttributes(c, pool);
  return {
    minor,
    major,
    pool,
    flags,
    thisClass,
    superClass,
    interfaces,
    fields,
    methods,
    attributes,
  };
}

// --- constant pool accessors -----------------------------------------------------

export function utf8(pool: readonly (Constant | undefined)[], index: number): string | undefined {
  const entry = pool[index];
  return entry?.tag === "utf8" ? entry.value : undefined;
}

/** The internal name (`java/lang/String`) behind a CONSTANT_Class index. */
export function className(
  pool: readonly (Constant | undefined)[],
  index: number,
): string | undefined {
  const entry = pool[index];
  return entry?.tag === "class" ? utf8(pool, entry.nameIndex) : undefined;
}

export interface MemberRef {
  readonly owner: string;
  readonly name: string;
  readonly descriptor: string;
}

/** The owner/name/descriptor behind a Fieldref/Methodref/InterfaceMethodref index. */
export function memberRef(
  pool: readonly (Constant | undefined)[],
  index: number,
): MemberRef | undefined {
  const entry = pool[index];
  if (
    entry?.tag !== "fieldref" &&
    entry?.tag !== "methodref" &&
    entry?.tag !== "interfaceMethodref"
  ) {
    return undefined;
  }
  const nameAndType = nameAndTypeAt(pool, entry.nameAndTypeIndex);
  if (!nameAndType) return undefined;
  return { owner: className(pool, entry.classIndex) ?? "", ...nameAndType };
}

export function nameAndTypeAt(
  pool: readonly (Constant | undefined)[],
  index: number,
): { name: string; descriptor: string } | undefined {
  const entry = pool[index];
  if (entry?.tag !== "nameAndType") return undefined;
  return {
    name: utf8(pool, entry.nameIndex) ?? "",
    descriptor: utf8(pool, entry.descriptorIndex) ?? "",
  };
}

// --- attributes -------------------------------------------------------------------

export function findAttribute(
  attributes: readonly Attribute[],
  name: string,
): Attribute | undefined {
  return attributes.find(a => a.name === name);
}

/** The Code attribute (JVMS 4.7.3) of a method, when it has one. */
export function readCode(
  method: Member,
  pool: readonly (Constant | undefined)[],
): Code | undefined {
  const attribute = findAttribute(method.attributes, "Code");
  if (!attribute) return undefined;
  const c = new Cursor(attribute.bytes);
  const maxStack = c.u2();
  const maxLocals = c.u2();
  const code = c.slice(c.u4());
  const exceptions: ExceptionEntry[] = [];
  const exceptionCount = c.u2();
  for (let i = 0; i < exceptionCount; i++) {
    const startPc = c.u2();
    const endPc = c.u2();
    const handlerPc = c.u2();
    const catchIndex = c.u2();
    exceptions.push({
      startPc,
      endPc,
      handlerPc,
      catchType: catchIndex === 0 ? undefined : className(pool, catchIndex),
    });
  }
  return { maxStack, maxLocals, code, exceptions, attributes: readAttributes(c, pool) };
}

/** The `throws` clause (JVMS 4.7.5) as internal names. */
export function readExceptions(member: Member, pool: readonly (Constant | undefined)[]): string[] {
  const attribute = findAttribute(member.attributes, "Exceptions");
  if (!attribute) return [];
  const c = new Cursor(attribute.bytes);
  const count = c.u2();
  const names: string[] = [];
  for (let i = 0; i < count; i++) {
    const name = className(pool, c.u2());
    if (name) names.push(name);
  }
  return names;
}

export interface BootstrapMethod {
  /** The CONSTANT_MethodHandle index of the bootstrap method. */
  readonly handleIndex: number;
  readonly argumentIndexes: number[];
}

/** BootstrapMethods (JVMS 4.7.23), the target table of every invokedynamic. */
export function readBootstrapMethods(classFile: ClassFile): BootstrapMethod[] {
  const attribute = findAttribute(classFile.attributes, "BootstrapMethods");
  if (!attribute) return [];
  const c = new Cursor(attribute.bytes);
  const count = c.u2();
  const methods: BootstrapMethod[] = [];
  for (let i = 0; i < count; i++) {
    const handleIndex = c.u2();
    const argumentCount = c.u2();
    const argumentIndexes: number[] = [];
    for (let a = 0; a < argumentCount; a++) argumentIndexes.push(c.u2());
    methods.push({ handleIndex, argumentIndexes });
  }
  return methods;
}

/** The source file name from the SourceFile attribute (JVMS 4.7.10). */
export function sourceFileName(classFile: ClassFile): string | undefined {
  const attribute = findAttribute(classFile.attributes, "SourceFile");
  if (!attribute || attribute.bytes.length < 2) return undefined;
  return utf8(classFile.pool, new Cursor(attribute.bytes).u2());
}

/** The generic signature (JVMS 4.7.9) of a class or member, when it has one. */
export function signatureOf(
  attributes: readonly Attribute[],
  pool: readonly (Constant | undefined)[],
): string | undefined {
  const attribute = findAttribute(attributes, "Signature");
  if (!attribute || attribute.bytes.length < 2) return undefined;
  return utf8(pool, new Cursor(attribute.bytes).u2());
}
