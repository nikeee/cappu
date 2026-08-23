// A javap-compatible disassembler: class bytes in, `javap -c -p` shaped text
// out (nikeee/cappu#43, phase 1.2). Matching javap's text exactly is what lets
// the tests reuse javapNormalize.ts as an oracle over the checked-in
// javac baselines - no JDK needed at test time.

import {
  type ClassFile,
  type Code,
  type Constant,
  type Member,
  readBootstrapMethods,
  readClassFile,
  readCode,
  readExceptions,
  className,
  memberRef,
  nameAndTypeAt,
  signatureOf,
  sourceFileName,
  utf8,
} from "./classfile.ts";

const ACC_PUBLIC = 0x0001;
const ACC_PRIVATE = 0x0002;
const ACC_PROTECTED = 0x0004;
const ACC_STATIC = 0x0008;
const ACC_FINAL = 0x0010;
const ACC_SYNCHRONIZED = 0x0020;
const ACC_VOLATILE = 0x0040;
const ACC_VARARGS = 0x0080;
const ACC_TRANSIENT = 0x0080;
const ACC_NATIVE = 0x0100;
const ACC_INTERFACE = 0x0200;
const ACC_ABSTRACT = 0x0400;

// javap's layout: a 6 column indent with the pc right-aligned in the next 4
// (wider pcs push the line out), the mnemonic in a 13 column field, and the
// `// ...` comment starting at column 46.
const PC_INDENT = 6;
const PC_WIDTH = 4;
const MNEMONIC_WIDTH = 13;
const COMMENT_COLUMN = 46;

// --- opcodes ----------------------------------------------------------------------

type OperandKind =
  | "none"
  | "local" // u1 local slot
  | "i1"
  | "i2"
  | "cp1"
  | "cp2"
  | "branch2"
  | "branch4"
  | "iinc"
  | "atype"
  | "invokeinterface"
  | "invokedynamic"
  | "multianewarray"
  | "tableswitch"
  | "lookupswitch"
  | "wide";

// Opcodes 0x00..0xc9 in order; "" marks an unused slot.
const MNEMONICS = (
  "nop aconst_null iconst_m1 iconst_0 iconst_1 iconst_2 iconst_3 iconst_4 iconst_5 lconst_0 " +
  "lconst_1 fconst_0 fconst_1 fconst_2 dconst_0 dconst_1 bipush sipush ldc ldc_w ldc2_w " +
  "iload lload fload dload aload iload_0 iload_1 iload_2 iload_3 lload_0 lload_1 lload_2 " +
  "lload_3 fload_0 fload_1 fload_2 fload_3 dload_0 dload_1 dload_2 dload_3 aload_0 aload_1 " +
  "aload_2 aload_3 iaload laload faload daload aaload baload caload saload istore lstore " +
  "fstore dstore astore istore_0 istore_1 istore_2 istore_3 lstore_0 lstore_1 lstore_2 " +
  "lstore_3 fstore_0 fstore_1 fstore_2 fstore_3 dstore_0 dstore_1 dstore_2 dstore_3 " +
  "astore_0 astore_1 astore_2 astore_3 iastore lastore fastore dastore aastore bastore " +
  "castore sastore pop pop2 dup dup_x1 dup_x2 dup2 dup2_x1 dup2_x2 swap iadd ladd fadd " +
  "dadd isub lsub fsub dsub imul lmul fmul dmul idiv ldiv fdiv ddiv irem lrem frem drem " +
  "ineg lneg fneg dneg ishl lshl ishr lshr iushr lushr iand land ior lor ixor lxor iinc " +
  "i2l i2f i2d l2i l2f l2d f2i f2l f2d d2i d2l d2f i2b i2c i2s lcmp fcmpl fcmpg dcmpl " +
  "dcmpg ifeq ifne iflt ifge ifgt ifle if_icmpeq if_icmpne if_icmplt if_icmpge if_icmpgt " +
  "if_icmple if_acmpeq if_acmpne goto jsr ret tableswitch lookupswitch ireturn lreturn " +
  "freturn dreturn areturn return getstatic putstatic getfield putfield invokevirtual " +
  "invokespecial invokestatic invokeinterface invokedynamic new newarray anewarray " +
  "arraylength athrow checkcast instanceof monitorenter monitorexit wide multianewarray " +
  "ifnull ifnonnull goto_w jsr_w"
).split(" ");

