// `cappu decompile`, phase 1.3 (nikeee/cappu#43): reconstruct Java source from
// straight-line bytecode. A symbolic stack interpreter walks a method's
// instructions and turns them back into expressions and statements; anything
// that needs control flow or a method call (later phases) renders as its
// disassembly plus a `throw new UnsupportedOperationException(...)`, so the
// output is always compilable Java.
//
// The text is deliberately rough - callers run it through the formatter
// (src/cli/decompile.ts), which is why this module stays free of a dependency
// on src/format.

import {
  ClassFileError,
  type ClassFile,
  type Code,
  type Constant,
  type Member,
  className,
  findAttribute,
  memberRef,
  readClassFile,
  readCode,
  readExceptions,
  utf8,
} from "./classfile.ts";
import {
  ACC_ABSTRACT,
  ACC_FINAL,
  ACC_INTERFACE,
  ACC_MODULE,
  ACC_NATIVE,
  ACC_PRIVATE,
  ACC_PROTECTED,
  ACC_PUBLIC,
  ACC_STATIC,
  ACC_SYNCHRONIZED,
  ACC_TRANSIENT,
  ACC_VARARGS,
  ACC_VOLATILE,
  type Instruction,
  decodeInstructions,
  descriptorType,
  escapeString,
  javaDoubleText,
  javaFloatText,
} from "./disasm.ts";

// Flags disasm.ts has no use for: they only matter when deciding what is source
// and what the compiler generated.
const ACC_SYNTHETIC = 0x1000;
const ACC_ANNOTATION = 0x2000;
const ACC_ENUM = 0x4000;
const ACC_BRIDGE = 0x0040;

/** Thrown per method when the body is beyond this phase; never escapes. */
class NotDecompilable extends Error {}

// --- expressions ---------------------------------------------------------------------

// Java operator precedence, high binds tighter. Only the levels straight-line
// code can produce are listed.
const PREC_PRIMARY = 15;
const PREC_UNARY = 14;
const PREC_MUL = 12;
const PREC_ADD = 11;
const PREC_SHIFT = 10;
const PREC_REL = 9;
const PREC_AND = 7;
const PREC_XOR = 6;
const PREC_OR = 5;

interface Expr {
  readonly text: string;
  readonly prec: number;
  /** The value's Java type as source text, used to declare locals. */
  readonly type: string;
}

function primary(text: string, type: string): Expr {
  return { text, prec: PREC_PRIMARY, type };
}

/** `expr` parenthesized when it binds looser than the context needs. */
function at(expr: Expr, minimum: number): string {
  return expr.prec < minimum ? `(${expr.text})` : expr.text;
}

function binary(left: Expr, operator: string, right: Expr, prec: number, type: string): Expr {
  // Every operator here is left-associative, so the right operand needs one
  // more level to keep `a - (b - c)` from losing its parentheses.
  return { text: `${at(left, prec)} ${operator} ${at(right, prec + 1)}`, prec, type };
}

const BINARY_OPS: Record<string, { operator: string; prec: number }> = {
  add: { operator: "+", prec: PREC_ADD },
  sub: { operator: "-", prec: PREC_ADD },
  mul: { operator: "*", prec: PREC_MUL },
  div: { operator: "/", prec: PREC_MUL },
  rem: { operator: "%", prec: PREC_MUL },
  shl: { operator: "<<", prec: PREC_SHIFT },
  shr: { operator: ">>", prec: PREC_SHIFT },
  ushr: { operator: ">>>", prec: PREC_SHIFT },
  and: { operator: "&", prec: PREC_AND },
  or: { operator: "|", prec: PREC_OR },
  xor: { operator: "^", prec: PREC_XOR },
};

const PRIMITIVE_OF_PREFIX: Record<string, string> = {
  i: "int",
  l: "long",
  f: "float",
  d: "double",
  a: "java.lang.Object",
  b: "byte",
  c: "char",
  s: "short",
};

const CONVERSIONS: Record<string, string> = {
  i2l: "long",
  i2f: "float",
  i2d: "double",
  l2i: "int",
  l2f: "float",
  l2d: "double",
  f2i: "int",
  f2l: "long",
  f2d: "double",
  d2i: "int",
  d2l: "long",
  d2f: "float",
  i2b: "byte",
  i2c: "char",
  i2s: "short",
};

