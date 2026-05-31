// Bytecode emitter: Java source -> JVM .class files. The structure follows the
// JVM Specification (JVMS SE 21) chapter 4 (the class file format) and chapter 6
// (instruction set). This is the first milestone: it produces a valid, verifiable
// class file for a top-level class, with the synthesized default constructor
// (aload_0; invokespecial Object.<init>; return). Real member/body code generation
// comes in later milestones. We target major version 65 (Java 21) so the output
// loads on the local JVM.
//
// Reference output is cross-checked against `javac` in the tests.

import type { Checker } from "./checker.ts";
import { type Type, TypeKind } from "./checkerTypes.ts";
import { forEachChild } from "./parser.ts";
import type { Program } from "./program.ts";
import { resolveTypeEntityName } from "./resolver.ts";
import { entityNameToString, tokenToString } from "./utilities.ts";
import {
  type ArrayType as AstArrayType,
  type AssignmentExpression,
  type BinaryExpression,
  type CallExpression,
  type ClassDeclaration,
  type ExpressionStatement,
  type LocalVariableDeclarationStatement,
  type FieldDeclaration,
  type Identifier,
  type LiteralExpression,
  type MethodDeclaration,
  type Node,
  type Parameter,
  type PrefixUnaryExpression,
  type PropertyAccessExpression,
  type ReturnStatement,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeNode,
  type TypeReference,
  type VariableDeclarator,
} from "./types.ts";

// Thrown when a construct is not yet handled by code generation; the caller
// falls back to a verifiable placeholder body so output is always valid.
class UnsupportedEmit extends Error {}

const MAGIC = 0xcafebabe;
const MINOR_VERSION = 0;
const MAJOR_VERSION = 65; // Java 21

// Access flags (JVMS 4.1 / 4.5 / 4.6).
const ACC_PUBLIC = 0x0001;
const ACC_PRIVATE = 0x0002;
const ACC_PROTECTED = 0x0004;
const ACC_STATIC = 0x0008;
const ACC_FINAL = 0x0010;
const ACC_SUPER = 0x0020;
const ACC_VOLATILE = 0x0040;
const ACC_TRANSIENT = 0x0080;
const ACC_SYNCHRONIZED = 0x0020;
const ACC_NATIVE = 0x0100;
const ACC_ABSTRACT = 0x0400;
const ACC_STRICT = 0x0800;
const ACC_VARARGS = 0x0080;

// Primitive field descriptors (JVMS 4.3.2).
const PRIMITIVE_DESCRIPTOR: Record<string, string> = {
  byte: "B",
  char: "C",
  double: "D",
  float: "F",
  int: "I",
  long: "J",
  short: "S",
  boolean: "Z",
  void: "V",
};

// Constant pool tags (JVMS 4.4, Table 4.4-A).
const CONSTANT_Utf8 = 1;
const CONSTANT_Integer = 3;
const CONSTANT_Long = 5;
const CONSTANT_Class = 7;
const CONSTANT_String = 8;
const CONSTANT_Fieldref = 9;
const CONSTANT_Methodref = 10;
const CONSTANT_InterfaceMethodref = 11;
const CONSTANT_NameAndType = 12;

// Opcodes (JVMS 6.5) used so far.
const OP_ACONST_NULL = 0x01;
const OP_ICONST_0 = 0x03;
const OP_LCONST_0 = 0x09;
const OP_FCONST_0 = 0x0b;
const OP_DCONST_0 = 0x0e;
const OP_IRETURN = 0xac;
const OP_LRETURN = 0xad;
const OP_FRETURN = 0xae;
const OP_DRETURN = 0xaf;
const OP_ARETURN = 0xb0;
const OP_ICONST_M1 = 0x02;
const OP_BIPUSH = 0x10;
const OP_SIPUSH = 0x11;
const OP_LDC = 0x12;
const OP_LDC_W = 0x13;
const OP_LDC2_W = 0x14;
const OP_ILOAD = 0x15;
const OP_LLOAD = 0x16;
const OP_FLOAD = 0x17;
const OP_DLOAD = 0x18;
const OP_ALOAD = 0x19;
const OP_ILOAD_0 = 0x1a;
const OP_LLOAD_0 = 0x1e;
const OP_FLOAD_0 = 0x22;
const OP_DLOAD_0 = 0x26;
const OP_ALOAD_BASE_0 = 0x2a; // aload_0
const OP_ISTORE = 0x36;
const OP_LSTORE = 0x37;
const OP_FSTORE = 0x38;
const OP_DSTORE = 0x39;
const OP_ASTORE = 0x3a;
const OP_ISTORE_0 = 0x3b;
const OP_LSTORE_0 = 0x3f;
const OP_FSTORE_0 = 0x43;
const OP_DSTORE_0 = 0x47;
const OP_ASTORE_0 = 0x4b;
const OP_IADD = 0x60; // arithmetic bases; + type offset (I=0,J=1,F=2,D=3)
const OP_ISUB = 0x64;
const OP_IMUL = 0x68;
const OP_IDIV = 0x6c;
const OP_IREM = 0x70;
const OP_INEG = 0x74; // negate base; + type offset
const OP_ISHL = 0x78; // shift bases; + (long ? 1 : 0)
const OP_ISHR = 0x7a;
const OP_IUSHR = 0x7c;
const OP_IAND = 0x7e;
const OP_IOR = 0x80;
const OP_IXOR = 0x82;
const OP_LXOR = 0x83;
const OP_I2L = 0x85;
const OP_I2F = 0x86;
const OP_I2D = 0x87;
const OP_L2F = 0x89;
const OP_L2D = 0x8a;
const OP_F2D = 0x8d;
const OP_POP = 0x57;
const OP_POP2 = 0x58;
const OP_GETSTATIC = 0xb2;
const OP_GETFIELD = 0xb4;
const OP_INVOKEVIRTUAL = 0xb6;
const OP_INVOKESPECIAL = 0xb7;
const OP_INVOKESTATIC = 0xb8;
const OP_INVOKEINTERFACE = 0xb9;
const OP_ALOAD_0 = 0x2a;
const OP_RETURN = 0xb1;

