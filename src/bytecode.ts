// JVM class-file writer and bytecode code generation. Implements the JVM
// Specification (JVMS SE 21): chapter 4 (the class file format, constant pool,
// fields/methods, Code and StackMapTable attributes) and chapter 6 (the
// instruction set). The entry point is emitClass(declaration) -> one .class file.
// emitter.ts drives this per source file and is where higher-level, source-level
// logic (e.g. constant folding) belongs. We target major version 65 (Java 21).
//
// Reference output is cross-checked against `javac` in the tests.

import type { Checker } from "./checker.ts";
import { type Type, TypeKind } from "./checkerTypes.ts";
import { foldConstant } from "./constfold.ts";
import type { Program } from "./program.ts";
import { resolveTypeEntityName } from "./resolver.ts";
import { entityNameToString, tokenToString } from "./utilities.ts";
import {
  type ArrayType as AstArrayType,
  type AssignmentExpression,
  type BinaryExpression,
  type CallExpression,
  type CastExpression,
  type ClassDeclaration,
  type ConstructorDeclaration,
  type DoStatement,
  type InstanceofExpression,
  type ExpressionStatement,
  type FieldDeclaration,
  type ForStatement,
  type Identifier,
  type IfStatement,
  type LiteralExpression,
  type LocalVariableDeclarationStatement,
  type MethodDeclaration,
  type Node,
  type ObjectCreationExpression,
  type Parameter,
  type PrefixUnaryExpression,
  type PropertyAccessExpression,
  type ReturnStatement,
  type Symbol,
  SymbolFlags,
  SyntaxKind,
  type TypeNode,
  type TypeReference,
  type VariableDeclarator,
  type WhileStatement,
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
const CONSTANT_Float = 4;
const CONSTANT_Long = 5;
const CONSTANT_Double = 6;
const CONSTANT_Class = 7;
const CONSTANT_String = 8;
const CONSTANT_Fieldref = 9;
const CONSTANT_Methodref = 10;
const CONSTANT_InterfaceMethodref = 11;
const CONSTANT_NameAndType = 12;
const CONSTANT_MethodHandle = 15;
const CONSTANT_InvokeDynamic = 18;
const REF_invokeStatic = 6; // MethodHandle reference_kind (JVMS 4.4.8)

// java.lang.invoke.StringConcatFactory.makeConcatWithConstants bootstrap (JLS 15.18.1).
const STRING_CONCAT_FACTORY = "java/lang/invoke/StringConcatFactory";
const MAKE_CONCAT = "makeConcatWithConstants";
const MAKE_CONCAT_BSM_DESCRIPTOR =
  "(Ljava/lang/invoke/MethodHandles$Lookup;Ljava/lang/String;Ljava/lang/invoke/MethodType;Ljava/lang/String;[Ljava/lang/Object;)Ljava/lang/invoke/CallSite;";
const OP_INVOKEDYNAMIC = 0xba;

// Opcodes (JVMS 6.5) used so far.
const OP_ACONST_NULL = 0x01;
const OP_ICONST_0 = 0x03;
const OP_ICONST_1 = 0x04;
const OP_LCONST_0 = 0x09;
const OP_FCONST_0 = 0x0b;
const OP_FCONST_1 = 0x0c;
const OP_FCONST_2 = 0x0d;
const OP_DCONST_0 = 0x0e;
const OP_DCONST_1 = 0x0f;
const OP_LCMP = 0x94;
const OP_FCMPL = 0x95;
const OP_FCMPG = 0x96;
const OP_DCMPL = 0x97;
const OP_DCMPG = 0x98;
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
const OP_L2I = 0x88;
const OP_L2F = 0x89;
const OP_L2D = 0x8a;
const OP_F2I = 0x8b;
const OP_F2L = 0x8c;
const OP_F2D = 0x8d;
const OP_D2I = 0x8e;
const OP_D2L = 0x8f;
const OP_D2F = 0x90;
const OP_I2B = 0x91;
const OP_I2C = 0x92;
const OP_I2S = 0x93;
const OP_CHECKCAST = 0xc0;
const OP_INSTANCEOF = 0xc1;
const OP_POP = 0x57;
const OP_POP2 = 0x58;
const OP_DUP = 0x59;
const OP_NEW = 0xbb;
const OP_GETSTATIC = 0xb2;
const OP_PUTSTATIC = 0xb3;
const OP_GETFIELD = 0xb4;
const OP_PUTFIELD = 0xb5;
const OP_INVOKEVIRTUAL = 0xb6;
const OP_INVOKESPECIAL = 0xb7;
const OP_INVOKESTATIC = 0xb8;
const OP_INVOKEINTERFACE = 0xb9;
const OP_IINC = 0x84;
const OP_IFEQ = 0x99; // if<cond> against 0; +offset within (eq,ne,lt,ge,gt,le)
const OP_IF_ICMPEQ = 0x9f; // if_icmp<cond>; same offset order
const OP_IF_ACMPEQ = 0xa5;
const OP_IF_ACMPNE = 0xa6;
const OP_GOTO = 0xa7;
const OP_IFNULL = 0xc6;
const OP_IFNONNULL = 0xc7;
const OP_ALOAD_0 = 0x2a;
const OP_RETURN = 0xb1;

// StackMapTable verification_type_info tags (JVMS 4.7.4).
const ITEM_Integer = 1;
const ITEM_Float = 2;
const ITEM_Double = 3;
const ITEM_Long = 4;
const ITEM_Object = 7;
const FULL_FRAME = 255;

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
  // Overwrite a previously-reserved u2 (for branch-offset backpatching).
  patchU2(pos: number, value: number): void {
    this.bytes[pos] = (value >>> 8) & 0xff;
    this.bytes[pos + 1] = value & 0xff;
  }
}

// Builds the constant pool, interning entries so each appears once. Indices are
// 1-based (JVMS 4.1: the pool is indexed 1..count-1).
class ConstantPool {
  private entries = new ByteBuffer();
  private count = 0; // number of entries (next index is count + 1)
  private cache = new Map<string, number>();
  // BootstrapMethods entries (JVMS 4.7.23): bootstrap MethodHandle + static args.
  private bootstraps: { handle: number; args: number[] }[] = [];

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

  float(value: number): number {
    const view = new DataView(new ArrayBuffer(4));
    view.setFloat32(0, value);
    return this.intern(`f:${view.getUint32(0)}`, b => {
      b.u1(CONSTANT_Float);
      b.u4(view.getUint32(0));
    });
  }