// --- constants -----------------------------------------------------------------------

/** A `Foo$Bar` binary name as source text. Nested names keep the `$`. */
function typeName(internalName: string): string {
  return internalName.startsWith("[")
    ? descriptorType(internalName, 0).text
    : internalName.replaceAll("/", ".");
}

function intLiteral(value: number): Expr {
  // A negative literal is a unary minus, not part of the token.
  return { text: String(value), prec: value < 0 ? PREC_UNARY : PREC_PRIMARY, type: "int" };
}

function constantExpr(pool: readonly (Constant | undefined)[], index: number): Expr {
  const entry = pool[index];
  switch (entry?.tag) {
    case "int":
      return intLiteral(entry.value);
    case "long":
      return {
        text: `${entry.value}L`,
        prec: entry.value < 0n ? PREC_UNARY : PREC_PRIMARY,
        type: "long",
      };
    case "float": {
      const text = `${javaFloatText(entry.value)}f`;
      return { text, prec: text.startsWith("-") ? PREC_UNARY : PREC_PRIMARY, type: "float" };
    }
    case "double": {
      const text = javaDoubleText(entry.value);
      return { text, prec: text.startsWith("-") ? PREC_UNARY : PREC_PRIMARY, type: "double" };
    }
    case "string":
      return primary(`"${escapeString(utf8(pool, entry.valueIndex) ?? "")}"`, "java.lang.String");
    case "class":
      return primary(`${typeName(utf8(pool, entry.nameIndex) ?? "")}.class`, "java.lang.Class");
    default:
      // A method handle/type or a dynamic constant: only reachable through the
      // features later phases add.
      throw new NotDecompilable(`unsupported constant #${index}`);
  }
}

/**
 * `expr` as it must be written to land in a `target`-typed slot. javac erases
 * boolean and char to int constants, so the literal has to be written back.
 */
function coerce(expr: Expr, target: string): string {
  if (expr.type !== "int" || !/^-?\d+$/.test(expr.text)) return expr.text;
  const value = Number(expr.text);
  if (target === "boolean" && (value === 0 || value === 1)) return value === 1 ? "true" : "false";
  if (target === "char") {
    if (value >= 0x20 && value < 0x7f) return `'${escapeString(String.fromCharCode(value))}'`;
    return `(char) ${expr.text}`;
  }
  return expr.text;
}

// --- locals --------------------------------------------------------------------------

interface LocalEntry {
  readonly startPc: number;
  readonly endPc: number;
  readonly slot: number;
  readonly name: string;
  readonly type: string;
}

/** LocalVariableTable (JVMS 4.7.13): present only for classes built with -g. */
function readLocalVariables(code: Code, pool: readonly (Constant | undefined)[]): LocalEntry[] {
  const attribute = findAttribute(code.attributes, "LocalVariableTable");
  if (!attribute) return [];
  const bytes = attribute.bytes;
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const out: LocalEntry[] = [];
  if (bytes.length < 2) return out;
  const count = view.getUint16(0);
  for (let i = 0; i < count; i++) {
    const at = 2 + i * 10;
    if (at + 10 > bytes.length) break;
    const startPc = view.getUint16(at);
    const descriptor = utf8(pool, view.getUint16(at + 6)) ?? "";
    out.push({
      startPc,
      endPc: startPc + view.getUint16(at + 2),
      name: utf8(pool, view.getUint16(at + 4)) ?? "",
      slot: view.getUint16(at + 8),
      type: descriptor === "" ? "" : descriptorType(descriptor, 0).text,
    });
  }
  return out;
}

interface Local {
  name: string;
  type: string;
  declared: boolean;
}

/** The slot each declared parameter occupies; long and double take two. */
function parameterSlots(descriptor: string, isStatic: boolean): { slot: number; type: string }[] {
  const out: { slot: number; type: string }[] = [];
  let slot = isStatic ? 0 : 1;
  let atIndex = 1; // past '('
  while (descriptor[atIndex] !== ")" && atIndex < descriptor.length) {
    const { text, next } = descriptorType(descriptor, atIndex);
    out.push({ slot, type: text });
    slot += text === "long" || text === "double" ? 2 : 1;
    atIndex = next;
  }
  return out;
}