// A growable big-endian byte buffer.
class ByteBuffer {
  private bytes: number[] = [];
  u1(v: number): void {
    this.bytes.push(v & 0xff);
  }
  u2(v: number): void {
    this.bytes.push((v >>> 8) & 0xff, v & 0xff);
  }
  u4(v: number): void {
    this.bytes.push((v >>> 24) & 0xff, (v >>> 16) & 0xff, (v >>> 8) & 0xff, v & 0xff);
  }
  // Modified UTF-8 (JVMS 4.4.7). ASCII and the BMP are handled; supplementary
  // characters would need surrogate-pair encoding (added when needed).
  utf8(s: string): void {
    for (let i = 0; i < s.length; i++) {
      const c = s.charCodeAt(i);
      if (c >= 0x01 && c <= 0x7f) {
        this.bytes.push(c);
      } else if (c <= 0x7ff) {
        this.bytes.push(0xc0 | (c >> 6), 0x80 | (c & 0x3f));
      } else {
        this.bytes.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
      }
    }
  }
  utf8Length(s: string): number {
    let n = 0;
    for (let i = 0; i < s.length; i++) {
      const c = s.charCodeAt(i);
      n += c >= 0x01 && c <= 0x7f ? 1 : c <= 0x7ff ? 2 : 3;
    }
    return n;
  }
  append(other: ByteBuffer): void {
    for (const b of other.bytes) this.bytes.push(b);
  }
  toUint8Array(): Uint8Array {
    return Uint8Array.from(this.bytes);
  }
  get length(): number {
    return this.bytes.length;
  }
}

// Builds the constant pool, interning entries so each appears once. Indices are
// 1-based (JVMS 4.1: the pool is indexed 1..count-1).
class ConstantPool {
  private entries = new ByteBuffer();
  private count = 0; // number of entries (next index is count + 1)
  private cache = new Map<string, number>();

  private intern(key: string, write: (b: ByteBuffer) => void): number {
    const existing = this.cache.get(key);
    if (existing !== undefined) return existing;
    write(this.entries);
    const index = ++this.count;
    this.cache.set(key, index);
    return index;
  }

  utf8(value: string): number {
    return this.intern(`u:${value}`, b => {
      b.u1(CONSTANT_Utf8);
      b.u2(b.utf8Length(value));
      b.utf8(value);
    });
  }

  classInfo(internalName: string): number {
    const nameIndex = this.utf8(internalName);
    return this.intern(`c:${internalName}`, b => {
      b.u1(CONSTANT_Class);
      b.u2(nameIndex);
    });
  }

  nameAndType(name: string, descriptor: string): number {
    const nameIndex = this.utf8(name);
    const descIndex = this.utf8(descriptor);
    return this.intern(`nt:${name}:${descriptor}`, b => {
      b.u1(CONSTANT_NameAndType);
      b.u2(nameIndex);
      b.u2(descIndex);
    });
  }

  methodref(internalClass: string, name: string, descriptor: string): number {
    const classIndex = this.classInfo(internalClass);
    const ntIndex = this.nameAndType(name, descriptor);
    return this.intern(`m:${internalClass}:${name}:${descriptor}`, b => {
      b.u1(CONSTANT_Methodref);
      b.u2(classIndex);
      b.u2(ntIndex);
    });
  }

  interfaceMethodref(internalClass: string, name: string, descriptor: string): number {
    const classIndex = this.classInfo(internalClass);
    const ntIndex = this.nameAndType(name, descriptor);
    return this.intern(`im:${internalClass}:${name}:${descriptor}`, b => {
      b.u1(CONSTANT_InterfaceMethodref);
      b.u2(classIndex);
      b.u2(ntIndex);
    });
  }

  fieldref(internalClass: string, name: string, descriptor: string): number {
    const classIndex = this.classInfo(internalClass);
    const ntIndex = this.nameAndType(name, descriptor);
    return this.intern(`f:${internalClass}:${name}:${descriptor}`, b => {
      b.u1(CONSTANT_Fieldref);
      b.u2(classIndex);
      b.u2(ntIndex);
    });
  }

  string(value: string): number {
    const utf8Index = this.utf8(value);
    return this.intern(`s:${value}`, b => {
      b.u1(CONSTANT_String);
      b.u2(utf8Index);
    });
  }

  integer(value: number): number {
    return this.intern(`i:${value}`, b => {
      b.u1(CONSTANT_Integer);
      b.u4(value);
    });
  }

  long(value: bigint): number {
    // Long occupies two pool entries (JVMS 4.4.5).
    const index = this.intern(`l:${value}`, b => {
      b.u1(CONSTANT_Long);
      b.u4(Number((value >> 32n) & 0xffffffffn));
      b.u4(Number(value & 0xffffffffn));
    });
    this.count++; // the second (unusable) slot
    return index;
  }