const OPERANDS = new Map<string, OperandKind>([
  ["bipush", "i1"],
  ["sipush", "i2"],
  ["ldc", "cp1"],
  ["ldc_w", "cp2"],
  ["ldc2_w", "cp2"],
  ["iload", "local"],
  ["lload", "local"],
  ["fload", "local"],
  ["dload", "local"],
  ["aload", "local"],
  ["istore", "local"],
  ["lstore", "local"],
  ["fstore", "local"],
  ["dstore", "local"],
  ["astore", "local"],
  ["ret", "local"],
  ["iinc", "iinc"],
  ["tableswitch", "tableswitch"],
  ["lookupswitch", "lookupswitch"],
  ["getstatic", "cp2"],
  ["putstatic", "cp2"],
  ["getfield", "cp2"],
  ["putfield", "cp2"],
  ["invokevirtual", "cp2"],
  ["invokespecial", "cp2"],
  ["invokestatic", "cp2"],
  ["invokeinterface", "invokeinterface"],
  ["invokedynamic", "invokedynamic"],
  ["new", "cp2"],
  ["newarray", "atype"],
  ["anewarray", "cp2"],
  ["checkcast", "cp2"],
  ["instanceof", "cp2"],
  ["wide", "wide"],
  ["multianewarray", "multianewarray"],
  ["goto_w", "branch4"],
  ["jsr_w", "branch4"],
]);

for (const branch of [
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
  "goto",
  "jsr",
  "ifnull",
  "ifnonnull",
]) {
  OPERANDS.set(branch, "branch2");
}

const ARRAY_TYPES: Record<number, string> = {
  4: "boolean",
  5: "char",
  6: "float",
  7: "double",
  8: "byte",
  9: "short",
  10: "int",
  11: "long",
};

const REFERENCE_KINDS: Record<number, string> = {
  1: "REF_getField",
  2: "REF_getStatic",
  3: "REF_putField",
  4: "REF_putStatic",
  5: "REF_invokeVirtual",
  6: "REF_invokeStatic",
  7: "REF_invokeSpecial",
  8: "REF_newInvokeSpecial",
  9: "REF_invokeInterface",
};

// --- constants as javap renders them ----------------------------------------------

/** Java's Double.toString / Float.toString formatting of a finite value. */
function javaFloatingPoint(negative: boolean, digits: string, exponent: number): string {
  const sign = negative ? "-" : "";
  if (exponent < -3 || exponent >= 7) {
    const tail = digits.slice(1);
    return `${sign}${digits[0] ?? "0"}.${tail === "" ? "0" : tail}E${exponent}`;
  }
  if (exponent >= 0) {
    const whole = digits.slice(0, exponent + 1).padEnd(exponent + 1, "0");
    const fraction = digits.slice(exponent + 1);
    return `${sign}${whole}.${fraction === "" ? "0" : fraction}`;
  }
  return `${sign}0.${"0".repeat(-exponent - 1)}${digits}`;
}

/** The two `precision`-digit decimals bracketing a digit string, with carry. */
function bracket(
  digits: string,
  precision: number,
): { down: { digits: string; carry: boolean }; up: { digits: string; carry: boolean } } {
  const kept = digits.slice(0, precision).padEnd(precision, "0");
  const raised = (BigInt(kept) + 1n).toString();
  return {
    down: { digits: kept, carry: false },
    up:
      raised.length > precision
        ? { digits: raised.slice(0, precision), carry: true }
        : { digits: raised.padStart(precision, "0"), carry: false },
  };
}

