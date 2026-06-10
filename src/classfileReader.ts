// Classpath support: parse compiled .class files and regenerate a Java stub
// source (public API only, erased types), which feeds through the normal
// parse/bind pipeline exactly like the hand-written JDK stub. Loaded types
// resolve for compilation and the LSP, but carry no code.

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

import type { Program } from "./program.ts";

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
}

interface ClassInfo {
  flags: number;
  /** Binary name, e.g. "com/app/Foo". */
  name: string;
  superName?: string;
  interfaces: string[];
  fields: MemberInfo[];
  methods: MemberInfo[];
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
        utf8[i] = new TextDecoder().decode(bytes.subarray(at, at + length));
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

  const readMembers = (): MemberInfo[] => {
    const count = u2();
    const members: MemberInfo[] = [];
    for (let i = 0; i < count; i++) {
      const memberFlags = u2();
      const memberName = utf8[u2()] ?? "";
      const descriptor = utf8[u2()] ?? "";
      const attributeCount = u2();
      for (let a = 0; a < attributeCount; a++) {
        at += 2; // attribute name
        // NOT `at += u4()`: the left operand is captured before u4() advances
        // `at` internally, which would silently lose those four bytes.
        const length = u4();
        at += length;
      }
      members.push({ flags: memberFlags, name: memberName, descriptor });
    }
    return members;
  };
  const fields = readMembers();
  const methods = readMembers();
  return { flags, name, superName, interfaces, fields, methods };
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
      .replace(/\//g, ".")
      .replace(/\$/g, ".");
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

/**
 * The Java stub source for a parsed class, or undefined when the class cannot
 * be expressed as a top-level stub (nested classes, annotations, module-info).
 */
export function classFileToStub(bytes: Uint8Array): { name: string; source: string } | undefined {
  const info = parseClassFile(bytes);
  if (info.name.includes("$") || info.name === "module-info") return undefined;
  if (info.flags & ACC_ANNOTATION) return undefined;

  const slash = info.name.lastIndexOf("/");
  const packageName = slash < 0 ? "" : info.name.slice(0, slash).replace(/\//g, ".");
  const simpleName = slash < 0 ? info.name : info.name.slice(slash + 1);
  const isInterface = (info.flags & ACC_INTERFACE) !== 0;
  const isEnum = (info.flags & ACC_ENUM) !== 0;

  const lines: string[] = [];
  if (packageName) lines.push(`package ${packageName};`, "");

  const head: string[] = ["public"];
  if (!isInterface && !isEnum && info.flags & ACC_ABSTRACT) head.push("abstract");
  if (!isInterface && !isEnum && info.flags & ACC_FINAL) head.push("final");
  head.push(isInterface ? "interface" : isEnum ? "enum" : "class", simpleName);
  const externalSuper =
    info.superName &&
    info.superName !== "java/lang/Object" &&
    info.superName !== "java/lang/Enum" &&
    info.superName !== "java/lang/Record"
      ? info.superName.replace(/\//g, ".")
      : undefined;
  if (externalSuper && !isInterface) head.push("extends", externalSuper);
  const interfaceNames = info.interfaces
    .filter(i => !i.includes("$"))
    .map(i => i.replace(/\//g, "."));
  if (interfaceNames.length > 0) {
    head.push(isInterface ? "extends" : "implements", interfaceNames.join(", "));
  }
  lines.push(`${head.join(" ")} {`);

  if (isEnum) {
    const constants = info.fields
      .filter(f => f.flags & ACC_ENUM && f.descriptor === `L${info.name};`)
      .map(f => f.name);
    lines.push(`  ${constants.join(", ")};`);
  }

  for (const field of info.fields) {
    if (!visible(field)) continue;
    if (isEnum && field.flags & ACC_ENUM) continue; // already listed as constants
    const mods = [
      field.flags & ACC_PROTECTED ? "protected" : "public",
      ...(field.flags & ACC_STATIC ? ["static"] : []),
      ...(field.flags & ACC_FINAL ? ["final"] : []),
    ];
    lines.push(`  ${mods.join(" ")} ${typeAt(field.descriptor, 0).text} ${field.name};`);
  }

  for (const method of info.methods) {
    if (!visible(method) || method.name === "<clinit>") continue;
    // values()/valueOf(String) collide with the checker's synthesized enum statics.
    if (isEnum && (method.name === "values" || method.name === "valueOf")) continue;
    const { params, returns } = methodTypes(method.descriptor);
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
      lines.push(`  ${access} ${simpleName}(${paramList}) {}`);
      continue;
    }
    const mods: string[] = isInterface
      ? [...(isStatic ? ["static"] : isAbstract ? [] : ["default"])]
      : [access, ...(isStatic ? ["static"] : []), ...(isAbstract ? ["abstract"] : [])];
    const signature = `${mods.length > 0 ? mods.join(" ") + " " : ""}${returns} ${method.name}(${paramList})`;
    if (isAbstract) lines.push(`  ${signature};`);
    else lines.push(`  ${signature} {${defaultReturn(returns)} }`);
  }

  lines.push("}", "");
  return { name: info.name, source: lines.join("\n") };
}

/**
 * Scan classpath directories recursively for .class files and register each as
 * a stub source under classpath:///<binary-name>.java. Returns the number of
 * types loaded; unreadable or inexpressible classes are skipped.
 */
export function loadClassPath(program: Program, directories: readonly string[]): number {
  let loaded = 0;
  const visit = (dir: string): void => {
    let entries: string[];
    try {
      entries = readdirSync(dir);
    } catch {
      return; // missing or unreadable classpath entry
    }
    for (const entry of entries) {
      const full = join(dir, entry);
      if (statSync(full).isDirectory()) {
        visit(full);
      } else if (entry.endsWith(".class")) {
        try {
          const stub = classFileToStub(readFileSync(full));
          if (stub) {
            program.addProjectFile(`classpath:///${stub.name}.java`, stub.source);
            loaded++;
          }
        } catch {
          // a malformed class file never breaks the build
        }
      }
    }
  };
  for (const dir of directories) visit(dir);
  return loaded;
}