  /** Write `constant_pool_count` followed by the pool entries. */
  writeInto(out: ByteBuffer): void {
    out.u2(this.count + 1);
    out.append(this.entries);
  }
}

function classAccessFlags(declaration: ClassDeclaration): number {
  let flags = ACC_SUPER;
  for (const modifier of declaration.modifiers ?? []) {
    if (modifier.kind === SyntaxKind.PublicKeyword) flags |= ACC_PUBLIC;
    else if (modifier.kind === SyntaxKind.FinalKeyword) flags |= ACC_FINAL;
    else if (modifier.kind === SyntaxKind.AbstractKeyword) flags |= ACC_ABSTRACT;
  }
  return flags;
}

function memberAccessFlags(modifiers: readonly Node[] | undefined): number {
  let flags = 0;
  for (const modifier of modifiers ?? []) {
    switch (modifier.kind) {
      case SyntaxKind.PublicKeyword:
        flags |= ACC_PUBLIC;
        break;
      case SyntaxKind.PrivateKeyword:
        flags |= ACC_PRIVATE;
        break;
      case SyntaxKind.ProtectedKeyword:
        flags |= ACC_PROTECTED;
        break;
      case SyntaxKind.StaticKeyword:
        flags |= ACC_STATIC;
        break;
      case SyntaxKind.FinalKeyword:
        flags |= ACC_FINAL;
        break;
      case SyntaxKind.VolatileKeyword:
        flags |= ACC_VOLATILE;
        break;
      case SyntaxKind.TransientKeyword:
        flags |= ACC_TRANSIENT;
        break;
      default:
        break;
    }
  }
  return flags;
}

// Internal (binary) name of a type symbol: package with '/' separators, nested
// types joined by '$'. e.g. java.lang.String -> "java/lang/String".
function binaryName(symbol: Symbol): string {
  const names = [symbol.escapedName];
  let parent = symbol.parent;
  while (parent && parent.flags & SymbolFlags.Type) {
    names.unshift(parent.escapedName);
    parent = parent.parent;
  }
  const pkg = parent && parent.flags & SymbolFlags.Package ? parent.escapedName : "";
  const nested = names.join("$");
  return pkg ? `${pkg.replace(/\./g, "/")}/${nested}` : nested;
}

// Field/return type descriptor (JVMS 4.3.2). Type references are resolved to a
// binary name; an unresolved name falls back to its written form (best effort).
function descriptorOf(typeNode: TypeNode, program: Program): string {
  switch (typeNode.kind) {
    case SyntaxKind.PrimitiveType: {
      const keyword = tokenToString((typeNode as { keyword: SyntaxKind }).keyword) ?? "int";
      return PRIMITIVE_DESCRIPTOR[keyword] ?? "I";
    }
    case SyntaxKind.ArrayType:
      return `[${descriptorOf((typeNode as AstArrayType).elementType, program)}`;
    case SyntaxKind.TypeReference: {
      const ref = typeNode as TypeReference;
      const symbol = resolveTypeEntityName(ref.typeName, typeNode, program);
      const name = symbol
        ? binaryName(symbol)
        : entityNameToString(ref.typeName).replace(/\./g, "/");
      return `L${name};`;
    }
    default:
      return "Ljava/lang/Object;";
  }
}

// One field_info per declarator (int a, b; emits two fields).
function emitFields(
  declaration: ClassDeclaration,
  cp: ConstantPool,
  program: Program,
): {
  buffer: ByteBuffer;
  count: number;
} {
  const buffer = new ByteBuffer();
  let count = 0;
  for (const member of declaration.members) {
    if (member.kind !== SyntaxKind.FieldDeclaration) continue;
    const field = member as FieldDeclaration;
    const descriptor = descriptorOf(field.type, program);
    const flags = memberAccessFlags(field.modifiers);
    for (const declarator of field.declarators) {
      const name = (declarator as VariableDeclarator).name.text;
      buffer.u2(flags);
      buffer.u2(cp.utf8(name));
      buffer.u2(cp.utf8(descriptor));
      buffer.u2(0); // attributes_count (ConstantValue comes later)
      count++;
    }
  }
  return { buffer, count };
}

// The default no-arg constructor: invokes the super constructor and returns.
// `accessFlags` mirrors the class's accessibility (JLS 8.8.9).
function emitDefaultConstructor(
  cp: ConstantPool,
  superInternalName: string,
  accessFlags: number,
): ByteBuffer {
  const superInit = cp.methodref(superInternalName, "<init>", "()V");

  const code = new ByteBuffer();
  code.u1(OP_ALOAD_0);
  code.u1(OP_INVOKESPECIAL);
  code.u2(superInit);
  code.u1(OP_RETURN);

  const codeAttr = new ByteBuffer();
  codeAttr.u2(cp.utf8("Code"));
  codeAttr.u4(12 + code.length); // max_stack(2)+max_locals(2)+code_length(4)+code+except(2)+attrs(2)
  codeAttr.u2(1); // max_stack
  codeAttr.u2(1); // max_locals (this)
  codeAttr.u4(code.length);
  codeAttr.append(code);
  codeAttr.u2(0); // exception_table_length
  codeAttr.u2(0); // attributes_count

  const method = new ByteBuffer();
  method.u2(accessFlags);
  method.u2(cp.utf8("<init>"));
  method.u2(cp.utf8("()V"));
  method.u2(1); // attributes_count
  method.append(codeAttr);
  return method;
}

