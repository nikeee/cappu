// Parser for java.util.Formatter conversion strings (the language of
// String.format / printf / formatted / Formatter.format). Pure and AST-free so
// it can be unit-tested in isolation and mirrored 1:1 by the Go port
// (togo/internal/compiler/formatstring.go).
//
// A specifier is  %[argument_index$][flags][width][.precision]conversion .
// We only need enough to (a) count how many arguments the string references and
// (b) classify each consuming conversion for a conservative type check. Anything
// we cannot parse cleanly returns `undefined` so the caller stays silent rather
// than risk a false positive.

/** One conversion that consumes an argument. `argIndex` is 1-based. */
export interface FormatConsumer {
  readonly argIndex: number;
  /** The conversion letter; `t`/`T` for the whole date/time family. */
  readonly conversion: string;
}

export interface FormatParse {
  readonly consumers: readonly FormatConsumer[];
  /** Highest argument index referenced (0 when nothing is consumed). */
  readonly maxIndex: number;
}

// Conversions that consume an argument. `%%` and `%n` do not and are handled
// separately; date/time `t`/`T` carry a trailing suffix letter.
const CONSUMING = new Set("bBhHsScCdoxXeEfgGaAtT".split(""));

// %[index$ or <][flags][width][.precision]conv , matched sticky at one position.
const SPEC = /%(\d+\$|<)?([-#+ 0,(]*)(\d+)?(?:\.(\d+))?([a-zA-Z%])/y;

/**
 * Parse a Formatter string. Returns `undefined` on any malformed or
 * unrecognized specifier (caller should then emit no diagnostics).
 */
export function parseFormatString(fmt: string): FormatParse | undefined {
  const consumers: FormatConsumer[] = [];
  let auto = 1; // next ordinary (auto-incrementing) index
  let lastIndex = 0; // last index used, for the '<' relative flag
  let maxIndex = 0;
  let i = 0;
  while (i < fmt.length) {
    if (fmt[i] !== "%") {
      i++;
      continue;
    }
    SPEC.lastIndex = i;
    const m = SPEC.exec(fmt);
    if (!m) return undefined; // a lone or malformed '%'
    let conversion = m[5]!;
    i += m[0].length;

    // Date/time conversions take one trailing suffix letter (e.g. %tY).
    if (conversion === "t" || conversion === "T") {
      const suffix = fmt[i];
      if (!suffix || !/[a-zA-Z]/.test(suffix)) return undefined;
      i += 1;
    }

    if (conversion === "%" || conversion === "n") continue; // consume no argument
    if (!CONSUMING.has(conversion)) return undefined;

    const indexTok = m[1];
    let idx: number;
    if (indexTok === undefined) {
      idx = auto++;
    } else if (indexTok === "<") {
      if (lastIndex === 0) return undefined; // '<' with no previous specifier
      idx = lastIndex;
    } else {
      idx = Number.parseInt(indexTok, 10); // "N$"
      if (idx === 0) return undefined;
    }
    lastIndex = idx;
    if (idx > maxIndex) maxIndex = idx;
    consumers.push({ argIndex: idx, conversion });
  }
  return { consumers, maxIndex };
}

// --- conservative type check for a single conversion -----------------------
// The argument descriptor is a primitive name ("int", "double", ...) or a class
// FQN ("java.lang.String"). We only judge a mismatch as definite when the type
// is provably incompatible; a supertype (Object, Number) or user type is
// "unknown" so the runtime type could still satisfy the conversion.

export type ArgTypeDescriptor = { readonly primitive: string } | { readonly fqn: string };

export type Accepts = "yes" | "no" | "unknown";

// Per category: primitives are fully decidable; reference types are decidable
// only against this leaf set (final/effectively-final classes that are not a
// supertype of any accepted boxed type).
const KNOWN_LEAF = new Set([
  "java.lang.String",
  "java.lang.Boolean",
  "java.lang.Character",
  "java.lang.Byte",
  "java.lang.Short",
  "java.lang.Integer",
  "java.lang.Long",
  "java.lang.Float",
  "java.lang.Double",
  "java.math.BigInteger",
  "java.math.BigDecimal",
  "java.lang.StringBuilder",
  "java.lang.StringBuffer",
]);

interface Category {
  readonly prims: ReadonlySet<string>;
  readonly fqns: ReadonlySet<string>;
}

const INTEGRAL: Category = {
  prims: new Set(["byte", "short", "int", "long"]),
  fqns: new Set([
    "java.lang.Byte",
    "java.lang.Short",
    "java.lang.Integer",
    "java.lang.Long",
    "java.math.BigInteger",
  ]),
};
const FLOAT: Category = {
  prims: new Set(["float", "double"]),
  fqns: new Set(["java.lang.Float", "java.lang.Double", "java.math.BigDecimal"]),
};
const CHAR: Category = {
  prims: new Set(["byte", "short", "char", "int"]),
  fqns: new Set(["java.lang.Character", "java.lang.Byte", "java.lang.Short", "java.lang.Integer"]),
};

function categoryOf(conversion: string): Category | undefined {
  switch (conversion) {
    case "d":
    case "o":
    case "x":
    case "X":
      return INTEGRAL;
    case "e":
    case "E":
    case "f":
    case "g":
    case "G":
    case "a":
    case "A":
      return FLOAT;
    case "c":
    case "C":
      return CHAR;
    default:
      return undefined; // general (s/b/h) and date/time: never a definite "no"
  }
}

export function conversionAccepts(conversion: string, arg: ArgTypeDescriptor): Accepts {
  const category = categoryOf(conversion);
  if (!category) return "unknown"; // s/S/b/B/h/H accept anything; t/T too intricate
  if ("primitive" in arg) {
    return category.prims.has(arg.primitive) ? "yes" : "no"; // primitives fully decidable
  }
  if (category.fqns.has(arg.fqn)) return "yes";
  return KNOWN_LEAF.has(arg.fqn) ? "no" : "unknown";
}