/**
 * The shortest decimal that reads back as the same value - what Float.toString
 * and Double.toString render. Both bracketing candidates are tried at every
 * length; when both read back, the one closer to the exact value wins, and an
 * exact halfway case goes to the even digit. Java never renders a subnormal
 * with a single significant digit, so those start the search at two.
 */
function shortestDigits(
  magnitude: number,
  maximumDigits: number,
  minimumDigits: number,
  readsBack: (candidate: number) => boolean,
): { digits: string; exponent: number } {
  // 40 significant digits is exact enough to round correctly (and to spot the
  // exact halfway cases, which terminate well inside that).
  const [mantissa, exponentText] = magnitude.toExponential(39).split("e");
  const exact = (mantissa ?? "0").replace(".", "");
  const exponent = Number(exponentText ?? "0");
  const value = (rounded: { digits: string; carry: boolean }, precision: number): number =>
    Number(`${rounded.digits}e${exponent + (rounded.carry ? 1 : 0) - precision + 1}`);

  for (let precision = minimumDigits; precision <= maximumDigits; precision++) {
    const { down, up } = bracket(exact, precision);
    const rest = exact.slice(precision);
    const downFits = readsBack(value(down, precision));
    const upFits = readsBack(value(up, precision));
    let pick: { digits: string; carry: boolean } | undefined;
    if (downFits && upFits) {
      const above = rest > "5".padEnd(rest.length, "0");
      const below = rest < "5".padEnd(rest.length, "0");
      pick = above ? up : below ? down : (Number(down.digits[precision - 1]) & 1) === 0 ? down : up;
    } else if (downFits) {
      pick = down;
    } else if (upFits) {
      pick = up;
    }
    if (pick) return { digits: pick.digits, exponent: exponent + (pick.carry ? 1 : 0) };
  }
  const { up } = bracket(exact, maximumDigits);
  return { digits: up.digits, exponent: exponent + (up.carry ? 1 : 0) };
}

const SMALLEST_NORMAL_FLOAT = 2 ** -126;
const SMALLEST_NORMAL_DOUBLE = 2 ** -1022;

export function javaDoubleText(value: number): string {
  if (Number.isNaN(value)) return "NaN";
  if (value === Infinity) return "Infinity";
  if (value === -Infinity) return "-Infinity";
  const negative = value < 0 || Object.is(value, -0);
  if (value === 0) return negative ? "-0.0" : "0.0";
  const magnitude = Math.abs(value);
  const { digits, exponent } = shortestDigits(
    magnitude,
    17,
    magnitude < SMALLEST_NORMAL_DOUBLE ? 2 : 1,
    candidate => candidate === magnitude,
  );
  return javaFloatingPoint(negative, digits, exponent);
}

export function javaFloatText(value: number): string {
  if (Number.isNaN(value)) return "NaN";
  if (value === Infinity) return "Infinity";
  if (value === -Infinity) return "-Infinity";
  const negative = value < 0 || Object.is(value, -0);
  if (value === 0) return negative ? "-0.0" : "0.0";
  const magnitude = Math.abs(value);
  const { digits, exponent } = shortestDigits(
    magnitude,
    9,
    magnitude < SMALLEST_NORMAL_FLOAT ? 2 : 1,
    candidate => Math.fround(candidate) === magnitude,
  );
  return javaFloatingPoint(negative, digits, exponent);
}

function escapeString(value: string): string {
  let out = "";
  for (const ch of value) {
    const code = ch.codePointAt(0)!;
    if (ch === "\\") out += "\\\\";
    else if (ch === '"') out += '\\"';
    else if (ch === "\n") out += "\\n";
    else if (ch === "\r") out += "\\r";
    else if (ch === "\t") out += "\\t";
    else if (ch === "\b") out += "\\b";
    else if (ch === "\f") out += "\\f";
    else if (ch === "'") out += "\\'";
    // javap escapes the C0 and C1 control ranges; everything else prints as is.
    else if (code < 0x20 || (code >= 0x7f && code <= 0x9f)) {
      out += `\\u${code.toString(16).padStart(4, "0")}`;
    } else out += ch;
  }
  return out;
}