function methodAccessFlags(method: MethodDeclaration): number {
  let flags = 0;
  for (const modifier of method.modifiers ?? []) {
    switch (modifier.kind) {
      case SyntaxKind.PublicKeyword:
        flags |= ACC_PUBLIC;
        break;
      case SyntaxKind.PrivateKeyword:
        flags |= ACC_PRIVATE;
        break;
      case SyntaxKind.ProtectedKeyword:
        flags |= ACC_PROTECTED;
        break;
      case SyntaxKind.StaticKeyword:
        flags |= ACC_STATIC;
        break;
      case SyntaxKind.FinalKeyword:
        flags |= ACC_FINAL;
        break;
      case SyntaxKind.AbstractKeyword:
        flags |= ACC_ABSTRACT;
        break;
      case SyntaxKind.SynchronizedKeyword:
        flags |= ACC_SYNCHRONIZED;
        break;
      case SyntaxKind.NativeKeyword:
        flags |= ACC_NATIVE;
        break;
      case SyntaxKind.StrictfpKeyword:
        flags |= ACC_STRICT;
        break;
      default:
        break;
    }
  }
  if (method.parameters.some(p => (p as Parameter).isVarArgs)) flags |= ACC_VARARGS;
  return flags;
}

function paramDescriptor(parameter: Parameter, program: Program): string {
  const base = descriptorOf(parameter.type, program);
  return parameter.isVarArgs ? `[${base}` : base; // T... is T[] at the bytecode level
}

function methodDescriptor(method: MethodDeclaration, program: Program): string {
  const params = method.parameters.map(p => paramDescriptor(p as Parameter, program)).join("");
  return `(${params})${descriptorOf(method.returnType, program)}`;
}

// One slot per value, two for long/double (JVMS 2.6.1).
function slotsOf(descriptor: string): number {
  return descriptor === "J" || descriptor === "D" ? 2 : 1;
}

// Placeholder body: return the default value for the return type. Real statement
// code generation replaces this in the next milestone; this keeps every emitted
// method verifiable in the meantime.
function defaultReturnBody(returnDescriptor: string): { code: ByteBuffer; maxStack: number } {
  const code = new ByteBuffer();
  switch (returnDescriptor[0]) {
    case "V":
      code.u1(OP_RETURN);
      return { code, maxStack: 0 };
    case "J":
      code.u1(OP_LCONST_0);
      code.u1(OP_LRETURN);
      return { code, maxStack: 2 };
    case "D":
      code.u1(OP_DCONST_0);
      code.u1(OP_DRETURN);
      return { code, maxStack: 2 };
    case "F":
      code.u1(OP_FCONST_0);
      code.u1(OP_FRETURN);
      return { code, maxStack: 1 };
    case "L":
    case "[":
      code.u1(OP_ACONST_NULL);
      code.u1(OP_ARETURN);
      return { code, maxStack: 1 };
    default: // B C S Z I
      code.u1(OP_ICONST_0);
      code.u1(OP_IRETURN);
      return { code, maxStack: 1 };
  }
}

// Split a method descriptor's parameter list into individual descriptors.
function parseParamDescriptors(methodDescriptor: string): string[] {
  const params: string[] = [];
  let i = methodDescriptor.indexOf("(") + 1;
  while (methodDescriptor[i] !== ")") {
    const start = i;
    while (methodDescriptor[i] === "[") i++;
    if (methodDescriptor[i] === "L") i = methodDescriptor.indexOf(";", i) + 1;
    else i++;
    params.push(methodDescriptor.slice(start, i));
  }
  return params;
}

function isStaticDeclaration(declaration: { modifiers?: readonly Node[] }): boolean {
  return (declaration.modifiers ?? []).some(m => m.kind === SyntaxKind.StaticKeyword);
}

