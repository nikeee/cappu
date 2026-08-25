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
//
// Only the class shape is reconstructed, not the declaration forms that carry
// generated members: an enum keeps its keyword but loses its constants (they
// live in <clinit>) and an obfuscated or non-javac class file can still produce
// something javac would reject. Those are later phases; the bail-out body keeps
// the *method* level honest, not the type level.

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

/**
 * A binary type name as a source *reference*: `java.util.Map$Entry` is written
 * `java.util.Map.Entry`, because `Map$Entry` resolves to nothing. Stops at the
 * first `$` segment that starts with a digit - an anonymous or local class has
 * no source name at all, so its binary one is the only thing left to print.
 *
 * `self` is the class being declared. This file declares it under its binary
 * name (restoring the nesting needs the enclosing file, a later phase), so
 * references to it keep the `$` and still resolve.
 */
function sourceTypeText(text: string, self: string): string {
  // Only the class itself keeps the binary name - a *sibling* nested class is a
  // different type, and `Outer$Other` resolves to nothing.
  if (!text.includes("$") || (self !== "" && text.replaceAll("[]", "") === self)) return text;
  const parts = text.split("$");
  let out = parts[0]!;
  for (const part of parts.slice(1)) {
    const anonymous = part === "" || (part[0]! >= "0" && part[0]! <= "9");
    out += anonymous || out.includes("$") ? `$${part}` : `.${part}`;
  }
  return out;
}

/** A `Foo$Bar` binary name as a source type reference. */
function typeName(internalName: string, self = ""): string {
  const text = internalName.startsWith("[")
    ? descriptorType(internalName, 0).text
    : internalName.replaceAll("/", ".");
  return sourceTypeText(text, self);
}

/** A descriptor as a source type reference. */
function descriptorSourceType(descriptor: string, self: string): string {
  return sourceTypeText(descriptorType(descriptor, 0).text, self);
}

/** The class being decompiled, as its own references have to spell it. */
function selfOf(classFile: ClassFile): string {
  return classFile.thisClass.replaceAll("/", ".");
}

/**
 * Java has no literal for NaN or an infinity - javap prints `NaNf`/`Infinity`,
 * which is not source - so those come back as the wrapper constants javac
 * inlined them from.
 */
function nonFinite(value: number, wrapper: string): string | undefined {
  if (Number.isNaN(value)) return `${wrapper}.NaN`;
  if (value === Infinity) return `${wrapper}.POSITIVE_INFINITY`;
  if (value === -Infinity) return `${wrapper}.NEGATIVE_INFINITY`;
  return undefined;
}

function intLiteral(value: number): Expr {
  // A negative literal is a unary minus, not part of the token.
  return { text: String(value), prec: value < 0 ? PREC_UNARY : PREC_PRIMARY, type: "int" };
}

function constantExpr(pool: readonly (Constant | undefined)[], index: number, self = ""): Expr {
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
      const wrapper = nonFinite(entry.value, "java.lang.Float");
      if (wrapper) return primary(wrapper, "float");
      const text = `${javaFloatText(entry.value)}f`;
      return { text, prec: text.startsWith("-") ? PREC_UNARY : PREC_PRIMARY, type: "float" };
    }
    case "double": {
      const wrapper = nonFinite(entry.value, "java.lang.Double");
      if (wrapper) return primary(wrapper, "double");
      const text = javaDoubleText(entry.value);
      return { text, prec: text.startsWith("-") ? PREC_UNARY : PREC_PRIMARY, type: "double" };
    }
    case "string":
      return primary(`"${escapeString(utf8(pool, entry.valueIndex) ?? "")}"`, "java.lang.String");
    case "class":
      return primary(
        `${typeName(utf8(pool, entry.nameIndex) ?? "", self)}.class`,
        "java.lang.Class",
      );
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

/** The types javac erases to int in the bytecode, leaving only the use to say so. */
const ERASED_TO_INT = ["boolean", "char", "byte", "short"];

