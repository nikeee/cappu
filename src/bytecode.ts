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
import { forEachChild } from "./parser.ts";
import type { Program } from "./program.ts";
import { resolveTypeEntityName } from "./resolver.ts";
import { entityNameToString, tokenToString } from "./utilities.ts";
import {
  type ArrayType as AstArrayType,
  type ArrayCreationExpression,
  type ArrayInitializer,
  type ElementAccessExpression,
  type ForEachStatement,
  type AssignmentExpression,
  type BinaryExpression,
  type CallExpression,
  type CastExpression,
  type ConditionalExpression,
  type LambdaExpression,
  type MethodReferenceExpression,
  type ClassDeclaration,
  type ConstructorDeclaration,
  type EnumDeclaration,
  type BreakStatement,
  type ContinueStatement,
  type LabeledStatement,
  type ThrowStatement,
  type TryStatement,
  type Block,
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
  type SwitchStatement,
  type SwitchClause,
  type SwitchExpression,
  type YieldStatement,
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
const ACC_SYNTHETIC = 0x1000;
const ACC_VARARGS = 0x0080;
const ACC_ENUM = 0x4000;

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
const CONSTANT_MethodType = 16;
const CONSTANT_InvokeDynamic = 18;
const REF_invokeVirtual = 5; // MethodHandle reference_kind (JVMS 4.4.8)
const REF_invokeStatic = 6;
const REF_invokeSpecial = 7;
const REF_newInvokeSpecial = 8;
const REF_invokeInterface = 9;

// java.lang.invoke.StringConcatFactory.makeConcatWithConstants bootstrap (JLS 15.18.1).
const STRING_CONCAT_FACTORY = "java/lang/invoke/StringConcatFactory";
const MAKE_CONCAT = "makeConcatWithConstants";
const MAKE_CONCAT_BSM_DESCRIPTOR =
  "(Ljava/lang/invoke/MethodHandles$Lookup;Ljava/lang/String;Ljava/lang/invoke/MethodType;Ljava/lang/String;[Ljava/lang/Object;)Ljava/lang/invoke/CallSite;";
// java.lang.invoke.LambdaMetafactory.metafactory bootstrap (JLS 15.27 lambdas).
const LAMBDA_METAFACTORY = "java/lang/invoke/LambdaMetafactory";
const LAMBDA_METAFACTORY_BSM_DESCRIPTOR =
  "(Ljava/lang/invoke/MethodHandles$Lookup;Ljava/lang/String;Ljava/lang/invoke/MethodType;Ljava/lang/invoke/MethodType;Ljava/lang/invoke/MethodHandle;Ljava/lang/invoke/MethodType;)Ljava/lang/invoke/CallSite;";
const OP_INVOKEDYNAMIC = 0xba;

// Boxing/unboxing (JLS 5.1.7/5.1.8): primitive descriptor -> wrapper internal
// name (Xxx.valueOf), and wrapper internal name -> [unboxing method, primitive].
const WRAPPER: Record<string, string> = {
  Z: "java/lang/Boolean",
  B: "java/lang/Byte",
  S: "java/lang/Short",
  C: "java/lang/Character",
  I: "java/lang/Integer",
  J: "java/lang/Long",
  F: "java/lang/Float",
  D: "java/lang/Double",
};
const UNBOX: Record<string, readonly [string, string]> = {
  "java/lang/Boolean": ["booleanValue", "Z"],
  "java/lang/Byte": ["byteValue", "B"],
  "java/lang/Short": ["shortValue", "S"],
  "java/lang/Character": ["charValue", "C"],
  "java/lang/Integer": ["intValue", "I"],
  "java/lang/Long": ["longValue", "J"],
  "java/lang/Float": ["floatValue", "F"],
  "java/lang/Double": ["doubleValue", "D"],
};

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
const OP_DUP2 = 0x5c;
const OP_NEW = 0xbb;
const OP_NEWARRAY = 0xbc;
const OP_ANEWARRAY = 0xbd;
const OP_ARRAYLENGTH = 0xbe;
const OP_ATHROW = 0xbf;
const OP_MULTIANEWARRAY = 0xc5;
// Array load/store base opcodes; per-element variants are at a fixed offset
// (see arrayElemOffset). AASTORE is used directly for the enum $VALUES array.
const OP_IALOAD = 0x2e;
const OP_IASTORE = 0x4f;
const OP_AASTORE = 0x53;
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
const OP_TABLESWITCH = 0xaa;
const OP_LOOKUPSWITCH = 0xab;
const OP_IFNULL = 0xc6;
const OP_IFNONNULL = 0xc7;
const OP_ALOAD_0 = 0x2a;
const OP_RETURN = 0xb1;

// StackMapTable verification_type_info tags (JVMS 4.7.4).
const ITEM_Top = 0;
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
  // Overwrite a previously-reserved u4 (for tableswitch/lookupswitch offsets).
  patchU4(pos: number, value: number): void {
    this.bytes[pos] = (value >>> 24) & 0xff;
    this.bytes[pos + 1] = (value >>> 16) & 0xff;
    this.bytes[pos + 2] = (value >>> 8) & 0xff;
    this.bytes[pos + 3] = value & 0xff;
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

  private intern(key: string, write: (b: ByteBuffer) => void, wide = false): number {
    const existing = this.cache.get(key);
    if (existing !== undefined) return existing;
    write(this.entries);
    const index = ++this.count;
    if (wide) this.count++; // long/double occupy a second, unusable slot (JVMS 4.4.5)
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
    return this.intern(
      `l:${value}`,
      b => {
        b.u1(CONSTANT_Long);
        b.u4(Number((value >> 32n) & 0xffffffffn));
        b.u4(Number(value & 0xffffffffn));
      },
      true,
    );
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
    return this.intern(
      `d:${view.getUint32(0)}:${view.getUint32(4)}`,
      b => {
        b.u1(CONSTANT_Double);
        b.u4(view.getUint32(0));
        b.u4(view.getUint32(4));
      },
      true,
    );
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

  private methodType(descriptor: string): number {
    const descIndex = this.utf8(descriptor);
    return this.intern(`mt:${descriptor}`, b => {
      b.u1(CONSTANT_MethodType);
      b.u2(descIndex);
    });
  }

  /**
   * An invokedynamic that builds a lambda via LambdaMetafactory.metafactory. The
   * call-site name is the SAM name; `indyDescriptor` takes the captured values and
   * returns the functional interface. `samErased` / `instantiated` are the SAM's
   * method type erased and as instantiated; `impl*` names the synthetic method
   * holding the lambda body. Returns the CONSTANT_InvokeDynamic index.
   */
  invokeDynamicLambda(
    samName: string,
    indyDescriptor: string,
    samErased: string,
    implRefKind: number,
    implOwner: string,
    implName: string,
    implDescriptor: string,
    instantiated: string,
    implIsInterface = false,
  ): number {
    const bsmHandle = this.methodHandle(
      REF_invokeStatic,
      this.methodref(LAMBDA_METAFACTORY, "metafactory", LAMBDA_METAFACTORY_BSM_DESCRIPTOR),
    );
    // The impl reference: a constructor (<init>) or a normal method, on a class
    // or an interface.
    const implRef =
      implName === "<init>"
        ? this.methodref(implOwner, implName, implDescriptor)
        : implIsInterface
          ? this.interfaceMethodref(implOwner, implName, implDescriptor)
          : this.methodref(implOwner, implName, implDescriptor);
    const implHandle = this.methodHandle(implRefKind, implRef);
    const args = [this.methodType(samErased), implHandle, this.methodType(instantiated)];
    const bootstrapIndex = this.bootstraps.length;
    this.bootstraps.push({ handle: bsmHandle, args });
    const nt = this.nameAndType(samName, indyDescriptor);
    return this.intern(`indy:${bootstrapIndex}:${samName}:${indyDescriptor}`, b => {
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
// The source file's base name (for the SourceFile attribute), or undefined.
function sourceNameOf(node: Node): string | undefined {
  let n: Node | undefined = node;
  while (n && n.kind !== SyntaxKind.SourceFile) n = n.parent;
  const fileName = (n as { fileName?: string } | undefined)?.fileName;
  return fileName ? fileName.split("/").pop() || undefined : undefined;
}

// The class-level attributes shared by classes and enums: SourceFile and (when
// any invokedynamic was emitted) BootstrapMethods. Must run before the constant
// pool is written so the attribute name Utf8s are interned.
function buildClassAttributes(
  cp: ConstantPool,
  sourceName: string | undefined,
): { buffer: ByteBuffer; count: number } {
  const buffer = new ByteBuffer();
  let count = 0;
  if (sourceName) {
    buffer.u2(cp.utf8("SourceFile"));
    buffer.u4(2);
    buffer.u2(cp.utf8(sourceName));
    count++;
  }
  if (cp.bootstrapMethodCount > 0) {
    buffer.u2(cp.utf8("BootstrapMethods"));
    const body = cp.bootstrapMethodsBody();
    buffer.u4(body.length);
    buffer.append(body);
    count++;
  }
  return { buffer, count };
}

// Field initializers split by static-ness: instance ones run in each
// constructor, static ones in <clinit>. static-final compile-time constants are
// excluded (they carry a ConstantValue attribute instead). Shared by classes
// and enums.
function collectFieldInits(
  members: readonly Node[],
  ownerName: string,
  program: Program,
): { instanceInits: FieldInit[]; staticInits: FieldInit[] } {
  const instanceInits: FieldInit[] = [];
  const staticInits: FieldInit[] = [];
  for (const member of members) {
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
        owner: ownerName,
        name: d.name.text,
        descriptor,
        init: d.initializer,
      });
    }
  }
  return { instanceInits, staticInits };
}

// Assemble a class file from its parts (the constant pool must be fully
// populated, including attribute-name Utf8s, before this runs). Shared by
// classes and enums.
function assembleClassFile(parts: {
  cp: ConstantPool;
  accessFlags: number;
  thisClassIndex: number;
  superClassIndex: number;
  interfaceIndices: number[];
  fields: ByteBuffer;
  fieldCount: number;
  methods: ByteBuffer;
  methodCount: number;
  attributes: ByteBuffer;
  attributeCount: number;
}): Uint8Array {
  const out = new ByteBuffer();
  out.u4(MAGIC);
  out.u2(MINOR_VERSION);
  out.u2(MAJOR_VERSION);
  parts.cp.writeInto(out);
  out.u2(parts.accessFlags);
  out.u2(parts.thisClassIndex);
  out.u2(parts.superClassIndex);
  out.u2(parts.interfaceIndices.length);
  for (const index of parts.interfaceIndices) out.u2(index);
  out.u2(parts.fieldCount);
  out.append(parts.fields);
  out.u2(parts.methodCount);
  out.append(parts.methods);
  out.u2(parts.attributeCount);
  out.append(parts.attributes);
  return out.toUint8Array();
}

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
      // A type variable erases to its leftmost bound, or Object if unbounded
      // (JLS 4.6). Method/field descriptors are always over erased types.
      if (symbol && symbol.flags & SymbolFlags.TypeParameter) return "Ljava/lang/Object;";
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

// A resolved field/enum-constant reference: where it lives and its descriptor.
interface FieldInfo {
  owner: string;
  name: string;
  descriptor: string;
  isStatic: boolean;
}

// A local variable / parameter slot and its descriptor (long/double take two
// slots but one entry).
interface LocalSlot {
  slot: number;
  descriptor: string;
}

// One Code-attribute exception_table entry (JVMS 4.7.3). catchType 0 is a
// catch-all (used for finally).
interface ExceptionTableEntry {
  start: number;
  end: number;
  handler: number;
  catchType: number;
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

// A synthetic method holding a lambda body. `params` are the captured outer
// locals followed by the lambda's own parameters. When `isInstance`, it is a
// private instance method (the lambda captured `this`); otherwise private static.
interface LambdaImpl {
  name: string;
  params: { symbol: Symbol; descriptor: string }[];
  returnDescriptor: string;
  body: Node;
  isInstance: boolean;
}

// The data the enum <clinit> needs: how to construct each constant and the
// $VALUES array of all of them.
interface EnumClinit {
  enumInternal: string;
  selfDesc: string; // L<enum>;
  arrayDesc: string; // [L<enum>;
  valuesField: string; // synthetic $VALUES field name
  constants: {
    name: string;
    ordinal: number;
    ctorDescriptor: string; // (Ljava/lang/String;I<userparams>)V
    userParamDescs: string[];
    args: Node[];
  }[];
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
  // Sink for synthetic lambda-body methods: each lambda encountered is emitted
  // eagerly (so a failure falls back this whole method) and its method_info is
  // appended here for emitClass to add to the class.
  lambdaMethods: ByteBuffer[] = [],
  // When set, emit a synthetic lambda body instead of a declared method: the
  // params (captures, then the lambda's own params) and return type are given,
  // and `body` is the lambda's expression or block.
  lambdaSpec?: {
    params: { symbol: Symbol; descriptor: string }[];
    returnDescriptor: string;
    body: Node;
    isInstance: boolean;
  },
  // Enum constructor: the synthetic (String name, int ordinal) leading parameters
  // are reserved (slots 1,2) and super(name, ordinal) calls java.lang.Enum.
  enumCtor = false,
  // Enum <clinit>: construct each constant and the $VALUES array before the
  // static field initializers.
  enumClinit?: EnumClinit,
): MethodBody {
  const isConstructor = !lambdaSpec && method.kind === SyntaxKind.ConstructorDeclaration;
  const isStatic = lambdaSpec
    ? !lambdaSpec.isInstance
    : !isConstructor && (methodAccessFlags(method as MethodDeclaration) & ACC_STATIC) !== 0;
  const returnDescriptor = lambdaSpec
    ? lambdaSpec.returnDescriptor
    : isConstructor
      ? "V"
      : descriptorOf((method as MethodDeclaration).returnType, program);
  // Name used for synthetic lambda methods declared in this body: lambda$<m>$<n>.
  const enclosingName = lambdaSpec
    ? "lambda"
    : isConstructor
      ? "new"
      : (method as MethodDeclaration).name.text;
  let lambdaCounter = 0;

  // Slots for parameters and (as they are declared) locals; shared map keyed by
  // the declaration symbol.
  const locals = new Map<Symbol, LocalSlot>();
  // Locals currently in scope, in slot order, for stack-map frames (this, then
  // params, then declared locals; long/double = one entry). Each carries its
  // slot so frames can mark a not-yet-assigned local as `top` (see `assigned`).
  const TOP = " top"; // sentinel descriptor for an unassigned slot
  const activeLocals: LocalSlot[] = [];
  // Slots that are definitely assigned at the current point (JLS 16). A frame
  // lists the real type for an assigned slot and `top` otherwise; the set is
  // intersected across the paths that reach each branch target.
  const assigned = new Set<number>();
  let reachable = true; // is the next instruction reachable by fall-through?
  let nextSlot = isStatic ? 0 : 1;
  if (!isStatic) {
    activeLocals.push({ slot: 0, descriptor: `L${thisInternalName};` });
    assigned.add(0);
  }
  // Parameters: a lambda impl's captures + own params, or the method's params.
  // An enum constructor has two synthetic leading parameters (name, ordinal).
  const params: { symbol?: Symbol; descriptor: string }[] = lambdaSpec
    ? lambdaSpec.params
    : [
        ...(enumCtor ? [{ descriptor: "Ljava/lang/String;" }, { descriptor: "I" }] : []),
        ...method.parameters.map(p => ({
          symbol: p.symbol,
          descriptor: paramDescriptor(p as Parameter, program),
        })),
      ];
  for (const p of params) {
    if (p.symbol) locals.set(p.symbol, { slot: nextSlot, descriptor: p.descriptor });
    activeLocals.push({ slot: nextSlot, descriptor: p.descriptor });
    assigned.add(nextSlot);
    nextSlot += slotsOf(p.descriptor);
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
    for (const slot of [...assigned]) if (slot >= savedSlot) assigned.delete(slot); // freed slots
    nextSlot = savedSlot;
    return terminated;
  };

  // The frame's locals: in-scope locals with their type, or `top` if the slot is
  // not in `assignedSet`. Trailing tops are trimmed (the javac convention).
  const frameLocals = (assignedSet: Set<number>): string[] => {
    const out = activeLocals.map(e => (assignedSet.has(e.slot) ? e.descriptor : TOP));
    while (out.length > 0 && out[out.length - 1] === TOP) out.pop();
    return out;
  };
  const intersect = (a: Set<number>, b: Set<number>): Set<number> => {
    const r = new Set<number>();
    for (const x of a) if (b.has(x)) r.add(x);
    return r;
  };

  // --- labels, branches and stack-map frames ---------------------------------------
  interface Label {
    offset: number; // resolved when placed
    targetStack?: string[]; // operand stack as seen at the branch target (recorded by branchTo)
    assignedAtTarget?: Set<number>; // slots assigned on every branch path to here
  }
  interface Frame {
    locals: string[];
    stack: string[];
  }
  const frameAt = new Map<number, Frame>(); // offset -> frame snapshot
  const fixups: { at: number; from: number; label: Label }[] = []; // u2 branch offsets
  const wideFixups: { at: number; from: number; label: Label }[] = []; // u4 switch offsets
  // try/catch handlers: exception_table entries and the handler offsets that
  // also need a stack-map frame (entered with the exception on the stack).
  const exceptionTable: ExceptionTableEntry[] = [];
  const handlerOffsets: number[] = [];
  // break/continue carry the finally depth at the loop/switch, so a jump out of
  // a try runs the intervening finally blocks first.
  // names: the labels of an enclosing labeled statement (JLS 14.7), so a
  // `break label` / `continue label` resolves to the matching target.
  const breakTargets: { label: Label; finallyDepth: number; names?: string[] }[] = [];
  const continueTargets: { label: Label; finallyDepth: number; names?: string[] }[] = [];
  // Labels declared just above a loop, consumed by that loop's targets.
  const pendingLabels: string[] = [];
  const takePending = (): string[] => pendingLabels.splice(0, pendingLabels.length);
  const yieldTargets: { label: Label; desc: string }[] = []; // enclosing switch-expression ends
  const finallyStack: Node[] = []; // enclosing finally blocks, innermost last
  const newLabel = (): Label => ({ offset: -1 });
  // A branch target's frame is defined by the operand stack on the branch-taken
  // path, which can differ from the live stack at the label site (e.g. when the
  // fall-through arrives after a terminator). branchTo records it; placeLabel
  // prefers it, falling back to the live stack for fall-through-only labels.
  const placeLabel = (label: Label): void => {
    label.offset = code.length;
    // Assignment state here: the branches' intersection, further intersected
    // with the fall-through state when the previous instruction can fall in.
    const here =
      label.assignedAtTarget === undefined
        ? new Set(assigned)
        : reachable
          ? intersect(label.assignedAtTarget, assigned)
          : new Set(label.assignedAtTarget);
    assigned.clear();
    for (const s of here) assigned.add(s);
    reachable = true;
    frameAt.set(label.offset, {
      locals: frameLocals(here),
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
    label.assignedAtTarget =
      label.assignedAtTarget === undefined
        ? new Set(assigned)
        : intersect(label.assignedAtTarget, assigned);
    if (op === OP_GOTO) reachable = false; // an unconditional jump does not fall through
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
  // Replace the live stack (e.g. at a switch clause boundary, where the previous
  // clause may have ended on a terminator and left dead values behind).
  const setStack = (to: string[]): void => {
    stack.length = 0;
    stack.push(...to);
  };

  // Numeric category of a descriptor: I (byte/char/short/boolean/int), J, F, D,
  // or A (reference). Used for promotion and conversion.
  const category = (descriptor: string): string => {
    const c = descriptor[0];
    return c === "J" || c === "D" || c === "F" ? c : c === "L" || c === "[" ? "A" : "I";
  };

  // Box a primitive (already on the stack) to its wrapper: Xxx.valueOf.
  const box = (prim: string): void => {
    const w = WRAPPER[prim];
    if (!w) return;
    code.u1(OP_INVOKESTATIC);
    code.u2(cp.methodref(w, "valueOf", `(${prim})L${w};`));
    pop();
    push(`L${w};`);
  };
  // Unbox a wrapper reference (already on the stack) to its primitive, returning
  // that primitive's descriptor (or undefined if `from` is not a wrapper).
  const unbox = (from: string): string | undefined => {
    if (from[0] !== "L") return undefined;
    const um = UNBOX[from.slice(1, -1)];
    if (!um) return undefined;
    code.u1(OP_INVOKEVIRTUAL);
    code.u2(cp.methodref(from.slice(1, -1), um[0], `()${um[1]}`));
    pop();
    push(um[1]);
    return um[1];
  };

  // Convert the value on top of the stack from `from` to `to` (JLS 5.1.2 widening,
  // 5.1.7 boxing, 5.1.8 unboxing), used for assignment, return and arguments.
  const coerce = (from: string, to: string): void => {
    if (from === to) return;
    const a = category(from);
    const b = category(to);
    // Boxing: a primitive into a reference target (its wrapper, or a supertype).
    if (a !== "A" && b === "A") {
      box(from);
      return;
    }
    // Unboxing: a wrapper into a primitive target, then widen the primitive.
    if (a === "A" && b !== "A") {
      const prim = unbox(from);
      if (prim !== undefined) coerce(prim, to);
      return;
    }
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
    assigned.add(varSlot); // the slot is now definitely assigned
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

  const fieldInfoOf = (symbol: Symbol): FieldInfo => {
    if (!symbol.parent) throw new UnsupportedEmit();
    // An enum constant is a public static final field of the enum, typed as the
    // enum itself.
    if (symbol.flags & SymbolFlags.EnumConstant) {
      const owner = binaryName(symbol.parent);
      return { owner, name: symbol.escapedName, descriptor: `L${owner};`, isStatic: true };
    }
    const declarator = symbol.valueDeclaration;
    if (!declarator || declarator.kind !== SyntaxKind.VariableDeclarator)
      throw new UnsupportedEmit();
    const field = declarator.parent as FieldDeclaration;
    if (field.kind !== SyntaxKind.FieldDeclaration) throw new UnsupportedEmit();
    return {
      owner: binaryName(symbol.parent),
      name: symbol.escapedName,
      descriptor: descriptorOf(field.type, program),
      isStatic: isStaticDeclaration(field),
    };
  };

  // Read a field: getstatic, or emit the receiver then getfield. `emitReceiver`
  // is only invoked for instance fields (skipped for statics, like javac).
  const emitFieldRead = (info: FieldInfo, emitReceiver: () => void): string => {
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
        pushRef("Ljava/lang/String;");
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
        pushRef(`L${thisInternalName};`);
        return `L${thisInternalName};`;
      case SyntaxKind.Identifier: {
        const symbol = checker.resolveName(node as Identifier);
        const local = symbol ? locals.get(symbol) : undefined;
        if (local) {
          loadVar(local.slot, local.descriptor);
          push(local.descriptor);
          return local.descriptor;
        }
        // A field or enum constant by its simple name: implicit `this.f` or a static.
        if (symbol && symbol.flags & (SymbolFlags.Field | SymbolFlags.EnumConstant)) {
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
        // arr.length -> arraylength (the implicit field of every array, JLS 10.7).
        if (
          access.name.text === "length" &&
          checker.getTypeOfExpression(access.expression).kind === TypeKind.Array
        ) {
          emitExpr(access.expression);
          code.u1(OP_ARRAYLENGTH);
          pop();
          push("I");
          return "I";
        }
        const symbol = checker.resolveName(access.name);
        if (!symbol || !(symbol.flags & (SymbolFlags.Field | SymbolFlags.EnumConstant)))
          throw new UnsupportedEmit();
        return emitFieldRead(fieldInfoOf(symbol), () => emitExpr(access.expression));
      }
      case SyntaxKind.ArrayCreationExpression:
        return emitArrayCreation(node as ArrayCreationExpression);
      case SyntaxKind.ElementAccessExpression:
        return emitElementAccess(node as ElementAccessExpression);
      case SyntaxKind.ConditionalExpression:
        return emitConditional(node as ConditionalExpression);
      case SyntaxKind.SwitchExpression:
        return emitSwitchExpression(node as SwitchExpression);
      case SyntaxKind.LambdaExpression:
        return emitLambda(node as LambdaExpression);
      case SyntaxKind.MethodReferenceExpression:
        return emitMethodRef(node);
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

  /**
   * A cast expression (JLS 15.16): a primitive cast is a narrowing/widening
   * conversion (JLS 5.5, 5.1.3); a reference cast is a `checkcast`.
   */
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

  /** The `instanceof` operator (JLS 15.20.2) -> the `instanceof` instruction. */
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

  // The implicit static enum methods E.values() / E.valueOf(String), which have
  // no source declaration. Returns the result descriptor, or undefined if the
  // call is not one of them.
  const emitEnumStaticCall = (call: CallExpression): string | undefined => {
    const callee = call.expression;
    if (callee.kind !== SyntaxKind.PropertyAccessExpression) return undefined;
    const access = callee as PropertyAccessExpression;
    if (access.expression.kind !== SyntaxKind.Identifier) return undefined;
    const recv = resolveTypeEntityName(access.expression as Identifier, access.expression, program);
    if (!recv || !(recv.flags & SymbolFlags.Enum)) return undefined;
    const enumInternal = binaryName(recv);
    const mname = access.name.text;
    if (mname === "values" && call.arguments.length === 0) {
      code.u1(OP_INVOKESTATIC);
      code.u2(cp.methodref(enumInternal, "values", `()[L${enumInternal};`));
      push(`[L${enumInternal};`);
      return `[L${enumInternal};`;
    }
    if (mname === "valueOf" && call.arguments.length === 1) {
      coerce(emitExpr(call.arguments[0]!), "Ljava/lang/String;");
      code.u1(OP_INVOKESTATIC);
      code.u2(cp.methodref(enumInternal, "valueOf", `(Ljava/lang/String;)L${enumInternal};`));
      pop(1);
      push(`L${enumInternal};`);
      return `L${enumInternal};`;
    }
    return undefined;
  };

  /**
   * A method invocation (JLS 15.12): the chosen overload (JLS 15.12.2) becomes
   * invokestatic / invokevirtual / invokeinterface; arguments are coerced to the
   * parameter types (JLS 5.3) and an erased generic return gets a synthetic
   * checkcast (JLS 5.2).
   */
  const emitCall = (call: CallExpression): string => {
    // The synthesized enum statics values()/valueOf(String) take precedence over
    // the inherited Enum.valueOf(Class, String) that resolveCall would otherwise
    // pick (it ignores the arity mismatch).
    const enumStatic = emitEnumStaticCall(call);
    if (enumStatic) return enumStatic;
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
    // Coerce each argument to its parameter type (box/unbox/widen) when the
    // arity matches exactly; a varargs spread is left to emit as-is.
    const paramDescs = parseParamDescriptors(descriptor);
    const coerceArgs = call.arguments.length === paramDescs.length;
    call.arguments.forEach((arg, i) => {
      const d = emitExpr(arg);
      if (coerceArgs) coerce(d, paramDescs[i]!);
    });

    const argSlots = paramDescs.reduce((n, d) => n + slotsOf(d), 0);
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
    if (returnDesc === "V") return returnDesc;
    push(returnDesc);
    // Synthetic cast after an erased generic return (JLS 5.2): the method ref
    // uses the erased descriptor (a type variable becomes Object), so when the
    // call's static type is more specific, checkcast to it - as javac does.
    if (returnDesc === "Ljava/lang/Object;") {
      const actual = checker.getTypeOfExpression(call);
      const actualDesc = typeDescriptor(actual);
      if (
        actualDesc !== "Ljava/lang/Object;" &&
        (actual.kind === TypeKind.Class || actual.kind === TypeKind.Array)
      ) {
        code.u1(OP_CHECKCAST);
        code.u2(cp.classInfo(actualDesc[0] === "[" ? actualDesc : actualDesc.slice(1, -1)));
        pop();
        push(actualDesc);
        return actualDesc;
      }
    }
    return returnDesc;
  };

  // new T(args): new; dup; <args>; invokespecial T.<init>:(...)V -> leaves the ref.
  /** Class instance creation `new T(args)` (JLS 15.9): new, dup, invokespecial. */
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

  // The xaload/xastore opcode offset for an array whose element has descriptor
  // `elem` (the families are contiguous: i,l,f,d,a,b,c,s).
  const arrayElemOffset = (elem: string): number => {
    switch (elem[0]) {
      case "J":
        return 1;
      case "F":
        return 2;
      case "D":
        return 3;
      case "L":
      case "[":
        return 4;
      case "Z":
      case "B":
        return 5;
      case "C":
        return 6;
      case "S":
        return 7;
      default:
        return 0; // int
    }
  };
  const NEWARRAY_ATYPE: Record<string, number> = {
    Z: 4,
    C: 5,
    F: 6,
    D: 7,
    B: 8,
    S: 9,
    I: 10,
    J: 11,
  };

  // Allocate a one-dimensional array whose element descriptor is `elem`; the
  // length is already on the stack. Leaves the array reference.
  const allocArray = (elem: string): string => {
    if (NEWARRAY_ATYPE[elem] !== undefined) {
      code.u1(OP_NEWARRAY);
      code.u1(NEWARRAY_ATYPE[elem]!);
    } else {
      code.u1(OP_ANEWARRAY);
      code.u2(cp.classInfo(elem[0] === "[" ? elem : elem.slice(1, -1)));
    }
    pop();
    push(`[${elem}`);
    return `[${elem}`;
  };

  // Build a one-dimensional array of `elem` from a brace initializer (nested
  // initializers recurse for multidimensional arrays).
  const arrayInitializer = (init: ArrayInitializer, elem: string): string => {
    intConst(init.elements.length);
    push("I");
    const arrDesc = allocArray(elem);
    init.elements.forEach((el, i) => {
      code.u1(OP_DUP);
      push(arrDesc);
      intConst(i);
      push("I");
      if (el.kind === SyntaxKind.ArrayInitializer) {
        arrayInitializer(el as ArrayInitializer, elem.slice(1));
      } else {
        coerce(emitExpr(el), elem);
      }
      code.u1(OP_IASTORE + arrayElemOffset(elem));
      pop(3); // array, index, value
    });
    return arrDesc;
  };

  // new T[n] / new T[m][n] / new T[]{ ... } (JLS 15.10).
  const emitArrayCreation = (node: ArrayCreationExpression): string => {
    const elementType = descriptorOf(node.elementType, program);
    const arrDesc = "[".repeat(node.dimensions.length + node.additionalRank) + elementType;
    if (node.initializer) return arrayInitializer(node.initializer, arrDesc.slice(1));
    if (node.dimensions.length === 1) {
      coerce(emitExpr(node.dimensions[0]!), "I");
      return allocArray(arrDesc.slice(1));
    }
    // Several given dimensions: multianewarray.
    for (const dim of node.dimensions) coerce(emitExpr(dim), "I");
    code.u1(OP_MULTIANEWARRAY);
    code.u2(cp.classInfo(arrDesc));
    code.u1(node.dimensions.length);
    pop(node.dimensions.length);
    push(arrDesc);
    return arrDesc;
  };

  /** Array access read a[i] (JLS 15.10.3): array, index, then xaload. */
  const emitElementAccess = (node: ElementAccessExpression): string => {
    const arrDesc = emitExpr(node.expression);
    const elem = arrDesc[0] === "[" ? arrDesc.slice(1) : "Ljava/lang/Object;";
    coerce(emitExpr(node.argumentExpression), "I");
    code.u1(OP_IALOAD + arrayElemOffset(elem));
    pop(2); // array, index -> element
    push(elem);
    return elem;
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
  // Like numericCategory, but a boxed wrapper type yields its primitive category
  // (the operand is unboxed in numeric contexts, JLS 5.6).
  const numericCat = (type: Type): string | undefined => {
    const c = numericCategory(type);
    if (c) return c;
    if (type.kind === TypeKind.Class) {
      const um = UNBOX[binaryName(type.symbol)];
      if (um) return um[1] === "J" || um[1] === "F" || um[1] === "D" ? um[1] : "I";
    }
    return undefined;
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

  /**
   * A non-boolean binary operator: multiplicative/additive (JLS 15.17, 15.18),
   * shift (15.19), and bitwise (15.22), with binary numeric promotion (5.6.2)
   * and operand unboxing (5.1.8). Comparisons and && / || go through emitBranch.
   */
  const emitBinary = (node: BinaryExpression): string => {
    const op = node.operatorToken;
    const lc = numericCat(checker.getTypeOfExpression(node.left));
    const rc = numericCat(checker.getTypeOfExpression(node.right));
    if (!lc || !rc) throw new UnsupportedEmit(); // String concat / comparisons: later

    const shift = SHIFTS[op];
    if (shift !== undefined) {
      const longShift = lc === "J";
      emitOperand(node.left, lc); // unbox the shifted value if it is a wrapper
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

  /** Unary +, -, ~ (JLS 15.15.3-15.15.5); logical ! goes through emitBoolean. */
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

  /** The return instruction for the method's return type (JLS 14.17). */
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
    reachable = false; // a return does not fall through
  };

  // Assignment used as a statement: store into a local or field, leaving nothing
  // Compound-assignment operator -> its underlying binary operator.
  const COMPOUND_BASE: Partial<Record<SyntaxKind, SyntaxKind>> = {
    [SyntaxKind.PlusEqualsToken]: SyntaxKind.PlusToken,
    [SyntaxKind.MinusEqualsToken]: SyntaxKind.MinusToken,
    [SyntaxKind.AsteriskEqualsToken]: SyntaxKind.AsteriskToken,
    [SyntaxKind.SlashEqualsToken]: SyntaxKind.SlashToken,
    [SyntaxKind.PercentEqualsToken]: SyntaxKind.PercentToken,
    [SyntaxKind.AmpersandEqualsToken]: SyntaxKind.AmpersandToken,
    [SyntaxKind.BarEqualsToken]: SyntaxKind.BarToken,
    [SyntaxKind.CaretEqualsToken]: SyntaxKind.CaretToken,
    [SyntaxKind.LessThanLessThanEqualsToken]: SyntaxKind.LessThanLessThanToken,
    [SyntaxKind.GreaterThanGreaterThanEqualsToken]: SyntaxKind.GreaterThanGreaterThanToken,
    [SyntaxKind.GreaterThanGreaterThanGreaterThanEqualsToken]:
      SyntaxKind.GreaterThanGreaterThanGreaterThanToken,
  };

  // For `target op= rhs`, with the current target value already on the stack,
  // combine it with rhs (JLS 15.26.2: implicit narrowing back to the target).
  const combineCompound = (targetDesc: string, baseOp: SyntaxKind, rhsNode: Node): void => {
    const tcat = category(targetDesc);
    if (tcat === "A") {
      // String concatenation: the current string is on the stack, append rhs.
      const rhsDesc = emitExpr(rhsNode);
      code.u1(OP_INVOKEDYNAMIC);
      code.u2(cp.invokeDynamicConcat(String.fromCharCode(1).repeat(2), `${targetDesc}${rhsDesc}`));
      code.u2(0);
      pop(2);
      push("Ljava/lang/String;");
      return;
    }
    const shift = SHIFTS[baseOp];
    if (shift !== undefined) {
      coerce(emitExpr(rhsNode), "I"); // shift distance is always int
      code.u1(shift + (tcat === "J" ? 1 : 0));
      pop(); // distance
      convertPrimitive(tcat, targetDesc); // narrow for byte/char/short
      return;
    }
    const base = ARITHMETIC[baseOp];
    if (base === undefined) throw new UnsupportedEmit();
    const rcat = numericCategory(checker.getTypeOfExpression(rhsNode)) ?? "I";
    const p = promote(tcat, rcat);
    coerce(tcat, p); // widen the current value to the promotion type
    emitOperand(rhsNode, p);
    code.u1(base + TYPE_OFFSET[p]!);
    pop(2);
    push(p);
    convertPrimitive(p, targetDesc); // narrow the result back to the target type
  };

  // Store into an assignable target (local / static field / instance field).
  // `emitValue(descriptor, loadCurrent)` must leave the value to store on the
  // stack; for a read-modify-write (compound assignment, increment) it calls
  // `loadCurrent` to push the current value. `needsCurrent` tells an instance
  // field to dup its receiver so the read and the write share it. Shared by
  // plain/compound assignment and by ++/-- on fields and wide locals.
  const emitStore = (
    target: Node,
    needsCurrent: boolean,
    emitValue: (descriptor: string, loadCurrent: () => void) => void,
  ): void => {
    const writeField = (info: FieldInfo, emitReceiver: () => void): void => {
      const ref = (): void => code.u2(cp.fieldref(info.owner, info.name, info.descriptor));
      if (info.isStatic) {
        emitValue(info.descriptor, () => {
          code.u1(OP_GETSTATIC);
          ref();
          push(info.descriptor);
        });
        code.u1(OP_PUTSTATIC);
        ref();
        pop(); // value
        return;
      }
      emitReceiver();
      if (needsCurrent) {
        code.u1(OP_DUP); // one receiver for the read, one for the write
        push(stack[stack.length - 1]!);
      }
      emitValue(info.descriptor, () => {
        code.u1(OP_GETFIELD);
        ref();
        pop(1); // receiver -> field value
        push(info.descriptor);
      });
      code.u1(OP_PUTFIELD);
      ref();
      pop(2); // receiver + value
    };

    if (target.kind === SyntaxKind.Identifier) {
      const symbol = checker.resolveName(target as Identifier);
      const local = symbol ? locals.get(symbol) : undefined;
      if (local) {
        emitValue(local.descriptor, () => {
          loadVar(local.slot, local.descriptor);
          push(local.descriptor);
        });
        storeVar(local.slot, local.descriptor);
        return;
      }
      // Field by simple name: implicit `this.f` or a static field.
      if (symbol && symbol.flags & SymbolFlags.Field) {
        writeField(fieldInfoOf(symbol), () => {
          code.u1(OP_ALOAD_0);
          pushRef(`L${thisInternalName};`);
        });
        return;
      }
      throw new UnsupportedEmit();
    }

    if (target.kind === SyntaxKind.PropertyAccessExpression) {
      const access = target as PropertyAccessExpression;
      const symbol = checker.resolveName(access.name);
      if (!symbol || !(symbol.flags & SymbolFlags.Field)) throw new UnsupportedEmit();
      writeField(fieldInfoOf(symbol), () => emitExpr(access.expression));
      return;
    }

    // a[i] = v / a[i] op= v: array and index, then xastore. A compound store
    // dup2s them so the read (xaload) and the write (xastore) share them.
    if (target.kind === SyntaxKind.ElementAccessExpression) {
      const access = target as ElementAccessExpression;
      const arrDesc = emitExpr(access.expression);
      const elem = arrDesc[0] === "[" ? arrDesc.slice(1) : "Ljava/lang/Object;";
      coerce(emitExpr(access.argumentExpression), "I");
      if (needsCurrent) {
        code.u1(OP_DUP2); // array, index for the read and the write
        push(arrDesc);
        push("I");
      }
      emitValue(elem, () => {
        code.u1(OP_IALOAD + arrayElemOffset(elem));
        pop(2); // array, index -> element
        push(elem);
      });
      code.u1(OP_IASTORE + arrayElemOffset(elem));
      pop(3); // array, index, value
      return;
    }

    throw new UnsupportedEmit();
  };

  // `target = rhs` and `target op= rhs` for locals and (static/instance) fields.
  // Array-element targets come with arrays.
  const emitAssignStatement = (assign: AssignmentExpression): void => {
    const op = assign.operatorToken;
    const baseOp = op === SyntaxKind.EqualsToken ? undefined : COMPOUND_BASE[op];
    if (op !== SyntaxKind.EqualsToken && baseOp === undefined) throw new UnsupportedEmit();
    emitStore(assign.left, baseOp !== undefined, (descriptor, loadCurrent) => {
      if (baseOp === undefined) {
        coerce(emitExpr(assign.right), descriptor);
        return;
      }
      loadCurrent();
      combineCompound(descriptor, baseOp, assign.right);
    });
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
          const leftType = checker.getTypeOfExpression(b.left);
          const rightType = checker.getTypeOfExpression(b.right);
          // Raw (primitive-only) categories decide reference vs numeric for ==/!=;
          // the wrapper-aware ones drive numeric comparison (operands unboxed).
          const rawLc = numericCategory(leftType);
          const rawRc = numericCategory(rightType);
          const lc = numericCat(leftType);
          const rc = numericCat(rightType);
          if (isEquality && (isNull(b.left) || isNull(b.right))) {
            emitExpr(isNull(b.left) ? b.right : b.left);
            const eq = op === SyntaxKind.EqualsEqualsToken;
            pop(1); // objectref consumed by the branch
            branchTo(eq === whenTrue ? OP_IFNULL : OP_IFNONNULL, label);
            return;
          }
          if (isEquality && !rawLc && !rawRc) {
            // Both operands are references (incl. two wrappers): reference equality.
            emitExpr(b.left);
            emitExpr(b.right);
            const eq = op === SyntaxKind.EqualsEqualsToken;
            pop(2);
            branchTo(eq === whenTrue ? OP_IF_ACMPEQ : OP_IF_ACMPNE, label);
            return;
          }
          if (lc === "I" && rc === "I") {
            emitOperand(b.left, "I"); // unbox if either side is a wrapper
            emitOperand(b.right, "I");
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
    // Fall back: evaluate a boolean value (unboxing a Boolean) and branch on it.
    coerce(emitExpr(expr), "Z");
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

  // Conditional expression c ? a : b (JLS 15.25). Both arms are promoted to the
  // result type; numeric promotion is computed from the arms (the same rule as
  // binary), otherwise the checker supplies the reference type.
  const emitConditional = (node: ConditionalExpression): string => {
    const tt = checker.getTypeOfExpression(node.whenTrue);
    const ft = checker.getTypeOfExpression(node.whenFalse);
    const lc = numericCategory(tt);
    const rc = numericCategory(ft);
    // The result (and stack-map) type must be one the verifier accepts for both
    // arms. For numerics that is binary promotion; for references, the shared
    // type of the arms (a null arm yields Object, so defer to the other arm),
    // else the checker's computed type.
    const OBJ = "Ljava/lang/Object;";
    const refDesc = (): string => {
      const dt = typeDescriptor(tt);
      const df = typeDescriptor(ft);
      if (dt === df) return dt;
      if (dt === OBJ) return df;
      if (df === OBJ) return dt;
      return typeDescriptor(checker.getTypeOfExpression(node));
    };
    const desc = lc && rc ? promote(lc, rc) : refDesc();
    const elseL = newLabel();
    const contL = newLabel();
    emitBranch(node.condition, elseL, false);
    coerce(emitExpr(node.whenTrue), desc);
    branchTo(OP_GOTO, contL);
    pop(); // the then-value is not on the stack along the else path
    placeLabel(elseL);
    coerce(emitExpr(node.whenFalse), desc);
    placeLabel(contL); // both arms converge with one value atop the entry stack
    return desc;
  };

  // A lambda (JLS 15.27): lowered to an invokedynamic bound by
  // LambdaMetafactory. The body becomes a synthetic method (emitted into
  // lambdaMethods) whose parameters are the captured outer locals followed by
  // the lambda's own parameters. A lambda that uses `this`/an instance member
  // captures the enclosing instance: the impl is a private instance method and
  // the receiver is the first dynamic argument (REF_invokeSpecial).
  const isVoidType = (t: Type): boolean =>
    t.kind === TypeKind.Primitive && (t as { name: string }).name === "void";
  const descOf = (t: Type): string => (isVoidType(t) ? "V" : typeDescriptor(t));

  const emitLambda = (node: LambdaExpression): string => {
    const info = checker.getLambdaInfo(node);
    if (!info) throw new UnsupportedEmit();

    // Capture analysis (JLS 15.27.2): captured locals become impl params; any
    // `this`/instance-member use means the enclosing instance is captured too.
    const captures: Symbol[] = [];
    const seen = new Set<Symbol>();
    let needsThis = false;
    const declStatic = (sym: Symbol): boolean => {
      const d = sym.valueDeclaration ?? sym.declarations?.[0];
      return !!d && isStaticDeclaration(d as { modifiers?: readonly Node[] });
    };
    const walk = (n: Node): void => {
      switch (n.kind) {
        case SyntaxKind.ThisExpression:
        case SyntaxKind.SuperExpression:
          needsThis = true;
          return;
        case SyntaxKind.PropertyAccessExpression:
          walk((n as PropertyAccessExpression).expression); // skip the member name
          return;
        case SyntaxKind.Identifier: {
          const sym = checker.resolveName(n as Identifier);
          if (sym && locals.has(sym)) {
            if (!seen.has(sym)) {
              seen.add(sym);
              captures.push(sym);
            }
          } else if (sym && sym.flags & SymbolFlags.Field && !fieldInfoOf(sym).isStatic) {
            needsThis = true; // implicit-this instance field
          }
          return;
        }
        case SyntaxKind.CallExpression: {
          const callee = (n as CallExpression).expression;
          if (callee.kind === SyntaxKind.Identifier) {
            const m = checker.resolveName(callee as Identifier);
            if (m && m.flags & SymbolFlags.Method && !declStatic(m)) needsThis = true;
          } else {
            walk(callee);
          }
          for (const arg of (n as CallExpression).arguments) walk(arg);
          return;
        }
        default:
          forEachChild(n, c => {
            walk(c);
            return undefined;
          });
      }
    };
    walk(node.body);
    if (needsThis && isStatic) throw new UnsupportedEmit(); // no enclosing instance to capture

    const instParamDescs = info.instParams.map(t => typeDescriptor(t));
    const instReturnDesc = descOf(info.instReturn);
    const samErased = `(${info.erasedParams.map(t => typeDescriptor(t)).join("")})${descOf(info.erasedReturn)}`;
    const instantiated = `(${instParamDescs.join("")})${instReturnDesc}`;

    const captureParams = captures.map(s => ({ symbol: s, descriptor: locals.get(s)!.descriptor }));
    const ownParams = node.parameters.map((p, i) => {
      const sym = (p as { symbol?: Symbol }).symbol;
      if (!sym || i >= instParamDescs.length) throw new UnsupportedEmit();
      return { symbol: sym, descriptor: instParamDescs[i]! };
    });
    const implParams = [...captureParams, ...ownParams];
    const implName = `lambda$${enclosingName}$${lambdaCounter++}`;
    const implDescriptor = `(${implParams.map(p => p.descriptor).join("")})${instReturnDesc}`;
    // Emit the body method eagerly: if it cannot be compiled, the exception
    // propagates and this whole enclosing method falls back (no dangling indy).
    lambdaMethods.push(
      emitLambdaMethod(
        {
          name: implName,
          params: implParams,
          returnDescriptor: instReturnDesc,
          body: node.body,
          isInstance: needsThis,
        },
        cp,
        program,
        checker,
        thisInternalName,
        lambdaMethods,
      ),
    );

    // invokedynamic: push the receiver (if captured), then the captured locals.
    const thisDesc = `L${thisInternalName};`;
    if (needsThis) {
      code.u1(OP_ALOAD_0);
      push(thisDesc);
    }
    for (const c of captureParams) {
      loadVar(locals.get(c.symbol)!.slot, c.descriptor);
      push(c.descriptor);
    }
    const interfaceDesc = typeDescriptor(info.interfaceType);
    const dynamicArgs = (needsThis ? thisDesc : "") + captureParams.map(c => c.descriptor).join("");
    const indyDescriptor = `(${dynamicArgs})${interfaceDesc}`;
    const idx = cp.invokeDynamicLambda(
      info.samName,
      indyDescriptor,
      samErased,
      needsThis ? REF_invokeSpecial : REF_invokeStatic,
      thisInternalName,
      implName,
      implDescriptor,
      instantiated,
    );
    code.u1(OP_INVOKEDYNAMIC);
    code.u2(idx);
    code.u2(0);
    pop(captureParams.length + (needsThis ? 1 : 0));
    push(interfaceDesc);
    return interfaceDesc;
  };

  // A method reference (JLS 15.13): an invokedynamic whose impl handle points
  // directly at the referenced method/constructor (no synthetic body). For a
  // bound reference (expr::m) the receiver is evaluated and captured; static,
  // unbound (Type::m), and constructor (Type::new) references capture nothing.
  const emitMethodRef = (node: Node): string => {
    const info = checker.getMethodRefInfo(node);
    if (!info) throw new UnsupportedEmit();
    const ref = node as MethodReferenceExpression;
    const instParamDescs = info.instParams.map(t => typeDescriptor(t));
    const samErased = `(${info.erasedParams.map(t => typeDescriptor(t)).join("")})${descOf(info.erasedReturn)}`;
    const instantiated = `(${instParamDescs.join("")})${descOf(info.instReturn)}`;
    const interfaceDesc = typeDescriptor(info.interfaceType);
    const ownerInternal = binaryName(info.ownerSymbol);
    const isInterface = (info.ownerSymbol.flags & SymbolFlags.Interface) !== 0;

    let refKind: number;
    let implName: string;
    let implDescriptor: string;
    let dynamicArgs = "";
    if (info.kind === "constructor") {
      refKind = REF_newInvokeSpecial;
      implName = "<init>";
      const ctor = findConstructor(info.ownerSymbol, info.instParams.length);
      const ctorParams = ctor
        ? ctor.parameters.map(p => paramDescriptor(p as Parameter, program))
        : [];
      implDescriptor = `(${ctorParams.join("")})V`;
    } else {
      const decl = info.target!;
      implName = decl.name.text;
      implDescriptor = methodDescriptor(decl, program);
      refKind =
        info.kind === "static"
          ? REF_invokeStatic
          : isInterface
            ? REF_invokeInterface
            : REF_invokeVirtual;
      if (info.kind === "bound") {
        dynamicArgs = emitExpr(ref.expression); // evaluate and capture the receiver
      }
    }
    const idx = cp.invokeDynamicLambda(
      info.samName,
      `(${dynamicArgs})${interfaceDesc}`,
      samErased,
      refKind,
      ownerInternal,
      implName,
      implDescriptor,
      instantiated,
      isInterface,
    );
    code.u1(OP_INVOKEDYNAMIC);
    code.u2(idx);
    code.u2(0);
    if (dynamicArgs) pop(1); // the captured receiver
    push(interfaceDesc);
    return interfaceDesc;
  };

  // Enum <clinit> prologue: construct each constant (new + <init>, putstatic) and
  // then the synthetic $VALUES array holding them all.
  const emitEnumClinitPrologue = (ec: EnumClinit): void => {
    for (const c of ec.constants) {
      code.u1(OP_NEW);
      code.u2(cp.classInfo(ec.enumInternal));
      pushRef(ec.selfDesc);
      code.u1(OP_DUP);
      pushRef(ec.selfDesc);
      ldc(cp.string(c.name));
      pushRef("Ljava/lang/String;");
      intConst(c.ordinal);
      push("I");
      c.args.forEach((arg, j) =>
        coerce(emitExpr(arg), c.userParamDescs[j] ?? "Ljava/lang/Object;"),
      );
      code.u1(OP_INVOKESPECIAL);
      code.u2(cp.methodref(ec.enumInternal, "<init>", c.ctorDescriptor));
      pop(1 + 2 + c.args.length); // dup'd ref + name + ordinal + args
      code.u1(OP_PUTSTATIC);
      code.u2(cp.fieldref(ec.enumInternal, c.name, ec.selfDesc));
      pop(1); // the constructed reference
    }
    intConst(ec.constants.length);
    push("I");
    code.u1(OP_ANEWARRAY);
    code.u2(cp.classInfo(ec.enumInternal));
    pop();
    push(ec.arrayDesc);
    ec.constants.forEach((c, i) => {
      code.u1(OP_DUP);
      push(ec.arrayDesc);
      intConst(i);
      push("I");
      code.u1(OP_GETSTATIC);
      code.u2(cp.fieldref(ec.enumInternal, c.name, ec.selfDesc));
      pushRef(ec.selfDesc);
      code.u1(OP_AASTORE);
      pop(3);
    });
    code.u1(OP_PUTSTATIC);
    code.u2(cp.fieldref(ec.enumInternal, ec.valuesField, ec.arrayDesc));
    pop(1);
  };

  // ++ / -- used as a statement (the result value is not needed). An int local
  // uses iinc; a field or wide local is a read-modify-write via emitStore.
  const emitIncrement = (expr: Node): void => {
    const u = expr as unknown as { operator: SyntaxKind; operand: Node };
    if (u.operator !== SyntaxKind.PlusPlusToken && u.operator !== SyntaxKind.MinusMinusToken) {
      throw new UnsupportedEmit();
    }
    const isInc = u.operator === SyntaxKind.PlusPlusToken;
    if (u.operand.kind === SyntaxKind.Identifier) {
      const symbol = checker.resolveName(u.operand as Identifier);
      const local = symbol ? locals.get(symbol) : undefined;
      if (local && category(local.descriptor) === "I") {
        code.u1(OP_IINC);
        code.u1(local.slot);
        code.u1((isInc ? 1 : -1) & 0xff);
        return;
      }
    }
    emitStore(u.operand, true, (descriptor, loadCurrent) => {
      const cat = category(descriptor);
      if (cat === "A") throw new UnsupportedEmit();
      loadCurrent();
      if (cat === "J") {
        longConst(1n);
        push("J");
      } else if (cat === "F") {
        floatConst(1);
        push("F");
      } else if (cat === "D") {
        doubleConst(1);
        push("D");
      } else {
        code.u1(OP_ICONST_1);
        push("I");
      }
      code.u1(
        ARITHMETIC[isInc ? SyntaxKind.PlusToken : SyntaxKind.MinusToken]! + TYPE_OFFSET[cat]!,
      );
      pop(2);
      push(cat);
      convertPrimitive(cat, descriptor); // narrow back for byte/char/short
    });
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

  // Constant value of a case label (integral or char). Non-constant or
  // non-integral labels are not supported (string/enum/pattern switch: later).
  const caseValue = (node: Node): number => {
    if (node.kind === SyntaxKind.CharacterLiteral) {
      return (node as LiteralExpression).value.charCodeAt(0);
    }
    const folded = foldConstant(node);
    if (!folded || folded.kind !== "int") throw new UnsupportedEmit();
    return Number(folded.value);
  };

  // tableswitch / lookupswitch (JVMS 6). Operands are 4-byte and must start on a
  // 4-byte boundary from the method's code start; offsets are relative to the
  // opcode. The choice mirrors javac's density heuristic (Gen.visitSwitch).
  const emitSwitchInstr = (cases: { value: number; label: Label }[], defaultLabel: Label): void => {
    const from = code.length; // opcode address; switch offsets are relative to it
    const n = cases.length;
    const lo = n ? cases[0]!.value : 0;
    const hi = n ? cases[n - 1]!.value : 0;
    const tableCost = 4 + (hi - lo + 1) + 3 * 3; // space + 3 * time (JVMS heuristic)
    const lookupCost = 3 + 2 * n + 3 * n;
    const useTable = n > 0 && tableCost <= lookupCost;
    const wide = (label: Label): void => {
      wideFixups.push({ at: code.length, from, label });
      code.u4(0); // placeholder, backpatched
      // Record the switch-entry state for this target (like branchTo does).
      if (label.targetStack === undefined) label.targetStack = [...stack];
      label.assignedAtTarget =
        label.assignedAtTarget === undefined
          ? new Set(assigned)
          : intersect(label.assignedAtTarget, assigned);
    };
    if (useTable) {
      code.u1(OP_TABLESWITCH);
      while (code.length % 4 !== 0) code.u1(0); // align operands to 4 bytes
      wide(defaultLabel);
      code.u4(lo & 0xffffffff);
      code.u4(hi & 0xffffffff);
      const byValue = new Map(cases.map(c => [c.value, c.label]));
      for (let v = lo; v <= hi; v++) wide(byValue.get(v) ?? defaultLabel);
    } else {
      code.u1(OP_LOOKUPSWITCH);
      while (code.length % 4 !== 0) code.u1(0);
      wide(defaultLabel);
      code.u4(n & 0xffffffff);
      for (const c of cases) {
        code.u4(c.value & 0xffffffff);
        wide(c.label);
      }
    }
    reachable = false; // a switch dispatches to its cases; it does not fall through
  };

  // Emit a switch selector and its dispatch (tableswitch/lookupswitch over an
  // integral selector, or a chained String.equals), shared by switch statements
  // and switch expressions. Returns the clause labels, the end label, and the
  // operand stack as the clause bodies see it.
  const emitSwitchDispatch = (
    selector: Node,
    clauses: readonly SwitchClause[],
    // For an exhaustive switch expression with no default clause, the no-match
    // path must throw rather than fall through without a value.
    throwOnNoMatch = false,
  ): { clauseLabels: Label[]; endL: Label; base: string[] } => {
    if (clauses.some(cl => cl.guard !== undefined)) throw new UnsupportedEmit();
    const selType = checker.getTypeOfExpression(selector);
    const isString = isStringType(selType);
    const enumSym =
      selType.kind === TypeKind.Class && selType.symbol.flags & SymbolFlags.Enum
        ? selType.symbol
        : undefined;
    if (!isString && !enumSym && numericCategory(selType) !== "I") throw new UnsupportedEmit();
    // An enum switch dispatches on the constant's ordinal (declaration order),
    // run-equivalent to javac's $SwitchMap when the enum is compiled with it.
    const enumOrdinal = (lab: Node): number => {
      const decl = (enumSym!.valueDeclaration ?? enumSym!.declarations?.[0]) as EnumDeclaration;
      const i =
        lab.kind === SyntaxKind.Identifier
          ? decl.enumConstants.findIndex(c => c.name.text === (lab as Identifier).text)
          : -1;
      if (i < 0) throw new UnsupportedEmit();
      return i;
    };

    const endL = newLabel();
    const clauseLabels = clauses.map(() => newLabel());
    const hasDefault = clauses.some(cl => cl.isDefault);
    const throwL = throwOnNoMatch && !hasDefault ? newLabel() : undefined;
    let defaultLabel = throwL ?? endL;
    clauses.forEach((cl, i) => {
      if (cl.isDefault) defaultLabel = clauseLabels[i]!;
    });

    if (isString) {
      const selDesc = "Ljava/lang/String;";
      emitExpr(selector);
      const tmp = nextSlot;
      nextSlot += 1;
      if (nextSlot > maxLocals) maxLocals = nextSlot;
      activeLocals.push({ slot: tmp, descriptor: selDesc });
      storeVar(tmp, selDesc); // selector evaluated once into a temp
      clauses.forEach((cl, i) => {
        for (const lab of cl.labels ?? []) {
          if (lab.kind !== SyntaxKind.StringLiteral) throw new UnsupportedEmit();
          loadVar(tmp, selDesc);
          push(selDesc);
          ldc(cp.string((lab as LiteralExpression).value));
          push(selDesc);
          code.u1(OP_INVOKEVIRTUAL);
          code.u2(cp.methodref("java/lang/String", "equals", "(Ljava/lang/Object;)Z"));
          pop(2);
          push("I");
          pop();
          branchTo(OP_IFEQ + 1, clauseLabels[i]!); // ifne: matched -> this clause
        }
      });
      branchTo(OP_GOTO, defaultLabel);
    } else {
      if (enumSym) {
        emitExpr(selector);
        code.u1(OP_INVOKEVIRTUAL);
        code.u2(cp.methodref("java/lang/Enum", "ordinal", "()I"));
        pop();
        push("I");
      } else {
        coerce(emitExpr(selector), "I");
      }
      pop(); // selector (ordinal) consumed by the switch instruction
      const cases: { value: number; label: Label }[] = [];
      clauses.forEach((cl, i) => {
        for (const lab of cl.labels ?? []) {
          cases.push({
            value: enumSym ? enumOrdinal(lab) : caseValue(lab),
            label: clauseLabels[i]!,
          });
        }
      });
      cases.sort((a, b) => a.value - b.value);
      emitSwitchInstr(cases, defaultLabel);
    }
    const base = [...stack];
    if (throwL) {
      // No-match path of an exhaustive switch expression: throw (never reached
      // at runtime, but every path must produce a value or throw to verify).
      setStack(base);
      placeLabel(throwL);
      const err = "java/lang/IncompatibleClassChangeError";
      code.u1(OP_NEW);
      code.u2(cp.classInfo(err));
      pushRef(`L${err};`);
      code.u1(OP_DUP);
      pushRef(`L${err};`);
      code.u1(OP_INVOKESPECIAL);
      code.u2(cp.methodref(err, "<init>", "()V"));
      pop(1);
      code.u1(OP_ATHROW);
      pop(1);
    }
    return { clauseLabels, endL, base };
  };

  // The result type of a switch expression, from its arrow-expression and yield
  // values: binary promotion when all are numeric, otherwise a shared reference.
  const switchResultDesc = (clauses: readonly SwitchClause[]): string => {
    const types: Type[] = [];
    const collectYields = (n: Node): void => {
      if (n.kind === SyntaxKind.YieldStatement) {
        types.push(checker.getTypeOfExpression((n as YieldStatement).expression));
      }
      forEachChild(n, c => {
        if (c.kind !== SyntaxKind.SwitchExpression) collectYields(c); // not a nested switch's yields
        return undefined;
      });
    };
    for (const cl of clauses) {
      const arrowExpr =
        cl.isArrow &&
        cl.statements.length === 1 &&
        cl.statements[0]!.kind === SyntaxKind.ExpressionStatement
          ? (cl.statements[0] as ExpressionStatement).expression
          : undefined;
      if (arrowExpr) types.push(checker.getTypeOfExpression(arrowExpr));
      else for (const st of cl.statements) collectYields(st);
    }
    const cats = types.map(t => numericCategory(t));
    if (cats.length > 0 && cats.every((c): c is string => c !== undefined)) {
      return cats.reduce((a, b) => promote(a, b));
    }
    for (const t of types) {
      const d = typeDescriptor(t);
      if (d !== "Ljava/lang/Object;") return d;
    }
    return "Ljava/lang/Object;";
  };

  // A switch expression (JLS 14.11.2): like a switch statement, but every arm
  // yields a value (arrow `case L -> v` or `yield v`), left on the stack at the end.
  const emitSwitchExpression = (node: SwitchExpression): string => {
    const resultDesc = switchResultDesc(node.clauses);
    const { clauseLabels, endL, base } = emitSwitchDispatch(node.expression, node.clauses, true);
    yieldTargets.push({ label: endL, desc: resultDesc });
    node.clauses.forEach((cl, i) => {
      setStack(base);
      placeLabel(clauseLabels[i]!);
      const arrowExpr =
        cl.isArrow &&
        cl.statements.length === 1 &&
        cl.statements[0]!.kind === SyntaxKind.ExpressionStatement
          ? (cl.statements[0] as ExpressionStatement).expression
          : undefined;
      if (arrowExpr) {
        coerce(emitExpr(arrowExpr), resultDesc);
        branchTo(OP_GOTO, endL); // the value is the result; records the merge stack
        pop(); // not on the live stack for the next clause
      } else {
        inScope(() => {
          let t = false;
          for (const st of cl.statements) t = emitStmt(st); // yields branch to endL
          return t;
        });
      }
    });
    yieldTargets.pop();
    setStack(base);
    push(resultDesc); // both paths converge here with the result on the stack
    placeLabel(endL);
    return resultDesc;
  };

  // Inline the finally blocks from the top of the stack down to `toDepth`,
  // innermost first, before an abrupt transfer (return/break/continue) crosses
  // them. Each is removed while emitted so a return inside it does not re-run it.
  const runFinallies = (toDepth: number): void => {
    const removed = finallyStack.splice(toDepth); // [toDepth..], leaving the outer ones pending
    for (let i = removed.length - 1; i >= 0; i--) inScope(() => emitStmt(removed[i]!));
    finallyStack.push(...removed);
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
          activeLocals.push({ slot, descriptor });
          if (declarator.initializer) {
            // A bare brace initializer (int[] a = {1,2,3}) gets its element type
            // from the declared type rather than from the expression.
            if (
              declarator.initializer.kind === SyntaxKind.ArrayInitializer &&
              descriptor[0] === "["
            ) {
              arrayInitializer(declarator.initializer as ArrayInitializer, descriptor.slice(1));
            } else {
              coerce(emitExpr(declarator.initializer), descriptor);
            }
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
        // A return inside a try runs the enclosing finally blocks first; the
        // value is stashed in a temp across them (finally code uses the stack).
        if (finallyStack.length > 0) {
          if (returnDescriptor !== "V") {
            const slot = nextSlot;
            nextSlot += slotsOf(returnDescriptor);
            if (nextSlot > maxLocals) maxLocals = nextSlot;
            activeLocals.push({ slot, descriptor: returnDescriptor });
            storeVar(slot, returnDescriptor);
            runFinallies(0);
            loadVar(slot, returnDescriptor);
            push(returnDescriptor);
          } else {
            runFinallies(0);
          }
        }
        emitReturn();
        return true;
      }
      // The `if` statement (JLS 14.9).
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
      // The `while` statement (JLS 14.12).
      case SyntaxKind.WhileStatement: {
        const s = stmt as WhileStatement;
        const startL = newLabel();
        const endL = newLabel();
        placeLabel(startL);
        emitBranch(s.condition, endL, false);
        const names = takePending();
        breakTargets.push({ label: endL, finallyDepth: finallyStack.length, names });
        continueTargets.push({ label: startL, finallyDepth: finallyStack.length, names }); // continue re-tests the condition
        inScope(() => emitStmt(s.statement));
        breakTargets.pop();
        continueTargets.pop();
        branchTo(OP_GOTO, startL);
        placeLabel(endL);
        return false;
      }
      // The `do` statement (JLS 14.13).
      case SyntaxKind.DoStatement: {
        const s = stmt as DoStatement;
        const startL = newLabel();
        const condL = newLabel();
        const endL = newLabel();
        placeLabel(startL);
        const names = takePending();
        breakTargets.push({ label: endL, finallyDepth: finallyStack.length, names });
        continueTargets.push({ label: condL, finallyDepth: finallyStack.length, names }); // continue jumps to the trailing condition
        inScope(() => emitStmt(s.statement));
        breakTargets.pop();
        continueTargets.pop();
        placeLabel(condL);
        emitBranch(s.condition, startL, true);
        placeLabel(endL);
        return false;
      }
      // The basic `for` statement (JLS 14.14.1).
      case SyntaxKind.ForStatement: {
        const s = stmt as ForStatement;
        return inScope(() => {
          if (s.initializer) emitStmt(s.initializer);
          for (const e of s.initializerExpressions ?? []) emitStatementExpression(e);
          const startL = newLabel();
          const stepL = newLabel();
          const endL = newLabel();
          placeLabel(startL);
          if (s.condition) emitBranch(s.condition, endL, false);
          const names = takePending();
          breakTargets.push({ label: endL, finallyDepth: finallyStack.length, names });
          continueTargets.push({ label: stepL, finallyDepth: finallyStack.length, names }); // continue runs the incrementors, then re-tests
          inScope(() => emitStmt(s.statement));
          breakTargets.pop();
          continueTargets.pop();
          placeLabel(stepL);
          for (const e of s.incrementors ?? []) emitStatementExpression(e);
          branchTo(OP_GOTO, startL);
          placeLabel(endL);
          return false;
        });
      }
      // The enhanced `for` statement (JLS 14.14.2): over an array or an Iterable.
      case SyntaxKind.ForEachStatement: {
        const s = stmt as ForEachStatement;
        const iterableType = checker.getTypeOfExpression(s.expression);
        const param = s.parameter;
        const reserve = (descriptor: string): number => {
          const slot = nextSlot;
          nextSlot += slotsOf(descriptor);
          if (nextSlot > maxLocals) maxLocals = nextSlot;
          activeLocals.push({ slot, descriptor });
          return slot;
        };
        // The loop variable's type (declared, or inferred from the element type).
        const varDesc =
          param.type && param.type.kind !== SyntaxKind.VarType
            ? descriptorOf(param.type, program)
            : typeDescriptor(checker.getTypeOfSymbol(param.symbol!));

        if (iterableType.kind === TypeKind.Array) {
          return inScope(() => {
            // for (T x : a) -> int $i = 0; while ($i < $a.length) { x = $a[$i]; body; $i++; }
            const arrDesc = emitExpr(s.expression);
            const elem = arrDesc[0] === "[" ? arrDesc.slice(1) : "Ljava/lang/Object;";
            const arrSlot = reserve(arrDesc);
            storeVar(arrSlot, arrDesc);
            const idxSlot = reserve("I");
            code.u1(OP_ICONST_0);
            push("I");
            storeVar(idxSlot, "I");
            const varSlot = reserve(varDesc);
            if (param.symbol) locals.set(param.symbol, { slot: varSlot, descriptor: varDesc });
            const startL = newLabel();
            const stepL = newLabel();
            const endL = newLabel();
            placeLabel(startL);
            loadVar(idxSlot, "I");
            push("I");
            loadVar(arrSlot, arrDesc);
            push(arrDesc);
            code.u1(OP_ARRAYLENGTH);
            pop();
            push("I");
            pop(2);
            branchTo(OP_IF_ICMPEQ + 3, endL); // if $i >= length, exit
            loadVar(arrSlot, arrDesc);
            push(arrDesc);
            loadVar(idxSlot, "I");
            push("I");
            code.u1(OP_IALOAD + arrayElemOffset(elem));
            pop(2);
            push(elem);
            coerce(elem, varDesc);
            storeVar(varSlot, varDesc);
            const names = takePending();
            breakTargets.push({ label: endL, finallyDepth: finallyStack.length, names });
            continueTargets.push({ label: stepL, finallyDepth: finallyStack.length, names });
            inScope(() => emitStmt(s.statement));
            breakTargets.pop();
            continueTargets.pop();
            placeLabel(stepL);
            code.u1(OP_IINC);
            code.u1(idxSlot);
            code.u1(1);
            branchTo(OP_GOTO, startL);
            placeLabel(endL);
            return false;
          });
        }
        if (iterableType.kind !== TypeKind.Class) throw new UnsupportedEmit();
        return inScope(() => {
          // for (T x : it) -> Iterator $i = it.iterator();
          //   while ($i.hasNext()) { T x = (T) $i.next(); body; }
          const ITER = "java/util/Iterator";
          emitExpr(s.expression);
          code.u1(OP_INVOKEINTERFACE);
          code.u2(
            cp.interfaceMethodref("java/lang/Iterable", "iterator", "()Ljava/util/Iterator;"),
          );
          code.u1(1);
          code.u1(0);
          pop();
          push("Ljava/util/Iterator;");
          const itSlot = reserve("Ljava/util/Iterator;");
          storeVar(itSlot, "Ljava/util/Iterator;");
          const varSlot = reserve(varDesc);
          if (param.symbol) locals.set(param.symbol, { slot: varSlot, descriptor: varDesc });
          const startL = newLabel();
          const endL = newLabel();
          placeLabel(startL);
          loadVar(itSlot, "Ljava/util/Iterator;");
          push("Ljava/util/Iterator;");
          code.u1(OP_INVOKEINTERFACE);
          code.u2(cp.interfaceMethodref(ITER, "hasNext", "()Z"));
          code.u1(1);
          code.u1(0);
          pop();
          push("I");
          pop();
          branchTo(OP_IFEQ, endL); // !hasNext -> exit
          loadVar(itSlot, "Ljava/util/Iterator;");
          push("Ljava/util/Iterator;");
          code.u1(OP_INVOKEINTERFACE);
          code.u2(cp.interfaceMethodref(ITER, "next", "()Ljava/lang/Object;"));
          code.u1(1);
          code.u1(0);
          pop();
          push("Ljava/lang/Object;");
          // (T) next(): a reference cast, or unbox via the wrapper for a primitive.
          if (category(varDesc) === "A") {
            if (varDesc !== "Ljava/lang/Object;") {
              code.u1(OP_CHECKCAST);
              code.u2(cp.classInfo(varDesc[0] === "[" ? varDesc : varDesc.slice(1, -1)));
              pop();
              push(varDesc);
            }
          } else {
            const w = WRAPPER[varDesc]!;
            code.u1(OP_CHECKCAST);
            code.u2(cp.classInfo(w));
            pop();
            push(`L${w};`);
            coerce(`L${w};`, varDesc);
          }
          storeVar(varSlot, varDesc);
          const names = takePending();
          breakTargets.push({ label: endL, finallyDepth: finallyStack.length, names });
          continueTargets.push({ label: startL, finallyDepth: finallyStack.length, names });
          inScope(() => emitStmt(s.statement));
          breakTargets.pop();
          continueTargets.pop();
          branchTo(OP_GOTO, startL);
          placeLabel(endL);
          return false;
        });
      }
      case SyntaxKind.ThrowStatement: {
        emitExpr((stmt as ThrowStatement).expression);
        code.u1(OP_ATHROW);
        pop(1);
        reachable = false;
        return true; // throw is a terminator
      }
      case SyntaxKind.TryStatement: {
        const t = stmt as TryStatement;
        if (t.resources?.length) throw new UnsupportedEmit(); // try-with-resources: later
        const fin = t.finallyBlock;
        const endL = newLabel();
        let reachesEnd = false;
        // Locals definitely assigned at try entry: a handler may be reached from
        // any point in the protected region, so its frame uses this state.
        const tryStartAssigned = new Set(assigned);
        // Code ranges covered by the finally catch-all (the try body and each
        // catch body, but NOT the inline finally copies between them).
        const protectedRanges: { start: number; end: number }[] = [];
        const setEntryState = (): void => {
          setStack([]);
          assigned.clear();
          for (const s of tryStartAssigned) assigned.add(s);
          reachable = true;
        };
        const emitFinallyInline = (): boolean =>
          inScope(() => {
            let term = false;
            for (const st of (fin as Block).statements) term = emitStmt(st);
            return term;
          });
        // After a region completes normally: run the finally (if any) inline,
        // then jump past the handlers - unless the finally itself terminates.
        const completeNormally = (): void => {
          if (fin && emitFinallyInline()) return; // finally aborts -> no fall-through
          reachesEnd = true;
          branchTo(OP_GOTO, endL);
        };

        const tryStart = code.length;
        if (fin) finallyStack.push(fin);
        const tryTerm = inScope(() => emitStmt(t.tryBlock));
        if (fin) finallyStack.pop();
        protectedRanges.push({ start: tryStart, end: code.length });
        if (!tryTerm) completeNormally();

        for (const cc of t.catchClauses) {
          setEntryState();
          const excDesc =
            cc.catchTypes.length === 1
              ? descriptorOf(cc.catchTypes[0]!, program)
              : "Ljava/lang/Throwable;";
          push(excDesc); // the JVM pushes the caught exception onto an empty stack
          const handlerL = newLabel();
          placeLabel(handlerL); // frame: locals = try entry, stack = [exc]
          handlerOffsets.push(handlerL.offset);
          for (const ty of cc.catchTypes) {
            const d = descriptorOf(ty, program);
            exceptionTable.push({
              start: tryStart,
              end: protectedRanges[0]!.end, // the try body only
              handler: handlerL.offset,
              catchType: cp.classInfo(d[0] === "[" ? d : d.slice(1, -1)),
            });
          }
          const bodyStart = code.length;
          if (fin) finallyStack.push(fin);
          const handlerTerm = inScope(() => {
            const slot = nextSlot;
            nextSlot += slotsOf(excDesc);
            if (nextSlot > maxLocals) maxLocals = nextSlot;
            activeLocals.push({ slot, descriptor: excDesc });
            if (cc.name.symbol) locals.set(cc.name.symbol, { slot, descriptor: excDesc });
            storeVar(slot, excDesc); // astore the exception into the catch parameter
            let term = false;
            for (const st of cc.block.statements) term = emitStmt(st);
            return term;
          });
          if (fin) finallyStack.pop();
          protectedRanges.push({ start: bodyStart, end: code.length });
          if (!handlerTerm) completeNormally();
        }

        // finally catch-all (JLS 14.20.2): store the in-flight exception, run the
        // finally, then rethrow. Covers the try and catch bodies, after the
        // specific catch entries so they win.
        if (fin) {
          setEntryState();
          const exc = "Ljava/lang/Throwable;";
          push(exc);
          const catchAllL = newLabel();
          placeLabel(catchAllL);
          handlerOffsets.push(catchAllL.offset);
          for (const r of protectedRanges) {
            exceptionTable.push({
              start: r.start,
              end: r.end,
              handler: catchAllL.offset,
              catchType: 0,
            });
          }
          const slot = nextSlot;
          nextSlot += 1;
          if (nextSlot > maxLocals) maxLocals = nextSlot;
          activeLocals.push({ slot, descriptor: exc });
          storeVar(slot, exc);
          emitFinallyInline();
          loadVar(slot, exc);
          push(exc);
          code.u1(OP_ATHROW);
          pop(1);
          reachable = false;
        }

        if (reachesEnd) {
          setStack([]);
          placeLabel(endL);
        }
        return !reachesEnd;
      }
      case SyntaxKind.YieldStatement: {
        const target = yieldTargets.at(-1);
        if (!target) throw new UnsupportedEmit();
        coerce(emitExpr((stmt as YieldStatement).expression), target.desc);
        branchTo(OP_GOTO, target.label); // the value is left on the stack at the end
        return true;
      }
      // `break` (JLS 14.15): exits the nearest loop/switch, or the named labeled
      // statement, running finally blocks crossed on the way out.
      case SyntaxKind.BreakStatement: {
        const name = (stmt as BreakStatement).label?.text;
        const target = name
          ? breakTargets.findLast(t => t.names?.includes(name))
          : breakTargets.at(-1);
        if (!target) throw new UnsupportedEmit();
        runFinallies(target.finallyDepth); // finally blocks between here and the loop/switch
        branchTo(OP_GOTO, target.label);
        return true;
      }
      // `continue` (JLS 14.16): resumes the nearest enclosing loop, or the named
      // labeled loop.
      case SyntaxKind.ContinueStatement: {
        const name = (stmt as ContinueStatement).label?.text;
        const target = name
          ? continueTargets.findLast(t => t.names?.includes(name))
          : continueTargets.at(-1);
        if (!target) throw new UnsupportedEmit();
        runFinallies(target.finallyDepth);
        branchTo(OP_GOTO, target.label);
        return true;
      }
      // A labeled statement (JLS 14.7). A label on a loop is consumed by the loop
      // (so `continue label` reaches it); a label on any other statement gets a
      // break target at its end.
      case SyntaxKind.LabeledStatement: {
        const names: string[] = [];
        let body: Node = stmt;
        while (body.kind === SyntaxKind.LabeledStatement) {
          names.push((body as LabeledStatement).label.text);
          body = (body as LabeledStatement).statement;
        }
        const k = body.kind;
        if (
          k === SyntaxKind.WhileStatement ||
          k === SyntaxKind.DoStatement ||
          k === SyntaxKind.ForStatement ||
          k === SyntaxKind.ForEachStatement
        ) {
          pendingLabels.push(...names);
          return emitStmt(body);
        }
        const endL = newLabel();
        breakTargets.push({ label: endL, finallyDepth: finallyStack.length, names });
        const term = inScope(() => emitStmt(body));
        breakTargets.pop();
        const used =
          fixups.some(f => f.label === endL) || wideFixups.some(f => f.label === endL);
        if (used) {
          placeLabel(endL);
          return false;
        }
        return term;
      }
      // The `switch` statement (JLS 14.11).
      case SyntaxKind.SwitchStatement: {
        const s = stmt as SwitchStatement;
        const { clauseLabels, endL, base } = emitSwitchDispatch(s.expression, s.clauses);
        breakTargets.push({ label: endL, finallyDepth: finallyStack.length });
        const arrow = s.clauses.some(cl => cl.isArrow); // arrow clauses do not fall through
        let lastTerminated = false;
        s.clauses.forEach((cl, i) => {
          setStack(base); // a prior clause's terminator may have left dead values
          placeLabel(clauseLabels[i]!);
          const term = inScope(() => {
            let t = false;
            for (const st of cl.statements) t = emitStmt(st);
            return t;
          });
          if (arrow && !term) branchTo(OP_GOTO, endL); // implicit break after an arrow clause
          lastTerminated = term;
        });
        setStack(base);
        placeLabel(endL);
        breakTargets.pop();

        // The switch terminates only if every value reaches a return: a default
        // exists, the last clause does not fall through, and nothing branches to
        // the end (a break, or a defaulting selector when no default clause).
        const hasDefault = s.clauses.some(cl => cl.isDefault);
        const endBranched =
          fixups.some(f => f.label === endL) || wideFixups.some(f => f.label === endL);
        return hasDefault && lastTerminated && !endBranched;
      }
      default:
        throw new UnsupportedEmit();
    }
  };

  if (lambdaSpec) {
    // Lambda body: an expression (its value is the result, or discarded when the
    // SAM returns void) or a block.
    if (lambdaSpec.body.kind === SyntaxKind.Block) {
      const terminated = emitStmt(lambdaSpec.body);
      if (!terminated) {
        if (returnDescriptor === "V") code.u1(OP_RETURN);
        else throw new UnsupportedEmit();
      }
    } else if (returnDescriptor === "V") {
      emitStatementExpression(lambdaSpec.body);
      code.u1(OP_RETURN);
    } else {
      coerce(emitExpr(lambdaSpec.body), returnDescriptor);
      emitReturn();
    }
  } else {
    if (!method.body || method.body.kind !== SyntaxKind.Block) throw new UnsupportedEmit();
    if (isConstructor && enumCtor) {
      // Implicit super(name, ordinal) for an enum constructor.
      code.u1(OP_ALOAD_0);
      pushRef();
      code.u1(OP_ALOAD_0 + 1); // aload_1 (name)
      pushRef("Ljava/lang/String;");
      code.u1(OP_ILOAD_0 + 2); // iload_2 (ordinal)
      push("I");
      code.u1(OP_INVOKESPECIAL);
      code.u2(cp.methodref("java/lang/Enum", "<init>", "(Ljava/lang/String;I)V"));
      pop(3);
    } else if (isConstructor && ctorSuper) {
      // Implicit super(): aload_0; invokespecial <super>.<init>:()V. (An explicit
      // super()/this() in the body is not handled and triggers the fallback.)
      code.u1(OP_ALOAD_0);
      pushRef();
      code.u1(OP_INVOKESPECIAL);
      code.u2(cp.methodref(ctorSuper, "<init>", "()V"));
      pop(1);
    }
    if (enumClinit) emitEnumClinitPrologue(enumClinit);
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
  }

  // Backpatch branch offsets (signed, relative to the branch opcode address).
  for (const { at, from, label } of fixups) {
    if (label.offset < 0) throw new UnsupportedEmit(); // label never placed
    code.patchU2(at, (label.offset - from) & 0xffff);
  }
  for (const { at, from, label } of wideFixups) {
    if (label.offset < 0) throw new UnsupportedEmit();
    code.patchU4(at, (label.offset - from) & 0xffffffff);
  }

  // StackMapTable: a full_frame at every branch-target offset (JVMS 4.7.4).
  const targetOffsets = [
    ...new Set([
      ...fixups.map(f => f.label.offset),
      ...wideFixups.map(f => f.label.offset),
      ...handlerOffsets,
    ]),
  ].sort((a, b) => a - b);
  let stackMapTable: ByteBuffer | undefined;
  if (targetOffsets.length > 0) {
    const writeVerification = (buf: ByteBuffer, descriptor: string): void => {
      if (descriptor === TOP) {
        buf.u1(ITEM_Top);
        return;
      }
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

  return { code, maxStack, maxLocals, stackMapTable, exceptionTable };
}

function emitMethod(
  method: MethodDeclaration,
  cp: ConstantPool,
  program: Program,
  checker: Checker,
  thisInternalName: string,
  lambdaMethods: ByteBuffer[],
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
    body = generateBody(
      method,
      cp,
      program,
      checker,
      thisInternalName,
      undefined,
      [],
      lambdaMethods,
    );
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

// A synthetic method holding a lambda body (private static, or private instance
// when the lambda captured `this`). Its own nested lambdas are emitted eagerly
// into `lambdaMethods` during generateBody.
function emitLambdaMethod(
  impl: LambdaImpl,
  cp: ConstantPool,
  program: Program,
  checker: Checker,
  thisInternalName: string,
  lambdaMethods: ByteBuffer[],
): ByteBuffer {
  const descriptor = `(${impl.params.map(p => p.descriptor).join("")})${impl.returnDescriptor}`;
  const info = new ByteBuffer();
  info.u2(ACC_PRIVATE | ACC_SYNTHETIC | (impl.isInstance ? 0 : ACC_STATIC));
  info.u2(cp.utf8(impl.name));
  info.u2(cp.utf8(descriptor));
  const body = generateBody(
    {} as MethodDeclaration,
    cp,
    program,
    checker,
    thisInternalName,
    undefined,
    [],
    lambdaMethods,
    {
      params: impl.params,
      returnDescriptor: impl.returnDescriptor,
      body: impl.body,
      isInstance: impl.isInstance,
    },
  );
  writeCodeAttribute(info, cp, body);
  return info;
}

interface MethodBody {
  code: ByteBuffer;
  maxStack: number;
  maxLocals: number;
  stackMapTable?: ByteBuffer;
  exceptionTable?: ExceptionTableEntry[];
}

// Append the Code attribute (with an optional StackMapTable sub-attribute) and
// set the method's attributes_count to 1.
function writeCodeAttribute(info: ByteBuffer, cp: ConstantPool, body: MethodBody): void {
  const smt = body.stackMapTable;
  const smtBytes = smt ? 6 + smt.length : 0;
  const handlers = body.exceptionTable ?? [];

  const codeAttr = new ByteBuffer();
  codeAttr.u2(cp.utf8("Code"));
  codeAttr.u4(12 + body.code.length + handlers.length * 8 + smtBytes);
  codeAttr.u2(body.maxStack);
  codeAttr.u2(body.maxLocals);
  codeAttr.u4(body.code.length);
  codeAttr.append(body.code);
  codeAttr.u2(handlers.length); // exception_table_length
  for (const h of handlers) {
    codeAttr.u2(h.start);
    codeAttr.u2(h.end);
    codeAttr.u2(h.handler);
    codeAttr.u2(h.catchType);
  }
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
  lambdaMethods: ByteBuffer[],
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
      lambdaMethods,
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

  const { instanceInits, staticInits } = collectFieldInits(declaration.members, name, program);

  // Constructors: the declared ones, or a synthesized default constructor (which
  // inherits the class's accessibility, JLS 8.8.9) when none are declared. Each
  // runs the instance field initializers.
  const methods = new ByteBuffer();
  let methodCount = 0;
  // Synthetic lambda-body methods, emitted eagerly into this list as each
  // method/ctor is generated, then appended after the declared members.
  const lambdaMethods: ByteBuffer[] = [];
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
        lambdaMethods,
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
          lambdaMethods,
        ),
      );
      methodCount++;
    }
  }
  for (const member of declaration.members) {
    if (member.kind !== SyntaxKind.MethodDeclaration) continue;
    methods.append(
      emitMethod(member as MethodDeclaration, cp, program, checker, name, lambdaMethods),
    );
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
    let clinitBody: MethodBody;
    try {
      clinitBody = generateBody(
        clinit,
        cp,
        program,
        checker,
        name,
        undefined,
        staticInits,
        lambdaMethods,
      );
    } catch (e) {
      if (!(e instanceof UnsupportedEmit)) throw e;
      // An unsupported static initializer: emit an empty <clinit> so the class
      // stays valid (the affected fields keep their default values).
      const code = new ByteBuffer();
      code.u1(OP_RETURN);
      clinitBody = { code, maxStack: 0, maxLocals: 0 };
    }
    writeCodeAttribute(info, cp, clinitBody);
    methods.append(info);
    methodCount++;
  }

  // Append the synthetic lambda-body methods emitted while generating the above.
  for (const impl of lambdaMethods) {
    methods.append(impl);
    methodCount++;
  }

  // Class attributes, built before writeInto so any new Utf8 names land in the
  // pool. BootstrapMethods carries the invokedynamic targets for string concat.
  const { buffer: classAttributes, count: classAttributeCount } = buildClassAttributes(
    cp,
    sourceNameOf(declaration),
  );

  return {
    name,
    bytes: assembleClassFile({
      cp,
      accessFlags,
      thisClassIndex,
      superClassIndex,
      interfaceIndices,
      fields: fields.buffer,
      fieldCount: fields.count,
      methods,
      methodCount,
      attributes: classAttributes,
      attributeCount: classAttributeCount,
    }),
  };
}

// One field_info with no attributes.
function emitFieldInfo(
  buffer: ByteBuffer,
  cp: ConstantPool,
  flags: number,
  name: string,
  descriptor: string,
): void {
  buffer.u2(flags);
  buffer.u2(cp.utf8(name));
  buffer.u2(cp.utf8(descriptor));
  buffer.u2(0); // attributes_count
}

// public static E[] values() { return (E[]) $VALUES.clone(); }
function emitValuesMethod(
  cp: ConstantPool,
  name: string,
  valuesField: string,
  arrayDesc: string,
): ByteBuffer {
  const info = new ByteBuffer();
  info.u2(ACC_PUBLIC | ACC_STATIC);
  info.u2(cp.utf8("values"));
  info.u2(cp.utf8(`()${arrayDesc}`));
  const code = new ByteBuffer();
  code.u1(OP_GETSTATIC);
  code.u2(cp.fieldref(name, valuesField, arrayDesc));
  code.u1(OP_INVOKEVIRTUAL);
  code.u2(cp.methodref(arrayDesc, "clone", "()Ljava/lang/Object;"));
  code.u1(OP_CHECKCAST);
  code.u2(cp.classInfo(arrayDesc));
  code.u1(OP_ARETURN);
  writeCodeAttribute(info, cp, { code, maxStack: 1, maxLocals: 0 });
  return info;
}

// public static E valueOf(String name) { return (E) Enum.valueOf(E.class, name); }
function emitValueOfMethod(cp: ConstantPool, name: string, selfDesc: string): ByteBuffer {
  const info = new ByteBuffer();
  info.u2(ACC_PUBLIC | ACC_STATIC);
  info.u2(cp.utf8("valueOf"));
  info.u2(cp.utf8(`(Ljava/lang/String;)${selfDesc}`));
  const code = new ByteBuffer();
  code.u1(OP_LDC_W);
  code.u2(cp.classInfo(name)); // ldc of the Class literal E.class
  code.u1(OP_ALOAD_0);
  code.u1(OP_INVOKESTATIC);
  code.u2(
    cp.methodref(
      "java/lang/Enum",
      "valueOf",
      "(Ljava/lang/Class;Ljava/lang/String;)Ljava/lang/Enum;",
    ),
  );
  code.u1(OP_CHECKCAST);
  code.u2(cp.classInfo(name));
  code.u1(OP_ARETURN);
  writeCodeAttribute(info, cp, { code, maxStack: 2, maxLocals: 1 });
  return info;
}

// An enum constructor: descriptor (Ljava/lang/String;I<userparams>)V, emitted in
// enumCtor mode (synthetic name/ordinal params, super(name, ordinal)).
function emitEnumConstructor(
  ctor: ConstructorDeclaration,
  cp: ConstantPool,
  program: Program,
  checker: Checker,
  name: string,
  instanceInits: FieldInit[],
  lambdaMethods: ByteBuffer[],
): ByteBuffer {
  const userParams = ctor.parameters.map(p => paramDescriptor(p as Parameter, program)).join("");
  const descriptor = `(Ljava/lang/String;I${userParams})V`;
  const info = new ByteBuffer();
  info.u2(ACC_PRIVATE);
  info.u2(cp.utf8("<init>"));
  info.u2(cp.utf8(descriptor));
  let body: MethodBody;
  try {
    body = generateBody(
      ctor,
      cp,
      program,
      checker,
      name,
      "java/lang/Enum",
      instanceInits,
      lambdaMethods,
      undefined,
      true,
    );
  } catch (e) {
    if (!(e instanceof UnsupportedEmit)) throw e;
    const argsSize =
      3 +
      ctor.parameters.reduce((n, p) => n + slotsOf(paramDescriptor(p as Parameter, program)), 0);
    const code = new ByteBuffer();
    code.u1(OP_ALOAD_0);
    code.u1(OP_ALOAD_0 + 1);
    code.u1(OP_ILOAD_0 + 2);
    code.u1(OP_INVOKESPECIAL);
    code.u2(cp.methodref("java/lang/Enum", "<init>", "(Ljava/lang/String;I)V"));
    code.u1(OP_RETURN);
    body = { code, maxStack: 3, maxLocals: argsSize };
  }
  writeCodeAttribute(info, cp, body);
  return info;
}

// Emit an enum declaration: a final class extending java.lang.Enum, with a
// static field per constant, a synthetic $VALUES array, the implicit values()
// and valueOf(String) methods, and a <clinit> that builds the constants.
export function emitEnum(
  declaration: EnumDeclaration,
  program: Program,
  checker: Checker,
): EmittedClass {
  program.getGlobalIndex();
  const name = declaration.symbol ? binaryName(declaration.symbol) : declaration.name.text;
  const selfDesc = `L${name};`;
  const arrayDesc = `[${selfDesc}`;
  const VALUES = "$VALUES";
  const superInternalName = "java/lang/Enum";
  const interfaceNames = (declaration.implementsTypes ?? [])
    .map(t => resolveInternalName(t, declaration, program))
    .filter((n): n is string => n !== undefined);
  const isPublic = (declaration.modifiers ?? []).some(m => m.kind === SyntaxKind.PublicKeyword);
  const accessFlags = ACC_SUPER | ACC_ENUM | ACC_FINAL | (isPublic ? ACC_PUBLIC : 0);

  const cp = new ConstantPool();
  const thisClassIndex = cp.classInfo(name);
  const superClassIndex = cp.classInfo(superInternalName);
  const interfaceIndices = interfaceNames.map(n => cp.classInfo(n));

  // Fields: declared fields, a constant field each, and the $VALUES array.
  const fieldsBuf = new ByteBuffer();
  const userFields = emitFields(declaration as unknown as ClassDeclaration, cp, program);
  fieldsBuf.append(userFields.buffer);
  let fieldCount = userFields.count;
  for (const c of declaration.enumConstants) {
    emitFieldInfo(
      fieldsBuf,
      cp,
      ACC_PUBLIC | ACC_STATIC | ACC_FINAL | ACC_ENUM,
      c.name.text,
      selfDesc,
    );
    fieldCount++;
  }
  emitFieldInfo(
    fieldsBuf,
    cp,
    ACC_PRIVATE | ACC_STATIC | ACC_FINAL | ACC_SYNTHETIC,
    VALUES,
    arrayDesc,
  );
  fieldCount++;

  const { instanceInits, staticInits } = collectFieldInits(declaration.members, name, program);

  const methods = new ByteBuffer();
  let methodCount = 0;
  const lambdaMethods: ByteBuffer[] = [];
  const declaredCtors = declaration.members.filter(
    m => m.kind === SyntaxKind.ConstructorDeclaration,
  ) as ConstructorDeclaration[];

  // Constructors (enum mode). A synthesized one when none is declared.
  if (declaredCtors.length === 0) {
    const defaultCtor = {
      kind: SyntaxKind.ConstructorDeclaration,
      parameters: [],
      body: { kind: SyntaxKind.Block, statements: [] },
    } as unknown as ConstructorDeclaration;
    methods.append(
      emitEnumConstructor(defaultCtor, cp, program, checker, name, instanceInits, lambdaMethods),
    );
    methodCount++;
  } else {
    for (const ctor of declaredCtors) {
      methods.append(
        emitEnumConstructor(ctor, cp, program, checker, name, instanceInits, lambdaMethods),
      );
      methodCount++;
    }
  }

  // Per-constant construction info for <clinit> (pick the ctor by arg count).
  const constants = declaration.enumConstants.map((c, i) => {
    const args = [...(c.arguments ?? [])];
    const ctor = declaredCtors.find(k => k.parameters.length === args.length);
    const userParamDescs = ctor
      ? ctor.parameters.map(p => paramDescriptor(p as Parameter, program))
      : [];
    return {
      name: c.name.text,
      ordinal: i,
      ctorDescriptor: `(Ljava/lang/String;I${userParamDescs.join("")})V`,
      userParamDescs,
      args,
    };
  });

  // Declared methods.
  for (const member of declaration.members) {
    if (member.kind !== SyntaxKind.MethodDeclaration) continue;
    methods.append(
      emitMethod(member as MethodDeclaration, cp, program, checker, name, lambdaMethods),
    );
    methodCount++;
  }

  // Implicit values() and valueOf(String).
  methods.append(emitValuesMethod(cp, name, VALUES, arrayDesc));
  methodCount++;
  methods.append(emitValueOfMethod(cp, name, selfDesc));
  methodCount++;

  // <clinit>: build the constants and $VALUES, then run static field inits.
  const clinit = {
    kind: SyntaxKind.MethodDeclaration,
    modifiers: [{ kind: SyntaxKind.StaticKeyword }],
    parameters: [],
    returnType: { kind: SyntaxKind.PrimitiveType, keyword: SyntaxKind.VoidKeyword },
    name: { text: "<clinit>" },
    body: { kind: SyntaxKind.Block, statements: [] },
  } as unknown as MethodDeclaration;
  const clinitInfo = new ByteBuffer();
  clinitInfo.u2(ACC_STATIC);
  clinitInfo.u2(cp.utf8("<clinit>"));
  clinitInfo.u2(cp.utf8("()V"));
  const enumClinitData = {
    enumInternal: name,
    selfDesc,
    arrayDesc,
    valuesField: VALUES,
    constants,
  };
  let clinitBody: MethodBody;
  try {
    clinitBody = generateBody(
      clinit,
      cp,
      program,
      checker,
      name,
      undefined,
      staticInits,
      lambdaMethods,
      undefined,
      false,
      enumClinitData,
    );
  } catch (e) {
    if (!(e instanceof UnsupportedEmit)) throw e;
    // An unsupported user static initializer: still build the constants and
    // $VALUES (essential for the enum), just drop the failing inits.
    try {
      clinitBody = generateBody(
        clinit,
        cp,
        program,
        checker,
        name,
        undefined,
        [],
        [],
        undefined,
        false,
        enumClinitData,
      );
    } catch (e2) {
      if (!(e2 instanceof UnsupportedEmit)) throw e2;
      // Even constructing a constant (e.g. an unsupported argument) failed:
      // fall back to an empty <clinit>. The class still verifies.
      clinitBody = generateBody(
        clinit,
        cp,
        program,
        checker,
        name,
        undefined,
        [],
        [],
        undefined,
        false,
        undefined,
      );
    }
  }
  writeCodeAttribute(clinitInfo, cp, clinitBody);
  methods.append(clinitInfo);
  methodCount++;

  for (const impl of lambdaMethods) {
    methods.append(impl);
    methodCount++;
  }

  const { buffer: classAttributes, count: classAttributeCount } = buildClassAttributes(
    cp,
    sourceNameOf(declaration),
  );

  return {
    name,
    bytes: assembleClassFile({
      cp,
      accessFlags,
      thisClassIndex,
      superClassIndex,
      interfaceIndices,
      fields: fieldsBuf,
      fieldCount,
      methods,
      methodCount,
      attributes: classAttributes,
      attributeCount: classAttributeCount,
    }),
  };
}