// Generate real bytecode for a method body. Throws UnsupportedEmit for anything
// not yet handled, so emitMethod can fall back to a verifiable placeholder.
function generateBody(
  method: MethodDeclaration,
  cp: ConstantPool,
  program: Program,
  checker: Checker,
  thisInternalName: string,
): { code: ByteBuffer; maxStack: number; maxLocals: number } {
  const isStatic = (methodAccessFlags(method) & ACC_STATIC) !== 0;
  const returnDescriptor = methodDescriptor(method, program).slice(
    methodDescriptor(method, program).lastIndexOf(")") + 1,
  );

  // Slots for parameters and (as they are declared) locals; shared map keyed by
  // the declaration symbol.
  const locals = new Map<Symbol, { slot: number; descriptor: string }>();
  let nextSlot = isStatic ? 0 : 1;
  for (const p of method.parameters) {
    const descriptor = paramDescriptor(p as Parameter, program);
    if (p.symbol) locals.set(p.symbol, { slot: nextSlot, descriptor });
    nextSlot += slotsOf(descriptor);
  }
  let maxLocals = nextSlot;

  const code = new ByteBuffer();
  let depth = 0;
  let maxStack = 0;
  const grow = (slots: number): void => {
    depth += slots;
    if (depth > maxStack) maxStack = depth;
  };
  const push = (descriptor: string): void => grow(slotsOf(descriptor));
  const pushRef = (): void => grow(1);
  const pop = (slots: number): void => {
    depth -= slots;
  };

  // Numeric category of a descriptor: I (byte/char/short/boolean/int), J, F, D,
  // or A (reference). Used for promotion and conversion.
  const category = (descriptor: string): string => {
    const c = descriptor[0];
    return c === "J" || c === "D" || c === "F" ? c : c === "L" || c === "[" ? "A" : "I";
  };

  // Widening numeric conversion of the value on top of the stack (JLS 5.1.2),
  // used for promotion, assignment and arguments. No-op when categories match.
  const coerce = (from: string, to: string): void => {
    const a = category(from);
    const b = category(to);
    if (a === b) return;
    const op =
      a === "I" && b === "J"
        ? OP_I2L
        : a === "I" && b === "F"
          ? OP_I2F
          : a === "I" && b === "D"
            ? OP_I2D
            : a === "J" && b === "F"
              ? OP_L2F
              : a === "J" && b === "D"
                ? OP_L2D
                : a === "F" && b === "D"
                  ? OP_F2D
                  : undefined;
    if (op === undefined) return; // narrowing / reference: nothing to insert here
    code.u1(op);
    grow(
      slotsOf(to === "J" || to === "D" ? "J" : "I") -
        slotsOf(from === "J" || from === "D" ? "J" : "I"),
    );
  };

  const ldc = (index: number): void => {
    if (index <= 0xff) {
      code.u1(OP_LDC);
      code.u1(index);
    } else {
      code.u1(OP_LDC_W);
      code.u2(index);
    }
  };
  const intConst = (value: number): void => {
    if (value >= -1 && value <= 5) code.u1(OP_ICONST_M1 + (value + 1));
    else if (value >= -128 && value <= 127) {
      code.u1(OP_BIPUSH);
      code.u1(value & 0xff);
    } else if (value >= -32768 && value <= 32767) {
      code.u1(OP_SIPUSH);
      code.u2(value & 0xffff);
    } else ldc(cp.integer(value));
  };
  const longConst = (value: bigint): void => {
    if (value === 0n) code.u1(OP_LCONST_0);
    else if (value === 1n) code.u1(0x0a);
    else {
      code.u1(OP_LDC2_W);
      code.u2(cp.long(value));
    }
  };
  const loadVar = (varSlot: number, descriptor: string): void => {
    const c = descriptor[0];
    const kind =
      c === "J" ? "J" : c === "D" ? "D" : c === "F" ? "F" : c === "L" || c === "[" ? "A" : "I";
    const full = { I: OP_ILOAD, J: OP_LLOAD, F: OP_FLOAD, D: OP_DLOAD, A: OP_ALOAD }[kind];
    const short0 = {
      I: OP_ILOAD_0,
      J: OP_LLOAD_0,
      F: OP_FLOAD_0,
      D: OP_DLOAD_0,
      A: OP_ALOAD_BASE_0,
    }[kind];
    if (varSlot <= 3) code.u1(short0 + varSlot);
    else {
      code.u1(full);
      code.u1(varSlot);
    }
  };
  const storeVar = (varSlot: number, descriptor: string): void => {
    const kind = category(descriptor) === "I" ? "I" : category(descriptor);
    const full = { I: OP_ISTORE, J: OP_LSTORE, F: OP_FSTORE, D: OP_DSTORE, A: OP_ASTORE }[kind]!;
    const short0 = {
      I: OP_ISTORE_0,
      J: OP_LSTORE_0,
      F: OP_FSTORE_0,
      D: OP_DSTORE_0,
      A: OP_ASTORE_0,
    }[kind]!;
    if (varSlot <= 3) code.u1(short0 + varSlot);
    else {
      code.u1(full);
      code.u1(varSlot);
    }
    pop(slotsOf(descriptor));
  };

  // Descriptor of a checker Type, for `var` locals.
  const typeDescriptor = (type: Type): string => {
    switch (type.kind) {
      case TypeKind.Primitive:
        return PRIMITIVE_DESCRIPTOR[type.name] ?? "I";
      case TypeKind.Class:
        return `L${binaryName(type.symbol)};`;
      case TypeKind.Array:
        return `[${typeDescriptor(type.elementType)}`;
      default:
        return "Ljava/lang/Object;"; // type variable / wildcard / null / error
    }
  };

  const fieldInfoOf = (
    symbol: Symbol,
  ): { owner: string; name: string; descriptor: string; isStatic: boolean } => {
    const declarator = symbol.valueDeclaration;
    if (!declarator || declarator.kind !== SyntaxKind.VariableDeclarator)
      throw new UnsupportedEmit();
    const field = declarator.parent as FieldDeclaration;
    if (field.kind !== SyntaxKind.FieldDeclaration || !symbol.parent) throw new UnsupportedEmit();
    return {
      owner: binaryName(symbol.parent),
      name: symbol.escapedName,
      descriptor: descriptorOf(field.type, program),
      isStatic: isStaticDeclaration(field),
    };
  };

  const emitExpr = (node: Node): string => {
    switch (node.kind) {
      case SyntaxKind.ParenthesizedExpression:
        return emitExpr((node as unknown as { expression: Node }).expression);
      case SyntaxKind.NumericLiteral: {
        const text = (node as LiteralExpression).value.replace(/_/g, "");
        if (/[lL]$/.test(text)) {
          longConst(BigInt(text.slice(0, -1)));
          push("J");
          return "J";
        }
        if (/[.eEfFdD]/.test(text)) throw new UnsupportedEmit(); // floating point: later
        intConst(/^0[0-7]+$/.test(text) ? parseInt(text, 8) : Number(text));
        push("I");
        return "I";
      }
      case SyntaxKind.StringLiteral:
      case SyntaxKind.TextBlockLiteral:
        ldc(cp.string((node as LiteralExpression).value));
        pushRef();
        return "Ljava/lang/String;";
      case SyntaxKind.CharacterLiteral:
        intConst((node as LiteralExpression).value.charCodeAt(0));
        push("I");
        return "C";
      case SyntaxKind.TrueKeyword:
        code.u1(0x04); // iconst_1
        push("I");
        return "Z";
      case SyntaxKind.FalseKeyword:
        code.u1(OP_ICONST_0);
        push("I");
        return "Z";
      case SyntaxKind.NullKeyword:
        code.u1(OP_ACONST_NULL);
        pushRef();
        return "Ljava/lang/Object;";
      case SyntaxKind.ThisExpression:
        code.u1(OP_ALOAD_0);
        pushRef();
        return `L${thisInternalName};`;
      case SyntaxKind.Identifier: {
        const symbol = checker.resolveName(node as Identifier);
        const local = symbol ? locals.get(symbol) : undefined;
        if (!local) throw new UnsupportedEmit(); // fields/statics via simple name: later
        loadVar(local.slot, local.descriptor);
        push(local.descriptor);
        return local.descriptor;
      }
      case SyntaxKind.BinaryExpression:
        return emitBinary(node as BinaryExpression);
      case SyntaxKind.PrefixUnaryExpression:
        return emitPrefixUnary(node as PrefixUnaryExpression);
      case SyntaxKind.PropertyAccessExpression: {
        const access = node as PropertyAccessExpression;
        const symbol = checker.resolveName(access.name);
        if (!symbol || !(symbol.flags & SymbolFlags.Field)) throw new UnsupportedEmit();
        const info = fieldInfoOf(symbol);
        if (info.isStatic) {
          code.u1(OP_GETSTATIC);
        } else {
          emitExpr(access.expression);
          code.u1(OP_GETFIELD);
          pop(1);
        }
        code.u2(cp.fieldref(info.owner, info.name, info.descriptor));
        push(info.descriptor);
        return info.descriptor;
      }
      case SyntaxKind.CallExpression:
        return emitCall(node as CallExpression);
      default:
        throw new UnsupportedEmit();
    }
  };

  const emitCall = (call: CallExpression): string => {
    const decl = checker.resolveCall(call);
    const owner = decl?.symbol?.parent;
    if (!decl || !decl.symbol || !owner) throw new UnsupportedEmit();
    const ownerName = binaryName(owner);
    const isInterface = (owner.flags & SymbolFlags.Interface) !== 0;
    const staticCall = isStaticDeclaration(decl);
    const descriptor = methodDescriptor(decl, program);
    const callee = call.expression;

    if (!staticCall) {
      if (callee.kind === SyntaxKind.PropertyAccessExpression) {
        emitExpr((callee as PropertyAccessExpression).expression);
      } else if (callee.kind === SyntaxKind.Identifier) {
        code.u1(OP_ALOAD_0); // implicit this
        pushRef();
      } else throw new UnsupportedEmit();
    }
    for (const arg of call.arguments) emitExpr(arg);

    const argSlots = parseParamDescriptors(descriptor).reduce((n, d) => n + slotsOf(d), 0);
    const returnDesc = descriptor.slice(descriptor.lastIndexOf(")") + 1);
    if (staticCall) {
      code.u1(OP_INVOKESTATIC);
      code.u2(cp.methodref(ownerName, decl.name.text, descriptor));
      pop(argSlots);
    } else if (isInterface) {
      code.u1(OP_INVOKEINTERFACE);
      code.u2(cp.interfaceMethodref(ownerName, decl.name.text, descriptor));
      code.u1(argSlots + 1);
      code.u1(0);
      pop(argSlots + 1);
    } else {
      code.u1(OP_INVOKEVIRTUAL);
      code.u2(cp.methodref(ownerName, decl.name.text, descriptor));
      pop(argSlots + 1);
    }
    if (returnDesc !== "V") push(returnDesc);
    return returnDesc;
  };

  // Numeric category of an expression's static type, or undefined for anything
  // non-numeric (references, String -> concatenation, unknown).
  const numericCategory = (type: Type): string | undefined => {
    if (type.kind !== TypeKind.Primitive) return undefined;
    switch (type.name) {
      case "long":
        return "J";
      case "float":
        return "F";
      case "double":
        return "D";
      case "byte":
      case "short":
      case "char":
      case "boolean":
      case "int":
        return "I";
      default:
        return undefined; // void
    }
  };
  const TYPE_OFFSET: Record<string, number> = { I: 0, J: 1, F: 2, D: 3 };
  const ARITHMETIC: Record<number, number> = {
    [SyntaxKind.PlusToken]: OP_IADD,
    [SyntaxKind.MinusToken]: OP_ISUB,
    [SyntaxKind.AsteriskToken]: OP_IMUL,
    [SyntaxKind.SlashToken]: OP_IDIV,
    [SyntaxKind.PercentToken]: OP_IREM,
    [SyntaxKind.AmpersandToken]: OP_IAND,
    [SyntaxKind.BarToken]: OP_IOR,
    [SyntaxKind.CaretToken]: OP_IXOR,
  };
  const SHIFTS: Record<number, number> = {
    [SyntaxKind.LessThanLessThanToken]: OP_ISHL,
    [SyntaxKind.GreaterThanGreaterThanToken]: OP_ISHR,
    [SyntaxKind.GreaterThanGreaterThanGreaterThanToken]: OP_IUSHR,
  };

  // Emit an operand promoted to `targetCat`. An int literal promoted to long is
  // folded to a long constant (as javac does: 1 -> lconst_1, not iconst_1; i2l).
  const emitOperand = (node: Node, targetCat: string): void => {
    if (targetCat === "J" && node.kind === SyntaxKind.NumericLiteral) {
      const text = (node as LiteralExpression).value.replace(/_/g, "");
      if (!/[.eEfFdDlL]/.test(text)) {
        longConst(BigInt(/^0[0-7]+$/.test(text) ? parseInt(text, 8) : Number(text)));
        push("J");
        return;
      }
    }
    coerce(emitExpr(node), targetCat);
  };

  const emitBinary = (node: BinaryExpression): string => {
    const op = node.operatorToken;
    const lc = numericCategory(checker.getTypeOfExpression(node.left));
    const rc = numericCategory(checker.getTypeOfExpression(node.right));
    if (!lc || !rc) throw new UnsupportedEmit(); // String concat / comparisons: later

    const shift = SHIFTS[op];
    if (shift !== undefined) {
      const longShift = lc === "J";
      emitExpr(node.left); // already int or long (unary promotion is a no-op here)
      coerce(emitExpr(node.right), "I"); // the shift distance is always int
      code.u1(shift + (longShift ? 1 : 0));
      pop(1); // pops the int distance; the result keeps the left operand's size
      return longShift ? "J" : "I";
    }

    const base = ARITHMETIC[op];
    if (base === undefined) throw new UnsupportedEmit();
    const bitwise = base === OP_IAND || base === OP_IOR || base === OP_IXOR;
    if (bitwise && (lc === "F" || lc === "D" || rc === "F" || rc === "D")) {
      throw new UnsupportedEmit();
    }
    // Binary numeric promotion (JLS 5.6.2).
    const t =
      lc === "D" || rc === "D"
        ? "D"
        : lc === "F" || rc === "F"
          ? "F"
          : lc === "J" || rc === "J"
            ? "J"
            : "I";
    emitOperand(node.left, t);
    emitOperand(node.right, t);
    code.u1(base + TYPE_OFFSET[t]!);
    pop(slotsOf(t)); // two operands of t -> one result of t
    return t;
  };

  const emitPrefixUnary = (node: PrefixUnaryExpression): string => {
    const op = node.operator;
    if (op === SyntaxKind.PlusToken) return emitExpr(node.operand); // unary plus: no-op
    const c = numericCategory(checker.getTypeOfExpression(node.operand));
    if (!c) throw new UnsupportedEmit();
    if (op === SyntaxKind.MinusToken) {
      emitExpr(node.operand);
      code.u1(OP_INEG + TYPE_OFFSET[c]!);
      return c;
    }
    if (op === SyntaxKind.TildeToken) {
      if (c !== "I" && c !== "J") throw new UnsupportedEmit();
      emitExpr(node.operand);
      if (c === "I") {
        code.u1(OP_ICONST_M1);
        grow(1);
        code.u1(OP_IXOR);
        pop(1);
        return "I";
      }
      longConst(-1n);
      code.u1(OP_LXOR);
      pop(2);
      return "J";
    }
    throw new UnsupportedEmit(); // logical '!': needs control flow
  };

  const emitReturn = (): void => {
    switch (returnDescriptor[0]) {
      case "V":
        code.u1(OP_RETURN);
        break;
      case "J":
        code.u1(OP_LRETURN);
        break;
      case "D":
        code.u1(OP_DRETURN);
        break;
      case "F":
        code.u1(OP_FRETURN);
        break;
      case "L":
      case "[":
        code.u1(OP_ARETURN);
        break;
      default:
        code.u1(OP_IRETURN);
        break;
    }
  };

  // Assignment used as a statement: store into a local, leaving nothing on the
  // stack (field/array targets and compound assignment come later).
  const emitAssignStatement = (assign: AssignmentExpression): void => {
    if (assign.operatorToken !== SyntaxKind.EqualsToken) throw new UnsupportedEmit();
    if (assign.left.kind !== SyntaxKind.Identifier) throw new UnsupportedEmit();
    const symbol = checker.resolveName(assign.left as Identifier);
    const local = symbol ? locals.get(symbol) : undefined;
    if (!local) throw new UnsupportedEmit();
    const rd = emitExpr(assign.right);
    coerce(rd, local.descriptor);
    storeVar(local.slot, local.descriptor);
  };

  // Returns true if the statement is a definite terminator (return).
  const emitStmt = (stmt: Node): boolean => {
    switch (stmt.kind) {
      case SyntaxKind.Block: {
        let terminated = false;
        for (const s of (stmt as unknown as { statements: readonly Node[] }).statements) {
          terminated = emitStmt(s);
        }
        return terminated;
      }
      case SyntaxKind.EmptyStatement:
        return false;
      case SyntaxKind.LocalVariableDeclarationStatement: {
        const decl = stmt as LocalVariableDeclarationStatement;
        for (const d of decl.declarators) {
          const declarator = d as VariableDeclarator;
          const isVar = decl.type.kind === SyntaxKind.VarType;
          if (isVar && !declarator.initializer) throw new UnsupportedEmit();
          const descriptor = isVar
            ? typeDescriptor(checker.getTypeOfExpression(declarator.initializer!))
            : descriptorOf(decl.type, program);
          const slot = nextSlot;
          nextSlot += slotsOf(descriptor);
          if (nextSlot > maxLocals) maxLocals = nextSlot;
          if (declarator.symbol) locals.set(declarator.symbol, { slot, descriptor });
          if (declarator.initializer) {
            const rd = emitExpr(declarator.initializer);
            coerce(rd, descriptor);
            storeVar(slot, descriptor);
          }
        }
        return false;
      }
      case SyntaxKind.ExpressionStatement: {
        const expression = (stmt as ExpressionStatement).expression;
        if (expression.kind === SyntaxKind.AssignmentExpression) {
          emitAssignStatement(expression as AssignmentExpression);
          return false;
        }
        const desc = emitExpr(expression);
        if (desc !== "V") {
          code.u1(slotsOf(desc) === 2 ? OP_POP2 : OP_POP);
          pop(slotsOf(desc));
        }
        return false;
      }
      case SyntaxKind.ReturnStatement: {
        const expr = (stmt as ReturnStatement).expression;
        if (expr) emitExpr(expr);
        emitReturn();
        return true;
      }
      default:
        throw new UnsupportedEmit();
    }
  };

  if (!method.body || method.body.kind !== SyntaxKind.Block) throw new UnsupportedEmit();
  const terminated = emitStmt(method.body);
  if (!terminated) {
    if (returnDescriptor === "V") code.u1(OP_RETURN);
    else throw new UnsupportedEmit(); // non-void path falls off the end
  }
  return { code, maxStack, maxLocals };
}