function returnType(descriptor: string): string {
  const close = descriptor.lastIndexOf(")");
  return descriptorType(descriptor, close + 1).text;
}

// --- the method body -----------------------------------------------------------------

/** Straight-line bytecode of one method, as Java statements. */
class BodyDecompiler {
  private readonly stack: Expr[] = [];
  readonly statements: string[] = [];

  constructor(
    private readonly classFile: ClassFile,
    private readonly locals: Map<number, Local>,
    private readonly localTable: LocalEntry[],
    private readonly methodReturnType: string,
    private readonly isStatic: boolean,
  ) {}

  private push(expr: Expr): void {
    this.stack.push(expr);
  }

  private pop(): Expr {
    const expr = this.stack.pop();
    if (!expr) throw new NotDecompilable("stack underflow");
    return expr;
  }

  private local(slot: number, pc: number, fallbackType: string): Local {
    const existing = this.locals.get(slot);
    if (existing) return existing;
    // A name from the debug table beats `var<slot>`; the scope that covers this
    // pc wins, so slots reused by sibling blocks keep their own names.
    const scoped =
      this.localTable.find(e => e.slot === slot && pc >= e.startPc && pc < e.endPc) ??
      this.localTable.find(e => e.slot === slot);
    const local: Local = {
      name: scoped?.name ?? `var${slot}`,
      type: scoped?.type !== undefined && scoped.type !== "" ? scoped.type : fallbackType,
      declared: false,
    };
    this.locals.set(slot, local);
    return local;
  }

  /** The local slot of `iload_1`-style mnemonics, or the decoded operand. */
  private slotOf(instruction: Instruction): number {
    const suffix = /_(\d)$/.exec(instruction.mnemonic);
    return suffix ? Number(suffix[1]) : instruction.arg;
  }

  private store(slot: number, pc: number, value: Expr, declaredType: string): void {
    const local = this.local(slot, pc, declaredType);
    const text = coerce(value, local.type);
    if (local.declared) {
      this.statements.push(`${local.name} = ${text};`);
    } else {
      local.declared = true;
      this.statements.push(`${local.type} ${local.name} = ${text};`);
    }
  }

  run(instructions: readonly Instruction[]): void {
    for (const [index, instruction] of instructions.entries()) {
      this.step(instruction);
      if (instruction.mnemonic.endsWith("return") && index !== instructions.length - 1) {
        // Code after a return only exists because something branches over it.
        throw new NotDecompilable("unreachable code");
      }
    }
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
  }

