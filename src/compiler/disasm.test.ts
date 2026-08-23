import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import TempDir from "../TempDir.ts";
import { disassemble, javaDoubleText, javaFloatText } from "./disasm.ts";
import { type Disasm, parseJavapText } from "./javapNormalize.ts";

const here = import.meta.dirname;
const emitBaselines = join(here, "..", "..", "test-fixtures", "emitter", "emit-baselines");
const javacBaselines = join(here, "..", "..", "test-fixtures", "emitter", "javac-baselines");
const disasmBaselines = join(here, "..", "..", "test-fixtures", "decompiler", "disasm-baselines");
const shouldUpdate = process.env.UPDATE_BASELINES === "1";

function hasTool(name: string): boolean {
  try {
    execFileSync(name, ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}
const HAS_JAVAC = hasTool("javac");
const HAS_JAVAP = hasTool("javap");

function disasmBaselineClass(name: string): string {
  return disassemble(readFileSync(join(emitBaselines, `${name}.class`)));
}

/** Our own text, normalized the way `javap -c -p` output is. */
function normalized(name: string): Disasm | undefined {
  return parseJavapText(disasmBaselineClass(name)).get(name);
}

// --- tier 1: the committed javac disassembly, no JDK needed -------------------------

// Every javac baseline is the normalized `javap -c -p` of the class our emitter
// produced, so our own disassembler has to reproduce it exactly.
for (const file of readdirSync(javacBaselines)
  .filter(f => f.endsWith(".json"))
  .sort()) {
  test(`matches the javac baseline: ${file}`, () => {
    const reference: Record<string, Disasm> = JSON.parse(
      readFileSync(join(javacBaselines, file), "utf8"),
    );
    for (const [className, expected] of Object.entries(reference)) {
      expect({ [className]: normalized(className) }).toEqual({ [className]: expected });
    }
  });
}

// --- tier 2: our own text baselines for the classes javac has none for ---------------

// The classes with no javac baseline (enum bodies, annotations, sealed types):
// their full listing is pinned here so a decoding change cannot pass unnoticed.
const TEXT_BASELINE_CLASSES = [
  "AnnAll",
  "EnumAbstract",
  "EnumMixed",
  "EnumMixed$1",
  "EnumUnqualified",
  "ImplicitSealed",
  "QualifiedAnon$1",
  "Sealed",
];

for (const name of TEXT_BASELINE_CLASSES) {
  test(`disassembly baseline: ${name}`, () => {
    const actual = disasmBaselineClass(name);
    const baseline = join(disasmBaselines, `${name}.txt`);
    if (shouldUpdate || !existsSync(baseline)) {
      mkdirSync(disasmBaselines, { recursive: true });
      writeFileSync(baseline, actual);
    }
    expect(actual).toEqual(readFileSync(baseline, "utf8"));
  });
}

test("every committed baseline class disassembles", () => {
  for (const file of readdirSync(emitBaselines).filter(f => f.endsWith(".class"))) {
    const text = disassemble(readFileSync(join(emitBaselines, file)));
    expect(text.endsWith("}\n")).toBe(true);
  }
});

// --- tier 3: live javap, over constructs our emitter cannot produce ------------------

// Straight from javac, so these cover switches, exception tables, invokedynamic,
// the wide prefix, `throws` clauses and older class-file versions.
const JAVAC_FIXTURES: Record<string, string> = {
  Switches:
    "public class Switches {\n" +
    "  int table(int x) { switch (x) { case 1: return 10; case 2: return 20; case 3: return 30; default: return 0; } }\n" +
    "  int lookup(int x) { switch (x) { case 1: return 1; case 100: return 2; case 10000: return 3; default: return 4; } }\n" +
    '  int strings(String s) { switch (s) { case "a": return 1; case "b": return 2; default: return 3; } }\n' +
    "}",
  Guards:
    "import java.io.*;\n" +
    "public class Guards {\n" +
    "  String read(String s) throws IOException {\n" +
    '    try { return s.trim(); } catch (RuntimeException e) { return "x"; } finally { System.out.print(""); }\n' +
    "  }\n" +
    "  void nested() { synchronized (this) { try { throw new IllegalStateException(); } catch (Exception e) {} } }\n" +
    "}",
  Dynamic:
    "import java.util.function.*;\n" +
    "public class Dynamic {\n" +
    '  Supplier<String> lambda() { return () -> "hi"; }\n' +
    "  Runnable ref() { return this::run; }\n" +
    "  void run() {}\n" +
    '  String concat(String a, int b) { return a + b + "!"; }\n' +
    "}",
  Numbers:
    "public class Numbers {\n" +
    "  double tiny() { return 4.9E-324; }\n" +
    "  double big() { return 1.5e300; }\n" +
    "  float subnormal() { return 1.4E-45f; }\n" +
    "  float exact() { return 359427.125f; }\n" +
    "  long l() { return 123456789012345L; }\n" +
    "  int[][] multi() { return new int[2][3]; }\n" +
    "  char[] chars() { return new char[5]; }\n" +
    '  String quoted() { return "tab\\tnul\\u0000 \\u00a0 end"; }\n' +
    "}",
  Wide:
    "public class Wide {\n" +
    "  int wide(int n) {\n" +
    "    int a0=n,a1=n,a2=n,a3=n,a4=n,a5=n,a6=n,a7=n,a8=n,a9=n;\n" +
    "    long b0=n,b1=n,b2=n,b3=n,b4=n,b5=n,b6=n,b7=n,b8=n,b9=n;\n" +
    "    long c0=n,c1=n,c2=n,c3=n,c4=n,c5=n,c6=n,c7=n,c8=n,c9=n;\n" +
    "    long d0=n,d1=n,d2=n,d3=n,d4=n,d5=n,d6=n,d7=n,d8=n,d9=n;\n" +
    "    long e0=n,e1=n,e2=n,e3=n,e4=n,e5=n,e6=n,e7=n,e8=n,e9=n;\n" +
    "    long f0=n,f1=n,f2=n,f3=n,f4=n,f5=n,f6=n,f7=n,f8=n,f9=n;\n" +
    "    long g0=n,g1=n,g2=n,g3=n,g4=n,g5=n,g6=n,g7=n,g8=n,g9=n;\n" +
    "    long h0=n,h1=n,h2=n,h3=n,h4=n,h5=n,h6=n,h7=n,h8=n,h9=n;\n" +
    "    long i0=n,i1=n,i2=n,i3=n,i4=n,i5=n,i6=n,i7=n,i8=n,i9=n;\n" +
    "    long j0=n,j1=n,j2=n,j3=n,j4=n,j5=n,j6=n,j7=n,j8=n,j9=n;\n" +
    "    long k0=n,k1=n,k2=n,k3=n,k4=n,k5=n,k6=n,k7=n,k8=n,k9=n;\n" +
    "    long l0=n,l1=n,l2=n,l3=n,l4=n,l5=n,l6=n,l7=n,l8=n,l9=n;\n" +
    "    long m0=n,m1=n,m2=n,m3=n,m4=n,m5=n,m6=n,m7=n,m8=n,m9=n;\n" +
    "    k9 += 100000;\n" +
    "    return (int) (a0 + b9 + k9 + m9);\n" +
    "  }\n" +
    "}",
  Shapes:
    "import java.util.*;\n" +
    "public class Shapes<T extends Number & Comparable<T>> implements Cloneable, java.io.Serializable {\n" +
    "  public static final int CONST = 7;\n" +
    "  private volatile transient List<? super String> sink;\n" +
    '  static { System.out.print(""); }\n' +
    "  public synchronized <X> X pick(List<? extends X> in, X... more) throws java.io.IOException { return null; }\n" +
    "  interface Inner { default int d() { return 1; } static int s() { return 2; } private int p() { return 3; } }\n" +
    "  record Pair(int a, String b) {}\n" +
    "  enum Kind { A, B }\n" +
    "  abstract static class Base { abstract void go(); }\n" +
    "}",
};

// Older class files exercise the pre-condy constant pool and pre-Java-9 layout.
// Only the fixtures that are valid Java 8 are compiled for the older release
// (`Shapes` uses records and private interface methods).
const OLD_RELEASE = "8";
const OLD_RELEASE_FIXTURES = ["Switches", "Guards", "Dynamic", "Numbers", "Wide"];
const JAVAC_RELEASES = ["21", OLD_RELEASE];

function compileWithJavac(source: string, name: string, release: string, outDir: string): string[] {
  writeFileSync(join(outDir, `${name}.java`), source);
  execFileSync("javac", ["--release", release, "-d", outDir, join(outDir, `${name}.java`)], {
    stdio: "pipe",
  });
  return readdirSync(outDir)
    .filter(f => f.endsWith(".class"))
    .sort()
    .map(f => join(outDir, f));
}

for (const release of JAVAC_RELEASES) {
  for (const [name, source] of Object.entries(JAVAC_FIXTURES)) {
    if (release === OLD_RELEASE && !OLD_RELEASE_FIXTURES.includes(name)) continue;
    test(
      `matches javap on javac --release ${release} output: ${name}`,
      { skip: HAS_JAVAC && HAS_JAVAP ? false : "no JDK (javac/javap)" },
      () => {
        using dir = TempDir.create("cappu-disasm-");
        const classFiles = compileWithJavac(source, name, release, dir.path);
        for (const classFile of classFiles) {
          const theirs = execFileSync("javap", ["-c", "-p", classFile], { encoding: "utf8" });
          expect(disassemble(readFileSync(classFile))).toEqual(theirs);
        }
      },
    );
  }
}

// --- Java's own number formatting ----------------------------------------------------

test("renders double constants like Double.toString", () => {
  const cases: [number, string][] = [
    [0, "0.0"],
    [-0, "-0.0"],
    [1, "1.0"],
    [100, "100.0"],
    [0.001, "0.001"],
    [0.0001, "1.0E-4"],
    [9999999, "9999999.0"],
    [1e7, "1.0E7"],
    [1.5e300, "1.5E300"],
    [Math.PI, "3.141592653589793"],
    [5.9604644775390625e-8, "5.960464477539063E-8"],
    [4.9e-324, "4.9E-324"],
    [Infinity, "Infinity"],
    [-Infinity, "-Infinity"],
    [NaN, "NaN"],
  ];
  for (const [value, expected] of cases)
    expect([value, javaDoubleText(value)]).toEqual([value, expected]);
});

test("renders float constants like Float.toString", () => {
  const cases: [number, string][] = [
    [Math.fround(0.1), "0.1"],
    [Math.fround(0.3), "0.3"],
    [Math.fround(3.14159), "3.14159"],
    [Math.fround(1e10), "1.0E10"],
    [Math.fround(16777217), "1.6777216E7"],
    [Math.fround(359427.125), "359427.12"],
    [2 ** -149, "1.4E-45"],
    [2 * 2 ** -149, "2.8E-45"],
    [3 * 2 ** -149, "4.2E-45"],
    [0, "0.0"],
  ];
  for (const [value, expected] of cases)
    expect([value, javaFloatText(value)]).toEqual([value, expected]);
});