function emitMethod(
  method: MethodDeclaration,
  cp: ConstantPool,
  program: Program,
  checker: Checker,
  thisInternalName: string,
): ByteBuffer {
  const flags = methodAccessFlags(method);
  const descriptor = methodDescriptor(method, program);

  const info = new ByteBuffer();
  info.u2(flags);
  info.u2(cp.utf8(method.name.text));
  info.u2(cp.utf8(descriptor));

  // abstract / native methods carry no Code attribute.
  if (flags & (ACC_ABSTRACT | ACC_NATIVE) || !method.body) {
    info.u2(0); // attributes_count
    return info;
  }

  let body: { code: ByteBuffer; maxStack: number; maxLocals: number };
  try {
    body = generateBody(method, cp, program, checker, thisInternalName);
  } catch (e) {
    if (!(e instanceof UnsupportedEmit)) throw e;
    // Unhandled construct: emit a verifiable placeholder so output stays valid.
    const isStatic = (flags & ACC_STATIC) !== 0;
    const argsSize =
      (isStatic ? 0 : 1) +
      method.parameters.reduce((n, p) => n + slotsOf(paramDescriptor(p as Parameter, program)), 0);
    const returnDescriptor = descriptor.slice(descriptor.lastIndexOf(")") + 1);
    const fallback = defaultReturnBody(returnDescriptor);
    body = { code: fallback.code, maxStack: fallback.maxStack, maxLocals: argsSize };
  }

  const codeAttr = new ByteBuffer();
  codeAttr.u2(cp.utf8("Code"));
  codeAttr.u4(12 + body.code.length);
  codeAttr.u2(body.maxStack);
  codeAttr.u2(body.maxLocals);
  codeAttr.u4(body.code.length);
  codeAttr.append(body.code);
  codeAttr.u2(0); // exception_table_length
  codeAttr.u2(0); // attributes_count

  info.u2(1); // attributes_count
  info.append(codeAttr);
  return info;
}

