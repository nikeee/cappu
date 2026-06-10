// Classpath support: parse compiled .class files and regenerate a Java stub
// source (public API only, erased types), which feeds through the normal
// parse/bind pipeline exactly like the hand-written JDK stub. Loaded types
// resolve for compilation and the LSP, but carry no code.

import { globSync, readFileSync } from "node:fs";
import { join } from "node:path";

import type { Program } from "./program.ts";
import { readZipEntries } from "./zipReader.ts";

const UTF8 = new TextDecoder();

const ACC_PUBLIC = 0x0001;
const ACC_PROTECTED = 0x0004;
const ACC_STATIC = 0x0008;
const ACC_FINAL = 0x0010;
const ACC_BRIDGE = 0x0040;
const ACC_VARARGS = 0x0080;
const ACC_INTERFACE = 0x0200;
const ACC_ABSTRACT = 0x0400;
const ACC_SYNTHETIC = 0x1000;
const ACC_ANNOTATION = 0x2000;
const ACC_ENUM = 0x4000;

interface MemberInfo {
  flags: number;
  name: string;
  descriptor: string;
  /** Generic signature (JVMS 4.7.9), when the declaration was generic. */
  signature?: string;
}

interface ClassInfo {
  flags: number;
  /** Binary name, e.g. "com/app/Foo". */
  name: string;
  superName?: string;
  interfaces: string[];
  fields: MemberInfo[];
  methods: MemberInfo[];
  signature?: string;
}

class ClassFileError extends Error {}

// Parse just the structure a stub needs: constant pool (for names), the class
// header, and each member's name/descriptor/flags. Attribute bodies are skipped.
function parseClassFile(bytes: Uint8Array): ClassInfo {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  let at = 0;
  const u1 = (): number => view.getUint8(at++);
  const u2 = (): number => {
    const v = view.getUint16(at);
    at += 2;
    return v;
  };
  const u4 = (): number => {
    const v = view.getUint32(at);
    at += 4;
    return v;
  };

  if (u4() !== 0xcafebabe) throw new ClassFileError("not a class file");
  at += 4; // minor + major version

  const poolCount = u2();
  const utf8: (string | undefined)[] = new Array(poolCount);
  const classNameIndex: (number | undefined)[] = new Array(poolCount);
  for (let i = 1; i < poolCount; i++) {
    const tag = u1();
    switch (tag) {
      case 1: {
        const length = u2();
        utf8[i] = UTF8.decode(bytes.subarray(at, at + length));
        at += length;
        break;
      }
      case 7:
        classNameIndex[i] = u2();
        break;
      case 8:
      case 16:
      case 19:
      case 20:
        at += 2;
        break;
      case 15:
        at += 3;
        break;
      case 3:
      case 4:
      case 9:
      case 10:
      case 11:
      case 12:
      case 17:
      case 18:
        at += 4;
        break;
      case 5:
      case 6:
        at += 8;
        i++; // longs/doubles take two pool slots
        break;
      default:
        throw new ClassFileError(`unknown constant pool tag ${tag}`);
    }
  }
  const className = (index: number): string | undefined => {
    const nameIndex = classNameIndex[index];
    return nameIndex === undefined ? undefined : utf8[nameIndex];
  };

  const flags = u2();
  const name = className(u2());
  if (!name) throw new ClassFileError("missing this_class");
  const superIndex = u2();
  const superName = superIndex === 0 ? undefined : className(superIndex);
  const interfaceCount = u2();
  const interfaces: string[] = [];
  for (let i = 0; i < interfaceCount; i++) {
    const n = className(u2());
    if (n) interfaces.push(n);
  }

  // Read an attribute table, returning the Signature attribute's string if one
  // is present; every attribute body is skipped.
  const readAttributes = (): string | undefined => {
    const attributeCount = u2();
    let signature: string | undefined;
    for (let a = 0; a < attributeCount; a++) {
      const attrName = utf8[u2()];
      // NOT `at += u4()`: the left operand is captured before u4() advances
      // `at` internally, which would silently lose those four bytes.
      const length = u4();
      if (attrName === "Signature" && length === 2) signature = utf8[view.getUint16(at)];
      at += length;
    }
    return signature;
  };

  const readMembers = (): MemberInfo[] => {
    const count = u2();
    const members: MemberInfo[] = [];
    for (let i = 0; i < count; i++) {
      const memberFlags = u2();
      const memberName = utf8[u2()] ?? "";
      const descriptor = utf8[u2()] ?? "";
      const signature = readAttributes();
      members.push({ flags: memberFlags, name: memberName, descriptor, signature });
    }
    return members;
  };
  const fields = readMembers();
  const methods = readMembers();
  const signature = readAttributes(); // class-level attributes
  return { flags, name, superName, interfaces, fields, methods, signature };
}