interface Local {
  name: string;
  type: string;
  declared: boolean;
  /** Where the declaration and every assignment landed, so a retype can rewrite them. */
  writes: { index: number; value: Expr }[];
  /** The debug-table row this name came from, when there is one. */
  origin: LocalEntry | undefined;
  /**
   * True when the type came from a parameter descriptor or the debug table, so
   * a store of a differently-typed value is an assignment to *this* variable
   * (`boolean b` taking `iconst_0`), not a second variable in the same slot.
   */
  authoritative: boolean;
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
  /** Every local name handed out so far, so a reused slot cannot shadow one. */
  private readonly names = new Set<string>();
  private readonly byName = new Map<string, Local>();
  readonly statements: string[] = [];

  constructor(
    private readonly classFile: ClassFile,
    private readonly locals: Map<number, Local>,
    private readonly localTable: LocalEntry[],
    private readonly methodReturnType: string,
    private readonly isStatic: boolean,
  ) {
    for (const local of locals.values()) {
      this.names.add(local.name);
      this.byName.set(local.name, local);
    }
  }

  /**
   * `value` as it has to read in a `target`-typed position. A local the
   * bytecode only says is an int, used where a boolean/char/byte/short belongs,
   * *is* one - the store opcode is the same for all of them - so its
   * declaration and every assignment to it are rewritten to that type.
   */
  private coerceInto(value: Expr, target: string): string {
    const local = this.byName.get(value.text);
    if (
      local !== undefined &&
      !local.authoritative &&
      local.type === "int" &&
      ERASED_TO_INT.includes(target)
    ) {
      local.type = target;
      for (const write of local.writes) {
        const assigned = coerce(write.value, target);
        this.statements[write.index] =
          write.index === local.writes[0]?.index
            ? `${target} ${local.name} = ${assigned};`
            : `${local.name} = ${assigned};`;
      }
    }
    return coerce(value, target);
  }

  private push(expr: Expr): void {
    this.stack.push(expr);
  }

  private pop(): Expr {
    const expr = this.stack.pop();
    if (!expr) throw new NotDecompilable("stack underflow");
    return expr;
  }

  /**
   * The variable living in `slot` at `pc`. javac reuses a slot for the next
   * variable once the previous one goes out of scope, so a slot is not a
   * variable: a new debug-table scope - or, with no debug table, a store of a
   * different type - starts a new one, which has to be declared under its own
   * name.
   */
  private get self(): string {
    return selfOf(this.classFile);
  }

  /**
   * A static field of this class is written with its simple name: that is what
   * source used, and a blank `static final` can only be *assigned* that way.
   * A local of the same name (declared before this point) shadows it, so then
   * the owner has to stay.
   */
  private staticRef(owner: string, name: string): string {
    if (owner === this.classFile.thisClass && !this.names.has(name)) return name;
    return `${typeName(owner, this.self)}.${name}`;
  }

  private local(slot: number, pc: number, fallbackType: string, isStore = false): Local {
    const scoped = this.localTable.find(e => e.slot === slot && pc >= e.startPc && pc < e.endPc);
    const existing = this.locals.get(slot);
    if (existing) {
      if (scoped !== undefined) {
        if (existing.origin === scoped) return existing;
      } else if (!isStore || existing.authoritative || existing.type === fallbackType) {
        return existing;
      }
    }
    // Reaching a slot that was never stored means the local is read
    // uninitialized - javac cannot produce that, so the input is doing
    // something this phase does not model.
    if (!isStore) throw new NotDecompilable(`local ${slot} is read before it is written`);
    const named = scoped?.name !== undefined && scoped.name !== "" ? scoped.name : `var${slot}`;
    const declared = scoped?.type !== undefined && scoped.type !== "" ? scoped.type : fallbackType;
    const local: Local = {
      name: this.freshName(named),
      type: sourceTypeText(declared, this.self),
      declared: false,
      origin: scoped,
      authoritative: scoped?.type !== undefined && scoped.type !== "",
      writes: [],
    };
    this.byName.set(local.name, local);
    this.locals.set(slot, local);
    return local;
  }