export interface EmittedClass {
  /** Simple class name (becomes <name>.class). */
  readonly name: string;
  readonly bytes: Uint8Array;
}

function emitClass(
  declaration: ClassDeclaration,
  program: Program,
  checker: Checker,
): EmittedClass {
  const name = declaration.name.text;
  const superInternalName = "java/lang/Object"; // resolving `extends` comes later

  const accessFlags = classAccessFlags(declaration);
  const cp = new ConstantPool();
  const thisClassIndex = cp.classInfo(name);
  const superClassIndex = cp.classInfo(superInternalName);
  const fields = emitFields(declaration, cp, program);

  // Methods: the synthesized default constructor (inherits the class's
  // accessibility, JLS 8.8.9) plus every declared method.
  const methods = new ByteBuffer();
  let methodCount = 1;
  methods.append(
    emitDefaultConstructor(
      cp,
      superInternalName,
      accessFlags & (ACC_PUBLIC | ACC_PROTECTED | ACC_PRIVATE),
    ),
  );
  for (const member of declaration.members) {
    if (member.kind !== SyntaxKind.MethodDeclaration) continue;
    methods.append(emitMethod(member as MethodDeclaration, cp, program, checker, name));
    methodCount++;
  }

  const out = new ByteBuffer();
  out.u4(MAGIC);
  out.u2(MINOR_VERSION);
  out.u2(MAJOR_VERSION);
  cp.writeInto(out);
  out.u2(accessFlags);
  out.u2(thisClassIndex);
  out.u2(superClassIndex);
  out.u2(0); // interfaces_count
  out.u2(fields.count);
  out.append(fields.buffer);
  out.u2(methodCount);
  out.append(methods);
  out.u2(0); // attributes_count

  return { name, bytes: out.toUint8Array() };
}

/** Emit a .class file for every top-level class declaration in a source file. */
export function emitSourceFile(
  sourceFile: SourceFile,
  program: Program,
  checker: Checker,
): EmittedClass[] {
  const result: EmittedClass[] = [];
  forEachChild(sourceFile, (node: Node) => {
    if (node.kind === SyntaxKind.ClassDeclaration) {
      result.push(emitClass(node as ClassDeclaration, program, checker));
    }
    return undefined;
  });
  return result;
}