// --- descriptor -> source type --------------------------------------------------

const PRIMITIVES: Record<string, string> = {
  B: "byte",
  C: "char",
  D: "double",
  F: "float",
  I: "int",
  J: "long",
  S: "short",
  Z: "boolean",
  V: "void",
};

function typeAt(descriptor: string, at: number): { text: string; next: number } {
  let arrays = 0;
  while (descriptor[at] === "[") {
    arrays++;
    at++;
  }
  let base: string;
  if (descriptor[at] === "L") {
    const end = descriptor.indexOf(";", at);
    base = descriptor
      .slice(at + 1, end)
      .replaceAll("/", ".")
      .replaceAll("$", ".");
    at = end + 1;
  } else {
    base = PRIMITIVES[descriptor[at]!] ?? "java.lang.Object";
    at++;
  }
  return { text: base + "[]".repeat(arrays), next: at };
}

function methodTypes(descriptor: string): { params: string[]; returns: string } {
  const params: string[] = [];
  let at = 1; // past '('
  while (descriptor[at] !== ")") {
    const { text, next } = typeAt(descriptor, at);
    params.push(text);
    at = next;
  }
  return { params, returns: typeAt(descriptor, at + 1).text };
}

// --- generic signature -> source (JVMS 4.7.9.1) -----------------------------------

// A cursor over a signature string; rendering mirrors the grammar directly.
class SignatureReader {
  at = 0;
  constructor(readonly text: string) {}

  peek(): string {
    return this.text[this.at] ?? "";
  }
  take(): string {
    return this.text[this.at++] ?? "";
  }

  /** FormalTypeParameters: `<T:...:...U:...>` -> "<T extends X & Y, U>" or "". */
  typeParameters(): string {
    if (this.peek() !== "<") return "";
    this.take();
    const params: string[] = [];
    while (this.peek() !== ">") {
      const colon = this.text.indexOf(":", this.at);
      const name = this.text.slice(this.at, colon);
      this.at = colon;
      const bounds: string[] = [];
      while (this.peek() === ":") {
        this.take();
        if (this.peek() === ":") continue; // empty class bound (interface first)
        bounds.push(this.referenceType());
      }
      const real = bounds.filter(b => b !== "java.lang.Object");
      params.push(real.length > 0 ? `${name} extends ${real.join(" & ")}` : name);
    }
    this.take(); // '>'
    return `<${params.join(", ")}>`;
  }

  /** JavaTypeSignature: a primitive or a ReferenceTypeSignature. */
  javaType(): string {
    const c = this.peek();
    if (PRIMITIVES[c]) {
      this.take();
      return PRIMITIVES[c]!;
    }
    return this.referenceType();
  }

