// `cappu decompile`, phases 1.3 to 1.10 (nikeee/cappu#43): reconstruct Java
// source from bytecode. A symbolic stack interpreter walks a method's basic
// blocks and turns them back into expressions and statements, with the control
// flow structured into `if`/`else`, `&&`/`||`, `?:`, the loop forms, method
// calls, `try`/`catch`, array initializers, string concatenation and `switch`;
// anything that needs a `finally` or an invokedynamic that is not a
// concatenation (later phases) renders as its disassembly plus a
// `throw new UnsupportedOperationException(...)`, so the output is always
// compilable Java.
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
  type ExceptionEntry,
  type Member,
  className,
  findAttribute,
  innerClassFlags,
  memberRef,
  type MemberRef,
  nameAndTypeAt,
  type BootstrapMethod,
  readBootstrapMethods,
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
const PREC_EQ = 8;
const PREC_AND = 7;
const PREC_XOR = 6;
const PREC_OR = 5;
const PREC_LAND = 4;
const PREC_LOR = 3;
const PREC_TERNARY = 2;

/**
 * The structured form of a boolean expression, kept alongside its text so
 * `negate` can flip the operator instead of wrapping everything in a `!`: the
 * bytecode branches on the *inverse* of what the source said, so every
 * condition is negated exactly once on the way back.
 */
type Logic =
  | { readonly kind: "compare"; readonly left: Expr; readonly op: string; readonly right: Expr }
  | { readonly kind: "and" | "or"; readonly left: Expr; readonly right: Expr }
  | { readonly kind: "not"; readonly value: Expr };

interface Expr {
  readonly text: string;
  readonly prec: number;
  /** The value's Java type as source text, used to declare locals. */
  readonly type: string;
  readonly logic?: Logic;
  /**
   * Set on a lambda: it has no type of its own, so it needs the interface named
   * wherever the context does not say it (a return type the interface erased to
   * `Object`, for one).
   */
  readonly lambda?: boolean;
  /**
   * The int form of a value javac materialized as `1`/`0`: written back as the
   * condition itself, which is a boolean, so a use that wants an int has to get
   * the ternary again (`array[c ? 1 : 0]`).
   */
  readonly asInt?: string;
  /**
   * The operands of an `lcmp`/`fcmp`/`dcmp`, which has no source form of its
   * own: the comparison it feeds is what source wrote.
   */
  readonly compared?: { readonly left: Expr; readonly right: Expr };
  /**
   * A value that does something when it runs - a call. Dropping it has to keep
   * it as a statement, and nothing may write it twice.
   */
  readonly effects?: true;
  /**
   * The object `new` left on the stack, which is not a value until its
   * constructor has run. Every copy carries the same id, so the call can put
   * `new C(...)` in all of their places at once.
   */
  readonly pending?: number;
  /**
   * An array creation javac may still be filling in: `{1, 2, 3}` is a `new
   * int[3]` that is duplicated once per element and written through. Every copy
   * carries the same `id`, and the elements written so far are what the literal
   * gets rendered from once it is full.
   */
  readonly init?: ArrayInit;
}

interface ArrayInit {
  readonly id: number;
  /** `new int[]` for an `int[3]`, so the literal is `prefix + "{...}"`. */
  readonly prefix: string;
  readonly element: string;
  readonly length: number;
  readonly elements: readonly string[];
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

/** `<`, `<=`, `>`, `>=`, `==` and `!=` bind at two different levels. */
function comparePrec(op: string): number {
  return op === "==" || op === "!=" ? PREC_EQ : PREC_REL;
}

/** Two integer literals compared, which only a constant condition produces. */
function foldComparison(left: Expr, op: string, right: Expr): boolean | undefined {
  if (!/^-?\d+$/.test(left.text) || !/^-?\d+$/.test(right.text)) return undefined;
  const a = Number(left.text);
  const b = Number(right.text);
  switch (op) {
    case "==":
      return a === b;
    case "!=":
      return a !== b;
    case "<":
      return a < b;
    case "<=":
      return a <= b;
    case ">":
      return a > b;
    default:
      return a >= b;
  }
}

function compare(left: Expr, op: string, right: Expr): Expr {
  // `while (true)` is a test against a constant to a compiler that does not fold
  // it, and `1 != 0` is not what anyone wrote.
  const folded = foldComparison(left, op, right);
  if (folded !== undefined) return primary(folded ? "true" : "false", "boolean");
  const prec = comparePrec(op);
  return {
    text: `${at(left, prec + 1)} ${op} ${at(right, prec + 1)}`,
    prec,
    type: "boolean",
    logic: { kind: "compare", left, op, right },
  };
}

function logical(kind: "and" | "or", left: Expr, right: Expr): Expr {
  const prec = kind === "and" ? PREC_LAND : PREC_LOR;
  return {
    text: `${at(left, prec)} ${kind === "and" ? "&&" : "||"} ${at(right, prec + 1)}`,
    prec,
    type: "boolean",
    logic: { kind, left, right },
  };
}

function not(value: Expr): Expr {
  if (value.text === "true") return primary("false", "boolean");
  if (value.text === "false") return primary("true", "boolean");
  return {
    text: `!${at(value, PREC_UNARY)}`,
    prec: PREC_UNARY,
    type: "boolean",
    logic: { kind: "not", value },
  };
}

const FLIPPED: Record<string, string> = {
  "==": "!=",
  "!=": "==",
  "<": ">=",
  ">=": "<",
  ">": "<=",
  "<=": ">",
};

/**
 * `!expr`, written the way source would have: a comparison flips its operator
 * and a `&&`/`||` goes through De Morgan, because the bytecode always carries
 * the negated form of what was written.
 */
function negate(expr: Expr): Expr {
  const logic = expr.logic;
  if (logic === undefined) return not(expr);
  switch (logic.kind) {
    case "compare":
      return compare(logic.left, FLIPPED[logic.op]!, logic.right);
    case "and":
      return logical("or", negate(logic.left), negate(logic.right));
    case "or":
      return logical("and", negate(logic.left), negate(logic.right));
    case "not":
      return logic.value;
  }
}

/** The wider of two numeric types, as binary numeric promotion picks it. */
const NUMERIC_WIDTH = ["int", "long", "float", "double"];

/**
 * A value in a position that wants a number. A condition javac materialized as
 * `1`/`0` reads as a boolean everywhere the type is known (a store, a return),
 * but arithmetic and comparisons splice the text in as it stands, so it has to
 * become the ternary again.
 */
function numeric(expr: Expr): Expr {
  if (expr.asInt === undefined) return expr;
  return { text: expr.asInt, prec: PREC_TERNARY, type: "int" };
}

/** `1` and `0` in a position where the other arm proves a boolean was meant. */
function asBoolean(expr: Expr): Expr {
  if (expr.text === "1") return primary("true", "boolean");
  if (expr.text === "0") return primary("false", "boolean");
  return expr;
}

function ternary(condition: Expr, thenValue: Expr, elseValue: Expr): Expr | undefined {
  // A lambda has no type of its own, and a conditional gives it none: the arm
  // has to name the interface, which is what source wrote.
  let whenTrue = named(thenValue);
  let whenFalse = named(elseValue);
  if ((whenTrue.type === "boolean") !== (whenFalse.type === "boolean")) {
    // One arm is a boolean and the other an int: either that int is the `1`/`0`
    // a boolean was erased to, or the boolean is a condition javac materialized
    // and the value really is a number, which is what `asInt` is for.
    const boolean = whenTrue.type === "boolean" ? whenTrue : whenFalse;
    const other = whenTrue.type === "boolean" ? whenFalse : whenTrue;
    const asBool = asBoolean(other);
    let replacement: Expr | undefined;
    let replacesOther = true;
    if (asBool !== other) {
      replacement = asBool;
    } else if (boolean.asInt !== undefined) {
      replacement = { text: boolean.asInt, prec: PREC_TERNARY, type: "int" };
      replacesOther = false;
    }
    if (replacement === undefined) return undefined; // a mix nothing can write
    if ((whenTrue.type === "boolean") === replacesOther) whenFalse = replacement;
    else whenTrue = replacement;
  }
  // `c ? true : x` and `c ? x : false` are how a short-circuit reads once its
  // value is materialized; writing them back as `||`/`&&` is both shorter and
  // what the source said.
  if (whenTrue.text === "true") return logical("or", condition, whenFalse);
  if (whenFalse.text === "false") return logical("and", condition, whenTrue);
  if (whenTrue.text === "false") return logical("and", negate(condition), whenFalse);
  if (whenFalse.text === "true") return logical("or", negate(condition), whenTrue);
  const left = NUMERIC_WIDTH.indexOf(whenTrue.type);
  const right = NUMERIC_WIDTH.indexOf(whenFalse.type);
  const type =
    whenTrue.type === whenFalse.type
      ? whenTrue.type
      : left >= 0 && right >= 0
        ? NUMERIC_WIDTH[Math.max(left, right)]!
        : whenTrue.type;
  return {
    text: `${at(condition, PREC_TERNARY + 1)} ? ${at(whenTrue, PREC_TERNARY)} : ${at(
      whenFalse,
      PREC_TERNARY,
    )}`,
    prec: PREC_TERNARY,
    type,
  };
}

/**
 * The value of a branch whose arms are `1` and `0`: that is a boolean, and
 * `value` is how the condition reads as one - but the int form has to keep the
 * *branch's* own arms (`c ? 0 : 1`, not `!c ? 1 : 0`), which is the form source
 * wrote and the one that recompiles to the same branch.
 */
/** Whether a value's text is the same every time it is read. */
function isConstantText(text: string): boolean {
  return (
    text === "null" ||
    text === "true" ||
    text === "false" ||
    /^-?\d[\w.]*$/.test(text) ||
    /^"(?:[^"\\]|\\.)*"$/.test(text) ||
    /^'(?:[^'\\]|\\.)*'$/.test(text)
  );
}

/** A lambda with its interface named, for a place that does not say it. */
function named(value: Expr): Expr {
  if (value.lambda !== true) return value;
  return { text: `(${value.type}) ${value.text}`, prec: 0, type: value.type };
}