/** javap quotes a member name that is not a plain Java identifier (`"<init>"`). */
function quoteName(name: string): string {
  return /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name) ? name : `"${name}"`;
}

/** javap quotes a class name that is an array descriptor (`class "[[I"`). */
function quoteClass(name: string): string {
  return name.startsWith("[") ? `"${name}"` : name;
}

/** The `// ...` comment javap prints for a constant-pool operand. */
function constantComment(classFile: ClassFile, index: number): string | undefined {
  const pool = classFile.pool;
  const entry: Constant | undefined = pool[index];
  if (!entry) return undefined;
  switch (entry.tag) {
    case "class":
      return `class ${quoteClass(className(pool, index) ?? "")}`;
    case "string":
      return `String ${escapeString(utf8(pool, entry.valueIndex) ?? "")}`;
    case "int":
      return `int ${entry.value}`;
    case "long":
      return `long ${entry.value}l`;
    case "float":
      return `float ${javaFloatText(entry.value)}f`;
    case "double":
      return `double ${javaDoubleText(entry.value)}d`;
    case "fieldref":
    case "methodref":
    case "interfaceMethodref": {
      const ref = memberRef(pool, index);
      if (!ref) return undefined;
      const kind =
        entry.tag === "fieldref"
          ? "Field"
          : entry.tag === "methodref"
            ? "Method"
            : "InterfaceMethod";
      // javap omits the owner when the member lives in the class being printed.
      const owner = ref.owner === classFile.thisClass ? "" : `${quoteClass(ref.owner)}.`;
      return `${kind} ${owner}${quoteName(ref.name)}:${ref.descriptor}`;
    }
    case "methodType":
      return `MethodType ${utf8(pool, entry.descriptorIndex) ?? ""}`;
    case "methodHandle": {
      const target = constantComment(classFile, entry.referenceIndex) ?? "";
      const kind = REFERENCE_KINDS[entry.referenceKind] ?? `REF_${entry.referenceKind}`;
      // The nested comment is "Method x.y:()V"; javap prints only its body.
      return `MethodHandle ${kind} ${target.slice(target.indexOf(" ") + 1)}`;
    }
    case "dynamic":
    case "invokeDynamic": {
      const nameAndType = nameAndTypeAt(pool, entry.nameAndTypeIndex);
      const kind = entry.tag === "dynamic" ? "Dynamic" : "InvokeDynamic";
      return `${kind} #${entry.bootstrapMethodIndex}:${nameAndType?.name ?? ""}:${nameAndType?.descriptor ?? ""}`;
    }
    default:
      return undefined;
  }
}

// --- instruction decoding ----------------------------------------------------------

export interface Instruction {
  readonly pc: number;
  readonly mnemonic: string;
  readonly operand: string;
  readonly comment: string | undefined;
  /** The `{ ... }` body lines of a table/lookupswitch, already indented. */
  readonly extraLines: string[];
}

class CodeCursor {
  at = 0;
  readonly view: DataView;
  constructor(readonly code: Uint8Array) {
    this.view = new DataView(code.buffer, code.byteOffset, code.byteLength);
  }
  u1(): number {
    return this.view.getUint8(this.at++);
  }
  i1(): number {
    return this.view.getInt8(this.at++);
  }
  u2(): number {
    const v = this.view.getUint16(this.at);
    this.at += 2;
    return v;
  }
  i2(): number {
    const v = this.view.getInt16(this.at);
    this.at += 2;
    return v;
  }
  i4(): number {
    const v = this.view.getInt32(this.at);
    this.at += 4;
    return v;
  }
}