  private step(instruction: Instruction): void {
    const { mnemonic, pc } = instruction;
    const pool = this.classFile.pool;

    // Constants.
    if (mnemonic === "nop") return;
    if (mnemonic === "aconst_null") return this.push(primary("null", "java.lang.Object"));
    if (mnemonic.startsWith("iconst_")) {
      return this.push(intLiteral(mnemonic === "iconst_m1" ? -1 : Number(mnemonic.slice(7))));
    }
    if (mnemonic.startsWith("lconst_")) {
      return this.push(primary(`${mnemonic.slice(7)}L`, "long"));
    }
    if (mnemonic.startsWith("fconst_")) {
      return this.push(primary(`${javaFloatText(Number(mnemonic.slice(7)))}f`, "float"));
    }
    if (mnemonic.startsWith("dconst_")) {
      return this.push(primary(javaDoubleText(Number(mnemonic.slice(7))), "double"));
    }
    if (mnemonic === "bipush" || mnemonic === "sipush") {
      return this.push(intLiteral(instruction.arg));
    }
    if (mnemonic === "ldc" || mnemonic === "ldc_w" || mnemonic === "ldc2_w") {
      return this.push(constantExpr(pool, instruction.arg));
    }

    // Loads and stores.
    const base = mnemonic.replace(/(_\d|_w)$/, "");
    if (/^[ilfda]load$/.test(base)) {
      const slot = this.slotOf(instruction);
      if (base === "aload" && slot === 0 && !this.isStatic) {
        return this.push(primary("this", typeName(this.classFile.thisClass)));
      }
      const local = this.local(slot, pc, PRIMITIVE_OF_PREFIX[base[0]!]!);
      return this.push(primary(local.name, local.type));
    }
    if (/^[ilfda]store$/.test(base)) {
      const value = this.pop();
      const fallback = base === "astore" ? value.type : PRIMITIVE_OF_PREFIX[base[0]!]!;
      return this.store(this.slotOf(instruction), pc, value, fallback);
    }
    if (base === "iinc") {
      const local = this.local(instruction.arg, pc, "int");
      const delta = instruction.arg2;
      if (delta === 1) this.statements.push(`${local.name}++;`);
      else if (delta === -1) this.statements.push(`${local.name}--;`);
      else if (delta < 0) this.statements.push(`${local.name} -= ${-delta};`);
      else this.statements.push(`${local.name} += ${delta};`);
      return;
    }

    // Arithmetic, bitwise and conversions.
    const operator = BINARY_OPS[mnemonic.slice(1)];
    if (operator && /^[ilfd]/.test(mnemonic)) {
      const right = this.pop();
      const left = this.pop();
      const type = PRIMITIVE_OF_PREFIX[mnemonic[0]!]!;
      return this.push(binary(left, operator.operator, right, operator.prec, type));
    }
    if (/^[ilfd]neg$/.test(mnemonic)) {
      const value = this.pop();
      return this.push({
        text: `-${at(value, PREC_UNARY)}`,
        prec: PREC_UNARY,
        type: PRIMITIVE_OF_PREFIX[mnemonic[0]!]!,
      });
    }
    const conversion = CONVERSIONS[mnemonic];
    if (conversion) {
      const value = this.pop();
      return this.push({
        text: `(${conversion}) ${at(value, PREC_UNARY)}`,
        prec: PREC_UNARY,
        type: conversion,
      });
    }

    // Fields.
    if (mnemonic === "getstatic" || mnemonic === "getfield") {
      const field = memberRef(pool, instruction.arg);
      if (!field) throw new NotDecompilable("bad field reference");
      const owner = mnemonic === "getstatic" ? typeName(field.owner) : at(this.pop(), PREC_PRIMARY);
      return this.push(primary(`${owner}.${field.name}`, descriptorType(field.descriptor, 0).text));
    }
    if (mnemonic === "putstatic" || mnemonic === "putfield") {
      const field = memberRef(pool, instruction.arg);
      if (!field) throw new NotDecompilable("bad field reference");
      const value = this.pop();
      const type = descriptorType(field.descriptor, 0).text;
      const owner = mnemonic === "putstatic" ? typeName(field.owner) : at(this.pop(), PREC_PRIMARY);
      this.statements.push(`${owner}.${field.name} = ${coerce(value, type)};`);
      return;
    }

    // Arrays.
    if (mnemonic === "arraylength") {
      return this.push(primary(`${at(this.pop(), PREC_PRIMARY)}.length`, "int"));
    }
    if (/^[ilfdabcs]aload$/.test(mnemonic)) {
      const index = this.pop();
      const array = this.pop();
      const element = array.type.endsWith("[]")
        ? array.type.slice(0, -2)
        : PRIMITIVE_OF_PREFIX[mnemonic[0]!]!;
      return this.push(primary(`${at(array, PREC_PRIMARY)}[${index.text}]`, element));
    }
    if (/^[ilfdabcs]astore$/.test(mnemonic)) {
      const value = this.pop();
      const index = this.pop();
      const array = this.pop();
      const element = array.type.endsWith("[]")
        ? array.type.slice(0, -2)
        : PRIMITIVE_OF_PREFIX[mnemonic[0]!]!;
      this.statements.push(
        `${at(array, PREC_PRIMARY)}[${index.text}] = ${coerce(value, element)};`,
      );
      return;
    }
    if (mnemonic === "newarray") {
      const length = this.pop();
      return this.push(
        primary(`new ${instruction.operand}[${length.text}]`, `${instruction.operand}[]`),
      );
    }
    if (mnemonic === "anewarray") {
      const element = typeName(className(pool, instruction.arg) ?? "java/lang/Object");
      const length = this.pop();
      return this.push(primary(`new ${element}[${length.text}]`, `${element}[]`));
    }
    if (mnemonic === "multianewarray") {
      const type = typeName(className(pool, instruction.arg) ?? "");
      const rank = (type.match(/\[\]/g) ?? []).length;
      const sizes: string[] = [];
      for (let i = 0; i < instruction.arg2; i++) sizes.unshift(this.pop().text);
      if (sizes.length > rank) throw new NotDecompilable("multianewarray rank mismatch");
      const element = type.slice(0, type.length - rank * 2);
      const dimensions = sizes.map(size => `[${size}]`).join("");
      return this.push(
        primary(`new ${element}${dimensions}${"[]".repeat(rank - sizes.length)}`, type),
      );
    }

    // Casts.
    if (mnemonic === "checkcast") {
      const type = typeName(className(pool, instruction.arg) ?? "java/lang/Object");
      const value = this.pop();
      return this.push({ text: `(${type}) ${at(value, PREC_UNARY)}`, prec: PREC_UNARY, type });
    }
    if (mnemonic === "instanceof") {
      const type = typeName(className(pool, instruction.arg) ?? "java/lang/Object");
      const value = this.pop();
      return this.push({
        text: `${at(value, PREC_REL + 1)} instanceof ${type}`,
        prec: PREC_REL,
        type: "boolean",
      });
    }

    // Stack shuffling. A dup of anything but a name would duplicate the
    // expression itself (`new int[2][0] = 1;`), so only the trivial case is
    // taken; array initializers and the rest wait for a later phase.
    if (mnemonic === "dup") {
      const value = this.pop();
      if (value.prec !== PREC_PRIMARY || value.text.startsWith("new ")) {
        throw new NotDecompilable("dup of a non-trivial value");
      }
      this.push(value);
      this.push(value);
      return;
    }
    if (mnemonic === "pop") {
      this.pop();
      return;
    }

    // Returns.
    if (mnemonic === "return") {
      this.statements.push("return;");
      return;
    }
    if (/^[ilfda]return$/.test(mnemonic)) {
      this.statements.push(`return ${coerce(this.pop(), this.methodReturnType)};`);
      return;
    }

    throw new NotDecompilable(`unsupported instruction ${mnemonic}`);
  }
}