  referenceType(): string {
    const c = this.peek();
    if (c === "T") {
      this.take();
      const semi = this.text.indexOf(";", this.at);
      const name = this.text.slice(this.at, semi);
      this.at = semi + 1;
      return name;
    }
    if (c === "[") {
      this.take();
      return `${this.javaType()}[]`;
    }
    // ClassTypeSignature: Lpkg/Name<args>.Inner<args>;
    this.take(); // 'L'
    let out = "";
    for (;;) {
      const ch = this.take();
      if (ch === ";") break;
      if (ch === "/") out += ".";
      else if (ch === "$") out += ".";
      else if (ch === "<") {
        out += `<${this.typeArguments()}>`;
        // after '>' the next char is ';' or '.' (inner class continuation)
      } else if (ch === ".") out += ".";
      else out += ch;
    }
    return out;
  }

  private typeArguments(): string {
    const args: string[] = [];
    while (this.peek() !== ">") {
      const c = this.peek();
      if (c === "*") {
        this.take();
        args.push("?");
      } else if (c === "+") {
        this.take();
        args.push(`? extends ${this.referenceType()}`);
      } else if (c === "-") {
        this.take();
        args.push(`? super ${this.referenceType()}`);
      } else {
        args.push(this.referenceType());
      }
    }
    this.take(); // '>'
    return args.join(", ");
  }
}

/** ClassSignature -> the pieces the stub head needs. */
function parseClassSignature(signature: string): {
  typeParameters: string;
  superType: string;
  interfaces: string[];
} {
  const r = new SignatureReader(signature);
  const typeParameters = r.typeParameters();
  const superType = r.referenceType();
  const interfaces: string[] = [];
  while (r.at < signature.length) interfaces.push(r.referenceType());
  return { typeParameters, superType, interfaces };
}

/** MethodSignature -> the pieces a method stub needs (throws are dropped). */
function parseMethodSignature(signature: string): {
  typeParameters: string;
  params: string[];
  returns: string;
} {
  const r = new SignatureReader(signature);
  const typeParameters = r.typeParameters();
  r.take(); // '('
  const params: string[] = [];
  while (r.peek() !== ")") params.push(r.javaType());
  r.take(); // ')'
  return { typeParameters, params, returns: r.javaType() };
}

// --- stub source generation ------------------------------------------------------

function defaultReturn(type: string): string {
  if (type === "void") return "";
  if (type === "boolean") return " return false;";
  if (/^(byte|char|short|int|long|float|double)$/.test(type)) return " return 0;";
  return " return null;";
}

function visible(member: MemberInfo): boolean {
  if ((member.flags & (ACC_PUBLIC | ACC_PROTECTED)) === 0) return false;
  return (member.flags & (ACC_SYNTHETIC | ACC_BRIDGE)) === 0;
}

const IMPLICIT_SUPERS = new Set(["java.lang.Object", "java.lang.Enum", "java.lang.Record"]);