  double(value: number): number {
    const view = new DataView(new ArrayBuffer(8));
    view.setFloat64(0, value);
    const index = this.intern(`d:${view.getUint32(0)}:${view.getUint32(4)}`, b => {
      b.u1(CONSTANT_Double);
      b.u4(view.getUint32(0));
      b.u4(view.getUint32(4));
    });
    this.count++; // the second (unusable) slot (JVMS 4.4.5)
    return index;
  }

  private methodHandle(referenceKind: number, referenceIndex: number): number {
    return this.intern(`mh:${referenceKind}:${referenceIndex}`, b => {
      b.u1(CONSTANT_MethodHandle);
      b.u1(referenceKind);
      b.u2(referenceIndex);
    });
  }

  /**
   * An invokedynamic to StringConcatFactory.makeConcatWithConstants with the given
   * recipe and dynamic-argument descriptor. Registers the bootstrap method and
   * returns the CONSTANT_InvokeDynamic index.
   */
  invokeDynamicConcat(recipe: string, dynamicArgsDescriptor: string): number {
    const handle = this.methodHandle(
      REF_invokeStatic,
      this.methodref(STRING_CONCAT_FACTORY, MAKE_CONCAT, MAKE_CONCAT_BSM_DESCRIPTOR),
    );
    const recipeIndex = this.string(recipe);
    const bootstrapIndex = this.bootstraps.length;
    this.bootstraps.push({ handle, args: [recipeIndex] });
    const nt = this.nameAndType(MAKE_CONCAT, `(${dynamicArgsDescriptor})Ljava/lang/String;`);
    return this.intern(`indy:${bootstrapIndex}:${dynamicArgsDescriptor}`, b => {
      b.u1(CONSTANT_InvokeDynamic);
      b.u2(bootstrapIndex);
      b.u2(nt);
    });
  }

  get bootstrapMethodCount(): number {
    return this.bootstraps.length;
  }

  /** The BootstrapMethods attribute body (num_bootstrap_methods + entries). */
  bootstrapMethodsBody(): ByteBuffer {
    const body = new ByteBuffer();
    body.u2(this.bootstraps.length);
    for (const { handle, args } of this.bootstraps) {
      body.u2(handle);
      body.u2(args.length);
      for (const a of args) body.u2(a);
    }
    return body;
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
      const d = declarator as VariableDeclarator;
      buffer.u2(flags);
      buffer.u2(cp.utf8(d.name.text));
      buffer.u2(cp.utf8(descriptor));
      // static final fields with a compile-time constant carry a ConstantValue.
      const constIndex = constantValueIndex(field, d, cp, program);
      if (constIndex === undefined) {
        buffer.u2(0); // attributes_count
      } else {
        buffer.u2(1);
        buffer.u2(cp.utf8("ConstantValue"));
        buffer.u4(2);
        buffer.u2(constIndex);
      }
      count++;
    }
  }
  return { buffer, count };
}

function hasFinalModifier(modifiers: readonly Node[] | undefined): boolean {
  return (modifiers ?? []).some(m => m.kind === SyntaxKind.FinalKeyword);
}

// True when a `static final` field's initializer is a constant eligible for a
// ConstantValue attribute (so it is excluded from <clinit>).
function isConstantValueField(
  field: FieldDeclaration,
  declarator: VariableDeclarator,
  program: Program,
): boolean {
  if (
    !isStaticDeclaration(field) ||
    !hasFinalModifier(field.modifiers) ||
    !declarator.initializer
  ) {
    return false;
  }
  const descriptor = descriptorOf(field.type, program);
  if (
    descriptor === "Ljava/lang/String;" &&
    declarator.initializer.kind === SyntaxKind.StringLiteral
  ) {
    return true;
  }
  return (
    foldConstant(declarator.initializer) !== undefined &&
    ["J", "Z", "I", "S", "B", "C"].includes(descriptor)
  );
}

// The constant-pool index of a field's ConstantValue (JVMS 4.7.2), or undefined.
function constantValueIndex(
  field: FieldDeclaration,
  declarator: VariableDeclarator,
  cp: ConstantPool,
  program: Program,
): number | undefined {
  if (!isConstantValueField(field, declarator, program)) return undefined;
  const init = declarator.initializer!;
  const descriptor = descriptorOf(field.type, program);
  if (descriptor === "Ljava/lang/String;") {
    return cp.string((init as LiteralExpression).value);
  }
  const folded = foldConstant(init)!;
  const intValue = folded.kind === "boolean" ? (folded.value ? 1 : 0) : Number(folded.value);
  if (descriptor === "J") {
    return cp.long(folded.kind === "boolean" ? BigInt(intValue) : (folded.value as bigint));
  }
  return cp.integer(intValue);
}