function materializedBoolean(value: Expr, condition: Expr, whenTrue: string): Expr {
  const whenFalse = whenTrue === "1" ? "0" : "1";
  return { ...value, asInt: `${at(condition, PREC_TERNARY + 1)} ? ${whenTrue} : ${whenFalse}` };
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

/** The source operator a branch tests, keyed by the mnemonic's suffix. */
const COMPARISONS: Record<string, string> = {
  eq: "==",
  ne: "!=",
  lt: "<",
  ge: ">=",
  gt: ">",
  le: "<=",
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
  // A condition javac materialized as `1`/`0` reads as a boolean everywhere but
  // where a number is what belongs.
  if (expr.asInt !== undefined && target !== "boolean") return expr.asInt;
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

/**
 * A statement, or the body of a nested block. The tree is flattened only once
 * the method is done, so a retype can still reach a statement that has already
 * been placed inside an `if`.
 */
type Stmt = string | Stmt[];

/** Drop a trailing `continue;` a loop's own fallthrough already says. */
/**
 * Whether `text` reads `name` - a variable or a field reference, matched whole
 * so a longer name that contains it does not count.
 */
function reads(text: string, name: string): boolean {
  if (!text.includes(name)) return false;
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  // Not `\b`: a name may end in a bracket (`a[i]`), where there is no word
  // boundary at all. A leading `.` is excluded so a field does not match a local
  // of the same name.
  return new RegExp(`(^|[^\\w$.])${escaped}($|[^\\w$])`).test(text);
}

/**
 * Whether one line is a statement *expression* - the only thing a lambda may
 * carry without a block. A `throw` and a declaration are statements and neither
 * is one, however much they look like a call.
 */
function isStatementExpression(line: string): boolean {
  if (!line.endsWith(";") || line.includes("{")) return false;
  if (
    /^(?:throw|if|while|for|do|switch|try|synchronized|assert|break|continue|return|else)\b/.test(
      line,
    )
  ) {
    return false;
  }
  // A declaration is a type and a name before the `=`; an assignment has only
  // the name.
  if (/^[A-Za-z_$][\w.$]*(?:\[\])*(?:<[^;]*>)?\s+[A-Za-z_$][\w$]*\s*[=;]/.test(line)) return false;
  return /^[A-Za-z_$(]/.test(line);
}

function trimTail(statements: Stmt[], text: string): void {
  if (statements[statements.length - 1] === text) statements.pop();
}

function flattenStatements(statements: readonly Stmt[]): string[] {
  return statements.flatMap(statement =>
    typeof statement === "string" ? [statement] : flattenStatements(statement),
  );
}

/** A statement already emitted, kept addressable so a retype can rewrite it. */
interface Emitted {
  readonly list: Stmt[];
  readonly index: number;
  readonly value: Expr;
}

interface Local {
  name: string;
  type: string;
  declared: boolean;
  /** Where the declaration landed; `inline` when it carries the first value. */
  declaration: { list: Stmt[]; index: number; inline: boolean } | undefined;
  /** Every assignment, so a retype can rewrite them. */
  writes: Emitted[];
  /** The blocks that store to it, which is what says whether a read is unambiguous. */
  readonly storeBlocks: Set<number>;
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

// --- the control-flow graph ----------------------------------------------------------

// A back edge is a loop, so pc order is not a topological order and the
// dominator analyses below are fixpoints rather than a single pass.

/** The virtual block every `return`/`athrow` falls into, so a merge always exists. */
const EXIT = -1;

const CONDITIONAL_BRANCHES = new Set([
  "ifeq",
  "ifne",
  "iflt",
  "ifge",
  "ifgt",
  "ifle",
  "if_icmpeq",
  "if_icmpne",
  "if_icmplt",
  "if_icmpge",
  "if_icmpgt",
  "if_icmple",
  "if_acmpeq",
  "if_acmpne",
  "ifnull",
  "ifnonnull",
]);

const INVOKES = new Set(["invokestatic", "invokevirtual", "invokeinterface", "invokespecial"]);

/** The stack-copying opcodes, which is how javac writes a compound assignment. */
const DUPS = new Set(["dup", "dup_x1", "dup_x2", "dup2", "dup2_x1", "dup2_x2"]);

function isGoto(mnemonic: string): boolean {
  return mnemonic === "goto" || mnemonic === "goto_w";
}

function isSwitch(mnemonic: string): boolean {
  return mnemonic === "tableswitch" || mnemonic === "lookupswitch";
}

function isBlockEnd(mnemonic: string): boolean {
  return mnemonic === "athrow" || /^(?:[ilfda])?return$/.test(mnemonic);
}

/** A conditional branch, once the tests that belong with it are folded in. */
interface Jump {
  readonly condition: Expr;
  readonly target: number;
  readonly fallthrough: number;
}

interface Block {
  readonly start: number;
  readonly instructions: Instruction[];
  readonly kind: "fall" | "conditional" | "goto" | "switch" | "end";
  /** A conditional's are `[fallthrough, target]`, in that order. */
  readonly successors: number[];
}

function buildBlocks(
  instructions: readonly Instruction[],
  exceptions: readonly ExceptionEntry[] = [],
  /** Where a `finally`'s copy begins, which nothing else would split at. */
  splits: readonly number[] = [],
): Map<number, Block> {
  const leaders = new Set<number>([instructions[0]?.pc ?? 0, ...splits]);
  // A protected range and a handler both begin a statement of their own, and
  // neither is a branch target, so nothing else would split the block there.
  for (const entry of exceptions) {
    leaders.add(entry.startPc);
    leaders.add(entry.handlerPc);
  }
  for (const [index, instruction] of instructions.entries()) {
    const { mnemonic } = instruction;
    // The subroutine opcodes: gone since Java 6, and their control flow is not
    // expressible as a branch.
    if (["jsr", "jsr_w", "ret", "ret_w"].includes(mnemonic)) {
      throw new NotDecompilable(`unsupported instruction ${mnemonic}`);
    }
    const branch = CONDITIONAL_BRANCHES.has(mnemonic) || isGoto(mnemonic);
    if (branch) leaders.add(instruction.arg);
    if (isSwitch(mnemonic)) {
      // `arg` is the default target, and every case target begins a statement.
      leaders.add(instruction.arg);
      for (const { target } of instruction.switchCases) leaders.add(target);
    }
    if (branch || isSwitch(mnemonic) || isBlockEnd(mnemonic)) {
      const next = instructions[index + 1];
      if (next) leaders.add(next.pc);
    }
  }
  const blocks = new Map<number, Block>();
  let current: Instruction[] = [];
  let start = instructions[0]?.pc ?? 0;
  const flush = (fallthrough: number | undefined): void => {
    if (current.length === 0) return;
    const last = current[current.length - 1]!;
    const { mnemonic } = last;
    let kind: Block["kind"] = "fall";
    let successors: number[] = fallthrough === undefined ? [] : [fallthrough];
    if (CONDITIONAL_BRANCHES.has(mnemonic)) {
      if (fallthrough === undefined) throw new NotDecompilable("a branch runs off the end");
      kind = "conditional";
      successors = [fallthrough, last.arg];
    } else if (isGoto(mnemonic)) {
      kind = "goto";
      successors = [last.arg];
    } else if (isSwitch(mnemonic)) {
      kind = "switch";
      // The default first, then each distinct case target once: the analyses
      // below walk this list, and a repeated edge would be walked twice.
      successors = [...new Set([last.arg, ...last.switchCases.map(entry => entry.target)])];
    } else if (isBlockEnd(mnemonic)) {
      kind = "end";
      successors = [];
    } else if (fallthrough === undefined) {
      throw new NotDecompilable("the code runs off the end of the method");
    }
    blocks.set(start, { start, instructions: current, kind, successors });
  };
  for (const instruction of instructions) {
    if (leaders.has(instruction.pc) && current.length > 0) {
      flush(instruction.pc);
      current = [];
      start = instruction.pc;
    }
    current.push(instruction);
  }
  flush(undefined);
  for (const block of blocks.values()) {
    for (const successor of block.successors) {
      if (!blocks.has(successor)) throw new NotDecompilable("a branch lands mid-instruction");
    }
  }
  return blocks;
}

/**
 * One `synchronized` statement: javac holds the monitor in a synthetic local and
 * guards the body with a catch-all that releases it and rethrows.
 */
interface MonitorRegion {
  readonly startPc: number;
  /** The end of the last range the handler guards, where the body leaves. */
  readonly endPc: number;
  readonly handlerPc: number;
  /** The synthetic local the monitor was copied into. */
  readonly slot: number;
}

/**
 * One `finally`: javac writes the body twice - once on the way out, once in a
 * catch-all that rethrows - and the copies are what says where it is.
 */
interface FinallyRegion {
  readonly startPc: number;
  readonly endPc: number;
  readonly handlerPc: number;
  /** The handler's copy of the body, without the store and the rethrow. */
  readonly body: readonly Instruction[];
}

/**
 * The body of a `finally`, when `block` is the catch-all that rethrows:
 * `astore e; <body>; aload e; athrow`, with the same slot at both ends.
 */
function finallyBody(block: Block | undefined): readonly Instruction[] | undefined {
  const kept = block?.instructions ?? [];
  if (kept.length < 4 || block?.kind !== "end") return undefined;
  const store = kept[0]!;
  const reload = kept[kept.length - 2]!;
  const throwing = kept[kept.length - 1]!;
  const slot = (instruction: Instruction): number =>
    /_\d$/.test(instruction.mnemonic) ? Number(instruction.mnemonic.slice(-1)) : instruction.arg;
  if (
    !store.mnemonic.startsWith("astore") ||
    !reload.mnemonic.startsWith("aload") ||
    throwing.mnemonic !== "athrow" ||
    slot(store) !== slot(reload)
  ) {
    return undefined;
  }
  return kept.slice(1, -2);
}

/** Whether two runs of instructions are the same code, which a copy has to be. */
function sameInstructions(left: readonly Instruction[], right: readonly Instruction[]): boolean {
  return (
    left.length === right.length &&
    left.every(
      (one, index) =>
        one.mnemonic === right[index]!.mnemonic &&
        one.arg === right[index]!.arg &&
        one.arg2 === right[index]!.arg2,
    )
  );
}

/** Whether `block` is the release-and-rethrow a `synchronized` is guarded by. */
function isMonitorHandler(block: Block | undefined): number | undefined {
  const kept = block?.instructions ?? [];
  if (kept.length !== 5) return undefined;
  const [store, load, exit, reload, throwing] = kept;
  const slot = (instruction: Instruction): number =>
    /_\d$/.test(instruction.mnemonic) ? Number(instruction.mnemonic.slice(-1)) : instruction.arg;
  if (
    !store!.mnemonic.startsWith("astore") ||
    !load!.mnemonic.startsWith("aload") ||
    exit!.mnemonic !== "monitorexit" ||
    !reload!.mnemonic.startsWith("aload") ||
    throwing!.mnemonic !== "athrow" ||
    slot(store!) !== slot(reload!)
  ) {
    return undefined;
  }
  return slot(load!);
}

/**
 * The `synchronized` statements among the catch-all ranges, and the exception
 * entries left for `tryRegions`. A `finally` and a try-with-resources are
 * catch-alls too, and those javac writes as duplicated code: not this phase.
 */
function monitorRegions(
  exceptions: readonly ExceptionEntry[],
  blocks: Map<number, Block>,
  instructions: readonly Instruction[],
): { monitors: MonitorRegion[]; finallys: FinallyRegion[]; rest: ExceptionEntry[] } {
  const monitors: MonitorRegion[] = [];
  const finallys: FinallyRegion[] = [];
  const rest: ExceptionEntry[] = [];
  const enters = new Set(
    instructions.filter(one => one.mnemonic === "monitorenter").map(one => one.pc + 1),
  );
  const byHandler = new Map<number, ExceptionEntry[]>();
  for (const entry of exceptions) {
    if (entry.catchType !== undefined) {
      rest.push(entry);
      continue;
    }
    const group = byHandler.get(entry.handlerPc);
    if (group === undefined) byHandler.set(entry.handlerPc, [entry]);
    else group.push(entry);
  }
  for (const [handlerPc, group] of byHandler) {
    const slot = isMonitorHandler(blocks.get(handlerPc));
    // The handler guards itself as well, so a throw out of the release runs it
    // again; that entry says nothing about where the statement is.
    const ranges = group.filter(entry => entry.startPc !== handlerPc);
    const start = Math.min(...ranges.map(entry => entry.startPc));
    if (slot !== undefined && ranges.length > 0 && enters.has(start)) {
      monitors.push({
        startPc: start,
        endPc: Math.max(...ranges.map(entry => entry.endPc)),
        handlerPc,
        slot,
      });
      continue;
    }
    // A `finally` javac wrote once per way out: this takes the shape with one
    // way out, where the range is not split and the copy sits right after it.
    const body = finallyBody(blocks.get(handlerPc));
    if (body === undefined || ranges.length !== 1 || body.length === 0) {
      throw new NotDecompilable("a finally or synchronized block");
    }
    const range = ranges[0]!;
    const copy = blocks.get(range.endPc);
    if (
      copy === undefined ||
      !isGoto(copy.instructions[copy.instructions.length - 1]!.mnemonic) ||
      !sameInstructions(body, copy.instructions.slice(0, body.length))
    ) {
      throw new NotDecompilable("a finally with more than one way out");
    }
    finallys.push({ startPc: range.startPc, endPc: range.endPc, handlerPc, body });
  }
  return { monitors, finallys, rest };
}

/** One `catch` clause: the types it names, and where its handler begins. */
interface Clause {
  readonly types: string[];
  readonly handlerPc: number;
}

/** One `try` statement: the range it protects, and the clauses guarding it. */
interface TryRegion {
  readonly startPc: number;
  readonly endPc: number;
  readonly clauses: Clause[];
}

/**
 * The `try` statements of one method, from its exception table. A clause is one
 * handler with the types that reach it (a multi-catch reaches the table as one
 * entry per type); the clauses guarding the same range are one statement, in
 * source order.
 */
function tryRegions(
  exceptions: readonly ExceptionEntry[],
  blocks: Map<number, Block>,
  self: string,
): TryRegion[] {
  const byHandler = new Map<
    number,
    { types: string[]; ranges: { startPc: number; endPc: number }[] }
  >();
  const order: number[] = [];
  for (const entry of exceptions) {
    // `monitorRegions` has taken the catch-alls it knows, and rejected the rest.
    if (entry.catchType === undefined) throw new NotDecompilable("a finally block");
    if (!blocks.has(entry.startPc) || !blocks.has(entry.handlerPc)) {
      throw new NotDecompilable("a try range that starts mid-instruction");
    }
    if (entry.handlerPc >= entry.startPc && entry.handlerPc < entry.endPc) {
      throw new NotDecompilable("a handler inside its own try");
    }
    let clause = byHandler.get(entry.handlerPc);
    if (clause === undefined) {
      clause = { types: [], ranges: [] };
      byHandler.set(entry.handlerPc, clause);
      order.push(entry.handlerPc);
    }
    const type = typeName(entry.catchType, self);
    if (!clause.types.includes(type)) clause.types.push(type);
    // A multi-catch is one entry per type over the same range: one clause, one
    // range.
    if (!clause.ranges.some(one => one.startPc === entry.startPc && one.endPc === entry.endPc)) {
      clause.ranges.push({ startPc: entry.startPc, endPc: entry.endPc });
    }
  }

  const byRange = new Map<string, TryRegion>();
  const guards = new Map<TryRegion, string>();
  const regions: TryRegion[] = [];
  for (const handlerPc of order) {
    const clause = byHandler.get(handlerPc)!;
    const range = clause.ranges
      .sort((a, b) => a.startPc - b.startPc)
      .reduce((left, right) => {
        // javac splits the range of a `try` around what it does not protect: the
        // return or jump that ends a nested `catch`, or the `return`/`break`/
        // `continue` that leaves the body. Both are body in source, so the gap
        // may only hold blocks that jump or end, and none may run past it.
        const gap = [...blocks.values()].filter(
          block => block.start >= left.endPc && block.start < right.startPc,
        );
        const joined =
          right.startPc <= left.endPc ||
          gap.every(
            block =>
              (block.kind === "goto" || block.kind === "end") &&
              block.instructions[block.instructions.length - 1]!.pc < right.startPc,
          );
        if (!joined) throw new NotDecompilable("a try with a split range");
        return { startPc: left.startPc, endPc: Math.max(left.endPc, right.endPc) };
      });
    const key = `${range.startPc}:${range.endPc}`;
    const guarded = clause.ranges.map(one => `${one.startPc}:${one.endPc}`).join(",");
    let region = byRange.get(key);
    if (region === undefined) {
      region = { startPc: range.startPc, endPc: range.endPc, clauses: [] };
      byRange.set(key, region);
      guards.set(region, guarded);
      regions.push(region);
    } else if (guards.get(region) !== guarded) {
      // Two clauses over the same span that do not guard the same ranges are not
      // the same `try`; which handler catches what is no longer the clause order.
      throw new NotDecompilable("clauses that guard different ranges");
    }
    region.clauses.push({ types: clause.types, handlerPc });
  }

  for (const region of regions) {
    for (const other of regions) {
      const disjoint = other.endPc <= region.startPc || other.startPc >= region.endPc;
      const nested =
        (other.startPc >= region.startPc && other.endPc <= region.endPc) ||
        (region.startPc >= other.startPc && region.endPc <= other.endPc);
      if (!disjoint && !nested) throw new NotDecompilable("overlapping try ranges");
    }
  }
  return regions;
}

/** Every block reachable from the roots, which is all the structuring may cover. */
function reachableBlocks(blocks: Map<number, Block>, roots: readonly number[]): Set<number> {
  const seen = new Set<number>();
  const queue = [...roots];
  while (queue.length > 0) {
    const at = queue.pop()!;
    if (seen.has(at) || !blocks.has(at)) continue;
    seen.add(at);
    queue.push(...blocks.get(at)!.successors);
  }
  return seen;
}

/**
 * The immediate post-dominator of every block: the point where the two arms of
 * a branch come back together, and so where the `if` it was written as ends.
 * Blocks whose paths all leave the method map to EXIT.
 *
 * Inside a loop this is computed over the loop's blocks alone, with the edges
 * that `break` and `continue` take cut: they leave the statement they sit in
 * exactly the way a `return` does, and counting them would put the merge of
 * every `if` in the body at the loop's own test.
 */
function postDominators(
  blocks: Map<number, Block>,
  within?: ReadonlySet<number>,
  cut?: ReadonlySet<number>,
): Map<number, number> {
  const starts = [...blocks.keys()]
    .filter(start => within?.has(start) ?? true)
    .sort((a, b) => a - b);
  const leaves = (successor: number): boolean =>
    successor === EXIT || (cut?.has(successor) ?? false) || !(within?.has(successor) ?? true);
  const all = new Set([...starts, EXIT]);
  // A back edge makes reverse pc order stop being a topological one, so this is
  // a fixpoint: every set starts full and shrinks until nothing moves.
  const sets = new Map<number, Set<number>>(starts.map(start => [start, new Set(all)]));
  for (let changed = true; changed;) {
    changed = false;
    for (const start of [...starts].reverse()) {
      const block = blocks.get(start)!;
      let shared: Set<number> | undefined;
      for (const successor of block.successors.length === 0 ? [EXIT] : block.successors) {
        const of = leaves(successor) ? new Set([EXIT]) : (sets.get(successor) ?? new Set([EXIT]));
        shared = shared === undefined ? new Set(of) : new Set([...shared].filter(x => of.has(x)));
      }
      const next = new Set([start, ...(shared ?? [EXIT])]);
      const before = sets.get(start)!;
      if (next.size === before.size && [...next].every(x => before.has(x))) continue;
      sets.set(start, next);
      changed = true;
    }
  }
  const immediate = new Map<number, number>();
  for (const start of starts) {
    const candidates = [...sets.get(start)!]
      .filter(x => x !== start && x !== EXIT)
      .sort((a, b) => a - b);
    // The nearest one: the one every other candidate post-dominates too.
    const nearest = candidates.find(x => candidates.every(other => sets.get(x)!.has(other)));
    immediate.set(start, nearest ?? EXIT);
  }
  return immediate;
}

/** Which blocks every path from the entry to a block has to pass through. */
function dominators(blocks: Map<number, Block>, entry: number): Map<number, Set<number>> {
  const starts = [...blocks.keys()].sort((a, b) => a - b);
  const predecessors = new Map<number, number[]>(starts.map(start => [start, []]));
  for (const block of blocks.values()) {
    for (const successor of block.successors) predecessors.get(successor)?.push(block.start);
  }
  const all = new Set(starts);
  const sets = new Map<number, Set<number>>(
    starts.map(start => [start, start === entry ? new Set([entry]) : new Set(all)]),
  );
  for (let changed = true; changed;) {
    changed = false;
    for (const start of starts) {
      if (start === entry) continue;
      let shared: Set<number> | undefined;
      for (const predecessor of predecessors.get(start)!) {
        const of = sets.get(predecessor)!;
        shared = shared === undefined ? new Set(of) : new Set([...shared].filter(x => of.has(x)));
      }
      const next = new Set([start, ...(shared ?? [])]);
      const before = sets.get(start)!;
      if (next.size === before.size && [...next].every(x => before.has(x))) continue;
      sets.set(start, next);
      changed = true;
    }
  }
  return sets;
}

/** A loop, as the blocks it is made of and the two places control leaves it. */
interface Loop {
  readonly header: number;
  /** Every block inside the loop, the header included. */
  readonly body: Set<number>;
  /** The blocks whose branch closes the loop. */
  readonly latches: number[];
  /** Where the code after the loop begins, or EXIT when nothing leaves it. */
  readonly follow: number;
}

/**
 * Every edge that closes a cycle, found by a depth-first walk: an edge to a
 * block the walk is still inside. pc order does not say this - javac lays a
 * `while (a && b)` out with the second test jumping *backwards* into the body.
 */
function retreatingEdges(blocks: Map<number, Block>, entry: number): [number, number][] {
  const edges: [number, number][] = [];
  const open = new Set<number>();
  const done = new Set<number>();
  const stack: { at: number; next: number }[] = [{ at: entry, next: 0 }];
  open.add(entry);
  while (stack.length > 0) {
    const frame = stack[stack.length - 1]!;
    const successors = blocks.get(frame.at)?.successors ?? [];
    if (frame.next >= successors.length) {
      open.delete(frame.at);
      done.add(frame.at);
      stack.pop();
      continue;
    }
    const successor = successors[frame.next++]!;
    if (!blocks.has(successor) || done.has(successor)) continue;
    if (open.has(successor)) {
      edges.push([frame.at, successor]);
      continue;
    }
    open.add(successor);
    stack.push({ at: successor, next: 0 });
  }
  return edges;
}

/**
 * Where the code after the loop begins. The test decides it - the one at the
 * head of a `while`, the one at the foot of a `do` - because a `break` leaves
 * from a block that no longer reaches the latch, so it is not in the loop's
 * body and its own target would otherwise look like a second way out.
 */
function loopFollow(
  blocks: Map<number, Block>,
  header: number,
  latches: readonly number[],
  body: ReadonlySet<number>,
  monitors: readonly MonitorRegion[] = [],
): number {
  // A `synchronized` *inside* the loop keeps its own blocks: the `return` javac
  // writes in there leaves the loop, but it is part of that statement and the
  // statement is what writes it. A loop inside a `synchronized` is the other way
  // round - then the range holds the whole body, and its blocks are the loop's.
  const held = monitors.filter(
    region =>
      [...body].some(start => start >= region.startPc && start < region.endPc) &&
      [...body].some(start => start < region.startPc || start >= region.endPc),
  );
  const inStatement = (start: number): boolean =>
    held.some(region => start >= region.startPc && start < region.endPc);
  const outside = (start: number): number[] =>
    (blocks.get(start)?.successors ?? []).filter(
      successor => !body.has(successor) && !inStatement(successor),
    );
  const fromHeader = outside(header);
  // Only a header that is the test itself: one that carries statements - a call
  // among them - is the start of a `do`'s body, and what leaves it is a `break`,
  // not the loop's end.
  if (
    blocks.get(header)!.kind === "conditional" &&
    isPureBlock(blocks.get(header)!) &&
    fromHeader.length === 1
  ) {
    return fromHeader[0]!;
  }
  if (latches.length === 1) {
    const latch = blocks.get(latches[0]!)!;
    const fromLatch = outside(latch.start);
    if (latch.kind === "conditional" && fromLatch.length === 1) return fromLatch[0]!;
  }
  const exits = new Set<number>();
  for (const start of body) for (const successor of outside(start)) exits.add(successor);
  if (exits.size <= 1) return [...exits][0] ?? EXIT;
  // Several ways out, none of them a test: the one they all reach ends the loop.
  const candidates = [...exits].sort((a, b) => a - b);
  const merged = candidates.find(candidate =>
    candidates.every(
      other => other === candidate || reachableBlocks(blocks, [other]).has(candidate),
    ),
  );
  if (merged === undefined) throw new NotDecompilable("a loop with more than one exit");
  return merged;
}

/**
 * The natural loops of the method, keyed by header. Every cycle has to be one:
 * an edge that closes a cycle without its target dominating its source means
 * two ways into the same loop, which is not something Java source can say.
 */
/**
 * The graph with an edge from every protected block to the handlers guarding it.
 * A handler is only reachable by throwing, so without those edges it has no
 * predecessor at all: its dominator set collapses to itself and poisons every
 * block it flows into, which makes a loop holding a `try` look irreducible.
 * Only the loop analysis wants them - a merge point does not, since an `if`
 * inside a `try` still ends where its own arms come back together.
 */
function withExceptionEdges(
  blocks: Map<number, Block>,
  regions: readonly TryRegion[],
  monitors: readonly MonitorRegion[] = [],
): Map<number, Block> {
  if (regions.length === 0 && monitors.length === 0) return blocks;
  const augmented = new Map<number, Block>();
  for (const [start, block] of blocks) {
    const handlers = [
      ...new Set([
        ...regions
          .filter(region => start >= region.startPc && start < region.endPc)
          .flatMap(region => region.clauses.map(clause => clause.handlerPc)),
        // A `synchronized` is guarded the same way, and a handler nothing reaches
        // has no dominators of its own - which poisons every block it flows into.
        ...monitors
          .filter(region => start >= region.startPc && start < region.endPc)
          .map(region => region.handlerPc),
      ]),
    ].filter(handler => !block.successors.includes(handler));
    augmented.set(
      start,
      handlers.length === 0 ? block : { ...block, successors: [...block.successors, ...handlers] },
    );
  }
  return augmented;
}

function findLoops(
  blocks: Map<number, Block>,
  entry: number,
  regions: readonly TryRegion[] = [],
  monitors: readonly MonitorRegion[] = [],
): Map<number, Loop> {
  // Everything that asks which blocks a loop is made of runs over the graph with
  // the throwing edges in it; where the loop *ends* is read off the real one,
  // where entering a handler is not a way out of the body.
  const flow = withExceptionEdges(blocks, regions, monitors);
  const retreating = retreatingEdges(flow, entry);
  if (retreating.length === 0) return new Map();
  const doms = dominators(flow, entry);
  const latchesOf = new Map<number, number[]>();
  for (const [from, to] of retreating) {
    if (!doms.get(from)!.has(to)) throw new NotDecompilable("irreducible control flow");
    latchesOf.set(to, [...(latchesOf.get(to) ?? []), from]);
  }
  const predecessors = new Map<number, number[]>();
  for (const block of flow.values()) {
    for (const successor of block.successors) {
      predecessors.set(successor, [...(predecessors.get(successor) ?? []), block.start]);
    }
  }
  const loops = new Map<number, Loop>();
  for (const [header, latches] of latchesOf) {
    // Everything that reaches a latch without leaving through the header.
    const body = new Set<number>([header]);
    const queue = [...latches];
    while (queue.length > 0) {
      const at = queue.pop()!;
      if (body.has(at)) continue;
      body.add(at);
      queue.push(...(predecessors.get(at) ?? []));
    }
    loops.set(header, {
      header,
      body,
      latches,
      follow: loopFollow(blocks, header, latches, body, monitors),
    });
  }
  // Two loops are either nested or disjoint; anything else is one loop entered
  // at two places, which no `while` describes.
  for (const outer of loops.values()) {
    for (const inner of loops.values()) {
      if (outer === inner) continue;
      const shared = [...inner.body].filter(start => outer.body.has(start));
      if (shared.length === 0 || shared.length === inner.body.size) continue;
      if (shared.length !== outer.body.size) throw new NotDecompilable("overlapping loops");
    }
  }
  return loops;
}

/**
 * Instructions a *condition* may be built from: no store, no call, nothing that
 * is a statement. A block made only of these can be folded into the condition of
 * the branch before it (`a && b`) or into a ternary without changing what runs.
 */
const PURE_MNEMONICS =
  /^(?:nop|aconst_null|[ilfd]const_\w+|bipush|sipush|ldc\w*|[ilfda]load(?:_\d|_w)?|arraylength|[ilfdabcs]aload|[ilfd](?:add|sub|mul|div|rem|neg|shl|shr|ushr|and|or|xor)|[ilfd]2[ilfdbcs]|lcmp|[fd]cmp[lg]|getstatic|getfield|checkcast|instanceof|dup)$/;

/**
 * Where the code after `block`'s body begins - the pc a store at the end of it
 * has to look its variable's scope up at. A branch ends at its terminator; a
 * block that falls through ends where the next one starts.
 */
function endOf(block: Block): number {
  if (block.kind === "fall") return block.successors[0]!;
  return block.instructions[block.instructions.length - 1]!.pc;
}

/**
 * Whether the test at the head of the loop is what leaves it - the `while (c)`
 * shape, which javac writes with the test at the bottom and a `goto` into it.
 * Only condition-only blocks count on the way there: `while (a && b)` is two of
 * them, while a loop that leaves from inside its body is a `do` or a `for (;;)`.
 */
function headerExits(blocks: Map<number, Block>, loop: Loop): boolean {
  const seen = new Set<number>();
  const queue = [loop.header];
  while (queue.length > 0) {
    const at = queue.pop()!;
    if (seen.has(at)) continue;
    seen.add(at);
    const block = blocks.get(at);
    if (block === undefined || block.kind !== "conditional" || !isConditionBlock(block)) continue;
    if (block.successors.includes(loop.follow)) return true;
    for (const successor of block.successors) {
      if (loop.body.has(successor)) queue.push(successor);
    }
  }
  return false;
}

/** A loop being written right now. */
interface ActiveLoop {
  readonly loop: Loop;
  /** Where `continue;` goes: the test, which a `do` keeps at the bottom. */
  readonly continueTarget: number;
  /**
   * Whether jumping to `continueTarget` really is `continue;`. A `do`'s latch is
   * the test *and* the tail of the body when nothing else jumps to it - and
   * `continue;` skips that tail, so a jump there is not one.
   */
  readonly continues: boolean;
}

interface ActiveSwitch {
  /** Where `break;` goes: the end of the statement. */
  readonly follow: number;
  /**
   * How many loops were being written when this `switch` was entered. It is the
   * innermost breakable statement only while that is still true - a loop opened
   * inside it takes every unlabeled `break` for itself.
   */
  readonly loopDepth: number;
}

/**
 * A block a condition may be folded from. Like `isPureBlock`, but a call is
 * allowed: folding runs it exactly once and in the same place, which is not
 * true of the ternary arms `isPureBlock` guards - those can be evaluated twice.
 */
function isConditionBlock(block: Block): boolean {
  return block.instructions.every(
    (instruction, index) =>
      PURE_MNEMONICS.test(instruction.mnemonic) ||
      INVOKES.has(instruction.mnemonic) ||
      instruction.mnemonic === "invokedynamic" ||
      (index === block.instructions.length - 1 &&
        (CONDITIONAL_BRANCHES.has(instruction.mnemonic) || isGoto(instruction.mnemonic))),
  );
}

function isPureBlock(block: Block): boolean {
  return block.instructions.every(
    (instruction, index) =>
      PURE_MNEMONICS.test(instruction.mnemonic) ||
      (index === block.instructions.length - 1 &&
        (CONDITIONAL_BRANCHES.has(instruction.mnemonic) || isGoto(instruction.mnemonic))),
  );
}

// --- the method body -----------------------------------------------------------------

/** Straight-line bytecode of one method, as Java statements. */
class BodyDecompiler {
  private readonly stack: Expr[] = [];
  /** Every local name handed out so far, so a reused slot cannot shadow one. */
  private names = new Set<string>();
  /** The lambda bodies being inlined right now, so one cannot inline itself. */
  private inlining = new Set<string>();
  private readonly byName = new Map<string, Local>();
  readonly statements: Stmt[] = [];
  /**
   * Declarations of locals first stored inside a branch. Java scopes them to
   * that branch, the bytecode does not, so they are declared up front and the
   * store becomes an assignment; `methodSource` puts these first.
   *
   * ponytail: hoisting to the top of the method, not to the innermost block that
   * encloses every use - that is the upgrade path if the output reads badly.
   */
  readonly hoisted: string[] = [];
  /** Where statements are being appended right now: a branch's arm, or the body. */
  private current: Stmt[] = this.statements;
  private depth = 0;
  private blocks = new Map<number, Block>();
  private entryPc = 0;
  /** The block whose instructions are running, for reasoning about reads. */
  private currentBlock = 0;
  private followOf = new Map<number, number>();
  private loops = new Map<number, Loop>();
  private regions: TryRegion[] = [];
  /**
   * The locals a lambda captured. Java takes only effectively final ones, and a
   * variable this hoisted to the top of the method is written where it was
   * declared - once per turn of any loop around it - which is only known when
   * the whole body is out.
   */
  private readonly captured: Local[] = [];
  /** The `synchronized` statements of this method, and the slots they hold. */
  private monitors: MonitorRegion[] = [];
  /** The `finally` statements of this method, and the ones being written now. */
  private finallys: FinallyRegion[] = [];
  private readonly activeFinallys = new Set<FinallyRegion>();
  /**
   * The slots held by the `synchronized` statements being written right now.
   * javac frees the monitor's local when the statement ends and reuses the slot
   * for the next variable, so this may not outlive the body it belongs to.
   */
  private readonly monitorSlots: number[] = [];
  /** Every instruction by pc, for the ones a statement has to look up. */
  private instructionAt = new Map<number, Instruction>();
  /** The `try` statements being written right now. */
  private readonly activeTries = new Set<TryRegion>();
  /**
   * Instructions to drop from the front of a block: a handler starts with the
   * store of the exception, which source writes as the catch parameter.
   */
  private readonly skip = new Map<number, number>();
  /** `followOf` over the whole method, which `followOf` itself is not inside a loop. */
  private methodFollowOf = new Map<number, number>();
  /** Dominators over the graph the throwing edges are in, for a `try`'s own end. */
  private dominators = new Map<number, Set<number>>();
  /** The loops being written right now, innermost last. */
  private readonly active: ActiveLoop[] = [];
  /** The `switch` statements being written right now, innermost last. */
  private readonly switches: ActiveSwitch[] = [];
  private readonly visited = new Set<number>();
  /** Hands out the ids that tell the copies of one `new` apart. */
  private pendingCount = 0;
  private initCount = 0;
  /** The access flags of the nested classes this file names, read once. */
  private readonly innerFlags: Map<string, number>;
  /** The BootstrapMethods table, which every invokedynamic indexes into. */
  private readonly bootstraps: readonly BootstrapMethod[];
  /**
   * Every `if (...)` line emitted, with the condition it came from: a local's
   * type can still narrow after the line is written (the use that proves it is a
   * boolean may come later), and the text has to follow.
   */
  private readonly conditions: {
    list: Stmt[];
    index: number;
    condition: Expr;
    wrap: (text: string) => string;
  }[] = [];

  constructor(
    private readonly classFile: ClassFile,
    private readonly locals: Map<number, Local>,
    private readonly localTable: LocalEntry[],
    private readonly methodReturnType: string,
    private readonly isStatic: boolean,
    /**
     * The names the *enclosing* method has handed out, when this body is a
     * lambda being inlined into one: Java lets neither shadow the other, so the
     * two share the set.
     */
    shared?: { names: Set<string>; inlining: Set<string> },
  ) {
    if (shared !== undefined) {
      this.names = shared.names;
      this.inlining = shared.inlining;
    }
    this.innerFlags = innerClassFlags(classFile);
    this.bootstraps = readBootstrapMethods(classFile);
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
    // A lambda takes its type from where it is written; when that is not the
    // interface itself, source had to say which one it is.
    if (value.lambda === true && target !== value.type) return `(${value.type}) ${value.text}`;
    const local = this.byName.get(value.text);
    if (
      local !== undefined &&
      !local.authoritative &&
      local.type === "int" &&
      ERASED_TO_INT.includes(target)
    ) {
      this.retype(local, target);
    }
    return coerce(value, target);
  }

  /** Give `local` a narrower type, rewriting its declaration and assignments. */
  private retype(local: Local, target: string): void {
    local.type = target;
    const declaration = local.declaration;
    if (declaration !== undefined && !declaration.inline) {
      declaration.list[declaration.index] = `${target} ${local.name};`;
    }
    for (const [index, write] of local.writes.entries()) {
      const assigned = coerce(write.value, target);
      write.list[write.index] =
        index === 0 && declaration?.inline === true
          ? `${target} ${local.name} = ${assigned};`
          : `${local.name} = ${assigned};`;
    }
    for (const emitted of this.conditions) {
      emitted.list[emitted.index] = emitted.wrap(this.renderCondition(emitted.condition).text);
    }
  }

  /** A condition as a line, kept re-renderable for as long as a local can retype. */
  private emitCondition(condition: Expr, wrap: (text: string) => string): void {
    this.conditions.push({ list: this.current, index: this.current.length, condition, wrap });
    this.current.push(wrap(this.renderCondition(condition).text));
  }

  /**
   * A condition rendered against the types its locals are known to have *now*.
   * `ifeq` on a local is how both `if (!b)` and `if (x == 0)` are compiled, so
   * the comparison is written against an int until something proves the variable
   * is a boolean - and then this rewrites it.
   */
  private renderCondition(condition: Expr): Expr {
    const logic = condition.logic;
    if (logic === undefined) return condition;
    switch (logic.kind) {
      case "and":
      case "or":
        return logical(
          logic.kind,
          this.renderCondition(logic.left),
          this.renderCondition(logic.right),
        );
      case "not":
        return not(this.renderCondition(logic.value));
      case "compare": {
        const local = this.byName.get(logic.left.text);
        if (local === undefined || local.type === logic.left.type) return condition;
        const value = primary(local.name, local.type);
        if (local.type === "boolean" && logic.right.text === "0") {
          if (logic.op === "!=") return value;
          if (logic.op === "==") return not(value);
        }
        return compare(value, logic.op, primary(coerce(logic.right, local.type), local.type));
      }
    }
  }

  /** The statements `run` appends, as a nested block rather than the body. */
  private capture(run: () => void): Stmt[] {
    const outer = this.current;
    const captured: Stmt[] = [];
    this.current = captured;
    this.depth++;
    try {
      run();
    } finally {
      this.current = outer;
      this.depth--;
    }
    return captured;
  }

  private push(expr: Expr): void {
    this.stack.push(expr);
  }

  /**
   * The literal a fresh array may still turn into. Only a constant length can:
   * `{1, 2, 3}` is the only source that duplicates a new array and writes
   * through the copies, and its length is the element count.
   */
  private arrayInit(prefix: string, element: string, length: Expr): ArrayInit | undefined {
    if (!/^\d+$/.test(length.text)) return undefined;
    return { id: this.initCount++, prefix, element, length: Number(length.text), elements: [] };
  }

  /**
   * One `dup; index; value; Xastore` of an array initializer. The store
   * consumed one copy of the array; the copy left below it is the same array,
   * so it is replaced by the literal grown by this element - and by the
   * finished literal once the last one lands.
   */
  private fillArray(init: ArrayInit, index: Expr, value: Expr): void {
    // javac writes an initializer front to back, one element per index.
    if (index.text !== String(init.elements.length) || init.elements.length >= init.length) {
      throw new NotDecompilable("array initializer out of order");
    }
    const below = this.popRaw();
    if (below.init?.id !== init.id) throw new NotDecompilable("array initializer copy lost");
    const elements = [...init.elements, this.coerceInto(value, init.element)];
    if (elements.length === init.length) {
      return this.push(primary(`${init.prefix}{${elements.join(", ")}}`, below.type));
    }
    this.push({ ...below, init: { ...init, elements } });
  }

  private popRaw(): Expr {
    const expr = this.stack.pop();
    if (!expr) throw new NotDecompilable("stack underflow");
    return expr;
  }

  private pop(): Expr {
    const expr = this.popRaw();
    // An `lcmp` result has no source form of its own (`Long.compare` is a call,
    // which is a later phase), so only the branch that follows may consume it.
    if (expr.compared !== undefined) throw new NotDecompilable("a comparison outside a branch");
    // A half-written array literal has no source form: the elements already
    // consumed are gone from the statement list.
    if (expr.init !== undefined && expr.init.elements.length > 0) {
      throw new NotDecompilable("incomplete array initializer");
    }
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
        // javac writes one row per scope range, so the same variable can appear
        // twice for one slot (once per arm of an `if`); the name and type are
        // what say it is the same one.
        const origin = existing.origin;
        if (
          origin === scoped ||
          (origin !== undefined && origin.name === scoped.name && origin.type === scoped.type)
        ) {
          return existing;
        }
      } else if (!isStore || existing.authoritative || existing.type === fallbackType) {
        // Without a debug table a slot is only a variable as long as one
        // definition explains every path to here: two arms that stored
        // differently-typed values were split into two variables, and which one
        // this reads is not something the bytecode still says.
        if (
          !isStore &&
          existing.storeBlocks.size > 0 &&
          this.reachesAvoiding(this.currentBlock, existing.storeBlocks)
        ) {
          throw new NotDecompilable(`local ${slot} is written in more than one branch`);
        }
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
      declaration: undefined,
      authoritative: scoped?.type !== undefined && scoped.type !== "",
      writes: [],
      storeBlocks: new Set(),
    };
    this.byName.set(local.name, local);
    this.locals.set(slot, local);
    return local;
  }

  /** The arguments of a call, popped in reverse and written as the parameters. */
  private arguments(descriptor: string): string[] {
    const params = parameterSlots(descriptor, true).map(p => p.type);
    const args: string[] = [];
    for (let i = params.length - 1; i >= 0; i--) {
      const target = params[i]!;
      const text = this.coerceInto(this.pop(), target);
      // Java narrows an int constant implicitly when it is assigned, but never
      // when it is passed: `f((byte) 3)` is the only way to write the call.
      const narrows = (target === "byte" || target === "short") && /^-?\d+$/.test(text);
      // A bare `null` where an `Object` is declared re-resolves the overload -
      // `String.valueOf(null)` binds `char[]` and throws - so the type the call
      // was compiled against is written back. Only `Object` gets it: a cast to
      // any other reference type is a `checkcast` the original did not have.
      //
      // ponytail: that leaves a narrower hole - a parameter typed `CharSequence`
      // with a `String` overload alongside it also re-resolves. Closing it needs
      // the callee's other overloads, which live outside this class file.
      const untyped = text === "null" && target === "java.lang.Object";
      args.unshift(narrows || untyped ? `(${sourceTypeText(target, this.self)}) ${text}` : text);
    }
    return args;
  }

  /**
   * `value` where a `target`-typed value belongs, kept an expression so the
   * caller can still parenthesize it.
   */
  private coercedExpr(value: Expr, target: string): Expr {
    const text = this.coerceInto(value, target);
    if (text === value.text) return value;
    // What the rewrite produces is a literal (`true`, `'a'`) or something with
    // an operator in it (`(char) 200`, a materialized boolean's own ternary);
    // the latter gets the lowest precedence, so it is parenthesized wherever it
    // lands rather than needing its real level worked out.
    return { text, prec: /^\S+$/.test(text) ? PREC_PRIMARY : 0, type: target };
  }

  /**
   * One invokedynamic. javac writes two of them: a string concatenation, and the
   * lambda or method reference `LambdaMetafactory` builds.
   */
  private dynamic(index: number): void {
    const pool = this.classFile.pool;
    const entry = pool[index];
    if (entry?.tag !== "invokeDynamic") throw new NotDecompilable("bad invokedynamic reference");
    const site = nameAndTypeAt(pool, entry.nameAndTypeIndex);
    const bootstrap = this.bootstraps[entry.bootstrapMethodIndex];
    const handle = bootstrap === undefined ? undefined : pool[bootstrap.handleIndex];
    const factory =
      handle?.tag === "methodHandle" ? memberRef(pool, handle.referenceIndex) : undefined;
    if (site === undefined || bootstrap === undefined || factory === undefined) {
      throw new NotDecompilable("bad invokedynamic reference");
    }
    if (factory.owner === "java/lang/invoke/StringConcatFactory") {
      return this.concat(site, bootstrap, factory);
    }
    // `altMetafactory` carries flags of its own - a serializable lambda, extra
    // interfaces, extra bridges - and dropping them would change what the class
    // implements.
    if (factory.owner === "java/lang/invoke/LambdaMetafactory" && factory.name === "metafactory") {
      return this.lambda(site, bootstrap);
    }
    throw new NotDecompilable("an invokedynamic that is neither a lambda nor a concatenation");
  }

  /**
   * A lambda or a method reference: `LambdaMetafactory.metafactory` is handed
   * the interface method's type, the method javac compiled the body into, and
   * the type it is instantiated at; the call site takes the captured values and
   * returns the interface. It comes back as a lambda that calls that method,
   * which is what the bytecode says - a `x -> ...` body would have to inline it.
   */
  private lambda(site: { name: string; descriptor: string }, bootstrap: BootstrapMethod): void {
    const pool = this.classFile.pool;
    const samType = pool[bootstrap.argumentIndexes[0] ?? -1];
    const handle = pool[bootstrap.argumentIndexes[1] ?? -1];
    if (samType?.tag !== "methodType" || handle?.tag !== "methodHandle") {
      throw new NotDecompilable("a lambda without an implementation");
    }
    const sam = utf8(pool, samType.descriptorIndex);
    const target = memberRef(pool, handle.referenceIndex);
    // The interface method's type is erased; what the lambda is instantiated at
    // is the third bootstrap argument, and it says what each parameter really is.
    const instantiated = pool[bootstrap.argumentIndexes[2] ?? -1];
    const exact =
      instantiated?.tag === "methodType" ? utf8(pool, instantiated.descriptorIndex) : undefined;
    if (sam === undefined || target === undefined || exact === undefined) {
      throw new NotDecompilable("a lambda without an implementation");
    }
    // The captured values are the call site's arguments; the interface method's
    // own parameters are what the lambda has to name.
    const captureTypes = parameterSlots(site.descriptor, true).map(one => one.type);
    const captures: string[] = [];
    for (let i = captureTypes.length - 1; i >= 0; i--) {
      const value = this.coerceInto(this.pop(), captureTypes[i]!);
      const local = this.byName.get(value);
      if (local !== undefined) this.captured.push(local);
      // javac evaluates a captured value *here* and hands it over; the lambda
      // this writes reads the text again every time it runs. That is the same
      // value only for a variable - a field or an array element can change, and
      // `name::toUpperCase` would then upper-case whatever the field holds later.
      if (local === undefined && value !== "this" && !isConstantText(value)) {
        throw new NotDecompilable("a lambda that captures more than a variable");
      }
      captures.unshift(value);
    }
    // A parameter the interface passes as its erased type is cast at the use,
    // which is what the body javac generated does.
    const erased = parameterSlots(sam, true).map(one => one.type);
    const wantedTypes = parameterSlots(exact, true).map(one => one.type);
    if (erased.length !== wantedTypes.length) {
      throw new NotDecompilable("a lambda that binds differently");
    }
    const parameters = erased.map(() => this.freshName("p"));
    const passed = [
      ...captures,
      ...parameters.map((name, index) =>
        erased[index] === wantedTypes[index] ? name : `(${wantedTypes[index]}) ${name}`,
      ),
    ];
    // What kind of call the handle is decides both forms below; an unknown one
    // is not a call at all.
    if (![5, 6, 7, 8, 9].includes(handle.referenceKind)) {
      throw new NotDecompilable("a lambda that is not a call");
    }
    const type = returnType(site.descriptor);
    // A body javac generated is the lambda source wrote: inlining it is what
    // brings that source back, and javac generates the same method from it.
    const body =
      target.owner === this.classFile.thisClass
        ? this.classFile.methods.find(
            one =>
              one.name === target.name &&
              one.descriptor === target.descriptor &&
              (one.flags & ACC_SYNTHETIC) !== 0,
          )
        : undefined;
    if (body !== undefined) {
      const text = this.inlineLambda(body, captures, passed, returnType(sam));
      return this.push({
        text: `(${parameters.join(", ")}) -> ${text}`,
        prec: 0,
        type,
        lambda: true,
      });
    }
    const wanted = parameterSlots(target.descriptor, true).length;
    let call: string;
    switch (handle.referenceKind) {
      case 6: // REF_invokeStatic
        if (passed.length !== wanted) throw new NotDecompilable("a lambda that binds differently");
        call = `${this.staticCallee(target.owner, target.name)}(${passed.join(", ")})`;
        break;
      case 8: // REF_newInvokeSpecial
        if (passed.length !== wanted) throw new NotDecompilable("a lambda that binds differently");
        call = `new ${typeName(target.owner, this.self)}(${passed.join(", ")})`;
        break;
      case 5: // REF_invokeVirtual
      case 7: // REF_invokeSpecial
      case 9: {
        // REF_invokeInterface: the receiver is the captured value, or - for an
        // unbound reference like `String::length` - the first parameter.
        const receiver = passed[0];
        if (receiver === undefined || passed.length !== wanted + 1) {
          throw new NotDecompilable("a lambda that binds differently");
        }
        // A cast receiver needs the parentheses source would have written.
        const target0 = receiver.startsWith("(") ? `(${receiver})` : receiver;
        call = `${target0}.${target.name}(${passed.slice(1).join(", ")})`;
        break;
      }
      default:
        throw new NotDecompilable("a lambda that is not a call");
    }
    // With nothing captured and nothing to cast, source's own form is a method
    // reference - and that is what javac compiles back to this same call site.
    const reference =
      captures.length === 0 && parameters.every((name, index) => passed[index] === name);
    if (reference) {
      const written =
        handle.referenceKind === 8
          ? `${typeName(target.owner, this.self)}::new`
          : `${typeName(target.owner, this.self)}::${target.name}`;
      // A method reference takes its type from where it is written, exactly as a
      // lambda does, so it needs the interface named in the same places.
      return this.push({ text: written, prec: PREC_PRIMARY, type, lambda: true });
    }
    this.push({ text: `(${parameters.join(", ")}) -> ${call}`, prec: 0, type, lambda: true });
  }

  /**
   * The body of a lambda, as the expression or block source wrote: the method
   * javac generated is decompiled with its parameters bound to what the call
   * site captured and to the lambda's own parameters.
   */
  private inlineLambda(
    body: Member,
    captures: readonly string[],
    passed: readonly string[],
    /** What the interface method returns, which is what the body has to yield. */
    yields: string,
  ): string {
    const key = `${body.name}${body.descriptor}`;
    if (this.inlining.has(key)) throw new NotDecompilable("a lambda that inlines itself");
    const isStatic = (body.flags & ACC_STATIC) !== 0;
    const code = readCode(body, this.classFile.pool);
    if (!code) throw new NotDecompilable("a lambda without a body");
    // What the body's parameters stand for: the captured values, then the
    // interface method's own parameters, cast where the interface erased them.
    // A cast binds looser than a member access, so a bound value that is not a
    // plain name is parenthesized where the body reads it.
    const bound = passed.map(text => (/^[\w$.]+$/.test(text) ? text : `(${text})`));
    if (!isStatic) {
      // An instance body reads the enclosing object as `this`, which is what the
      // call site captured first.
      if (captures[0] !== "this") throw new NotDecompilable("a lambda on another object");
      bound.shift();
    }
    const localTable = readLocalVariables(code, this.classFile.pool);
    const locals = buildLocals(body, localTable, isStatic, this.self);
    const slots = parameterSlots(body.descriptor, isStatic);
    if (slots.length !== bound.length) throw new NotDecompilable("a lambda that binds differently");
    slots.forEach(({ slot }, index) => {
      const local = locals.get(slot);
      if (local !== undefined) local.name = bound[index]!;
    });
    const nested = new BodyDecompiler(this.classFile, locals, localTable, yields, isStatic, {
      names: this.names,
      inlining: this.inlining,
    });
    this.inlining.add(key);
    try {
      nested.run(decodeInstructions(this.classFile, code.code), code.exceptions);
    } finally {
      this.inlining.delete(key);
    }
    // A body that assigns to one of its parameters would be assigning to what
    // the call site handed it, which source cannot write.
    for (const { slot } of slots) {
      if ((locals.get(slot)?.writes.length ?? 0) > 0) {
        throw new NotDecompilable("a lambda that assigns to its parameter");
      }
    }
    const lines = [...nested.hoisted, ...flattenStatements(nested.statements)];
    if (lines[lines.length - 1] === "return;") lines.pop();
    const only = lines.length === 1 ? lines[0]! : undefined;
    // One expression is the form source wrote; anything else needs the block.
    if (only?.startsWith("return ") === true && only.endsWith(";")) {
      return only.slice("return ".length, -1);
    }
    if (only !== undefined && isStatementExpression(only)) return only.slice(0, -1);
    return `{ ${lines.join(" ")} }`;
  }

  /**
   * A string concatenation, which javac compiles to an invokedynamic whose
   * bootstrap is `StringConcatFactory.makeConcatWithConstants`: the recipe (its
   * first bootstrap argument) is the result text with `\u0001` where a stack
   * argument goes and `\u0002` where one of the remaining bootstrap constants
   * does. `makeConcat` is the same call with no literal parts at all.
   */
  private concat(
    site: { name: string; descriptor: string },
    bootstrap: BootstrapMethod,
    factory: { owner: string; name: string },
  ): void {
    const pool = this.classFile.pool;
    if (
      (factory.name !== "makeConcatWithConstants" && factory.name !== "makeConcat") ||
      returnType(site.descriptor) !== "java.lang.String"
    ) {
      throw new NotDecompilable("an invokedynamic that is not a string concatenation");
    }
    const params = parameterSlots(site.descriptor, true).map(p => p.type);
    const args: Expr[] = [];
    for (let i = params.length - 1; i >= 0; i--) {
      args.unshift(this.coercedExpr(this.pop(), params[i]!));
    }

    let recipe = "\u0001".repeat(args.length);
    if (factory.name === "makeConcatWithConstants") {
      const first = pool[bootstrap.argumentIndexes[0] ?? 0];
      if (first?.tag !== "string") throw new NotDecompilable("a concatenation without a recipe");
      recipe = utf8(pool, first.valueIndex) ?? "";
    }
    const parts: { expr: Expr; isString: boolean }[] = [];
    let literal = "";
    const flush = (): void => {
      if (literal === "") return;
      parts.push({
        expr: primary(`"${escapeString(literal)}"`, "java.lang.String"),
        isString: true,
      });
      literal = "";
    };
    let taken = 0;
    let constant = 1;
    for (const char of recipe) {
      if (char === "\u0001") {
        const arg = args[taken];
        if (arg === undefined) throw new NotDecompilable("a recipe that wants more arguments");
        flush();
        parts.push({ expr: arg, isString: params[taken] === "java.lang.String" });
        taken++;
      } else if (char === "\u0002") {
        // A literal part javac could not put in the recipe itself, because it
        // contains one of the two tag characters: it is spliced back in as text.
        const at = pool[bootstrap.argumentIndexes[constant] ?? 0];
        constant++;
        if (at?.tag !== "string") {
          throw new NotDecompilable(
            constant > bootstrap.argumentIndexes.length
              ? "a recipe that wants more constants"
              : "a concatenation constant that is not a string",
          );
        }
        literal += utf8(pool, at.valueIndex) ?? "";
      } else {
        literal += char;
      }
    }
    flush();
    if (taken !== args.length) throw new NotDecompilable("a recipe that leaves arguments unused");
    // With every part in the recipe the result is a constant expression, and
    // javac folds one of those to an `ldc` instead of the call it came from.
    if (args.length === 0) throw new NotDecompilable("a concatenation of constants");

    // The result is a String, so one of the first two operands has to be one:
    // `"" + i + j` and `i + j` compile to the same arguments but are not the
    // same expression. A single operand needs the empty literal too - `s + ""`
    // is a concatenation, and `null + ""` is `"null"` where `s` alone is null.
    const concatenates = parts.length > 1 && (parts[0]!.isString || parts[1]!.isString);
    if (!concatenates) {
      parts.unshift({ expr: primary('""', "java.lang.String"), isString: true });
    }
    let result = parts[0]!.expr;
    for (const part of parts.slice(1)) {
      result = binary(result, "+", part.expr, PREC_ADD, "java.lang.String");
    }
    // A part that calls something makes the concatenation itself a value that
    // does something: dropping it has to keep the call, not delete it.
    if (args.some(arg => arg.effects === true)) result = { ...result, effects: true };
    this.push(result);
  }

  /** A static call: unqualified when it is this class's own method. */
  private staticCallee(owner: string, name: string): string {
    if (owner === this.classFile.thisClass) return name;
    return `${typeName(owner, this.self)}.${name}`;
  }

  /** An instance call, with the receiver the bytecode pushed before the arguments. */
  private receiverCallee(mnemonic: string, owner: string, name: string): string {
    const receiver = this.pop();
    // A lambda has no type of its own, so calling a method on one needs the
    // interface named - source could not have written it any other way.
    if (receiver.lambda === true) return `(${named(receiver).text}).${name}`;
    if (mnemonic !== "invokespecial" || owner === this.classFile.thisClass) {
      return `${at(receiver, PREC_PRIMARY)}.${name}`;
    }
    // The only other invokespecial source writes is `super.m()`; an interface's
    // `Iface.super.m()` needs the interface named, which this phase does not do.
    if (owner !== (this.classFile.superClass ?? "java/lang/Object") || receiver.text !== "this") {
      throw new NotDecompilable("unsupported instruction invokespecial");
    }
    return `super.${name}`;
  }

  /**
   * A constructor call: either `new C(...)`, whose object is already on the
   * stack, or the `super(...)`/`this(...)` that opens a constructor - which is
   * not a call in source but the shape of one, and without which no constructor
   * decompiles at all.
   */
  private construct(target: MemberRef): void {
    // An inner class's constructor takes the enclosing instance as its first
    // argument, and source cannot pass it: `outer.new Inner(...)` is the only
    // way to write one. The `InnerClasses` attribute of *this* file says which
    // of the nested classes it names are `static`; without an entry, the shape
    // of the descriptor is all there is to go on.
    const enclosing = target.owner.slice(0, Math.max(0, target.owner.lastIndexOf("$")));
    if (enclosing !== "") {
      const access = this.innerFlags.get(target.owner);
      const first = parameterSlots(target.descriptor, true)[0]?.type;
      const inner =
        access === undefined
          ? first === enclosing.replaceAll("/", ".")
          : (access & ACC_STATIC) === 0;
      if (inner) throw new NotDecompilable("an inner class constructor");
    }
    const args = this.arguments(target.descriptor);
    const receiver = this.pop();
    if (receiver.pending === undefined) {
      const isSuper = target.owner === (this.classFile.superClass ?? "java/lang/Object");
      const isThis = target.owner === this.classFile.thisClass;
      if (!isSuper && !isThis) throw new NotDecompilable("constructor call to an unrelated class");
      if (receiver.text !== "this") throw new NotDecompilable("constructor call on another object");
      if (this.statements.length > 0 || this.depth > 0) {
        throw new NotDecompilable("constructor call is not first");
      }
      // javac writes the implicit `super()` into every constructor; source does
      // not, and re-emitting puts it back. An enum constructor's `super(name,
      // ordinal)` is generated too - and writing it is a compile error.
      if (isSuper && (args.length === 0 || isEnumDeclaration(this.classFile))) return;
      this.current.push(`${isSuper ? "super" : "this"}(${args.join(", ")});`);
      return;
    }
    const value: Expr = {
      text: `new ${receiver.type}(${args.join(", ")})`,
      prec: PREC_PRIMARY,
      type: receiver.type,
      effects: true,
    };
    // The `dup` in front of the call left one other copy of the same object.
    // Two would mean the object is used twice, and writing `new C(...)` in both
    // places would make two of them.
    let kept = 0;
    for (const [index, entry] of this.stack.entries()) {
      if (entry.pending !== receiver.pending) continue;
      this.stack[index] = value;
      kept++;
    }
    if (kept > 1) throw new NotDecompilable("one object used twice");
    if (kept === 0) this.current.push(`${value.text};`);
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
    local.storeBlocks.add(this.currentBlock);
    const text = this.coerceInto(value, local.type);
    if (!local.declared) {
      local.declared = true;
      if (this.depth === 0) {
        local.declaration = { list: this.current, index: this.current.length, inline: true };
        local.writes.push({ list: this.current, index: this.current.length, value });
        this.current.push(`${local.type} ${local.name} = ${text};`);
        return;
      }
      local.declaration = { list: this.hoisted, index: this.hoisted.length, inline: false };
      this.hoisted.push(`${local.type} ${local.name};`);
    }
    local.writes.push({ list: this.current, index: this.current.length, value });
    this.current.push(`${local.name} = ${text};`);
  }

  run(instructions: readonly Instruction[], exceptions: readonly ExceptionEntry[] = []): void {
    this.blocks = buildBlocks(instructions, exceptions);
    // A `finally` writes its body twice, and the copy begins where the protected
    // range ends - in the middle of a block. Only that shape wants the split: a
    // monitor's range ends inside the expression it releases.
    const splits = exceptions
      .filter(
        entry =>
          entry.catchType === undefined &&
          entry.startPc !== entry.handlerPc &&
          isMonitorHandler(this.blocks.get(entry.handlerPc)) === undefined &&
          finallyBody(this.blocks.get(entry.handlerPc)) !== undefined,
      )
      .map(entry => entry.endPc);
    if (splits.length > 0) this.blocks = buildBlocks(instructions, exceptions, splits);
    this.instructionAt = new Map(instructions.map(one => [one.pc, one]));
    const { monitors, finallys, rest } = monitorRegions(exceptions, this.blocks, instructions);
    this.monitors = monitors;
    this.finallys = finallys;
    this.regions = tryRegions(rest, this.blocks, this.self);
    this.followOf = postDominators(this.blocks);
    this.methodFollowOf = this.followOf;
    const entry = instructions[0]?.pc ?? 0;
    this.dominators =
      this.regions.length === 0 && this.monitors.length === 0
        ? new Map()
        : dominators(withExceptionEdges(this.blocks, this.regions, this.monitors), entry);
    this.loops = findLoops(this.blocks, entry, this.regions, this.monitors);
    this.entryPc = entry;
    this.currentBlock = entry;
    this.structure(entry, EXIT);
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    // Only now is it known how often a captured variable is written: Java takes
    // an effectively final one, and hoisting can leave a loop variable assigned
    // once per turn.
    for (const local of this.captured) {
      // A parameter is written nowhere; anything else has to carry its one value
      // at its declaration. A variable this *hoisted* out of the block it was
      // declared in does not: the assignment left behind runs once per turn of
      // the loop around it, and what this writes is then not effectively final -
      // even though the source variable was. Scoping a declaration to its block
      // is what would take these.
      const settled =
        local.writes.length === 0 ||
        (local.writes.length === 1 && local.declaration?.inline === true);
      if (!settled) {
        throw new NotDecompilable("a lambda that captures a variable that is not final");
      }
    }
    // A block that was never entered would silently drop its statements, and one
    // entered twice would duplicate them: either means the layout is not the
    // nest of `if`s this phase reconstructs.
    const handlers = this.regions.flatMap(region => region.clauses.map(c => c.handlerPc));
    for (const start of reachableBlocks(this.blocks, [entry, ...handlers])) {
      if (!this.visited.has(start)) throw new NotDecompilable("unstructured control flow");
    }
  }

  /** Statements for the blocks from `entry` up to (not including) `stop`. */
  private structure(entry: number, stop: number, opening = false): void {
    let at = entry;
    // The block a `while (true)` opens with *is* its continue target: arriving
    // there later is a `continue`, but entering it is the body starting.
    let entering = opening;
    while (at !== stop && at !== EXIT) {
      const jump = entering ? undefined : this.loopJump(at);
      entering = false;
      if (jump !== undefined) {
        this.current.push(jump);
        return;
      }
      const guarded = this.finallys.find(
        one => one.startPc === at && !this.visited.has(at) && !this.activeFinallys.has(one),
      );
      if (guarded !== undefined) {
        at = this.finallyStatement(guarded);
        continue;
      }
      const region = this.regionAt(at);
      if (region !== undefined) {
        at = this.tryStatement(region);
        continue;
      }
      const loop = this.loops.get(at);
      if (loop !== undefined && !this.active.some(entered => entered.loop === loop)) {
        at = this.loop(loop);
        continue;
      }
      const block = this.blocks.get(at);
      if (block === undefined) throw new NotDecompilable("a branch lands outside the method");
      if (this.visited.has(at)) {
        // The `continue` of a `for` jumps to its update, not to the test - so
        // the update is entered twice, once from the jump and once from the
        // body running off its end. Writing it needs the `for` form.
        const inner = this.active[this.active.length - 1];
        if (inner !== undefined && inner.loop.body.has(at) && at !== inner.continueTarget) {
          throw new NotDecompilable("a jump into the middle of a loop");
        }
        throw new NotDecompilable("unstructured control flow");
      }
      this.visited.add(at);
      const isBranch =
        block.kind === "conditional" || block.kind === "goto" || block.kind === "switch";
      const kept = block.instructions.slice(this.skip.get(at) ?? 0);
      if (kept[kept.length - 1]?.mnemonic === "monitorenter") {
        at = this.synchronizedStatement(block, kept);
        continue;
      }
      const body = isBranch ? kept.slice(0, -1) : kept;
      this.runInstructions(body, endOf(block), block.start);
      if (block.kind === "end") return;
      if (block.kind === "switch") {
        at = this.switchStatement(block, stop);
        continue;
      }
      if (block.kind !== "conditional") {
        at = block.successors[0]!;
        continue;
      }
      at = this.conditional(block, stop);
    }
  }

  /**
   * One `if`, from the branch that ends `block`. Returns where the statement
   * after it begins.
   */
  private conditional(block: Block, stop: number): number {
    const taken: number[] = [];
    const jump = this.jumpConditionOf(block, taken);
    for (const start of taken) this.visited.add(start);
    // javac emits the arms in source order, so the `then` is whichever comes
    // first; the branch is written to select it.
    const targetIsThen = jump.target < jump.fallthrough;
    const condition = targetIsThen ? jump.condition : negate(jump.condition);
    const whenTrue = targetIsThen ? jump.target : jump.fallthrough;
    const whenFalse = targetIsThen ? jump.fallthrough : jump.target;
    const merge = this.followOf.get(block.start) ?? EXIT;

    if (merge !== EXIT) {
      const value = this.tryTernary(condition, whenTrue, whenFalse, merge);
      if (value !== undefined) {
        this.push(value);
        return merge;
      }
    }
    // An arm that flows into the other one is an `if` without an `else`: the
    // second arm is what follows the statement, not a branch of it. The merge
    // point cannot say so when the first arm also ends in a `return`, a `break`
    // or a `continue` - those paths never reach it.
    let follow = this.reachesArm(whenTrue, whenFalse) ? whenFalse : merge;
    // Inside a `try` the merge can sit outside the statement: a `return` on one
    // path makes the post-dominator EXIT, but the arms still come back together
    // where the statement around this one ends.
    if (
      follow === EXIT &&
      stop !== EXIT &&
      (this.reachesArm(whenTrue, stop) || this.reachesArm(whenFalse, stop))
    ) {
      follow = stop;
    }
    // `if (c) return x;` has no merge point. Both arms leave the method - every
    // path does, since a block that runs off the end is rejected - so the arm
    // that branches is the whole statement and the other one is what follows it,
    // at the same level rather than inside an `else`.
    if (follow === EXIT) {
      // The arm still ends where the statement around it does: `stop` is the end
      // of the `try` or the loop body this `if` sits in, not always the method.
      const exiting = this.capture(() => this.structure(whenTrue, stop));
      this.pushIf(condition, exiting);
      return whenFalse;
    }
    const thenStatements = this.capture(() => this.structure(whenTrue, follow));
    const elseStatements = this.capture(() => this.structure(whenFalse, follow));
    if (thenStatements.length === 0 && elseStatements.length > 0) {
      this.pushIf(negate(condition), elseStatements);
      return follow;
    }
    this.pushIf(condition, thenStatements, elseStatements);
    return follow;
  }

  private pushIf(condition: Expr, thenStatements: Stmt[], elseStatements: Stmt[] = []): void {
    this.emitCondition(condition, text => `if (${text}) {`);
    this.current.push(thenStatements);
    if (elseStatements.length > 0) this.current.push("} else {", elseStatements);
    this.current.push("}");
  }

  /**
   * Where a `switch` ends when its cases do not all come back together: a case
   * that returns leaves the post-dominator at EXIT, and what follows the
   * statement is then the first block the switch as a whole leads to that no
   * single case owns.
   */
  private switchFollowOf(
    block: Block,
    cases: readonly number[],
    defaultTarget: number,
    orDefaultBody = true,
  ): number {
    // A jump that leaves an enclosing loop is that loop's, not this statement's:
    // taking it for the follow would write a `break` that breaks the wrong one.
    const barriers = new Set<number>();
    for (const entered of this.active) {
      barriers.add(entered.continueTarget);
      barriers.add(entered.loop.follow);
    }
    // Only a `try` needs the dominators up front; a `switch` this deep into the
    // rules is rare enough to pay for them here instead of in every method.
    if (this.dominators.size === 0) {
      this.dominators = dominators(
        withExceptionEdges(this.blocks, this.regions, this.monitors),
        this.entryPc,
      );
    }
    const reachable = reachableBlocks(this.blocks, [...cases, defaultTarget]);
    const leadsTo = (owners: readonly number[]): number => {
      const candidates = [...reachable].filter(start => {
        const above = this.dominators.get(start);
        return (
          start > block.start &&
          !barriers.has(start) &&
          above !== undefined &&
          above.has(block.start) &&
          !owners.some(owner => above.has(owner))
        );
      });
      return candidates.length === 0 ? EXIT : Math.min(...candidates);
    };
    const merged = leadsTo([...cases, defaultTarget]);
    if (merged !== EXIT || !orDefaultBody) return merged;
    // A `switch` with no `default` of its own ends where the table's default
    // sends it - which the pass above ruled out as the default's own body. A
    // default that a case falls into is still ruled out, by that case.
    return leadsTo(cases);
  }

  /**
   * One `switch`, from the table that ends `block`. Returns where the statement
   * after it begins.
   */
  private switchStatement(block: Block, stop: number): number {
    const table = block.instructions[block.instructions.length - 1]!;
    const selector = this.pop();
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    // javac compiles a `switch` over an enum into a lookup through a synthetic
    // `$SwitchMap$` array held by an *anonymous* class - which has no name
    // source can write, so the reconstruction would not compile. Restoring the
    // `case CONSTANT:` form needs that holder's initializer, in another file.
    if (selector.text.includes("$SwitchMap$")) throw new NotDecompilable("an enum switch");
    const defaultTarget = table.arg;
    // Every key that lands on the same block is one list of labels. A key that
    // lands on the default is dropped: a tableswitch pads its gaps that way, and
    // a case that shares the default's body says nothing a `default:` does not.
    const keysOf = new Map<number, number[]>();
    const seen = new Set<number>();
    for (const { key, target } of table.switchCases) {
      // A repeated key is not a table javac wrote, and source cannot say it
      // twice, so the reconstruction would not compile.
      if (seen.has(key)) throw new NotDecompilable("a switch table with a repeated key");
      seen.add(key);
      if (target === defaultTarget) continue;
      const keys = keysOf.get(target);
      if (keys === undefined) keysOf.set(target, [key]);
      else keys.push(key);
    }
    const targets = [...new Set([...keysOf.keys(), defaultTarget])].sort((a, b) => a - b);
    if (targets[0]! <= block.start) throw new NotDecompilable("a switch that jumps backwards");

    let follow = this.followOf.get(block.start) ?? EXIT;
    // The merge can sit outside this statement: a case that returns makes the
    // post-dominator EXIT, while the rest still comes back together where the
    // statement around this one ends.
    if (
      follow === EXIT &&
      // Where the statement around this one ends is only where this one can end
      // when no case begins past it: a loop's `stop` is its header, behind them.
      // A case *on* it is the end of this statement, which is `case k: break;`.
      targets.every(target => target <= stop) &&
      targets.some(target => this.reachesArm(target, stop))
    ) {
      follow = stop;
    }
    if (follow === EXIT) follow = this.switchFollowOf(block, [...keysOf.keys()], defaultTarget);
    // A `continue` in one case skips the end of the statement, which leaves both
    // the post-dominator and the end of the statement around this one on the
    // loop's own edge instead of on it. Where it really ends is then the block
    // every case leads to - which is never that edge. (Only that strict pass: the
    // `default`'s own body ends a `switch` that has none, and inside a loop that
    // is not this.)
    if (
      follow !== EXIT &&
      this.active.some(
        entered => entered.continueTarget === follow || entered.loop.follow === follow,
      )
    ) {
      const earlier = this.switchFollowOf(block, [...keysOf.keys()], defaultTarget, false);
      if (earlier !== EXIT && earlier < follow) follow = earlier;
    }
    // javac lays the case bodies out in one run before the end of the statement,
    // so a candidate that sits between them ends nothing: the statement has no
    // follow at all then, and every case runs into the next or leaves on its own.
    if (follow !== EXIT && targets.some(target => target > follow)) follow = EXIT;
    // A case that lands on the end of the statement has no body: it is
    // `case k: break;`, written last so nothing can fall into it.
    const bodies = targets.filter(target => target !== follow);

    this.switches.push({ follow, loopDepth: this.active.length });
    // The bodyless cases come first: nothing can fall into them there, and their
    // own `break;` keeps them from falling into the first body.
    const clauses: Stmt[] = [];
    for (const target of targets.filter(t => t === follow && keysOf.has(t))) {
      for (const key of keysOf.get(target)!) clauses.push(`case ${key}:`);
      clauses.push(["break;"]);
    }
    try {
      for (const [index, target] of bodies.entries()) {
        // Running off the end of one case is a fallthrough into the next, so a
        // body stops where the next one begins.
        const end = bodies[index + 1] ?? follow;
        for (const key of keysOf.get(target) ?? []) clauses.push(`case ${key}:`);
        if (target === defaultTarget) clauses.push("default:");
        clauses.push(this.capture(() => this.structure(target, end)));
        if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
      }
    } finally {
      this.switches.pop();
    }
    this.current.push(`switch (${selector.text}) {`, clauses, "}");
    return follow;
  }

  /**
   * One `try`/`finally`. javac writes the body of the `finally` twice - once on
   * the way out of the protected range, once in the catch-all that rethrows -
   * and only the second one is written back; the copy on the way out is dropped,
   * because source wrote it once.
   */
  private finallyStatement(region: FinallyRegion): number {
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    const copy = this.blocks.get(region.endPc)!;
    const jump = copy.instructions[copy.instructions.length - 1]!;
    const follow = jump.arg;
    if (!this.blocks.has(follow)) throw new NotDecompilable("a finally that leaves the method");
    // The copy on the way out is not a statement: what is left of that block is
    // the jump over the handler.
    this.skip.set(copy.start, region.body.length);
    this.visited.add(region.handlerPc);
    this.activeFinallys.add(region);
    let body: Stmt[];
    try {
      body = this.capture(() => this.structure(region.startPc, copy.start));
    } finally {
      this.activeFinallys.delete(region);
    }
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    const handler = this.blocks.get(region.handlerPc)!;
    const cleanup = this.capture(() =>
      this.runInstructions(region.body, endOf(handler), region.handlerPc),
    );
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    this.current.push("try {", body, "} finally {", cleanup, "}");
    return copy.start;
  }

  /**
   * One `synchronized`, from the `monitorenter` that ends `block`. Returns where
   * the statement after it begins.
   */
  private synchronizedStatement(block: Block, kept: readonly Instruction[]): number {
    const region = this.monitors.find(one => one.startPc === block.successors[0]);
    if (region === undefined) throw new NotDecompilable("unsupported instruction monitorenter");
    // javac evaluates the monitor, copies it into a synthetic local and enters:
    // the copy is what the handler releases, and source wrote only the
    // expression.
    const head = kept.slice(0, -3);
    const [copy, store] = kept.slice(-3, -1);
    if (
      copy?.mnemonic !== "dup" ||
      store === undefined ||
      !store.mnemonic.startsWith("astore") ||
      this.slotOf(store) !== region.slot
    ) {
      throw new NotDecompilable("a monitor that is not held in a local");
    }
    this.runInstructions(head, copy.pc, block.start);
    const monitor = this.pop();
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    // The body leaves through the jump over the handler; with no jump there,
    // every path out of it returns or throws.
    const exit = this.instructionAt.get(region.endPc);
    const follow =
      exit !== undefined && isGoto(exit.mnemonic) && this.blocks.has(exit.arg) ? exit.arg : EXIT;
    // The release and the rethrow are the statement's own, not the body's.
    this.visited.add(region.handlerPc);
    // Inside the body, a merge is a merge of the body's own paths: what leaves
    // the statement leaves it the way a `return` does, and counting it would put
    // the merge of an `if` in here past the end of the `synchronized`.
    const within = new Set<number>();
    for (const queue = [region.startPc]; queue.length > 0;) {
      const at = queue.pop()!;
      if (at === follow || within.has(at) || !this.blocks.has(at)) continue;
      within.add(at);
      queue.push(...this.blocks.get(at)!.successors);
    }
    const outer = this.followOf;
    this.followOf = postDominators(this.blocks, within, new Set([follow]));
    this.monitorSlots.push(region.slot);
    let statements: Stmt[];
    try {
      statements = this.capture(() => this.structure(region.startPc, follow));
    } finally {
      this.followOf = outer;
      this.monitorSlots.pop();
    }
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    this.current.push(`synchronized (${monitor.text}) {`, statements, "}");
    return follow;
  }

  /**
   * The `try` statement that begins at `at`, if one does. The outermost comes
   * first: an inner `try` sharing the start is written when the body reaches it
   * again. A loop whose header is the same block is the outer statement, unless
   * the protected range covers the whole loop.
   */
  private regionAt(at: number): TryRegion | undefined {
    if (this.visited.has(at)) return undefined;
    const candidates = this.regions
      .filter(region => region.startPc === at && !this.activeTries.has(region))
      .sort((a, b) => b.endPc - a.endPc);
    const region = candidates[0];
    if (region === undefined) return undefined;
    const loop = this.loops.get(at);
    if (loop !== undefined && !this.active.some(entered => entered.loop === loop)) {
      const covered = [...loop.body].every(
        start => start >= region.startPc && start < region.endPc,
      );
      if (!covered) return undefined;
    }
    return region;
  }

  /** One `try`, with its clauses. Returns where the statement after it begins. */
  private tryStatement(region: TryRegion): number {
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    const bodyStop = this.tryFollow(region);
    // Where the body leaves off is not always where the statement ends: when the
    // exit is also a branch target, javac's jump over the handlers stands in a
    // block of its own. That block is the end of the `try`, not a statement.
    let follow = bodyStop;
    while (follow !== EXIT) {
      const block = this.blocks.get(follow)!;
      if (block.kind !== "goto" || block.instructions.length !== 1 || this.visited.has(follow))
        break;
      this.visited.add(follow);
      follow = block.successors[0]!;
    }
    this.activeTries.add(region);
    const clauses: { head: string; statements: Stmt[] }[] = [];
    let body: Stmt[];
    try {
      body = this.capture(() => this.structure(region.startPc, bodyStop));
      if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
      for (const clause of region.clauses) {
        // The parameter has to be named before the handler runs: its store is
        // what the name comes from, and that store is not a statement.
        const { name, restore } = this.catchName(clause);
        const statements = this.capture(() => this.structure(clause.handlerPc, follow));
        // The parameter's scope is its clause; the slot goes back to whatever
        // variable it held before, which javac reuses it for afterwards.
        restore();
        if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
        clauses.push({ head: `} catch (${clause.types.join(" | ")} ${name}) {`, statements });
      }
    } finally {
      this.activeTries.delete(region);
    }
    this.current.push("try {", body);
    for (const clause of clauses) this.current.push(clause.head, clause.statements);
    this.current.push("}");
    return follow;
  }

  /**
   * Where a `try` statement ends. What leaves the protected range normally is
   * the statement's own follow; a `break`, a `continue` or a `return` leaves the
   * statement the way it leaves an `if`, and says nothing about where it ends.
   * When nothing leaves the body at all, the code after the statement is
   * whatever the handlers fall into.
   */
  private tryFollow(region: TryRegion): number {
    const inside = new Set(
      [...this.blocks.keys()].filter(start => start >= region.startPc && start < region.endPc),
    );
    const jumps = new Set<number>();
    for (const entered of this.active) {
      jumps.add(entered.loop.follow);
      // A `do`'s continue target is its latch, and javac puts the tail of the
      // body in there: falling into it is the body running on, not a `continue`.
      const latch = this.blocks.get(entered.continueTarget);
      if (entered.continueTarget === entered.loop.header || (latch && isPureBlock(latch))) {
        jumps.add(entered.continueTarget);
      }
    }
    const exits = new Set<number>();
    for (const start of inside) {
      for (const successor of this.blocks.get(start)!.successors) {
        if (!inside.has(successor) && !jumps.has(successor)) exits.add(successor);
      }
    }
    // A `return` or a `throw` javac kept out of the protected range shows up as
    // an edge leaving it, but it is body, not the end of the statement: it only
    // counts when nothing else leaves.
    const leaving = [...exits].filter(exit => this.blocks.get(exit)?.kind !== "end");
    if (leaving.length === 1) return leaving[0]!;
    if (leaving.length > 1 || exits.size > 1) {
      throw new NotDecompilable("unstructured control flow");
    }
    const [bodyFollow] = exits;
    if (bodyFollow !== undefined) return bodyFollow;
    const follows = new Set<number>();
    for (const clause of region.clauses) {
      const follow = this.methodFollowOf.get(clause.handlerPc) ?? EXIT;
      // The post-dominator of a handler that branches is a merge *inside* it, and
      // so is anything only the handler reaches: taking either would end the
      // statement in the middle of its own `catch`.
      if (this.dominators.get(follow)?.has(clause.handlerPc) === false) follows.add(follow);
    }
    for (const jump of [...jumps, EXIT]) follows.delete(jump);
    if (follows.size > 1) throw new NotDecompilable("unstructured control flow");
    return [...follows][0] ?? EXIT;
  }

  /**
   * The catch parameter of one clause. A handler is entered with the exception
   * on the stack, so it starts by storing it - or dropping it, when source
   * never named it.
   */
  private catchName(clause: Clause): { name: string; restore: () => void } {
    const block = this.blocks.get(clause.handlerPc)!;
    const first = block.instructions[0];
    if (first === undefined || (first.mnemonic !== "pop" && !first.mnemonic.startsWith("astore"))) {
      throw new NotDecompilable("an exception handler that keeps the exception");
    }
    this.skip.set(clause.handlerPc, 1);
    if (first.mnemonic === "pop") return { name: this.freshName("e"), restore: () => {} };
    const slot = this.slotOf(first);
    // The debug table scopes the parameter from *after* its store, which is
    // where the handler's own code begins - and when the store is all there is,
    // that is where the block ends.
    const scopePc = block.instructions[1]?.pc ?? endOf(block);
    const scoped = this.localTable.find(
      entry => entry.slot === slot && scopePc >= entry.startPc && scopePc < entry.endPc,
    );
    // ponytail: a multi-catch with no debug table is declared as its first type -
    // the common supertype source used is not written to the class file.
    const declared =
      scoped?.type !== undefined && scoped.type !== "" ? scoped.type : clause.types[0]!;
    const local: Local = {
      name: this.freshName(scoped?.name !== undefined && scoped.name !== "" ? scoped.name : "e"),
      type: sourceTypeText(declared, this.self),
      declared: true,
      origin: scoped,
      declaration: undefined,
      authoritative: true, // the clause says what the parameter's type is
      writes: [],
      storeBlocks: new Set([clause.handlerPc]),
    };
    const shadowed = this.locals.get(slot);
    this.byName.set(local.name, local);
    this.locals.set(slot, local);
    return {
      name: local.name,
      restore: () => {
        if (shadowed === undefined) this.locals.delete(slot);
        else this.locals.set(slot, shadowed);
      },
    };
  }

  /**
   * `break;` or `continue;` when `at` is where the innermost loop's next
   * iteration, or the code after it, begins. Leaving an *enclosing* loop needs a
   * label, which this phase does not write.
   */
  private loopJump(at: number): string | undefined {
    const inner = this.active[this.active.length - 1];
    const enclosing = this.switches[this.switches.length - 1];
    // Only the innermost breakable statement can be left without a label, and a
    // loop opened inside a `switch` is the innermost one.
    const switchIsInner = enclosing !== undefined && enclosing.loopDepth === this.active.length;
    // The end of the `switch` comes first, even when it is also the loop's
    // continue target: a `do`'s continue target is its latch, and javac puts the
    // tail of the body in there - `continue;` would skip it.
    if (switchIsInner && at === enclosing.follow) return "break;";
    // A `switch` catches `break` but not `continue`, so the innermost loop still
    // owns its own continue target even from inside one.
    if (inner !== undefined && at === inner.continueTarget) {
      // Unless the jump lands in the tail of a `do`'s body, which a `continue;`
      // would skip: nothing in this phase can write that jump.
      if (!inner.continues) throw new NotDecompilable("a jump into the tail of a do-while");
      return "continue;";
    }
    if (!switchIsInner && inner !== undefined && at === inner.loop.follow) {
      return "break;";
    }
    for (const outer of switchIsInner ? this.active : this.active.slice(0, -1)) {
      if (at === outer.continueTarget || at === outer.loop.follow) {
        throw new NotDecompilable("a labeled break or continue");
      }
    }
    for (const outer of this.switches.slice(0, switchIsInner ? -1 : undefined)) {
      if (at === outer.follow) throw new NotDecompilable("a labeled break or continue");
    }
    return undefined;
  }

  /**
   * Whether one arm of a branch runs into the other, which makes the second one
   * the code after the `if` rather than its `else`. A `break` or a `continue`
   * ends the walk: it leaves the statement, like a `return`.
   */
  private reachesArm(from: number, to: number): boolean {
    const inner = this.active[this.active.length - 1];
    const stop = new Set<number>(
      inner === undefined ? [] : [inner.continueTarget, inner.loop.follow],
    );
    for (const entered of this.switches) stop.add(entered.follow);
    const seen = new Set<number>();
    const queue = [from];
    while (queue.length > 0) {
      const at = queue.pop()!;
      if (at === to) return true;
      if (seen.has(at) || stop.has(at) || !this.blocks.has(at)) continue;
      seen.add(at);
      queue.push(...this.blocks.get(at)!.successors);
    }
    return false;
  }

  /** Whether `start` is a loop's own edge, which no expression may fold away. */
  private isLoopEdge(start: number): boolean {
    return (
      this.loops.has(start) ||
      this.active.some(entered => entered.continueTarget === start || entered.loop.follow === start)
    );
  }

  /** One loop, from its header. Returns where the statement after it begins. */
  private loop(loop: Loop): number {
    if (this.stack.length > 0) throw new NotDecompilable("values left on the stack");
    const header = this.blocks.get(loop.header)!;
    const latch = loop.latches.length === 1 ? this.blocks.get(loop.latches[0]!) : undefined;
    const isWhile =
      loop.follow !== EXIT &&
      header.kind === "conditional" &&
      isConditionBlock(header) &&
      headerExits(this.blocks, loop);
    const isDoWhile =
      !isWhile &&
      loop.follow !== EXIT &&
      latch !== undefined &&
      latch.kind === "conditional" &&
      latch.successors.includes(loop.header) &&
      latch.successors.includes(loop.follow);
    // Inside the body, the merge of an `if` is a merge of the body's own paths:
    // the edges a `continue` and a `break` take are exits, not joins. A `do`'s
    // latch only counts when it is the test alone - javac puts the tail of the
    // body in the same block when nothing jumps to the test, and that tail is a
    // join like any other. A call in there is body, not test, so this is the
    // strict predicate: cutting a join would drop the statements after it.
    const cut = new Set<number>([loop.header]);
    if (isDoWhile && isPureBlock(latch!)) cut.add(latch!.start);
    const outer = this.followOf;
    this.followOf = postDominators(this.blocks, loop.body, cut);
    try {
      if (isWhile) return this.whileLoop(loop, header);
      if (isDoWhile) return this.doWhileLoop(loop, latch!);
      return this.foreverLoop(loop);
    } finally {
      this.followOf = outer;
    }
  }

  /** `while (c) { ... }`: the header is the test, and the loop runs while it holds. */
  private whileLoop(loop: Loop, header: Block): number {
    const update = this.forUpdate(loop);
    this.active.push({
      loop,
      continueTarget: update?.start ?? loop.header,
      continues: true,
    });
    try {
      this.visited.add(loop.header);
      const taken: number[] = [];
      const last = header.instructions[header.instructions.length - 1]!;
      const statementsBefore = this.current.length;
      this.runInstructions(header.instructions.slice(0, -1), last.pc, header.start);
      // The test runs once per iteration, and what it computes goes into the
      // `while (...)` line - a statement in there would run once, ahead of the
      // loop, which is not what the bytecode says.
      if (this.current.length !== statementsBefore) {
        throw new NotDecompilable("a loop test that is a statement");
      }
      const jump = this.jumpConditionOf(header, taken);
      for (const start of taken) this.visited.add(start);
      if (jump.target !== loop.follow && jump.fallthrough !== loop.follow) {
        throw new NotDecompilable("an unstructured loop");
      }
      // The branch that leaves the loop is the negation of what source wrote.
      const leaves = jump.target === loop.follow;
      const condition = leaves ? negate(jump.condition) : jump.condition;
      const body = leaves ? jump.fallthrough : jump.target;
      const stop = update?.start ?? loop.header;
      const statements = this.capture(() => this.structure(body, stop));
      trimTail(statements, "continue;");
      // `for (; c; update)` is the same bytecode as the `while` whose last
      // statement is the update - but it is where a `continue` goes, so it is
      // the only form that can write one.
      const clause = update === undefined ? "" : this.updateClause(update);
      if (clause === "") {
        // A `continue` target with nothing in it: the jump back to the test, on
        // its own. There is no update to write, so this stays a `while`.
        this.emitCondition(condition, text => `while (${text}) {`);
      } else {
        this.emitCondition(condition, text => `for (; ${text}; ${clause}) {`);
      }
      this.current.push(statements, "}");
    } finally {
      this.active.pop();
    }
    return loop.follow;
  }

  /**
   * The update of a `for`, which javac lays out at the bottom of the body with
   * the test at the top. It is only worth naming when something *jumps* to it -
   * a body that simply runs into it is a `while` whose last statement is the
   * update, which is what this wrote before there was a `for` form.
   */
  private forUpdate(loop: Loop): Block | undefined {
    if (loop.latches.length !== 1) return undefined;
    const latch = this.blocks.get(loop.latches[0]!);
    if (latch === undefined) return undefined;
    // The update runs before the test, so it ends in the jump back to it.
    if (latch.start === loop.header || latch.kind !== "goto") return undefined;
    if (this.visited.has(latch.start)) return undefined;
    // Only an increment can be written as the update clause, and this has to be
    // decided before the body is - `continue;` goes here only if it does. Any
    // other tail stays a statement of the body, where the `while` form has
    // always put it: a local stored in here could still be *retyped*, and a
    // clause frozen into the `for` line would not carry the rewrite.
    const update = latch.instructions.slice(0, -1);
    if (!update.every(instruction => instruction.mnemonic.replace(/_w$/, "") === "iinc")) {
      return undefined;
    }
    let predecessors = 0;
    for (const block of this.blocks.values()) {
      if (block.successors.includes(latch.start)) predecessors++;
    }
    return predecessors > 1 ? latch : undefined;
  }

  /**
   * The `for`'s update clause: the update block's statements, which have to be
   * expressions - a `for` takes a comma-separated list of them, nothing else.
   */
  private updateClause(update: Block): string {
    this.visited.add(update.start);
    const statements = this.capture(() =>
      this.runInstructions(update.instructions.slice(0, -1), endOf(update), update.start),
    );
    // Increments only, so every one of them is an expression statement; an empty
    // block is a `continue` target with nothing to update.
    return flattenStatements(statements)
      .map(text => text.replace(/;$/, ""))
      .join(", ");
  }

  /** `do { ... } while (c);`: the test is the latch, and the body runs first. */
  private doWhileLoop(loop: Loop, latch: Block): number {
    this.active.push({ loop, continueTarget: latch.start, continues: isPureBlock(latch) });
    let condition: Expr | undefined;
    try {
      const statements = this.capture(() => {
        this.structure(loop.header, latch.start);
        if (this.visited.has(latch.start)) throw new NotDecompilable("unstructured control flow");
        this.visited.add(latch.start);
        // The latch holds the last of the body and then the test, which is what
        // the instructions before its branch leave on the stack.
        const last = latch.instructions[latch.instructions.length - 1]!;
        this.runInstructions(latch.instructions.slice(0, -1), last.pc, latch.start);
        const taken: number[] = [];
        const jump = this.jumpConditionOf(latch, taken);
        for (const start of taken) this.visited.add(start);
        if (jump.target !== loop.header || jump.fallthrough !== loop.follow) {
          throw new NotDecompilable("an unstructured loop");
        }
        condition = jump.condition;
      });
      this.current.push("do {", statements);
      this.emitCondition(condition!, text => `} while (${text});`);
    } finally {
      this.active.pop();
    }
    return loop.follow;
  }

  /** `while (true) { ... }`: nothing at the head decides whether to go round again. */
  private foreverLoop(loop: Loop): number {
    this.active.push({ loop, continueTarget: loop.header, continues: true });
    try {
      const statements = this.capture(() => this.structure(loop.header, EXIT, true));
      trimTail(statements, "continue;");
      this.current.push("while (true) {", statements, "}");
    } finally {
      this.active.pop();
    }
    return loop.follow;
  }

  /**
   * The condition under which `block`'s branch is taken, folding a
   * single-predecessor condition block behind it into a `&&`/`||`: javac lays a
   * short-circuit out as two branches to the same place.
   */
  /**
   * The condition under which `block`'s branch is taken, with any further tests
   * that belong to the same source condition folded in: javac lays a
   * short-circuit out as a chain of branches that share their outcomes. The
   * blocks folded away are appended to `taken`, for the caller to account for.
   */
  private jumpConditionOf(block: Block, taken: number[], folded?: Set<number>): Jump {
    const inChain = folded ?? new Set<number>([block.start]);
    let condition = this.branchExpr(block.instructions[block.instructions.length - 1]!);
    let target = block.successors[1]!;
    let fallthrough = block.successors[0]!;
    for (;;) {
      let merged = false;
      // The shortest fold first: a test that carries its own chain may not line
      // up with this one, while the single branch at its head does.
      for (const deep of [false, true]) {
        // A test on the *fallthrough* path shares an outcome with this one:
        // `a || b` when both jump to the same place, `a || !b` when the second
        // falls into where the first jumped.
        const onFall = this.chainFrom(fallthrough, taken, inChain, deep);
        if (onFall !== undefined) {
          if (onFall.target === target || onFall.fallthrough === target) {
            const jumpsAway = onFall.target === target;
            condition = logical(
              "or",
              condition,
              jumpsAway ? onFall.condition : negate(onFall.condition),
            );
            fallthrough = jumpsAway ? onFall.fallthrough : onFall.target;
            merged = true;
            break;
          }
          onFall.undo();
        }
        // A test on the *target* path: landing on the fallthrough now means
        // either this branch was not taken, or the second one sent us there.
        const onTarget = this.chainFrom(target, taken, inChain, deep);
        if (onTarget !== undefined) {
          if (onTarget.target === fallthrough || onTarget.fallthrough === fallthrough) {
            const jumpsBack = onTarget.target === fallthrough;
            condition = logical(
              "or",
              negate(condition),
              jumpsBack ? onTarget.condition : negate(onTarget.condition),
            );
            target = fallthrough;
            fallthrough = jumpsBack ? onTarget.fallthrough : onTarget.target;
            merged = true;
            break;
          }
          onTarget.undo();
        }
      }
      if (!merged) return { condition, target, fallthrough };
    }
  }

  /**
   * The branch `start` amounts to once its own chain is folded, or nothing when
   * it is not a test that belongs to this condition. Speculative: `undo` puts
   * everything back when the outcomes turn out not to line up.
   */
  private chainFrom(
    start: number,
    taken: number[],
    folded: Set<number>,
    deep: boolean,
  ): (Jump & { undo: () => void }) | undefined {
    const next = this.blocks.get(start);
    if (next === undefined || next.kind !== "conditional" || !isConditionBlock(next)) {
      return undefined;
    }
    if (folded.has(start) || this.visited.has(start)) return undefined;
    // A loop's own test is a statement, not a term of the condition in front of it.
    if (this.isLoopEdge(start)) return undefined;
    // Nothing outside the chain may reach it, or folding would skip a path in.
    if (this.predecessorsOf(start, folded) !== 0) return undefined;
    const stackBefore = [...this.stack];
    const takenCount = taken.length;
    const foldedBefore = [...folded];
    const statementsBefore = this.current.length;
    folded.add(start);
    taken.push(start);
    const last = next.instructions[next.instructions.length - 1]!;
    this.runInstructions(next.instructions.slice(0, -1), last.pc, start);
    const jump = deep
      ? this.jumpConditionOf(next, taken, folded)
      : {
          condition: this.branchExpr(last),
          target: next.successors[1]!,
          fallthrough: next.successors[0]!,
        };
    const undo = (): void => {
      this.stack.splice(0, this.stack.length, ...stackBefore);
      this.current.length = statementsBefore;
      taken.length = takenCount;
      folded.clear();
      for (const block of foldedBefore) folded.add(block);
    };
    // A statement means the block did more than compute a value - a call whose
    // result is dropped, say - and folding it into a condition would move it.
    if (this.current.length !== statementsBefore) {
      undo();
      return undefined;
    }
    return { ...jump, undo };
  }

  private predecessorsOf(start: number, ignore: ReadonlySet<number>): number {
    let count = 0;
    for (const block of this.blocks.values()) {
      if (!ignore.has(block.start) && block.successors.includes(start)) count++;
    }
    return count;
  }

  /** The source condition a branch instruction tests, with its operands popped. */
  private branchExpr(instruction: Instruction): Expr {
    const { mnemonic } = instruction;
    if (mnemonic === "ifnull" || mnemonic === "ifnonnull") {
      return compare(
        this.pop(),
        mnemonic === "ifnull" ? "==" : "!=",
        primary("null", "java.lang.Object"),
      );
    }
    if (mnemonic === "if_acmpeq" || mnemonic === "if_acmpne") {
      const right = this.pop();
      return compare(this.pop(), mnemonic === "if_acmpeq" ? "==" : "!=", right);
    }
    const op = COMPARISONS[mnemonic.replace("if_icmp", "if").replace(/^if/, "")];
    if (op === undefined) throw new NotDecompilable(`unsupported branch ${mnemonic}`);
    if (mnemonic.startsWith("if_icmp")) {
      const right = numeric(this.pop());
      return compare(numeric(this.pop()), op, right);
    }
    const value = this.popRaw();
    // `lcmp`/`fcmpl`/`dcmpg` only exist to feed one of these: what source wrote
    // is the comparison of their two operands.
    if (value.compared !== undefined) {
      return compare(value.compared.left, op, value.compared.right);
    }
    if (value.type === "boolean" && (op === "==" || op === "!=")) {
      return op === "!=" ? value : negate(value);
    }
    return compare(numeric(value), op, intLiteral(0));
  }

  /**
   * The two arms of a branch as `condition ? a : b`, when both are a single
   * side-effect-free block that leaves one value behind - which is how javac
   * writes a conditional expression, and how a `boolean` ends up in a variable.
   */
  private tryTernary(
    condition: Expr,
    whenTrue: number,
    whenFalse: number,
    follow: number,
  ): Expr | undefined {
    const consumed: number[] = [];
    const before = [...this.stack];
    const statements = this.current;
    const statementsBefore = statements.length;
    const value = this.armValues(condition, whenTrue, whenFalse, follow, consumed);
    // A statement means an arm did more than compute a value - a call whose
    // result is dropped, say - and it would end up in front of the `?:`.
    if (value === undefined || statements.length !== statementsBefore) {
      // An arm may have consumed values that were already on the stack, so the
      // depth alone does not put it back.
      this.stack.splice(0, this.stack.length, ...before);
      statements.length = statementsBefore;
      return undefined;
    }
    for (const start of consumed) this.visited.add(start);
    return value;
  }

  private armValues(
    condition: Expr,
    whenTrue: number,
    whenFalse: number,
    follow: number,
    consumed: number[],
  ): Expr | undefined {
    const thenValue = this.valueOfRegion(whenTrue, follow, consumed);
    const elseValue =
      thenValue === undefined ? undefined : this.valueOfRegion(whenFalse, follow, consumed);
    if (thenValue === undefined || elseValue === undefined) return undefined;
    // `c ? 1 : 0` is a boolean that javac erased to an int; source wrote the
    // condition itself.
    if (thenValue.text === "1" && elseValue.text === "0") {
      return materializedBoolean(condition, condition, "1");
    }
    if (thenValue.text === "0" && elseValue.text === "1") {
      return materializedBoolean(negate(condition), condition, "0");
    }
    return ternary(condition, thenValue, elseValue);
  }

  /**
   * The blocks from `start` to `follow` as a single value: either one
   * side-effect-free block that leaves it on the stack, or - because a
   * short-circuit nests them - another branch whose arms are values themselves.
   */
  private valueOfRegion(start: number, follow: number, consumed: number[]): Expr | undefined {
    const block = this.blocks.get(start);
    if (block === undefined) return undefined;
    // A block the two arms share (the merge of a `||`) is taken twice, so it may
    // only be one that has no side effects - then evaluating it twice is the
    // same value twice. An arm of its own may call something.
    if (!isPureBlock(block) && (consumed.includes(start) || !isConditionBlock(block))) {
      return undefined;
    }
    if (this.isLoopEdge(start)) return undefined;
    // A block already emitted as a statement cannot also be a value.
    if (this.visited.has(start)) return undefined;
    const terminator = block.instructions[block.instructions.length - 1]!;
    if (block.kind === "conditional") {
      if ((this.followOf.get(start) ?? EXIT) !== follow) return undefined;
      this.runInstructions(block.instructions.slice(0, -1), terminator.pc, start);
      const jump = this.jumpConditionOf(block, consumed);
      consumed.push(start);
      const targetIsThen = jump.target < jump.fallthrough;
      return this.armValues(
        targetIsThen ? jump.condition : negate(jump.condition),
        targetIsThen ? jump.target : jump.fallthrough,
        targetIsThen ? jump.fallthrough : jump.target,
        follow,
        consumed,
      );
    }
    if (block.successors.length !== 1 || block.successors[0] !== follow) return undefined;
    const before = this.stack.length;
    const body = block.kind === "goto" ? block.instructions.slice(0, -1) : block.instructions;
    this.runInstructions(body, endOf(block), start);
    if (this.stack.length !== before + 1) return undefined;
    consumed.push(start);
    return this.pop();
  }

  /** True when a path from the entry reaches `target` without passing a store. */
  private reachesAvoiding(target: number, stores: ReadonlySet<number>): boolean {
    // A store in the reading block itself comes first: the read is what follows
    // it, so that path is covered.
    if (stores.has(target)) return false;
    const seen = new Set<number>();
    const queue = [this.entryPc];
    while (queue.length > 0) {
      const at = queue.pop()!;
      if (seen.has(at) || stores.has(at) || !this.blocks.has(at)) continue;
      if (at === target) return true;
      seen.add(at);
      queue.push(...this.blocks.get(at)!.successors);
    }
    return false;
  }

  private runInstructions(
    instructions: readonly Instruction[],
    endPc: number,
    blockStart: number,
  ): void {
    const outer = this.currentBlock;
    this.currentBlock = blockStart;
    try {
      this.runSteps(instructions, endPc);
    } finally {
      this.currentBlock = outer;
    }
  }

  private runSteps(instructions: readonly Instruction[], endPc: number): void {
    for (const [index, instruction] of instructions.entries()) {
      // A store's variable comes into scope after the store, so the debug table
      // is searched at the next instruction's pc, not the store's own.
      this.step(instruction, instructions[index + 1]?.pc ?? endPc);
    }
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
    // The monitor of a `synchronized` lives in a synthetic local: source never
    // named it, and reading it back is part of the release, not a statement.
    if (mnemonic === "monitorexit") {
      this.pop();
      return;
    }
    if (/^[ilfda]load$/.test(base)) {
      const slot = this.slotOf(instruction);
      if (base === "aload" && this.monitorSlots.includes(slot)) {
        return this.push(primary("null", "java.lang.Object"));
      }
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
      // `istore` is what a boolean, char, byte and short are stored with too;
      // when the value knows which one it is, that is the variable's type. A
      // condition javac materialized as `1`/`0` does *not* know: `int x = c ? 1
      // : 0` and `boolean b = c` compile to the same store, so it starts as an
      // int and a use that needs a boolean narrows it (as for a literal).
      const fallback =
        base === "astore" || (ERASED_TO_INT.includes(value.type) && value.asInt === undefined)
          ? value.type
          : PRIMITIVE_OF_PREFIX[base[0]!]!;
      return this.store(this.slotOf(instruction), nextPc, value, fallback);
    }
    if (base === "iinc") {
      const local = this.local(instruction.arg, pc, "int");
      // `i++` is a statement here, so anything already on the stack that reads
      // the variable would read the *new* value: `f(i++, i)` and `"x" + i++ + i`
      // are not what this writes back. Their form needs the increment to stay an
      // expression, which this phase does not do.
      if (this.stack.some(value => reads(value.text, local.name))) {
        throw new NotDecompilable("an increment of a variable that is already on the stack");
      }
      const delta = instruction.arg2;
      if (delta === 1) this.current.push(`${local.name}++;`);
      else if (delta === -1) this.current.push(`${local.name}--;`);
      else if (delta < 0) this.current.push(`${local.name} -= ${-delta};`);
      else this.current.push(`${local.name} += ${delta};`);
      return;
    }

    // Arithmetic, bitwise and conversions.
    const operator = BINARY_OPS[mnemonic.slice(1)];
    if (operator && /^[ilfd]/.test(mnemonic)) {
      const right = numeric(this.pop());
      const left = numeric(this.pop());
      const type = PRIMITIVE_OF_PREFIX[mnemonic[0]!]!;
      return this.push(binary(left, operator.operator, right, operator.prec, type));
    }
    if (/^[ilfd]neg$/.test(mnemonic)) {
      const value = numeric(this.pop());
      return this.push({
        // A unary operand needs the parens too: `-(-a)` is not `--a`.
        text: `-${at(value, PREC_UNARY + 1)}`,
        prec: PREC_UNARY,
        type: PRIMITIVE_OF_PREFIX[mnemonic[0]!]!,
      });
    }
    const conversion = CONVERSIONS[mnemonic];
    if (conversion) {
      const value = numeric(this.pop());
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
      // The assignment is a statement here, so anything already on the stack that
      // reads the same field would read the *new* value: `arr[idx++]` writes the
      // increment out first, and the read has to stay behind it.
      if (this.stack.some(stacked => reads(stacked.text, target))) {
        throw new NotDecompilable("an assignment to a field that is already on the stack");
      }
      this.current.push(`${target} = ${this.coerceInto(value, type)};`);
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
      return this.push(primary(`${at(array, PREC_PRIMARY)}[${coerce(index, "int")}]`, element));
    }
    if (/^[ilfdabcs]astore$/.test(mnemonic)) {
      const value = this.pop();
      const index = this.pop();
      const array = this.popRaw();
      if (array.init !== undefined) return this.fillArray(array.init, index, value);
      const element = array.type.endsWith("[]")
        ? array.type.slice(0, -2)
        : PRIMITIVE_OF_PREFIX[mnemonic[0]!]!;
      const target = `${at(array, PREC_PRIMARY)}[${coerce(index, "int")}]`;
      // The assignment is a statement here, so anything already on the stack that
      // reads the same element would read the *new* value: `a[i]++` yields the
      // old one, which needs the assignment to stay an expression.
      if (this.stack.some(stacked => reads(stacked.text, target))) {
        throw new NotDecompilable("an assignment to an array element that is already on the stack");
      }
      this.current.push(`${target} = ${this.coerceInto(value, element)};`);
      return;
    }
    if (mnemonic === "newarray") {
      const length = numeric(this.pop());
      const element = instruction.operand ?? "int";
      return this.push({
        text: `new ${element}[${length.text}]`,
        prec: PREC_PRIMARY,
        type: `${element}[]`,
        init: this.arrayInit(`new ${element}[]`, element, length),
      });
    }
    if (mnemonic === "anewarray") {
      const element = typeName(className(pool, instruction.arg) ?? "java/lang/Object", this.self);
      const length = numeric(this.pop());
      // The element type may itself be an array: the new dimension goes first,
      // so `new String[n][]`, never `new String[][n]`.
      const base = element.replaceAll("[]", "");
      const rest = "[]".repeat((element.length - base.length) / 2);
      return this.push({
        text: `new ${base}[${length.text}]${rest}`,
        prec: PREC_PRIMARY,
        type: `${element}[]`,
        init: this.arrayInit(`new ${base}[]${rest}`, element, length),
      });
    }
    if (mnemonic === "multianewarray") {
      const type = typeName(className(pool, instruction.arg) ?? "", this.self);
      const rank = (type.match(/\[\]/g) ?? []).length;
      const sizes: string[] = [];
      for (let i = 0; i < instruction.arg2; i++) sizes.unshift(numeric(this.pop()).text);
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

    // Object creation. `new` leaves a reference that is not a value yet: only
    // the constructor call makes one, and javac dups it first so the call can
    // consume a copy and leave the object behind.
    if (mnemonic === "new") {
      const type = typeName(className(pool, instruction.arg) ?? "java/lang/Object", this.self);
      return this.push({ text: "", prec: PREC_PRIMARY, type, pending: this.pendingCount++ });
    }
    if (INVOKES.has(mnemonic)) {
      const target = memberRef(pool, instruction.arg);
      if (!target) throw new NotDecompilable("bad method reference");
      if (target.name === "<init>") return this.construct(target);
      const args = this.arguments(target.descriptor);
      const callee =
        mnemonic === "invokestatic"
          ? this.staticCallee(target.owner, target.name)
          : this.receiverCallee(mnemonic, target.owner, target.name);
      const text = `${callee}(${args.join(", ")})`;
      const type = sourceTypeText(returnType(target.descriptor), this.self);
      if (type === "void") {
        this.current.push(`${text};`);
        return;
      }
      return this.push({ text, prec: PREC_PRIMARY, type, effects: true });
    }

    if (mnemonic === "invokedynamic") return this.dynamic(instruction.arg);

    // Stack shuffling. A dup of anything but a name or an array being filled in
    // would duplicate the expression itself (`new int[2][0] = 1;`), so only
    // those cases are taken; the rest waits for a later phase.
    if (DUPS.has(mnemonic)) {
      // How many values the copy is of, and how far down it goes: `dup2` is one
      // long or double, or two of anything else, and the `_xN` says how many
      // values it is pushed under.
      const wide = (value: Expr): boolean => value.type === "long" || value.type === "double";
      // `popRaw`, because the copy being duplicated is the array literal that is
      // still being filled in, which `pop` rejects as incomplete.
      const taken: Expr[] = [this.popRaw()];
      if (mnemonic.startsWith("dup2") && !wide(taken[0]!)) taken.unshift(this.popRaw());
      const under: Expr[] = [];
      const depth = mnemonic.endsWith("_x1") ? 1 : mnemonic.endsWith("_x2") ? 2 : 0;
      for (let left = depth; left > 0;) {
        const value = this.popRaw();
        under.unshift(value);
        left -= wide(value) ? 2 : 1;
      }
      for (const value of [...taken, ...under]) {
        if (value.effects === true) throw new NotDecompilable("dup of a call");
        if (value.compared !== undefined) throw new NotDecompilable("dup of a comparison");
        // Only a value that reads the same thing every time may be written
        // twice; an expression would be *computed* twice (`new int[2][0] = 1;`).
        if (
          value.pending === undefined &&
          value.init === undefined &&
          (value.prec !== PREC_PRIMARY || value.text.startsWith("new "))
        ) {
          throw new NotDecompilable("dup of a non-trivial value");
        }
      }
      for (const value of taken) this.push(value);
      for (const value of under) this.push(value);
      for (const value of taken) this.push(value);
      return;
    }
    if (mnemonic === "pop" || mnemonic === "pop2") {
      const value = this.pop();
      // `pop2` drops one long or double, or two of anything else - and two of
      // anything else is a shape this phase does not produce.
      if (mnemonic === "pop2" && value.type !== "long" && value.type !== "double") {
        throw new NotDecompilable("pop2 of two values");
      }
      // The value of a call is what is being dropped, not the call itself. Only a
      // call is a statement in Java, though: a dropped concatenation would have
      // to keep the calls inside it, and `"a" + f();` is not something to write.
      if (value.effects === true) {
        if (value.prec !== PREC_PRIMARY) {
          throw new NotDecompilable("a dropped value that is not a statement");
        }
        this.current.push(`${value.text};`);
      }
      return;
    }

    // Comparisons. `lcmp` and the float ones have no source form: they only
    // exist to feed the branch that follows, which is what was written.
    if (mnemonic === "lcmp" || /^[fd]cmp[lg]$/.test(mnemonic)) {
      const right = this.pop();
      const left = this.pop();
      return this.push({ text: "", prec: PREC_PRIMARY, type: "int", compared: { left, right } });
    }

    // Returns.
    if (mnemonic === "athrow") {
      this.current.push(`throw ${this.pop().text};`);
      return;
    }
    if (mnemonic === "return") {
      this.current.push("return;");
      return;
    }
    if (/^[ilfda]return$/.test(mnemonic)) {
      this.current.push(`return ${this.coerceInto(this.pop(), this.methodReturnType)};`);
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
      declaration: undefined,
      authoritative: true, // the descriptor says what a parameter's type is
      writes: [],
      storeBlocks: new Set(),
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
    decompiler.run(instructions, code.exceptions);
    body = [...decompiler.hoisted, ...flattenStatements(decompiler.statements)];
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
      typeof reached === "string" && /^(?:super|this)\(/.test(reached)
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