function switchBody(entries: [string, number][]): string[] {
  const lines = entries.map(([key, target]) => `${key.padStart(24)}: ${target}`);
  lines.push(`${" ".repeat(12)}}`);
  return lines;
}

/** Decode a Code attribute's bytes into javap's instruction stream. */
export function decodeInstructions(classFile: ClassFile, code: Uint8Array): Instruction[] {
  const c = new CodeCursor(code);
  const out: Instruction[] = [];
  while (c.at < code.length) {
    const pc = c.at;
    const opcode = c.u1();
    let mnemonic = MNEMONICS[opcode];
    if (mnemonic === undefined) {
      out.push({
        pc,
        mnemonic: `unknown 0x${opcode.toString(16)}`,
        operand: "",
        comment: undefined,
        extraLines: [],
      });
      continue;
    }
    let kind = OPERANDS.get(mnemonic) ?? "none";
    let wide = false;
    if (kind === "wide") {
      // The wide prefix widens the following instruction's operands; javap
      // prints those as `<mnemonic>_w`.
      wide = true;
      const widened = MNEMONICS[c.u1()];
      mnemonic = `${widened}_w`;
      kind = OPERANDS.get(widened ?? "") ?? "none";
    }
    let operand = "";
    let comment: string | undefined;
    const extraLines: string[] = [];
    switch (kind) {
      case "none":
        break;
      case "local":
        operand = String(wide ? c.u2() : c.u1());
        break;
      case "i1":
        operand = String(c.i1());
        break;
      case "i2":
        operand = String(c.i2());
        break;
      case "cp1":
      case "cp2": {
        const index = kind === "cp1" ? c.u1() : c.u2();
        operand = `#${index}`;
        comment = constantComment(classFile, index);
        break;
      }
      case "branch2":
        operand = String(pc + c.i2());
        break;
      case "branch4":
        operand = String(pc + c.i4());
        break;
      case "iinc": {
        const slot = wide ? c.u2() : c.u1();
        const delta = wide ? c.i2() : c.i1();
        operand = `${slot}, ${delta}`;
        break;
      }
      case "atype":
        operand = ARRAY_TYPES[c.u1()] ?? "?";
        break;
      case "invokeinterface": {
        const index = c.u2();
        const count = c.u1();
        c.u1(); // the required trailing zero byte
        operand = `#${index},  ${count}`;
        comment = constantComment(classFile, index);
        break;
      }
      case "invokedynamic": {
        const index = c.u2();
        c.u2(); // two zero bytes
        operand = `#${index},  0`;
        comment = constantComment(classFile, index);
        break;
      }
      case "multianewarray": {
        const index = c.u2();
        const dimensions = c.u1();
        operand = `#${index},  ${dimensions}`;
        comment = constantComment(classFile, index);
        break;
      }
      case "tableswitch": {
        c.at += (4 - (c.at % 4)) % 4; // pad to the next 4-byte boundary
        const defaultTarget = pc + c.i4();
        const low = c.i4();
        const high = c.i4();
        const entries: [string, number][] = [];
        for (let key = low; key <= high; key++) entries.push([String(key), pc + c.i4()]);
        entries.push(["default", defaultTarget]);
        operand = `{ // ${low} to ${high}`;
        extraLines.push(...switchBody(entries));
        break;
      }
      case "lookupswitch": {
        c.at += (4 - (c.at % 4)) % 4;
        const defaultTarget = pc + c.i4();
        const pairs = c.i4();
        const entries: [string, number][] = [];
        for (let i = 0; i < pairs; i++) {
          const key = c.i4();
          entries.push([String(key), pc + c.i4()]);
        }
        entries.push(["default", defaultTarget]);
        operand = `{ // ${pairs}`;
        extraLines.push(...switchBody(entries));
        break;
      }
      default:
        break;
    }
    out.push({ pc, mnemonic, operand, comment, extraLines });
  }
  return out;
}