function methodAccessFlags(method: MethodDeclaration | ConstructorDeclaration): number {
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

// A declared constructor of `typeSymbol` taking `argCount` parameters, if any.
function findConstructor(typeSymbol: Symbol, argCount: number): ConstructorDeclaration | undefined {
  const declaration = typeSymbol.valueDeclaration ?? typeSymbol.declarations?.[0];
  const members = (declaration as { members?: readonly Node[] } | undefined)?.members ?? [];
  return members.find(
    m =>
      m.kind === SyntaxKind.ConstructorDeclaration &&
      (m as ConstructorDeclaration).parameters.length === argCount,
  ) as ConstructorDeclaration | undefined;
}

// Generate real bytecode for a method body. Throws UnsupportedEmit for anything
// not yet handled, so emitMethod can fall back to a verifiable placeholder.
interface FieldInit {
  isStatic: boolean;
  owner: string;
  name: string;
  descriptor: string;
  init: Node;
}

function generateBody(
  method: MethodDeclaration | ConstructorDeclaration,
  cp: ConstantPool,
  program: Program,
  checker: Checker,
  thisInternalName: string,
  // For constructors: the super class's internal name; an implicit super.<init>()
  // call is emitted before the body (explicit super()/this() is not yet handled).
  ctorSuper?: string,
  // Field initializers run in the prologue: instance fields after super() in a
  // constructor, static fields at the top of <clinit>.
  fieldInits: FieldInit[] = [],
): { code: ByteBuffer; maxStack: number; maxLocals: number; stackMapTable?: ByteBuffer } {
  const isConstructor = method.kind === SyntaxKind.ConstructorDeclaration;
  const isStatic =
    !isConstructor && (methodAccessFlags(method as MethodDeclaration) & ACC_STATIC) !== 0;
  const returnDescriptor = isConstructor
    ? "V"
    : descriptorOf((method as MethodDeclaration).returnType, program);

  // Slots for parameters and (as they are declared) locals; shared map keyed by
  // the declaration symbol.
  const locals = new Map<Symbol, { slot: number; descriptor: string }>();
  // Descriptors of the locals currently in scope, in slot order, for stack-map
  // frames (this, then params, then declared locals; long/double = one entry).
  const activeLocals: string[] = [];
  let nextSlot = isStatic ? 0 : 1;
  if (!isStatic) activeLocals.push(`L${thisInternalName};`);
  for (const p of method.parameters) {
    const descriptor = paramDescriptor(p as Parameter, program);
    if (p.symbol) locals.set(p.symbol, { slot: nextSlot, descriptor });
    activeLocals.push(descriptor);
    nextSlot += slotsOf(descriptor);
  }
  let maxLocals = nextSlot;
  const code = new ByteBuffer();

  // Run `body` in a nested local scope: locals it declares are dropped (and their
  // slots reusable) afterwards, so a stack-map frame at a later label lists only
  // the locals actually in scope there.
  const inScope = (body: () => boolean): boolean => {
    const savedActive = activeLocals.length;
    const savedSlot = nextSlot;
    const terminated = body();
    activeLocals.length = savedActive;
    nextSlot = savedSlot;
    return terminated;
  };

  // --- labels, branches and stack-map frames ---------------------------------------
  interface Label {
    offset: number; // resolved when placed
    targetStack?: string[]; // operand stack as seen at the branch target (recorded by branchTo)
  }
  interface Frame {
    locals: string[];
    stack: string[];
  }
  const frameAt = new Map<number, Frame>(); // offset -> frame snapshot
  const fixups: { at: number; from: number; label: Label }[] = [];
  const newLabel = (): Label => ({ offset: -1 });
  // A branch target's frame is defined by the operand stack on the branch-taken
  // path, which can differ from the live stack at the label site (e.g. when the
  // fall-through arrives after a terminator). branchTo records it; placeLabel
  // prefers it, falling back to the live stack for fall-through-only labels.
  const placeLabel = (label: Label): void => {
    label.offset = code.length;
    frameAt.set(label.offset, {
      locals: [...activeLocals],
      stack: [...(label.targetStack ?? stack)],
    });
  };
  const branchTo = (op: number, label: Label): void => {
    const from = code.length;
    code.u1(op);
    const at = code.length;
    code.u2(0); // placeholder offset, backpatched below
    fixups.push({ at, from, label });
    if (label.targetStack === undefined) label.targetStack = [...stack];
  };

  // Typed operand stack: one descriptor per value (top last). Drives max_stack and
  // the stack-map frames snapshotted at branch targets.
  const stack: string[] = [];
  let maxStack = 0;
  const push = (descriptor: string): void => {
    stack.push(descriptor);
    const slots = stack.reduce((n, d) => n + slotsOf(d), 0);
    if (slots > maxStack) maxStack = slots;
  };
  const pushRef = (descriptor = "Ljava/lang/Object;"): void => push(descriptor);
  // Pop `count` operand VALUES (not slots).
  const pop = (count = 1): void => {
    for (let i = 0; i < count; i++) stack.pop();
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
    pop(); // replace the converted value on the typed stack
    push(b);
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
  const floatConst = (value: number): void => {
    if (value === 0) code.u1(OP_FCONST_0);
    else if (value === 1) code.u1(OP_FCONST_1);
    else if (value === 2) code.u1(OP_FCONST_2);
    else ldc(cp.float(value));
  };
  const doubleConst = (value: number): void => {
    if (value === 0) code.u1(OP_DCONST_0);
    else if (value === 1) code.u1(OP_DCONST_1);
    else {
      code.u1(OP_LDC2_W);
      code.u2(cp.double(value));
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
    const kind = category(descriptor);
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
    pop(); // the value being stored
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

  // Read a field: getstatic, or emit the receiver then getfield. `emitReceiver`
  // is only invoked for instance fields (skipped for statics, like javac).
  const emitFieldRead = (
    info: { owner: string; name: string; descriptor: string; isStatic: boolean },
    emitReceiver: () => void,
  ): string => {
    if (info.isStatic) {
      code.u1(OP_GETSTATIC);
    } else {
      emitReceiver();
      code.u1(OP_GETFIELD);
      pop(1); // the receiver, replaced by the field value
    }
    code.u2(cp.fieldref(info.owner, info.name, info.descriptor));
    push(info.descriptor);
    return info.descriptor;
  };

  const emitExpr = (node: Node): string => {
    // Fold compile-time constant expressions (JLS 15.28), as javac does, so e.g.
    // 6 * 7 emits `bipush 42`. Only composite expressions are folded here; plain
    // literals fall through to their own (identical) emission below.
    if (
      node.kind === SyntaxKind.BinaryExpression ||
      node.kind === SyntaxKind.PrefixUnaryExpression
    ) {
      const folded = foldConstant(node);
      if (folded) {
        if (folded.kind === "long") {
          longConst(folded.value);
          push("J");
          return "J";
        }
        if (folded.kind === "boolean") {
          code.u1(folded.value ? OP_ICONST_1 : OP_ICONST_0);
          push("I");
          return "Z";
        }
        intConst(Number(folded.value));
        push("I");
        return "I";
      }
    }
    switch (node.kind) {
      case SyntaxKind.ParenthesizedExpression:
        return emitExpr((node as unknown as { expression: Node }).expression);
      case SyntaxKind.NumericLiteral: {
        const text = (node as LiteralExpression).value.replace(/_/g, "");
        // In hex/binary integer literals the letters a-f are digits, not type
        // suffixes, so f/d/e must not be read as float/double/exponent there;
        // only a trailing L is a suffix. (parseFloat would silently return 0.)
        const isHex = /^0[xX]/.test(text);
        const isBin = /^0[bB]/.test(text);
        if (isHex && /[pP]/.test(text)) throw new UnsupportedEmit(); // hex floating-point literal
        if (!isHex && !isBin && /[fF]$/.test(text)) {
          floatConst(parseFloat(text.slice(0, -1)));
          push("F");
          return "F";
        }
        if (!isHex && !isBin && (/[.eE]/.test(text) || /[dD]$/.test(text))) {
          doubleConst(parseFloat(/[dD]$/.test(text) ? text.slice(0, -1) : text));
          push("D");
          return "D";
        }
        if (/[lL]$/.test(text)) {
          const body = text.slice(0, -1);
          const v =
            isHex || isBin
              ? BigInt(body)
              : BigInt(/^0[0-7]+$/.test(body) ? parseInt(body, 8) : body);
          longConst(BigInt.asIntN(64, v));
          push("J");
          return "J";
        }
        const value =
          isHex || isBin
            ? Number(BigInt.asIntN(32, BigInt(text))) // wrap to signed 32-bit (0xFFFFFFFF -> -1)
            : /^0[0-7]+$/.test(text)
              ? parseInt(text, 8)
              : Number(text);
        intConst(value);
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
        if (local) {
          loadVar(local.slot, local.descriptor);
          push(local.descriptor);
          return local.descriptor;
        }
        // A field referenced by its simple name: implicit `this.f` or a static.
        if (symbol && symbol.flags & SymbolFlags.Field) {
          return emitFieldRead(fieldInfoOf(symbol), () => {
            code.u1(OP_ALOAD_0);
            pushRef();
          });
        }
        throw new UnsupportedEmit();
      }
      case SyntaxKind.ObjectCreationExpression:
        return emitNew(node);
      case SyntaxKind.BinaryExpression: {
        const b = node as BinaryExpression;
        if (
          b.operatorToken === SyntaxKind.PlusToken &&
          isStringType(checker.getTypeOfExpression(b))
        ) {
          return emitStringConcat(b);
        }
        return isBooleanOperator(b.operatorToken) ? emitBoolean(node) : emitBinary(b);
      }
      case SyntaxKind.PrefixUnaryExpression: {
        const u = node as PrefixUnaryExpression;
        return u.operator === SyntaxKind.ExclamationToken ? emitBoolean(node) : emitPrefixUnary(u);
      }
      case SyntaxKind.PropertyAccessExpression: {
        const access = node as PropertyAccessExpression;
        const symbol = checker.resolveName(access.name);
        if (!symbol || !(symbol.flags & SymbolFlags.Field)) throw new UnsupportedEmit();
        return emitFieldRead(fieldInfoOf(symbol), () => emitExpr(access.expression));
      }
      case SyntaxKind.CallExpression:
        return emitCall(node as CallExpression);
      case SyntaxKind.CastExpression:
        return emitCast(node as CastExpression);
      case SyntaxKind.InstanceofExpression:
        return emitInstanceof(node as InstanceofExpression);
      default:
        throw new UnsupportedEmit();
    }
  };

  // Numeric conversion from one category to another, for primitive casts.
  const PRIMITIVE_CONVERSION: Record<string, number | undefined> = {
    IJ: OP_I2L,
    IF: OP_I2F,
    ID: OP_I2D,
    JI: OP_L2I,
    JF: OP_L2F,
    JD: OP_L2D,
    FI: OP_F2I,
    FJ: OP_F2L,
    FD: OP_F2D,
    DI: OP_D2I,
    DJ: OP_D2L,
    DF: OP_D2F,
  };
  const convertPrimitive = (fromCat: string, targetDescriptor: string): void => {
    const targetCat = category(targetDescriptor); // B/C/S/Z/I all collapse to I
    if (fromCat !== targetCat) {
      const op = PRIMITIVE_CONVERSION[`${fromCat}${targetCat}`];
      if (op === undefined) throw new UnsupportedEmit();
      code.u1(op);
      pop();
      push(targetCat);
    }
    if (targetDescriptor === "B") code.u1(OP_I2B);
    else if (targetDescriptor === "C") code.u1(OP_I2C);
    else if (targetDescriptor === "S") code.u1(OP_I2S);
  };

  const emitCast = (node: CastExpression): string => {
    const targetDescriptor = descriptorOf(node.type, program);
    const c = targetDescriptor[0]!;
    if ("BCDFIJSZ".includes(c)) {
      const fromCat = numericCategory(checker.getTypeOfExpression(node.expression));
      if (!fromCat) throw new UnsupportedEmit();
      emitExpr(node.expression);
      convertPrimitive(fromCat, targetDescriptor);
      return targetDescriptor;
    }
    // Reference cast: checkcast to the target class/array (no stack-size change).
    emitExpr(node.expression);
    const klass = c === "[" ? targetDescriptor : targetDescriptor.slice(1, -1);
    code.u1(OP_CHECKCAST);
    code.u2(cp.classInfo(klass));
    return targetDescriptor;
  };

  const emitInstanceof = (node: InstanceofExpression): string => {
    if (node.name) throw new UnsupportedEmit(); // pattern binding (x instanceof T t): later
    emitExpr(node.expression);
    const descriptor = descriptorOf(node.type, program);
    const klass = descriptor[0] === "[" ? descriptor : descriptor.slice(1, -1);
    code.u1(OP_INSTANCEOF);
    code.u2(cp.classInfo(klass));
    pop(1); // objectref
    push("I"); // boolean result
    return "Z";
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
    const argCount = call.arguments.length;
    const returnDesc = descriptor.slice(descriptor.lastIndexOf(")") + 1);
    if (staticCall) {
      code.u1(OP_INVOKESTATIC);
      code.u2(cp.methodref(ownerName, decl.name.text, descriptor));
      pop(argCount);
    } else if (isInterface) {
      code.u1(OP_INVOKEINTERFACE);
      code.u2(cp.interfaceMethodref(ownerName, decl.name.text, descriptor));
      code.u1(argSlots + 1); // invokeinterface "count" is in argument slots
      code.u1(0);
      pop(argCount + 1);
    } else {
      code.u1(OP_INVOKEVIRTUAL);
      code.u2(cp.methodref(ownerName, decl.name.text, descriptor));
      pop(argCount + 1);
    }
    if (returnDesc !== "V") push(returnDesc);
    return returnDesc;
  };

  // new T(args): new; dup; <args>; invokespecial T.<init>:(...)V -> leaves the ref.
  const emitNew = (node: Node): string => {
    const created = checker.getTypeOfExpression(node);
    if (created.kind !== TypeKind.Class) throw new UnsupportedEmit();
    const owner = binaryName(created.symbol);
    const args = (node as ObjectCreationExpression).arguments ?? [];

    const ctor = findConstructor(created.symbol, args.length);
    const ctorParams = ctor
      ? ctor.parameters.map(p => paramDescriptor(p as Parameter, program))
      : [];
    if (!ctor && args.length > 0) throw new UnsupportedEmit(); // unknown constructor
    const ctorDescriptor = `(${ctorParams.join("")})V`;

    const ref = `L${owner};`;
    code.u1(OP_NEW);
    code.u2(cp.classInfo(owner));
    pushRef(ref);
    code.u1(OP_DUP);
    pushRef(ref);
    args.forEach((arg, i) => {
      const d = emitExpr(arg);
      if (i < ctorParams.length) coerce(d, ctorParams[i]!);
    });
    code.u1(OP_INVOKESPECIAL);
    code.u2(cp.methodref(owner, "<init>", ctorDescriptor));
    pop(1 + args.length); // invokespecial consumes the dup'd ref and the arguments
    return ref;
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
  // Binary numeric promotion (JLS 5.6.2): the wider of the two operand categories.
  const promote = (a: string, b: string): string =>
    a === "D" || b === "D"
      ? "D"
      : a === "F" || b === "F"
        ? "F"
        : a === "J" || b === "J"
          ? "J"
          : "I";
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

  const isStringType = (type: Type): boolean =>
    type.kind === TypeKind.Class && binaryName(type.symbol) === "java/lang/String";

  // String concatenation a + b + ... -> invokedynamic makeConcatWithConstants
  // (JLS 15.18.1). Every operand is a dynamic argument (recipe of  markers);
  // operand types drive the indy descriptor (so char appends a char, not its code).
  const emitStringConcat = (node: BinaryExpression): string => {
    const operands: Node[] = [];
    const flatten = (n: Node): void => {
      if (
        n.kind === SyntaxKind.BinaryExpression &&
        (n as BinaryExpression).operatorToken === SyntaxKind.PlusToken &&
        isStringType(checker.getTypeOfExpression(n))
      ) {
        flatten((n as BinaryExpression).left);
        flatten((n as BinaryExpression).right);
      } else {
        operands.push(n);
      }
    };
    flatten(node);

    let descriptor = "";
    for (const operand of operands) {
      descriptor += typeDescriptor(checker.getTypeOfExpression(operand));
      emitExpr(operand);
    }
    code.u1(OP_INVOKEDYNAMIC);
    code.u2(cp.invokeDynamicConcat(String.fromCharCode(1).repeat(operands.length), descriptor));
    code.u2(0);
    pop(operands.length);
    push("Ljava/lang/String;");
    return "Ljava/lang/String;";
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
      pop(); // pops the int distance; the result keeps the left operand
      return longShift ? "J" : "I";
    }

    const base = ARITHMETIC[op];
    if (base === undefined) throw new UnsupportedEmit();
    const bitwise = base === OP_IAND || base === OP_IOR || base === OP_IXOR;
    if (bitwise && (lc === "F" || lc === "D" || rc === "F" || rc === "D")) {
      throw new UnsupportedEmit();
    }
    const t = promote(lc, rc);
    emitOperand(node.left, t);
    emitOperand(node.right, t);
    code.u1(base + TYPE_OFFSET[t]!);
    pop(2); // two operands -> one result
    push(t);
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
        push("I");
        code.u1(OP_IXOR);
        pop(2);
        push("I");
        return "I";
      }
      longConst(-1n);
      push("J");
      code.u1(OP_LXOR);
      pop(2);
      push("J");
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

  // Assignment used as a statement: store into a local or field, leaving nothing
  // on the stack (array targets and compound assignment come later).
  const emitAssignStatement = (assign: AssignmentExpression): void => {
    if (assign.operatorToken !== SyntaxKind.EqualsToken) throw new UnsupportedEmit();
    const target = assign.left;

    // Local: emit value (coerced) and store.
    if (target.kind === SyntaxKind.Identifier) {
      const symbol = checker.resolveName(target as Identifier);
      const local = symbol ? locals.get(symbol) : undefined;
      if (local) {
        coerce(emitExpr(assign.right), local.descriptor);
        storeVar(local.slot, local.descriptor);
        return;
      }
      // Field by simple name: implicit `this.f = v` or a static field.
      if (symbol && symbol.flags & SymbolFlags.Field) {
        const info = fieldInfoOf(symbol);
        if (info.isStatic) {
          coerce(emitExpr(assign.right), info.descriptor);
          code.u1(OP_PUTSTATIC);
          code.u2(cp.fieldref(info.owner, info.name, info.descriptor));
          pop(); // value
        } else {
          code.u1(OP_ALOAD_0);
          pushRef();
          coerce(emitExpr(assign.right), info.descriptor);
          code.u1(OP_PUTFIELD);
          code.u2(cp.fieldref(info.owner, info.name, info.descriptor));
          pop(2); // receiver + value
        }
        return;
      }
      throw new UnsupportedEmit();
    }

    // `obj.f = v` / `Type.staticF = v`.
    if (target.kind === SyntaxKind.PropertyAccessExpression) {
      const access = target as PropertyAccessExpression;
      const symbol = checker.resolveName(access.name);
      if (!symbol || !(symbol.flags & SymbolFlags.Field)) throw new UnsupportedEmit();
      const info = fieldInfoOf(symbol);
      if (info.isStatic) {
        coerce(emitExpr(assign.right), info.descriptor);
        code.u1(OP_PUTSTATIC);
        code.u2(cp.fieldref(info.owner, info.name, info.descriptor));
        pop(); // value
      } else {
        emitExpr(access.expression); // receiver
        coerce(emitExpr(assign.right), info.descriptor);
        code.u1(OP_PUTFIELD);
        code.u2(cp.fieldref(info.owner, info.name, info.descriptor));
        pop(2); // receiver + value
      }
      return;
    }

    throw new UnsupportedEmit();
  };

  // Comparison operator -> offset into the if_icmp<cond> family (eq,ne,lt,ge,gt,le).
  const COMPARE_OFFSET: Record<number, number> = {
    [SyntaxKind.EqualsEqualsToken]: 0,
    [SyntaxKind.ExclamationEqualsToken]: 1,
    [SyntaxKind.LessThanToken]: 2,
    [SyntaxKind.GreaterThanEqualsToken]: 3,
    [SyntaxKind.GreaterThanToken]: 4,
    [SyntaxKind.LessThanEqualsToken]: 5,
  };
  const NEGATED = [1, 0, 3, 2, 5, 4]; // negation of each comparison offset

  // Emit a branch to `label` taken when `expr` is true (whenTrue) or false.
  const emitBranch = (expr: Node, label: Label, whenTrue: boolean): void => {
    switch (expr.kind) {
      case SyntaxKind.ParenthesizedExpression:
        emitBranch((expr as unknown as { expression: Node }).expression, label, whenTrue);
        return;
      case SyntaxKind.PrefixUnaryExpression: {
        const u = expr as PrefixUnaryExpression;
        if (u.operator === SyntaxKind.ExclamationToken) {
          emitBranch(u.operand, label, !whenTrue);
          return;
        }
        break;
      }
      case SyntaxKind.BinaryExpression: {
        const b = expr as BinaryExpression;
        const op = b.operatorToken;
        if (op === SyntaxKind.AmpersandAmpersandToken) {
          if (whenTrue) {
            const skip = newLabel();
            emitBranch(b.left, skip, false);
            emitBranch(b.right, label, true);
            placeLabel(skip);
          } else {
            emitBranch(b.left, label, false);
            emitBranch(b.right, label, false);
          }
          return;
        }
        if (op === SyntaxKind.BarBarToken) {
          if (whenTrue) {
            emitBranch(b.left, label, true);
            emitBranch(b.right, label, true);
          } else {
            const skip = newLabel();
            emitBranch(b.left, skip, true);
            emitBranch(b.right, label, false);
            placeLabel(skip);
          }
          return;
        }
        const offset = COMPARE_OFFSET[op];
        if (offset !== undefined) {
          const isEquality =
            op === SyntaxKind.EqualsEqualsToken || op === SyntaxKind.ExclamationEqualsToken;
          const isNull = (n: Node): boolean => n.kind === SyntaxKind.NullKeyword;
          const lc = numericCategory(checker.getTypeOfExpression(b.left));
          const rc = numericCategory(checker.getTypeOfExpression(b.right));
          if (isEquality && (isNull(b.left) || isNull(b.right))) {
            emitExpr(isNull(b.left) ? b.right : b.left);
            const eq = op === SyntaxKind.EqualsEqualsToken;
            pop(1); // objectref consumed by the branch
            branchTo(eq === whenTrue ? OP_IFNULL : OP_IFNONNULL, label);
            return;
          }
          if (isEquality && !lc && !rc) {
            emitExpr(b.left);
            emitExpr(b.right);
            const eq = op === SyntaxKind.EqualsEqualsToken;
            pop(2);
            branchTo(eq === whenTrue ? OP_IF_ACMPEQ : OP_IF_ACMPNE, label);
            return;
          }
          if (lc === "I" && rc === "I") {
            emitExpr(b.left);
            emitExpr(b.right);
            pop(2);
            branchTo(OP_IF_ICMPEQ + (whenTrue ? offset : NEGATED[offset]!), label);
            return;
          }
          if (lc && rc) {
            // long/float/double: cmp leaves -1/0/1, then branch against 0.
            const t = promote(lc, rc);
            emitOperand(b.left, t);
            emitOperand(b.right, t);
            // For < and <=, use the "g" variant so NaN compares as greater.
            const g = op === SyntaxKind.LessThanToken || op === SyntaxKind.LessThanEqualsToken;
            code.u1(
              t === "J" ? OP_LCMP : t === "F" ? (g ? OP_FCMPG : OP_FCMPL) : g ? OP_DCMPG : OP_DCMPL,
            );
            pop(2); // two operands -> one int result, then consumed by the branch
            branchTo(OP_IFEQ + (whenTrue ? offset : NEGATED[offset]!), label);
            return;
          }
          throw new UnsupportedEmit();
        }
        break;
      }
      default:
        break;
    }
    // Fall back: evaluate a boolean value and branch on zero/non-zero.
    emitExpr(expr);
    pop(1); // the value is consumed by the branch
    branchTo(OP_IFEQ + (whenTrue ? 1 : 0), label); // ifne when true, ifeq when false
  };

  const isBooleanOperator = (op: SyntaxKind): boolean =>
    op === SyntaxKind.AmpersandAmpersandToken ||
    op === SyntaxKind.BarBarToken ||
    COMPARE_OFFSET[op] !== undefined;

  // Materialize a boolean expression as an int 0/1 on the stack, via the standard
  // branch-and-push pattern. The merge label carries one int on the stack.
  const emitBoolean = (expr: Node): string => {
    const trueL = newLabel();
    const contL = newLabel();
    emitBranch(expr, trueL, true);
    code.u1(OP_ICONST_0);
    push("I");
    branchTo(OP_GOTO, contL);
    pop(); // the false-path value is not on the stack when the true label is reached
    placeLabel(trueL);
    code.u1(OP_ICONST_1);
    push("I");
    placeLabel(contL); // both paths converge with one int atop the entry stack
    return "Z";
  };

  // i++ / ++i / i-- / --i on an int local -> iinc.
  const emitIncrement = (expr: Node): void => {
    const u = expr as unknown as { operator: SyntaxKind; operand: Node };
    if (u.operator !== SyntaxKind.PlusPlusToken && u.operator !== SyntaxKind.MinusMinusToken) {
      throw new UnsupportedEmit();
    }
    if (u.operand.kind !== SyntaxKind.Identifier) throw new UnsupportedEmit();
    const symbol = checker.resolveName(u.operand as Identifier);
    const local = symbol ? locals.get(symbol) : undefined;
    if (!local || category(local.descriptor) !== "I") throw new UnsupportedEmit();
    code.u1(OP_IINC);
    code.u1(local.slot);
    code.u1((u.operator === SyntaxKind.PlusPlusToken ? 1 : -1) & 0xff);
  };

  // An expression used as a statement (its value, if any, is discarded).
  const emitStatementExpression = (expr: Node): void => {
    if (expr.kind === SyntaxKind.PostfixUnaryExpression) {
      emitIncrement(expr);
      return;
    }
    if (expr.kind === SyntaxKind.PrefixUnaryExpression) {
      const u = expr as PrefixUnaryExpression;
      if (u.operator === SyntaxKind.PlusPlusToken || u.operator === SyntaxKind.MinusMinusToken) {
        emitIncrement(expr);
        return;
      }
    }
    if (expr.kind === SyntaxKind.AssignmentExpression) {
      emitAssignStatement(expr as AssignmentExpression);
      return;
    }
    const desc = emitExpr(expr);
    if (desc !== "V") {
      code.u1(slotsOf(desc) === 2 ? OP_POP2 : OP_POP);
      pop(); // discard the unused value
    }
  };

  // Returns true if the statement is a definite terminator (return).
  const emitStmt = (stmt: Node): boolean => {
    switch (stmt.kind) {
      case SyntaxKind.Block: {
        return inScope(() => {
          let terminated = false;
          for (const s of (stmt as unknown as { statements: readonly Node[] }).statements) {
            terminated = emitStmt(s);
          }
          return terminated;
        });
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
          activeLocals.push(descriptor);
          if (declarator.initializer) {
            const rd = emitExpr(declarator.initializer);
            coerce(rd, descriptor);
            storeVar(slot, descriptor);
          }
        }
        return false;
      }
      case SyntaxKind.ExpressionStatement:
        emitStatementExpression((stmt as ExpressionStatement).expression);
        return false;
      case SyntaxKind.ReturnStatement: {
        const expr = (stmt as ReturnStatement).expression;
        // Widen the value to the return type (JLS 5.2 assignment context), e.g.
        // `double f(long x){ return x; }` needs an l2d before dreturn.
        if (expr) coerce(emitExpr(expr), returnDescriptor);
        emitReturn();
        return true;
      }
      case SyntaxKind.IfStatement: {
        const s = stmt as IfStatement;
        if (s.elseStatement) {
          const elseL = newLabel();
          const endL = newLabel();
          emitBranch(s.condition, elseL, false);
          const thenTerm = inScope(() => emitStmt(s.thenStatement));
          if (!thenTerm) branchTo(OP_GOTO, endL);
          placeLabel(elseL);
          const elseTerm = inScope(() => emitStmt(s.elseStatement!));
          const terminated = thenTerm && elseTerm;
          if (!terminated) placeLabel(endL);
          return terminated;
        }
        const endL = newLabel();
        emitBranch(s.condition, endL, false);
        inScope(() => emitStmt(s.thenStatement));
        placeLabel(endL);
        return false;
      }
      case SyntaxKind.WhileStatement: {
        const s = stmt as WhileStatement;
        const startL = newLabel();
        const endL = newLabel();
        placeLabel(startL);
        emitBranch(s.condition, endL, false);
        inScope(() => emitStmt(s.statement));
        branchTo(OP_GOTO, startL);
        placeLabel(endL);
        return false;
      }
      case SyntaxKind.DoStatement: {
        const s = stmt as DoStatement;
        const startL = newLabel();
        placeLabel(startL);
        inScope(() => emitStmt(s.statement));
        emitBranch(s.condition, startL, true);
        return false;
      }
      case SyntaxKind.ForStatement: {
        const s = stmt as ForStatement;
        return inScope(() => {
          if (s.initializer) emitStmt(s.initializer);
          for (const e of s.initializerExpressions ?? []) emitStatementExpression(e);
          const startL = newLabel();
          const endL = newLabel();
          placeLabel(startL);
          if (s.condition) emitBranch(s.condition, endL, false);
          inScope(() => emitStmt(s.statement));
          for (const e of s.incrementors ?? []) emitStatementExpression(e);
          branchTo(OP_GOTO, startL);
          placeLabel(endL);
          return false;
        });
      }
      default:
        throw new UnsupportedEmit();
    }
  };

  if (!method.body || method.body.kind !== SyntaxKind.Block) throw new UnsupportedEmit();
  if (isConstructor && ctorSuper) {
    // Implicit super(): aload_0; invokespecial <super>.<init>:()V. (An explicit
    // super()/this() in the body is not handled and triggers the fallback.)
    code.u1(OP_ALOAD_0);
    pushRef();
    code.u1(OP_INVOKESPECIAL);
    code.u2(cp.methodref(ctorSuper, "<init>", "()V"));
    pop(1);
  }
  for (const fi of fieldInits) {
    if (fi.isStatic) {
      coerce(emitExpr(fi.init), fi.descriptor);
      code.u1(OP_PUTSTATIC);
      code.u2(cp.fieldref(fi.owner, fi.name, fi.descriptor));
      pop(); // value
    } else {
      code.u1(OP_ALOAD_0);
      pushRef();
      coerce(emitExpr(fi.init), fi.descriptor);
      code.u1(OP_PUTFIELD);
      code.u2(cp.fieldref(fi.owner, fi.name, fi.descriptor));
      pop(2); // receiver + value
    }
  }
  const terminated = emitStmt(method.body);
  if (!terminated) {
    if (returnDescriptor === "V") code.u1(OP_RETURN);
    else throw new UnsupportedEmit(); // non-void path falls off the end
  }

  // Backpatch branch offsets (signed, relative to the branch opcode address).
  for (const { at, from, label } of fixups) {
    if (label.offset < 0) throw new UnsupportedEmit(); // label never placed
    code.patchU2(at, (label.offset - from) & 0xffff);
  }

  // StackMapTable: a full_frame at every branch-target offset (JVMS 4.7.4).
  const targetOffsets = [...new Set(fixups.map(f => f.label.offset))].sort((a, b) => a - b);
  let stackMapTable: ByteBuffer | undefined;
  if (targetOffsets.length > 0) {
    const writeVerification = (buf: ByteBuffer, descriptor: string): void => {
      const c = category(descriptor);
      if (c === "I") buf.u1(ITEM_Integer);
      else if (c === "F") buf.u1(ITEM_Float);
      else if (c === "D") buf.u1(ITEM_Double);
      else if (c === "J") buf.u1(ITEM_Long);
      else {
        const internal = descriptor[0] === "[" ? descriptor : descriptor.slice(1, -1);
        buf.u1(ITEM_Object);
        buf.u2(cp.classInfo(internal));
      }
    };
    stackMapTable = new ByteBuffer();
    stackMapTable.u2(targetOffsets.length);
    let previous = -1;
    for (const offset of targetOffsets) {
      const frame = frameAt.get(offset)!;
      stackMapTable.u1(FULL_FRAME);
      stackMapTable.u2(previous < 0 ? offset : offset - previous - 1);
      stackMapTable.u2(frame.locals.length);
      for (const d of frame.locals) writeVerification(stackMapTable, d);
      stackMapTable.u2(frame.stack.length);
      for (const d of frame.stack) writeVerification(stackMapTable, d);
      previous = offset;
    }
  }

  return { code, maxStack, maxLocals, stackMapTable };
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

  let body: MethodBody;
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

  writeCodeAttribute(info, cp, body);
  return info;
}

interface MethodBody {
  code: ByteBuffer;
  maxStack: number;
  maxLocals: number;
  stackMapTable?: ByteBuffer;
}

// Append the Code attribute (with an optional StackMapTable sub-attribute) and
// set the method's attributes_count to 1.
function writeCodeAttribute(info: ByteBuffer, cp: ConstantPool, body: MethodBody): void {
  const smt = body.stackMapTable;
  const smtBytes = smt ? 6 + smt.length : 0;

  const codeAttr = new ByteBuffer();
  codeAttr.u2(cp.utf8("Code"));
  codeAttr.u4(12 + body.code.length + smtBytes);
  codeAttr.u2(body.maxStack);
  codeAttr.u2(body.maxLocals);
  codeAttr.u4(body.code.length);
  codeAttr.append(body.code);
  codeAttr.u2(0); // exception_table_length
  codeAttr.u2(smt ? 1 : 0); // attributes_count
  if (smt) {
    codeAttr.u2(cp.utf8("StackMapTable"));
    codeAttr.u4(smt.length);
    codeAttr.append(smt);
  }

  info.u2(1); // attributes_count
  info.append(codeAttr);
}

function emitConstructorMethod(
  ctor: ConstructorDeclaration,
  flags: number,
  cp: ConstantPool,
  program: Program,
  checker: Checker,
  thisInternalName: string,
  superInternalName: string,
  instanceInits: FieldInit[],
): ByteBuffer {
  const descriptor = `(${ctor.parameters.map(p => paramDescriptor(p as Parameter, program)).join("")})V`;
  const info = new ByteBuffer();
  info.u2(flags);
  info.u2(cp.utf8("<init>"));
  info.u2(cp.utf8(descriptor));

  let body: MethodBody;
  try {
    body = generateBody(
      ctor,
      cp,
      program,
      checker,
      thisInternalName,
      superInternalName,
      instanceInits,
    );
  } catch (e) {
    if (!(e instanceof UnsupportedEmit)) throw e;
    // Fallback: a valid constructor that just calls super() (body skipped).
    const argsSize =
      1 +
      ctor.parameters.reduce((n, p) => n + slotsOf(paramDescriptor(p as Parameter, program)), 0);
    const code = new ByteBuffer();
    code.u1(OP_ALOAD_0);
    code.u1(OP_INVOKESPECIAL);
    code.u2(cp.methodref(superInternalName, "<init>", "()V"));
    code.u1(OP_RETURN);
    body = { code, maxStack: 1, maxLocals: argsSize };
  }

  writeCodeAttribute(info, cp, body);
  return info;
}

export interface EmittedClass {
  /** Internal/binary name, e.g. "com/app/Foo" (becomes <name>.class under outdir). */
  readonly name: string;
  readonly bytes: Uint8Array;
}

// Resolve a type reference to its internal name, falling back to its written
// (dotted -> slashed) form when it does not resolve.
function resolveInternalName(
  typeNode: TypeNode | undefined,
  from: Node,
  program: Program,
): string | undefined {
  if (!typeNode || typeNode.kind !== SyntaxKind.TypeReference) return undefined;
  const ref = typeNode as TypeReference;
  const symbol = resolveTypeEntityName(ref.typeName, from, program);
  return symbol ? binaryName(symbol) : entityNameToString(ref.typeName).replace(/\./g, "/");
}

export function emitClass(
  declaration: ClassDeclaration,
  program: Program,
  checker: Checker,
): EmittedClass {
  // Ensure the global index is built so symbols carry their package parent.
  program.getGlobalIndex();
  const name = declaration.symbol ? binaryName(declaration.symbol) : declaration.name.text;
  const superInternalName =
    resolveInternalName(declaration.extendsType, declaration, program) ?? "java/lang/Object";
  const interfaceNames = (declaration.implementsTypes ?? [])
    .map(t => resolveInternalName(t, declaration, program))
    .filter((n): n is string => n !== undefined);

  const accessFlags = classAccessFlags(declaration);
  const cp = new ConstantPool();
  const thisClassIndex = cp.classInfo(name);
  const superClassIndex = cp.classInfo(superInternalName);
  const interfaceIndices = interfaceNames.map(n => cp.classInfo(n));
  const fields = emitFields(declaration, cp, program);

  // Field initializers: instance ones run in each constructor, static ones in
  // <clinit>; static-final compile-time constants are excluded (ConstantValue).
  const instanceInits: FieldInit[] = [];
  const staticInits: FieldInit[] = [];
  for (const member of declaration.members) {
    if (member.kind !== SyntaxKind.FieldDeclaration) continue;
    const field = member as FieldDeclaration;
    const isStatic = isStaticDeclaration(field);
    const descriptor = descriptorOf(field.type, program);
    for (const declarator of field.declarators) {
      const d = declarator as VariableDeclarator;
      if (!d.initializer) continue;
      if (isStatic && isConstantValueField(field, d, program)) continue;
      (isStatic ? staticInits : instanceInits).push({
        isStatic,
        owner: name,
        name: d.name.text,
        descriptor,
        init: d.initializer,
      });
    }
  }

  // Constructors: the declared ones, or a synthesized default constructor (which
  // inherits the class's accessibility, JLS 8.8.9) when none are declared. Each
  // runs the instance field initializers.
  const methods = new ByteBuffer();
  let methodCount = 0;
  const declaredConstructors = declaration.members.filter(
    m => m.kind === SyntaxKind.ConstructorDeclaration,
  ) as ConstructorDeclaration[];
  if (declaredConstructors.length === 0) {
    const defaultCtor = {
      kind: SyntaxKind.ConstructorDeclaration,
      parameters: [],
      body: { kind: SyntaxKind.Block, statements: [] },
    } as unknown as ConstructorDeclaration;
    const flags = accessFlags & (ACC_PUBLIC | ACC_PROTECTED | ACC_PRIVATE);
    methods.append(
      emitConstructorMethod(
        defaultCtor,
        flags,
        cp,
        program,
        checker,
        name,
        superInternalName,
        instanceInits,
      ),
    );
    methodCount++;
  } else {
    for (const ctor of declaredConstructors) {
      methods.append(
        emitConstructorMethod(
          ctor,
          methodAccessFlags(ctor),
          cp,
          program,
          checker,
          name,
          superInternalName,
          instanceInits,
        ),
      );
      methodCount++;
    }
  }
  for (const member of declaration.members) {
    if (member.kind !== SyntaxKind.MethodDeclaration) continue;
    methods.append(emitMethod(member as MethodDeclaration, cp, program, checker, name));
    methodCount++;
  }

  // Static field initializers -> <clinit>.
  if (staticInits.length > 0) {
    const clinit = {
      kind: SyntaxKind.MethodDeclaration,
      modifiers: [{ kind: SyntaxKind.StaticKeyword }],
      parameters: [],
      returnType: { kind: SyntaxKind.PrimitiveType, keyword: SyntaxKind.VoidKeyword },
      name: { text: "<clinit>" },
      body: { kind: SyntaxKind.Block, statements: [] },
    } as unknown as MethodDeclaration;
    const info = new ByteBuffer();
    info.u2(ACC_STATIC);
    info.u2(cp.utf8("<clinit>"));
    info.u2(cp.utf8("()V"));
    writeCodeAttribute(
      info,
      cp,
      generateBody(clinit, cp, program, checker, name, undefined, staticInits),
    );
    methods.append(info);
    methodCount++;
  }

  // Class attributes, built before writeInto so any new Utf8 names land in the
  // pool. BootstrapMethods carries the invokedynamic targets for string concat.
  const classAttributes = new ByteBuffer();
  let classAttributeCount = 0;
  if (cp.bootstrapMethodCount > 0) {
    const nameIndex = cp.utf8("BootstrapMethods");
    const body = cp.bootstrapMethodsBody();
    classAttributes.u2(nameIndex);
    classAttributes.u4(body.length);
    classAttributes.append(body);
    classAttributeCount++;
  }

  const out = new ByteBuffer();
  out.u4(MAGIC);
  out.u2(MINOR_VERSION);
  out.u2(MAJOR_VERSION);
  cp.writeInto(out);
  out.u2(accessFlags);
  out.u2(thisClassIndex);
  out.u2(superClassIndex);
  out.u2(interfaceIndices.length);
  for (const index of interfaceIndices) out.u2(index);
  out.u2(fields.count);
  out.append(fields.buffer);
  out.u2(methodCount);
  out.append(methods);
  out.u2(classAttributeCount);
  out.append(classAttributes);

  return { name, bytes: out.toUint8Array() };
}
