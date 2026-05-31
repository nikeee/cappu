// Bytecode emitter: Java source -> JVM .class files. The structure follows the
// JVM Specification (JVMS SE 21) chapter 4 (the class file format) and chapter 6
// (instruction set). This is the first milestone: it produces a valid, verifiable
// class file for a top-level class, with the synthesized default constructor
// (aload_0; invokespecial Object.<init>; return). Real member/body code generation
// comes in later milestones. We target major version 65 (Java 21) so the output
// loads on the local JVM.
//
// Reference output is cross-checked against `javac` in the tests.

import { forEachChild } from "./parser.ts";
import { type ClassDeclaration, type Node, type SourceFile, SyntaxKind } from "./types.ts";

const MAGIC = 0xcafebabe;
const MINOR_VERSION = 0;
const MAJOR_VERSION = 65; // Java 21

// Class access flags (JVMS 4.1, Table 4.1-B).
const ACC_PUBLIC = 0x0001;
const ACC_FINAL = 0x0010;
const ACC_SUPER = 0x0020;
const ACC_ABSTRACT = 0x0400;

// Constant pool tags (JVMS 4.4, Table 4.4-A).
const CONSTANT_Utf8 = 1;
const CONSTANT_Class = 7;
const CONSTANT_NameAndType = 12;
const CONSTANT_Methodref = 10;

// Opcodes (JVMS 6.5) used so far.
const OP_ALOAD_0 = 0x2a;
const OP_INVOKESPECIAL = 0xb7;
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

// The default no-arg constructor: invokes the super constructor and returns.
function emitDefaultConstructor(cp: ConstantPool, superInternalName: string): ByteBuffer {
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
  method.u2(0); // access_flags
  method.u2(cp.utf8("<init>"));
  method.u2(cp.utf8("()V"));
  method.u2(1); // attributes_count
  method.append(codeAttr);
  return method;
}

export interface EmittedClass {
  /** Simple class name (becomes <name>.class). */
  readonly name: string;
  readonly bytes: Uint8Array;
}

function emitClass(declaration: ClassDeclaration): EmittedClass {
  const name = declaration.name.text;
  const superInternalName = "java/lang/Object"; // resolving `extends` comes later

  const cp = new ConstantPool();
  // Reserve the well-known entries in a stable order; the constructor adds the
  // super Methodref/Class/NameAndType as needed.
  const thisClassIndex = cp.classInfo(name);
  const superClassIndex = cp.classInfo(superInternalName);
  const constructor = emitDefaultConstructor(cp, superInternalName);

  const out = new ByteBuffer();
  out.u4(MAGIC);
  out.u2(MINOR_VERSION);
  out.u2(MAJOR_VERSION);
  cp.writeInto(out);
  out.u2(classAccessFlags(declaration));
  out.u2(thisClassIndex);
  out.u2(superClassIndex);
  out.u2(0); // interfaces_count
  out.u2(0); // fields_count
  out.u2(1); // methods_count
  out.append(constructor);
  out.u2(0); // attributes_count

  return { name, bytes: out.toUint8Array() };
}

/** Emit a .class file for every top-level class declaration in a source file. */
export function emitSourceFile(sourceFile: SourceFile): EmittedClass[] {
  const result: EmittedClass[] = [];
  forEachChild(sourceFile, (node: Node) => {
    if (node.kind === SyntaxKind.ClassDeclaration) result.push(emitClass(node as ClassDeclaration));
    return undefined;
  });
  return result;
}