// The declaration lines of one type (and, recursively, its nested types).
// `nestedOf` yields the directly nested classes of a binary name; a nested
// declaration is rendered `static` (its real inner-ness lives in the
// InnerClasses attribute, which a resolution stub does not need).
function typeDeclLines(
  info: ClassInfo,
  simpleName: string,
  nestedOf: (binaryName: string) => ClassInfo[],
  indent: string,
  nested: boolean,
): string[] {
  const isInterface = (info.flags & ACC_INTERFACE) !== 0;
  const isEnum = (info.flags & ACC_ENUM) !== 0;
  const lines: string[] = [];

  // The class Signature (when generic) carries type parameters and the
  // parameterized super types; otherwise the erased binary names are used.
  const classSig = info.signature ? parseClassSignature(info.signature) : undefined;
  const superSource = classSig
    ? classSig.superType
    : (info.superName?.replaceAll("/", ".") ?? "java.lang.Object");
  const superBase = superSource.replace(/<.*/, "");
  const interfaceSources = classSig
    ? classSig.interfaces
    : info.interfaces.map(i => i.replaceAll("/", "."));

  const head: string[] = ["public"];
  if (nested) head.push("static");
  if (!isInterface && !isEnum && info.flags & ACC_ABSTRACT) head.push("abstract");
  if (!isInterface && !isEnum && info.flags & ACC_FINAL) head.push("final");
  head.push(
    isInterface ? "interface" : isEnum ? "enum" : "class",
    `${simpleName}${classSig?.typeParameters ?? ""}`,
  );
  if (!isInterface && !isEnum && !IMPLICIT_SUPERS.has(superBase)) {
    head.push("extends", superSource);
  }
  const interfaceNames = interfaceSources.map(i => i.replaceAll("$", "."));
  if (interfaceNames.length > 0) {
    head.push(isInterface ? "extends" : "implements", interfaceNames.join(", "));
  }
  lines.push(`${indent}${head.join(" ")} {`);

  if (isEnum) {
    const constants = info.fields
      .filter(f => f.flags & ACC_ENUM && f.descriptor === `L${info.name};`)
      .map(f => f.name);
    lines.push(`${indent}  ${constants.join(", ")};`);
  }

  for (const field of info.fields) {
    if (!visible(field)) continue;
    if (isEnum && field.flags & ACC_ENUM) continue; // already listed as constants
    const mods = [
      field.flags & ACC_PROTECTED ? "protected" : "public",
      ...(field.flags & ACC_STATIC ? ["static"] : []),
      ...(field.flags & ACC_FINAL ? ["final"] : []),
    ];
    const fieldType = field.signature
      ? new SignatureReader(field.signature).javaType()
      : typeAt(field.descriptor, 0).text;
    lines.push(`${indent}  ${mods.join(" ")} ${fieldType} ${field.name};`);
  }

  for (const method of info.methods) {
    if (!visible(method) || method.name === "<clinit>") continue;
    // values()/valueOf(String) collide with the checker's synthesized enum statics.
    if (isEnum && (method.name === "values" || method.name === "valueOf")) continue;
    // The method Signature (when generic) carries its own type parameters and
    // the un-erased parameter/return types (synthetic params are omitted there).
    const methodSig = method.signature ? parseMethodSignature(method.signature) : undefined;
    const { params, returns } = methodSig ?? methodTypes(method.descriptor);
    const typeParams = methodSig?.typeParameters ? `${methodSig.typeParameters} ` : "";
    const isVarargs = (method.flags & ACC_VARARGS) !== 0;
    const paramList = params
      .map((p, i) => {
        const varargs = isVarargs && i === params.length - 1 && p.endsWith("[]");
        return `${varargs ? p.slice(0, -2) + "..." : p} p${i}`;
      })
      .join(", ");
    const isAbstract = (method.flags & ACC_ABSTRACT) !== 0;
    const isStatic = (method.flags & ACC_STATIC) !== 0;
    const access = method.flags & ACC_PROTECTED ? "protected" : "public";
    if (method.name === "<init>") {
      if (isEnum) continue; // enum constructors are not callable from outside
      lines.push(`${indent}  ${access} ${typeParams}${simpleName}(${paramList}) {}`);
      continue;
    }
    const mods: string[] = isInterface
      ? [...(isStatic ? ["static"] : isAbstract ? [] : ["default"])]
      : [access, ...(isStatic ? ["static"] : []), ...(isAbstract ? ["abstract"] : [])];
    const signature = `${mods.length > 0 ? mods.join(" ") + " " : ""}${typeParams}${returns} ${method.name}(${paramList})`;
    if (isAbstract) lines.push(`${indent}  ${signature};`);
    else lines.push(`${indent}  ${signature} {${defaultReturn(returns)} }`);
  }

  for (const child of nestedOf(info.name)) {
    const childSimple = child.name.slice(child.name.lastIndexOf("$") + 1);
    lines.push("", ...typeDeclLines(child, childSimple, nestedOf, `${indent}  `, true));
  }

  lines.push(`${indent}}`);
  return lines;
}