// --- declarations --------------------------------------------------------------------

function accessModifiers(flags: number): string[] {
  if (flags & ACC_PUBLIC) return ["public"];
  if (flags & ACC_PROTECTED) return ["protected"];
  if (flags & ACC_PRIVATE) return ["private"];
  return [];
}

function simpleName(internalName: string): string {
  const slash = internalName.lastIndexOf("/");
  // A nested class keeps its `$` name: it is a legal Java identifier, and
  // restoring the nesting needs the whole enclosing file (a later phase).
  return slash < 0 ? internalName : internalName.slice(slash + 1);
}

/** The ConstantValue (JVMS 4.7.2) a `static final` field must be initialized to. */
function constantValue(field: Member, classFile: ClassFile): Expr | undefined {
  const attribute = findAttribute(field.attributes, "ConstantValue");
  if (!attribute || attribute.bytes.length < 2) return undefined;
  const index = (attribute.bytes[0]! << 8) | attribute.bytes[1]!;
  try {
    return constantExpr(classFile.pool, index);
  } catch {
    return undefined;
  }
}

function fieldSource(field: Member, classFile: ClassFile): string {
  const type = descriptorType(field.descriptor, 0).text;
  const modifiers = [
    ...accessModifiers(field.flags),
    ...(field.flags & ACC_STATIC ? ["static"] : []),
    ...(field.flags & ACC_FINAL ? ["final"] : []),
    ...(field.flags & ACC_TRANSIENT ? ["transient"] : []),
    ...(field.flags & ACC_VOLATILE ? ["volatile"] : []),
  ];
  const value = constantValue(field, classFile);
  const initializer = value ? ` = ${coerce(value, type)}` : "";
  return `${[...modifiers, type].join(" ")} ${field.name}${initializer};`;
}