  /**
   * `wanted`, kept distinct from the names already handed out. Two sibling
   * scopes can declare the same name over the same slot; the body they
   * decompile to is flat, so the second one has to be renamed.
   */
  private freshName(wanted: string): string {
    let name = wanted;
    for (let n = 2; this.names.has(name); n++) name = `${wanted}_${n}`;
    this.names.add(name);
    return name;
  }

  /** The local slot of `iload_1`-style mnemonics, or the decoded operand. */
  private slotOf(instruction: Instruction): number {
    const suffix = /_(\d)$/.exec(instruction.mnemonic);
    return suffix ? Number(suffix[1]) : instruction.arg;
  }

  private store(slot: number, scopePc: number, value: Expr, declaredType: string): void {
    const local = this.local(slot, scopePc, declaredType, true);
    const text = this.coerceInto(value, local.type);
    local.writes.push({ index: this.statements.length, value });
    if (local.declared) {
      this.statements.push(`${local.name} = ${text};`);
    } else {
      local.declared = true;
      this.statements.push(`${local.type} ${local.name} = ${text};`);
    }
  }

  run(instructions: readonly Instruction[]): void {
    for (const [index, instruction] of instructions.entries()) {
      // A store's variable comes into scope after the store, so the debug table
      // is searched at the next instruction's pc, not the store's own.
      this.step(instruction, instructions[index + 1]?.pc ?? instruction.pc + 1);
      if (instruction.mnemonic.endsWith("return") && index !== instructions.length - 1) {
        // Code after a return only exists because something branches over it.
        throw new NotDecompilable("unreachable code");
      }
    }
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
  }

  private step(instruction: Instruction, nextPc: number): void {
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
      return this.push(constantExpr(pool, instruction.arg, this.self));
    }

    // Loads and stores.
    const base = mnemonic.replace(/(_\d|_w)$/, "");
    if (/^[ilfda]load$/.test(base)) {
      const slot = this.slotOf(instruction);
      if (base === "aload" && slot === 0 && !this.isStatic) {
        return this.push(primary("this", this.self));
      }
      const local = this.local(slot, pc, PRIMITIVE_OF_PREFIX[base[0]!]!);
      return this.push(primary(local.name, local.type));
    }
    if (/^[ilfda]store$/.test(base)) {
      // `this` is final in source; a class file may still store over slot 0.
      if (!this.isStatic && this.slotOf(instruction) === 0) {
        throw new NotDecompilable("the method assigns to `this`");
      }
      const value = this.pop();
      const fallback = base === "astore" ? value.type : PRIMITIVE_OF_PREFIX[base[0]!]!;
      return this.store(this.slotOf(instruction), nextPc, value, fallback);
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
        // A unary operand needs the parens too: `-(-a)` is not `--a`.
        text: `-${at(value, PREC_UNARY + 1)}`,
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
      const type = descriptorSourceType(field.descriptor, this.self);
      if (mnemonic === "getstatic") {
        return this.push(primary(this.staticRef(field.owner, field.name), type));
      }
      return this.push(primary(`${at(this.pop(), PREC_PRIMARY)}.${field.name}`, type));
    }
    if (mnemonic === "putstatic" || mnemonic === "putfield") {
      const field = memberRef(pool, instruction.arg);
      if (!field) throw new NotDecompilable("bad field reference");
      const value = this.pop();
      const type = descriptorSourceType(field.descriptor, this.self);
      const target =
        mnemonic === "putstatic"
          ? this.staticRef(field.owner, field.name)
          : `${at(this.pop(), PREC_PRIMARY)}.${field.name}`;
      this.statements.push(`${target} = ${this.coerceInto(value, type)};`);
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
        `${at(array, PREC_PRIMARY)}[${index.text}] = ${this.coerceInto(value, element)};`,
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
      const element = typeName(className(pool, instruction.arg) ?? "java/lang/Object", this.self);
      const length = this.pop();
      // The element type may itself be an array: the new dimension goes first,
      // so `new String[n][]`, never `new String[][n]`.
      const base = element.replaceAll("[]", "");
      const rest = "[]".repeat((element.length - base.length) / 2);
      return this.push(primary(`new ${base}[${length.text}]${rest}`, `${element}[]`));
    }
    if (mnemonic === "multianewarray") {
      const type = typeName(className(pool, instruction.arg) ?? "", this.self);
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
      const type = typeName(className(pool, instruction.arg) ?? "java/lang/Object", this.self);
      const value = this.pop();
      return this.push({ text: `(${type}) ${at(value, PREC_UNARY)}`, prec: PREC_UNARY, type });
    }
    if (mnemonic === "instanceof") {
      const type = typeName(className(pool, instruction.arg) ?? "java/lang/Object", this.self);
      const value = this.pop();
      return this.push({
        text: `${at(value, PREC_REL + 1)} instanceof ${type}`,
        prec: PREC_REL,
        type: "boolean",
      });
    }