// A nested binary name segment starting with a digit is an anonymous or local
// class - never referencable from source, so never stubbed.
function isAnonymousOrLocal(binaryName: string): boolean {
  return binaryName.split("$").some((segment, i) => i > 0 && /^\d/.test(segment));
}

function stubbable(info: ClassInfo): boolean {
  return info.name !== "module-info" && (info.flags & ACC_ANNOTATION) === 0;
}

/**
 * Group parsed classes into top-level stub sources, nesting Outer$Inner. A
 * nested class whose outer type is missing from the classpath stays orphaned -
 * it was unreachable from source without the outer type anyway.
 */
function buildStubs(classes: ClassInfo[]): { name: string; source: string }[] {
  // group nested classes by their immediate enclosing class (the binary name
  // up to the last '$') so each lookup below is O(1)
  const byParent = Map.groupBy(
    classes.filter(c => c.name.includes("$")),
    c => c.name.slice(0, c.name.lastIndexOf("$")),
  );
  const nestedOf = (binaryName: string): ClassInfo[] =>
    (byParent.get(binaryName) ?? []).filter(c => stubbable(c) && !isAnonymousOrLocal(c.name));
  const stubs: { name: string; source: string }[] = [];
  for (const info of classes) {
    if (info.name.includes("$") || !stubbable(info)) continue;
    const slash = info.name.lastIndexOf("/");
    const packageName = slash < 0 ? "" : info.name.slice(0, slash).replaceAll("/", ".");
    const simpleName = slash < 0 ? info.name : info.name.slice(slash + 1);
    const lines = packageName ? [`package ${packageName};`, ""] : [];
    lines.push(...typeDeclLines(info, simpleName, nestedOf, "", false), "");
    stubs.push({ name: info.name, source: lines.join("\n") });
  }
  return stubs;
}

/**
 * The Java stub source for a single parsed class, or undefined when the class
 * cannot be expressed standalone (nested classes, annotations, module-info).
 */
export function classFileToStub(bytes: Uint8Array): { name: string; source: string } | undefined {
  const info = parseClassFile(bytes);
  if (info.name.includes("$") || !stubbable(info)) return undefined;
  return buildStubs([info])[0];
}

/**
 * Scan classpath entries - directories (recursively) or .jar files - for
 * .class files and register each as a stub source under
 * classpath:///<binary-name>.java. Returns the number of types loaded;
 * unreadable or inexpressible classes are skipped.
 */
export function loadClassPath(program: Program, entries: readonly string[]): number {
  // Collect everything first so Outer$Inner classes can nest into their outer
  // type's stub regardless of file order.
  const collected: ClassInfo[] = [];
  const addClassBytes = (bytes: Uint8Array): void => {
    try {
      collected.push(parseClassFile(bytes));
    } catch {
      // a malformed class file never breaks the build
    }
  };
  const visitJar = (path: string): void => {
    try {
      const zipEntries = readZipEntries(readFileSync(path));
      for (const entry of zipEntries ?? []) {
        if (!entry.name.endsWith(".class") || entry.name.startsWith("META-INF/")) continue;
        addClassBytes(entry.read());
      }
    } catch {
      // an unreadable or corrupt jar never breaks the build
    }
  };
  const visitDirectory = (dir: string): void => {
    // a missing or unreadable classpath entry simply matches nothing
    for (const relative of globSync("**/*.{class,jar}", { cwd: dir })) {
      const full = join(dir, relative);
      if (relative.endsWith(".class")) addClassBytes(readFileSync(full));
      else visitJar(full);
    }
  };
  for (const entry of entries) {
    if (entry.endsWith(".jar")) visitJar(entry);
    else visitDirectory(entry);
  }
  const stubs = buildStubs(collected);
  for (const stub of stubs) {
    program.addProjectFile(`classpath:///${stub.name}.java`, stub.source);
  }
  return stubs.length;
}