function methodModifiers(method: Member, classFile: ClassFile): string[] {
  const isInterface = (classFile.flags & ACC_INTERFACE) !== 0;
  const isStatic = (method.flags & ACC_STATIC) !== 0;
  const isAbstract = (method.flags & ACC_ABSTRACT) !== 0;
  return [
    ...accessModifiers(method.flags),
    ...(isAbstract ? ["abstract"] : []),
    ...(isStatic ? ["static"] : []),
    ...(method.flags & ACC_FINAL ? ["final"] : []),
    ...(method.flags & ACC_SYNCHRONIZED ? ["synchronized"] : []),
    ...(method.flags & ACC_NATIVE ? ["native"] : []),
    ...(isInterface && !isStatic && !isAbstract && !(method.flags & ACC_PRIVATE)
      ? ["default"]
      : []),
  ];
}

/** Parameter and `this` slots, named from the debug table when there is one. */
function buildLocals(
  method: Member,
  localTable: readonly LocalEntry[],
  isStatic: boolean,
): Map<number, Local> {
  const locals = new Map<number, Local>();
  parameterSlots(method.descriptor, isStatic).forEach(({ slot, type }, index) => {
    const scoped = localTable.find(e => e.slot === slot && e.startPc === 0);
    locals.set(slot, {
      name: scoped?.name ?? `arg${index}`,
      type: scoped?.type !== undefined && scoped.type !== "" ? scoped.type : type,
      declared: true,
    });
  });
  return locals;
}

function parameterList(method: Member, locals: Map<number, Local>, isStatic: boolean): string {
  const parameters = parameterSlots(method.descriptor, isStatic).map(({ slot, type }, index) => {
    const local = locals.get(slot);
    return { type: local?.type ?? type, name: local?.name ?? `arg${index}` };
  });
  if (method.flags & ACC_VARARGS && parameters.length > 0) {
    const last = parameters[parameters.length - 1]!;
    if (last.type.endsWith("[]")) last.type = `${last.type.slice(0, -2)}...`;
  }
  return parameters.map(p => `${p.type} ${p.name}`).join(", ");
}

/** The disassembly of a body this phase cannot reconstruct, as a comment. */
function bailComment(instructions: readonly Instruction[], reason: string): string[] {
  const lines = [`/* cappu: ${reason}; the bytecode is:`];
  for (const instruction of instructions) {
    const operand = instruction.operand === "" ? "" : ` ${instruction.operand}`;
    for (const text of [
      `${instruction.pc}: ${instruction.mnemonic}${operand}`,
      ...instruction.extraLines.map(l => l.trim()),
    ]) {
      // A string constant may contain the comment terminator.
      lines.push(` * ${text.replaceAll("*/", "* /")}`);
    }
  }
  lines.push(" */");
  return lines;
}

/**
 * The `<init>()` javac writes when a class declares no constructor: nothing but
 * the implicit `super()` call. Java puts it back, so it is not source.
 */
function isDefaultConstructor(method: Member, classFile: ClassFile): boolean {
  if (method.name !== "<init>" || method.descriptor !== "()V") return false;
  const code = readCode(method, classFile.pool);
  if (!code || code.exceptions.length > 0) return false;
  const instructions = decodeInstructions(classFile, code.code);
  if (instructions.length !== 3) return false;
  const [load, call, ret] = instructions as [Instruction, Instruction, Instruction];
  if (
    load.mnemonic !== "aload_0" ||
    call.mnemonic !== "invokespecial" ||
    ret.mnemonic !== "return"
  ) {
    return false;
  }
  const target = memberRef(classFile.pool, call.arg);
  return (
    target?.name === "<init>" &&
    target.descriptor === "()V" &&
    target.owner === (classFile.superClass ?? "java/lang/Object")
  );
}