    // Constructor chaining. `super(...)`/`this(...)` is not a method call in
    // source, it is the shape of a constructor - without it no constructor
    // decompiles at all. Any other invokespecial (a `new`, a private call) is
    // still a later phase.
    if (mnemonic === "invokespecial") {
      const target = memberRef(pool, instruction.arg);
      if (target?.name !== "<init>") {
        throw new NotDecompilable("unsupported instruction invokespecial");
      }
      const isSuper = target.owner === (this.classFile.superClass ?? "java/lang/Object");
      const isThis = target.owner === this.classFile.thisClass;
      if (!isSuper && !isThis) throw new NotDecompilable("constructor call to an unrelated class");
      const params = parameterSlots(target.descriptor, true).map(p => p.type);
      const args: string[] = [];
      for (let i = params.length - 1; i >= 0; i--) {
        args.unshift(this.coerceInto(this.pop(), params[i]!));
      }
      if (this.pop().text !== "this")
        throw new NotDecompilable("constructor call on another object");
      if (this.statements.length > 0) throw new NotDecompilable("constructor call is not first");
      // javac writes the implicit `super()` into every constructor; source does
      // not, and re-emitting puts it back. An enum constructor's `super(name,
      // ordinal)` is generated too - and writing it is a compile error.
      if (isSuper && (args.length === 0 || isEnumDeclaration(this.classFile))) return;
      this.statements.push(`${isSuper ? "super" : "this"}(${args.join(", ")});`);
      return;
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
      this.statements.push(`return ${this.coerceInto(this.pop(), this.methodReturnType)};`);
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

function fieldSource(field: Member, classFile: ClassFile, keepFinal = true): string {
  const type = descriptorSourceType(field.descriptor, selfOf(classFile));
  const modifiers = [
    ...accessModifiers(field.flags),
    ...(field.flags & ACC_STATIC ? ["static"] : []),
    ...(field.flags & ACC_FINAL ? ["final"] : []),
    ...(field.flags & ACC_TRANSIENT ? ["transient"] : []),
    ...(field.flags & ACC_VOLATILE ? ["volatile"] : []),
  ];
  const value = constantValue(field, classFile);
  const initializer = value ? ` = ${coerce(value, type)}` : "";
  // A blank `static final` is only legal when something assigns it; when the
  // static initializer could not be reconstructed, nothing does.
  const shown = keepFinal || value !== undefined ? modifiers : modifiers.filter(m => m !== "final");
  return `${[...shown, type].join(" ")} ${field.name}${initializer};`;
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
  self: string,
): Map<number, Local> {
  const locals = new Map<number, Local>();
  parameterSlots(method.descriptor, isStatic).forEach(({ slot, type }, index) => {
    const scoped = localTable.find(e => e.slot === slot && e.startPc === 0);
    const declared = scoped?.type !== undefined && scoped.type !== "" ? scoped.type : type;
    locals.set(slot, {
      name: scoped?.name ?? `arg${index}`,
      type: sourceTypeText(declared, self),
      declared: true,
      origin: scoped,
      authoritative: true, // the descriptor says what a parameter's type is
      writes: [],
    });
  });
  return locals;
}

function parameterList(
  method: Member,
  locals: Map<number, Local>,
  isStatic: boolean,
  dropLeading = 0,
): string {
  const parameters = parameterSlots(method.descriptor, isStatic)
    .slice(dropLeading)
    .map(({ slot, type }, index) => {
      const local = locals.get(slot);
      return {
        type: local?.type ?? type,
        name: local?.name ?? `arg${index + dropLeading}`,
      };
    });
  if (method.flags & ACC_VARARGS && parameters.length > 0) {
    const last = parameters[parameters.length - 1]!;
    if (last.type.endsWith("[]")) last.type = `${last.type.slice(0, -2)}...`;
  }
  return parameters.map(p => `${p.type} ${p.name}`).join(", ");
}

/** A value of `type` that compiles, for a chain call this phase cannot rebuild. */
function defaultValue(type: string): string {
  if (type === "boolean") return "false";
  if (type === "int" || type === "long" || type === "float" || type === "double") {
    return `(${type}) 0`;
  }
  return DESCRIPTOR_ERASED.includes(type) ? `(${type}) 0` : `(${type}) null`;
}

const DESCRIPTOR_ERASED = ["byte", "char", "short"];

/**
 * The `super(...)`/`this(...)` a constructor that gave up still has to make:
 * without it the class does not compile when the superclass has no no-arg
 * constructor. The arguments are placeholders - the body throws before anything
 * can observe them - but their types come from the real descriptor.
 */
function chainCallStub(
  instructions: readonly Instruction[],
  classFile: ClassFile,
): string | undefined {
  if (isEnumDeclaration(classFile)) return undefined; // generated, never source
  for (const instruction of instructions) {
    if (instruction.mnemonic !== "invokespecial") continue;
    const target = memberRef(classFile.pool, instruction.arg);
    if (target?.name !== "<init>") continue;
    const isSuper = target.owner === (classFile.superClass ?? "java/lang/Object");
    // `new Foo()` in an argument is an invokespecial too; the chain call is the
    // one on this class or its superclass.
    if (!isSuper && target.owner !== classFile.thisClass) continue;
    const params = parameterSlots(target.descriptor, true).map(p =>
      sourceTypeText(p.type, selfOf(classFile)),
    );
    if (params.length === 0) return undefined; // the implicit super(), regenerated
    return `${isSuper ? "super" : "this"}(${params.map(defaultValue).join(", ")});`;
  }
  return undefined;
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
 * The `<init>()` javac writes when a class declares no constructor: the sole
 * constructor, carrying the class' own access, whose body is nothing but the
 * implicit `super()` call. Java puts exactly that back, so it is not source -
 * but a declared no-arg constructor that merely looks like it (one of several,
 * or `private` on a package-private class) has to stay, or the class' API
 * changes.
 */
function generatedConstructor(classFile: ClassFile): Member | undefined {
  const constructors = classFile.methods.filter(m => m.name === "<init>");
  const method = constructors[0];
  if (constructors.length !== 1 || !method || method.descriptor !== "()V") return undefined;
  const access = ACC_PUBLIC | ACC_PROTECTED | ACC_PRIVATE;
  if ((method.flags & access) !== (classFile.flags & access)) return undefined;
  const code = readCode(method, classFile.pool);
  if (!code || code.exceptions.length > 0) return undefined;
  const instructions = decodeInstructions(classFile, code.code);
  if (instructions.length !== 3) return undefined;
  const [load, call, ret] = instructions as [Instruction, Instruction, Instruction];
  if (
    load.mnemonic !== "aload_0" ||
    call.mnemonic !== "invokespecial" ||
    ret.mnemonic !== "return"
  ) {
    return undefined;
  }
  const target = memberRef(classFile.pool, call.arg);
  return target?.name === "<init>" &&
    target.descriptor === "()V" &&
    target.owner === (classFile.superClass ?? "java/lang/Object")
    ? method
    : undefined;
}

interface MethodSource {
  readonly lines: string[];
  /** False when the body is the bail-out rendering rather than reconstructed code. */
  readonly reconstructed: boolean;
}

function methodSource(method: Member, classFile: ClassFile): MethodSource {
  const isStatic = (method.flags & ACC_STATIC) !== 0;
  const code = readCode(method, classFile.pool);
  const localTable = code ? readLocalVariables(code, classFile.pool) : [];
  const locals = buildLocals(method, localTable, isStatic, selfOf(classFile));

  const self = selfOf(classFile);
  const thrown = readExceptions(method, classFile.pool).map(name => typeName(name, self));
  const throwsClause = thrown.length > 0 ? ` throws ${thrown.join(", ")}` : "";
  const head =
    method.name === "<clinit>"
      ? "static"
      : [
          ...methodModifiers(method, classFile),
          ...(method.name === "<init>"
            ? [
                // Every enum constructor starts with the generated name and
                // ordinal; source declares neither.
                `${simpleName(classFile.thisClass)}(${parameterList(
                  method,
                  locals,
                  isStatic,
                  isEnumDeclaration(classFile) ? 2 : 0,
                )})`,
              ]
            : [
                sourceTypeText(returnType(method.descriptor), self),
                `${method.name}(${parameterList(method, locals, isStatic)})`,
              ]),
        ].join(" ") + throwsClause;

  if (!code) return { lines: [`${head};`], reconstructed: true };

  const instructions = decodeInstructions(classFile, code.code);
  const decompiler = new BodyDecompiler(
    classFile,
    locals,
    localTable,
    returnType(method.descriptor),
    isStatic,
  );
  let body: string[];
  let reconstructed = true;
  try {
    if (code.exceptions.length > 0) throw new NotDecompilable("the method catches exceptions");
    decompiler.run(instructions);
    body = decompiler.statements;
    // Every void method ends in a `return` javac inserted; source does not.
    if (body[body.length - 1] === "return;") body = body.slice(0, -1);
  } catch (e) {
    if (!(e instanceof NotDecompilable)) throw e;
    reconstructed = false;
    // A constructor that gave up after chaining keeps the chain call: without
    // it the class does not compile when the superclass has no no-arg
    // constructor.
    const reached = decompiler.statements[0];
    const chained =
      reached !== undefined && /^(?:super|this)\(/.test(reached)
        ? reached
        : method.name === "<init>"
          ? chainCallStub(instructions, classFile)
          : undefined;
    body = [
      ...(chained === undefined ? [] : [chained]),
      ...bailComment(instructions, e.message),
      // A static initializer has to be able to complete normally, so the throw
      // that marks every other unreconstructed body would not compile here.
      ...(method.name === "<clinit>"
        ? []
        : ['throw new UnsupportedOperationException("cappu: not decompiled");']),
    ];
  }
  return { lines: [`${head} {`, ...body, "}"], reconstructed };
}

/**
 * ACC_ENUM is also set on the anonymous subclass javac writes for an enum
 * constant with a body - which is a plain class, not an enum declaration: it
 * extends the enum type, and `enum X extends Y` is not Java.
 */
function isEnumDeclaration(classFile: ClassFile): boolean {
  return (classFile.flags & ACC_ENUM) !== 0 && classFile.superClass === "java/lang/Enum";
}

/** Fields javac writes for itself, and regenerates from source. */
const GENERATED_FIELDS = ["$VALUES", "$assertionsDisabled"];

/** The constants of an enum, in declaration order, as the body must open. */
function enumConstants(classFile: ClassFile): string[] {
  const self = `L${classFile.thisClass};`;
  return classFile.fields.filter(f => f.flags & ACC_ENUM && f.descriptor === self).map(f => f.name);
}

/**
 * `values`, `valueOf` and the two leading constructor parameters are generated
 * for every enum; writing them out is a compile error ("already defined"), so
 * they are not source.
 */
function isGeneratedEnumMember(method: Member, classFile: ClassFile): boolean {
  if (!isEnumDeclaration(classFile)) return false;
  const self = `L${classFile.thisClass};`;
  return (
    (method.name === "values" && method.descriptor === `()[${self}`) ||
    (method.name === "valueOf" && method.descriptor === `(Ljava/lang/String;)${self}`)
  );
}

function classHead(classFile: ClassFile): string {
  const isInterface = (classFile.flags & ACC_INTERFACE) !== 0;
  const isAnnotation = (classFile.flags & ACC_ANNOTATION) !== 0;
  const isEnum = isEnumDeclaration(classFile);
  const self = selfOf(classFile);
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
    // An enum carrying constant bodies is ACC_ABSTRACT, but `abstract enum` is
    // not something Java lets you write.
    ...(!isInterface && !isEnum && classFile.flags & ACC_ABSTRACT ? ["abstract"] : []),
    keyword,
    simpleName(classFile.thisClass),
  ];
  // The implicit supertypes are not written in source.
  const IMPLICIT = ["java/lang/Object", "java/lang/Enum", "java/lang/Record"];
  const superClass = classFile.superClass;
  if (!isInterface && superClass && !IMPLICIT.includes(superClass)) {
    head.push("extends", typeName(superClass, self));
  }
  const interfaces = classFile.interfaces.filter(i => i !== "java/lang/annotation/Annotation");
  if (interfaces.length > 0) {
    head.push(
      isInterface ? "extends" : "implements",
      interfaces.map(name => typeName(name, self)).join(", "),
    );
  }
  return `${head.join(" ")} {`;
}

// --- entry points --------------------------------------------------------------------

/** One class as (unformatted) Java source. */
export function decompileClass(classFile: ClassFile): string {
  const lines: string[] = [];
  const slash = classFile.thisClass.lastIndexOf("/");
  const packageName = slash > 0 ? classFile.thisClass.slice(0, slash).replaceAll("/", ".") : "";
  // A package-info.class only carries the package's annotations, and its name
  // is not an identifier - the package declaration is the whole source.
  if (simpleName(classFile.thisClass) === "package-info") {
    return packageName === "" ? "" : `package ${packageName};\n`;
  }
  if (packageName !== "") lines.push(`package ${packageName};`, "");
  const generated = generatedConstructor(classFile);
  // Methods first: whether the static initializer came back decides how the
  // static fields have to be declared.
  const bodies: string[][] = [];
  let staticInitializerLost = false;
  for (const method of classFile.methods) {
    if (method.flags & (ACC_SYNTHETIC | ACC_BRIDGE)) continue;
    if (method === generated || isGeneratedEnumMember(method, classFile)) continue;
    const { lines: body, reconstructed } = methodSource(method, classFile);
    if (!reconstructed && method.name === "<clinit>") staticInitializerLost = true;
    bodies.push(body);
  }

  lines.push(classHead(classFile));
  if (isEnumDeclaration(classFile)) {
    // An enum body opens with its constant list; even an empty one needs the
    // `;` before any member. How the constants are constructed lives in
    // <clinit>, which this phase does not reconstruct.
    lines.push(`${enumConstants(classFile).join(", ")};`);
  }
  for (const field of classFile.fields) {
    // Enum constants are the constant list above, not fields. A synthetic field
    // is kept - the captured outer instance (`this$0`) and captured locals
    // (`val$x`) are referenced by real method bodies - except the two javac
    // generates on its own, which would clash with the ones it regenerates.
    if (field.flags & ACC_ENUM) continue;
    if (field.flags & ACC_SYNTHETIC && GENERATED_FIELDS.includes(field.name)) continue;
    const keepFinal = !staticInitializerLost || (field.flags & ACC_STATIC) === 0;
    lines.push(fieldSource(field, classFile, keepFinal));
  }
  for (const body of bodies) lines.push("", ...body);
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