function instructionLine(instruction: Instruction): string {
  const prefix = `${" ".repeat(PC_INDENT)}${String(instruction.pc).padStart(PC_WIDTH)}: `;
  let line = `${prefix}${instruction.mnemonic}`;
  if (instruction.operand !== "") {
    // `newarray` is the one mnemonic javap pads one column wider.
    const width = instruction.mnemonic === "newarray" ? MNEMONIC_WIDTH + 1 : MNEMONIC_WIDTH;
    line = `${prefix}${instruction.mnemonic.padEnd(width)} ${instruction.operand}`;
  }
  if (instruction.comment !== undefined) {
    line = `${line.padEnd(COMMENT_COLUMN)}// ${instruction.comment}`;
  }
  // Java's String.trim (which javap applies to each line) strips characters up
  // to U+0020 only, so a trailing NBSP inside a string constant survives.
  let end = line.length;
  while (end > 0 && line.charCodeAt(end - 1) <= 0x20) end--;
  return line.slice(0, end);
}

// --- declarations -------------------------------------------------------------------

/** A field/method descriptor type as source text; nested names keep their `$`. */
export function descriptorType(descriptor: string, at: number): { text: string; next: number } {
  let arrays = 0;
  while (descriptor[at] === "[") {
    arrays++;
    at++;
  }
  const primitives: Record<string, string> = {
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
  let base: string;
  if (descriptor[at] === "L") {
    const end = descriptor.indexOf(";", at);
    const stop = end < 0 ? descriptor.length : end;
    base = descriptor.slice(at + 1, stop).replaceAll("/", ".");
    at = end < 0 ? descriptor.length : end + 1;
  } else {
    base = primitives[descriptor[at] ?? ""] ?? "java.lang.Object";
    at++;
  }
  return { text: base + "[]".repeat(arrays), next: at };
}

function methodDescriptorTypes(descriptor: string): { params: string[]; returns: string } {
  const params: string[] = [];
  let at = 1; // past '('
  while (descriptor[at] !== ")" && at < descriptor.length) {
    const { text, next } = descriptorType(descriptor, at);
    params.push(text);
    at = next;
  }
  return { params, returns: descriptorType(descriptor, at + 1).text };
}

// A generic-signature cursor (JVMS 4.7.9.1). Deliberately separate from the one
// in classfileReader.ts: that one renders stub source, which flattens `$` to `.`
// and drops throws clauses; javap keeps both.
class SignatureCursor {
  at = 0;
  constructor(readonly text: string) {}

  peek(): string {
    return this.text[this.at] ?? "";
  }
  take(): string {
    return this.text[this.at++] ?? "";
  }

  typeParameters(): string {
    if (this.peek() !== "<") return "";
    this.take();
    const params: string[] = [];
    while (this.peek() !== ">" && this.at < this.text.length) {
      const colon = this.text.indexOf(":", this.at);
      if (colon < 0) break; // truncated signature
      const name = this.text.slice(this.at, colon);
      this.at = colon;
      const bounds: string[] = [];
      while (this.peek() === ":") {
        this.take();
        if (this.peek() === ":") continue; // empty class bound (interface first)
        bounds.push(this.referenceType());
      }
      const shown = bounds.filter(b => b !== "java.lang.Object");
      params.push(shown.length > 0 ? `${name} extends ${shown.join(" & ")}` : name);
    }
    this.take(); // '>'
    return `<${params.join(", ")}>`;
  }

  javaType(): string {
    const primitives: Record<string, string> = {
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
    const primitive = primitives[this.peek()];
    if (primitive) {
      this.take();
      return primitive;
    }
    return this.referenceType();
  }

  referenceType(): string {
    const c = this.peek();
    if (c === "T") {
      this.take();
      const semi = this.text.indexOf(";", this.at);
      const stop = semi < 0 ? this.text.length : semi;
      const name = this.text.slice(this.at, stop);
      this.at = semi < 0 ? this.text.length : semi + 1;
      return name;
    }
    if (c === "[") {
      this.take();
      return `${this.javaType()}[]`;
    }
    this.take(); // 'L'
    let out = "";
    for (;;) {
      const ch = this.take();
      if (ch === ";" || ch === "") break;
      if (ch === "/") out += ".";
      else if (ch === "<") out += `<${this.typeArguments()}>`;
      else out += ch;
    }
    return out;
  }

  private typeArguments(): string {
    const args: string[] = [];
    while (this.peek() !== ">" && this.at < this.text.length) {
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

function accessModifier(flags: number): string[] {
  if (flags & ACC_PUBLIC) return ["public"];
  if (flags & ACC_PROTECTED) return ["protected"];
  if (flags & ACC_PRIVATE) return ["private"];
  return [];
}

function fieldDeclaration(field: Member, classFile: ClassFile): string {
  const modifiers = [
    ...accessModifier(field.flags),
    ...(field.flags & ACC_STATIC ? ["static"] : []),
    ...(field.flags & ACC_FINAL ? ["final"] : []),
    ...(field.flags & ACC_VOLATILE ? ["volatile"] : []),
    ...(field.flags & ACC_TRANSIENT ? ["transient"] : []),
  ];
  const signature = signatureOf(field.attributes, classFile.pool);
  const type = signature
    ? new SignatureCursor(signature).javaType()
    : descriptorType(field.descriptor, 0).text;
  return `${[...modifiers, type].join(" ")} ${field.name};`;
}

function methodDeclaration(method: Member, classFile: ClassFile): string {
  const isInterface = (classFile.flags & ACC_INTERFACE) !== 0;
  const isStatic = (method.flags & ACC_STATIC) !== 0;
  const isAbstract = (method.flags & ACC_ABSTRACT) !== 0;
  if (method.name === "<clinit>") return "static {};";

  const modifiers = [
    ...accessModifier(method.flags),
    ...(isAbstract ? ["abstract"] : []),
    ...(isStatic ? ["static"] : []),
    ...(method.flags & ACC_FINAL ? ["final"] : []),
    ...(method.flags & ACC_SYNCHRONIZED ? ["synchronized"] : []),
    ...(method.flags & ACC_NATIVE ? ["native"] : []),
    // A private interface method is not a default method.
    ...(isInterface && !isStatic && !isAbstract && !(method.flags & ACC_PRIVATE)
      ? ["default"]
      : []),
  ];

  const signature = signatureOf(method.attributes, classFile.pool);
  let typeParameters = "";
  let params: string[];
  let returns: string;
  let thrown: string[] = readExceptions(method, classFile.pool).map(n => n.replaceAll("/", "."));
  if (signature) {
    const cursor = new SignatureCursor(signature);
    typeParameters = cursor.typeParameters();
    cursor.take(); // '('
    params = [];
    while (cursor.peek() !== ")" && cursor.at < signature.length) params.push(cursor.javaType());
    cursor.take(); // ')'
    returns = cursor.javaType();
    const signatureThrows: string[] = [];
    while (cursor.peek() === "^") {
      cursor.take();
      signatureThrows.push(cursor.referenceType());
    }
    if (signatureThrows.length > 0) thrown = signatureThrows;
  } else {
    ({ params, returns } = methodDescriptorTypes(method.descriptor));
  }

  if (method.flags & ACC_VARARGS && params.length > 0) {
    const last = params[params.length - 1]!;
    if (last.endsWith("[]")) params[params.length - 1] = `${last.slice(0, -2)}...`;
  }

  const head = [...modifiers, ...(typeParameters ? [typeParameters] : [])].join(" ");
  const externalName = classFile.thisClass.replaceAll("/", ".");
  const declared =
    method.name === "<init>"
      ? `${externalName}(${params.join(", ")})`
      : `${returns} ${method.name}(${params.join(", ")})`;
  const throwsClause = thrown.length > 0 ? ` throws ${thrown.join(", ")}` : "";
  return `${head === "" ? "" : `${head} `}${declared}${throwsClause};`;
}

function classDeclaration(classFile: ClassFile): string {
  const isInterface = (classFile.flags & ACC_INTERFACE) !== 0;
  const signature = signatureOf(classFile.attributes, classFile.pool);
  let typeParameters = "";
  let superType = classFile.superClass?.replaceAll("/", ".");
  let interfaces = classFile.interfaces.map(i => i.replaceAll("/", "."));
  // javap renders the erased interface list without spaces after the commas,
  // but a generic one (from the Signature attribute) with them.
  let interfaceSeparator = ",";
  if (signature) {
    interfaceSeparator = ", ";
    const cursor = new SignatureCursor(signature);
    typeParameters = cursor.typeParameters();
    superType = cursor.referenceType();
    interfaces = [];
    while (cursor.at < signature.length) interfaces.push(cursor.referenceType());
  }
  const head = [
    // Only ACC_PUBLIC is meaningful on a class; a nested type's real access
    // lives in the InnerClasses attribute, which javap ignores here too.
    ...(classFile.flags & ACC_PUBLIC ? ["public"] : []),
    ...(!isInterface && classFile.flags & ACC_FINAL ? ["final"] : []),
    ...(!isInterface && classFile.flags & ACC_ABSTRACT ? ["abstract"] : []),
    isInterface ? "interface" : "class",
    `${classFile.thisClass.replaceAll("/", ".")}${typeParameters}`,
  ];
  if (superType && superType.replace(/<.*/, "") !== "java.lang.Object") {
    head.push("extends", superType);
  }
  if (interfaces.length > 0) {
    head.push(isInterface ? "extends" : "implements", interfaces.join(interfaceSeparator));
  }
  return `${head.join(" ")} {`;
}

// --- rendering -----------------------------------------------------------------------

function exceptionTableLines(code: Code): string[] {
  if (code.exceptions.length === 0) return [];
  const lines = ["      Exception table:", "         from    to  target type"];
  for (const entry of code.exceptions) {
    const type = entry.catchType === undefined ? "any" : `Class ${entry.catchType}`;
    lines.push(
      `${String(entry.startPc).padStart(14)}${String(entry.endPc).padStart(6)}` +
        `${String(entry.handlerPc).padStart(6)}   ${type}`,
    );
  }
  return lines;
}

function memberBlock(classFile: ClassFile, method: Member): string[] {
  const lines = [`  ${methodDeclaration(method, classFile)}`];
  const code = readCode(method, classFile.pool);
  if (!code) return lines;
  lines.push("    Code:");
  for (const instruction of decodeInstructions(classFile, code.code)) {
    lines.push(instructionLine(instruction), ...instruction.extraLines);
  }
  lines.push(...exceptionTableLines(code));
  return lines;
}

/** One class, in `javap -c -p` layout. */
export function renderClass(classFile: ClassFile): string {
  const lines: string[] = [];
  const source = sourceFileName(classFile);
  if (source) lines.push(`Compiled from "${source}"`);
  lines.push(classDeclaration(classFile));
  // javap terminates every field with a blank line, but only separates methods
  // with one - so a field-only class keeps a blank line before the closing brace.
  for (const field of classFile.fields) {
    lines.push(`  ${fieldDeclaration(field, classFile)}`, "");
  }
  classFile.methods.forEach((method, i) => {
    if (i > 0) lines.push("");
    lines.push(...memberBlock(classFile, method));
  });
  lines.push("}");
  return `${lines.join("\n")}\n`;
}

/** Disassemble one class file's bytes. Throws ClassFileError on malformed input. */
export function disassemble(bytes: Uint8Array): string {
  const classFile = readClassFile(bytes);
  // BootstrapMethods is read so a malformed table fails here rather than
  // silently rendering a wrong `invokedynamic` comment.
  readBootstrapMethods(classFile);
  return renderClass(classFile);
}