function methodSource(method: Member, classFile: ClassFile): string[] {
  const isStatic = (method.flags & ACC_STATIC) !== 0;
  const code = readCode(method, classFile.pool);
  const localTable = code ? readLocalVariables(code, classFile.pool) : [];
  const locals = buildLocals(method, localTable, isStatic);

  const thrown = readExceptions(method, classFile.pool).map(typeName);
  const throwsClause = thrown.length > 0 ? ` throws ${thrown.join(", ")}` : "";
  const head =
    method.name === "<clinit>"
      ? "static"
      : [
          ...methodModifiers(method, classFile),
          ...(method.name === "<init>"
            ? [`${simpleName(classFile.thisClass)}(${parameterList(method, locals, isStatic)})`]
            : [
                returnType(method.descriptor),
                `${method.name}(${parameterList(method, locals, isStatic)})`,
              ]),
        ].join(" ") + throwsClause;

  if (!code) return [`${head};`];

  const instructions = decodeInstructions(classFile, code.code);
  let body: string[];
  try {
    if (code.exceptions.length > 0) throw new NotDecompilable("the method catches exceptions");
    const decompiler = new BodyDecompiler(
      classFile,
      locals,
      localTable,
      returnType(method.descriptor),
      isStatic,
    );
    decompiler.run(instructions);
    body = decompiler.statements;
    // Every void method ends in a `return` javac inserted; source does not.
    if (body[body.length - 1] === "return;") body = body.slice(0, -1);
  } catch (e) {
    if (!(e instanceof NotDecompilable)) throw e;
    body = [
      ...bailComment(instructions, e.message),
      'throw new UnsupportedOperationException("cappu: not decompiled");',
    ];
  }
  return [`${head} {`, ...body, "}"];
}

function classHead(classFile: ClassFile): string {
  const isInterface = (classFile.flags & ACC_INTERFACE) !== 0;
  const isAnnotation = (classFile.flags & ACC_ANNOTATION) !== 0;
  const isEnum = (classFile.flags & ACC_ENUM) !== 0;
  const keyword = isAnnotation
    ? "@interface"
    : isInterface
      ? "interface"
      : isEnum
        ? "enum"
        : "class";
  const head = [
    ...(classFile.flags & ACC_PUBLIC ? ["public"] : []),
    ...(!isInterface && !isEnum && classFile.flags & ACC_FINAL ? ["final"] : []),
    ...(!isInterface && classFile.flags & ACC_ABSTRACT ? ["abstract"] : []),
    keyword,
    simpleName(classFile.thisClass),
  ];
  // The implicit supertypes are not written in source.
  const IMPLICIT = ["java/lang/Object", "java/lang/Enum", "java/lang/Record"];
  const superClass = classFile.superClass;
  if (!isInterface && superClass && !IMPLICIT.includes(superClass)) {
    head.push("extends", typeName(superClass));
  }
  const interfaces = classFile.interfaces.filter(i => i !== "java/lang/annotation/Annotation");
  if (interfaces.length > 0) {
    head.push(isInterface ? "extends" : "implements", interfaces.map(typeName).join(", "));
  }
  return `${head.join(" ")} {`;
}

// --- entry points --------------------------------------------------------------------

/** One class as (unformatted) Java source. */
export function decompileClass(classFile: ClassFile): string {
  const lines: string[] = [];
  const slash = classFile.thisClass.lastIndexOf("/");
  if (slash > 0)
    lines.push(`package ${classFile.thisClass.slice(0, slash).replaceAll("/", ".")};`, "");
  lines.push(classHead(classFile));
  for (const field of classFile.fields) {
    if (field.flags & (ACC_SYNTHETIC | ACC_ENUM)) continue;
    lines.push(fieldSource(field, classFile));
  }
  for (const method of classFile.methods) {
    if (method.flags & (ACC_SYNTHETIC | ACC_BRIDGE)) continue;
    if (isDefaultConstructor(method, classFile)) continue;
    lines.push("", ...methodSource(method, classFile));
  }
  lines.push("}");
  return `${lines.join("\n")}\n`;
}

/**
 * Decompile one class file's bytes to Java source. The text is unformatted:
 * callers pass it through the formatter. Throws ClassFileError on malformed
 * input, exactly like `disassemble`.
 */
export function decompile(bytes: Uint8Array): string {
  const classFile = readClassFile(bytes);
  // Same reasoning as disassemble(): a module descriptor carries no members, so
  // rendering it as a class would print a plausible-looking empty type.
  if (classFile.flags & ACC_MODULE) {
    throw new ClassFileError("module descriptors are not supported yet");
  }
  return decompileClass(classFile);
}
