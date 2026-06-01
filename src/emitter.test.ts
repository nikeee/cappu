import { test } from "node:test";
import { expect } from "expect";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";

const here = dirname(fileURLToPath(import.meta.url));
const baselinesDir = join(here, "__fixtures__", "emit-baselines");
const shouldUpdate = process.env.UPDATE_BASELINES === "1";

function hasTool(name: string): boolean {
  try {
    execFileSync(name, ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}
const HAS_JAVA = hasTool("java") && hasTool("javap");
const HAS_JAVAC = hasTool("javac");

// Each fixture is a single class named after its key; the source defines `class
// <name>` so the .java/.class file names line up for javac comparison.
const FIXTURES: Record<string, string> = {
  Empty: "class Empty {}",
  Fields: "class Fields { int a; java.lang.String b; long c; int[] d; boolean e; double[][] f; }",
  ModifiedFields:
    "public class ModifiedFields { public int x; private static final long y = 0; protected java.lang.String z; }",
  Methods:
    "public class Methods { void a() {} public int b(int p) { return p; } static long c(long x, int y) { return x; } java.lang.String d(java.lang.String s) { return s; } int[] e(int[] arr) { return arr; } }",
  VarargsAndAbstract:
    "abstract class VarargsAndAbstract { abstract int f(int n); int g(int... xs) { return 0; } static double h(double a, double b) { return a; } }",
  Hello:
    'public class Hello { public static void main(String[] args) { System.out.println("Hello, world"); } }',
  ReturnLiterals:
    'class ReturnLiterals { int i() { return 42; } long l() { return 7L; } boolean b() { return true; } java.lang.String s() { return "hi"; } int big() { return 1000000; } int echo(int p) { return p; } void v() {} }',
  Arithmetic:
    "class Arithmetic { int add(int a, int b) { return a + b; } int poly(int a, int b, int c) { return a * b + c; } long mix(int a, long b) { return a + b; } double dm(double x, int y) { return x * y; } int shift(int a, int n) { return a << n; } int bits(int a, int b) { return (a & b) | (a ^ b); } int neg(int a) { return -a; } int not(int a) { return ~a; } int rem(int a, int b) { return a % b; } }",
  Locals:
    "class Locals { int compute(int n) { int x = n + 1; int y = x * 2; int z; z = x + y; return z; } long widen(int n) { long w = n; return w + 1; } int reassign(int n) { int t = n; t = t + t; return t; } }",
  Fold: "class Fold { int a() { return 6 * 7; } long b() { return 100L * 100L; } int c() { return 1 << 10; } boolean d() { return 3 < 5; } int e() { return 10 / 3 + 7 % 4; } int f() { return -(2 + 3); } int g() { return (1 + 2) * (3 + 4); } }",
  Pt: "public class Pt { int x; int y; Pt(int x, int y) { this.x = x; this.y = y; } int sum() { return x + y; } }",
  // Uses a method call (not a constant expression) so javac does not fold it,
  // keeping the comparison honest until constant folding (JLS 15.28) is added.
  Compute:
    "public class Compute { static int v() { return 42; } public static void main(String[] args) { int a = v(); int b = a - 2; System.out.println(b); } }",
};

// Fixtures with a runnable main and the output they must print.
const RUNS: Record<string, string> = {
  Hello: "Hello, world\n",
  Compute: "40\n",
};

// Control-flow fixtures: verified by the JVM (our StackMapTable must be accepted)
// and run for their output. Not byte-matched to javac (we emit full_frame frames
// and may allocate slots differently).
const CONTROL: Record<string, { source: string; stdout: string }> = {
  ControlFlow: {
    source: [
      "public class ControlFlow {",
      "  static int sum(int n) { int s = 0; for (int i = 0; i < n; i++) { s = s + i; } return s; }",
      "  static int absish(int x) { if (x < 0) { return -x; } else { return x; } }",
      "  static int countdown(int n) { int c = 0; while (n > 0) { c = c + 1; n = n - 1; } return c; }",
      "  static int firstSet(int x) { int i = 0; do { i = i + 1; } while (i < x); return i; }",
      "  static boolean inRange(int x, int lo, int hi) { return x >= lo && x <= hi; }",
      "  public static void main(String[] args) {",
      "    System.out.println(sum(5));",
      "    System.out.println(absish(-7));",
      "    System.out.println(countdown(3));",
      "    System.out.println(firstSet(4));",
      "    System.out.println(inRange(5, 1, 10));",
      "  }",
      "}",
    ].join("\n"),
    stdout: "10\n7\n3\n4\ntrue\n",
  },
};

// javap -c per-method instruction lines, with constant-pool indices stripped so
// only mnemonics + symbolic operands remain (comparable across compilers).
function codeByMethod(classFile: string): Map<string, string[]> {
  const lines = execFileSync("javap", ["-c", "-p", classFile], { encoding: "utf8" }).split("\n");
  const map = new Map<string, string[]>();
  let current: string | undefined;
  for (const raw of lines) {
    const t = raw.trim();
    if (/^\d+:/.test(t)) {
      if (current) {
        map.get(current)!.push(
          t
            .replace(/^\d+:\s*/, "")
            .replace(/#\d+/g, "#")
            .replace(/\s+/g, " ")
            .trim(),
        );
      }
    } else if (t.endsWith(";") && t.includes("(") && !t.startsWith("//")) {
      current = t; // a method/constructor declaration line
      map.set(current, []);
    }
  }
  return map;
}

function emit(name: string, source: string): Uint8Array {
  const program = createProgram();
  loadJdkStub(program);
  const uri = `file:///${name}.java`;
  program.setOpenDocument(uri, source, 1);
  const classes = emitSourceFile(program.getSourceFile(uri)!, program, createChecker(program));
  const cls = classes.find(c => c.name === name);
  if (!cls) throw new Error(`no emitted class named ${name}`);
  return cls.bytes;
}

// javap -p member signature lines (fields, constructors, methods), normalized.
function members(classFile: string): string[] {
  const out = execFileSync("javap", ["-p", classFile], { encoding: "utf8" });
  return out
    .split("\n")
    .map(l => l.trim())
    .filter(l => l.endsWith(";") && !l.startsWith("Compiled"))
    .sort();
}

for (const [name, source] of Object.entries(FIXTURES)) {
  test(`emit binary baseline: ${name}`, () => {
    const bytes = emit(name, source);
    const baseline = join(baselinesDir, `${name}.class`);
    if (shouldUpdate || !existsSync(baseline)) {
      mkdirSync(baselinesDir, { recursive: true });
      writeFileSync(baseline, bytes);
    }
    expect(Buffer.from(bytes).equals(readFileSync(baseline))).toBe(true);
  });

  test(`emit is JVM-valid: ${name}`, { skip: HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-"));
    writeFileSync(join(dir, `${name}.class`), emit(name, source));
    const out = execFileSync("javap", ["-p", join(dir, `${name}.class`)], { encoding: "utf8" });
    expect(out).toContain(`class ${name}`);
    // Loading the class runs the bytecode verifier over every method. A bad body
    // would raise VerifyError/ClassFormatError; "Main method not found" (or an
    // actual run) means it verified cleanly.
    let stderr = "";
    try {
      execFileSync("java", ["-cp", dir, name], { encoding: "utf8", stdio: "pipe" });
    } catch (e) {
      stderr = String((e as { stderr?: string }).stderr ?? "");
    }
    expect(stderr).not.toMatch(/VerifyError|ClassFormatError|Incompatible/);
  });

  test(`members match javac: ${name}`, { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-"));
    // Reference: compile the same source with javac (Java 21 target).
    writeFileSync(join(dir, `${name}.java`), source);
    execFileSync("javac", ["--release", "21", "-d", dir, join(dir, `${name}.java`)]);
    const reference = members(join(dir, `${name}.class`));
    // Ours, written to a separate dir to avoid overwriting javac's output.
    const oursDir = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(oursDir, `${name}.class`), emit(name, source));
    expect(members(join(oursDir, `${name}.class`))).toEqual(reference);
  });

  test(
    `bytecode matches javac: ${name}`,
    { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" },
    () => {
      const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
      writeFileSync(join(ref, `${name}.java`), source);
      execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
      const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
      writeFileSync(join(ours, `${name}.class`), emit(name, source));

      const reference = codeByMethod(join(ref, `${name}.class`));
      const generated = codeByMethod(join(ours, `${name}.class`));
      expect([...generated.keys()].sort()).toEqual([...reference.keys()].sort());
      for (const [sig, instrs] of reference) {
        expect(generated.get(sig)).toEqual(instrs);
      }
    },
  );
}

for (const [name, expected] of Object.entries(RUNS)) {
  test(`runs and prints: ${name}`, { skip: HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-run-"));
    writeFileSync(join(dir, `${name}.class`), emit(name, source(name)));
    const out = execFileSync("java", ["-cp", dir, name], { encoding: "utf8" });
    expect(out).toBe(expected);
  });
}

function source(name: string): string {
  return FIXTURES[name]!;
}

test(
  "folded overflow constants run identically to javac",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Overflow";
    const src = [
      "public class Overflow {",
      "  public static void main(String[] args) {",
      "    System.out.println(2147483647 + 1);",
      "    System.out.println(-8 >>> 1);",
      "    System.out.println(9223372036854775807L + 1L);",
      "    System.out.println(1 << 33);",
      "    System.out.println(2147483647 * 2);",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });

    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("-2147483648\n2147483644\n-9223372036854775808\n2\n-2\n");
  },
);

test(
  "inheritance, interfaces and packages run identically to javac",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" },
  () => {
    const sources: Record<string, string> = {
      "Animal.java": 'package com.app;\npublic class Animal { String sound() { return "?"; } }',
      "Dog.java":
        'package com.app;\npublic class Dog extends Animal implements Runnable { public void run() {} String sound() { return "woof"; } }',
      "Main.java":
        "package com.app;\npublic class Main { public static void main(String[] args) { Animal a = new Dog(); System.out.println(a.sound()); } }",
    };

    // javac reference.
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    for (const [file, text] of Object.entries(sources)) writeFileSync(join(ref, file), text);
    execFileSync("javac", [
      "--release",
      "21",
      "-d",
      ref,
      ...Object.keys(sources).map(f => join(ref, f)),
    ]);
    const refOut = execFileSync("java", ["-cp", ref, "com.app.Main"], { encoding: "utf8" });

    // Ours: one program over all sources; write each class to its package path.
    const program = createProgram();
    loadJdkStub(program);
    for (const [file, text] of Object.entries(sources)) {
      program.addProjectFile(`file:///${file}`, text);
    }
    const checker = createChecker(program);
    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    for (const file of Object.keys(sources)) {
      for (const cls of emitSourceFile(
        program.getSourceFile(`file:///${file}`)!,
        program,
        checker,
      )) {
        const out = join(ours, `${cls.name}.class`);
        mkdirSync(dirname(out), { recursive: true });
        writeFileSync(out, cls.bytes);
      }
    }
    const ourOut = execFileSync("java", ["-cp", ours, "com.app.Main"], { encoding: "utf8" });

    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("woof\n");
  },
);

test(
  "string concatenation (invokedynamic) runs identically to javac",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Sc";
    const src = [
      "public class Sc {",
      "  public static void main(String[] args) {",
      '    String who = "world";',
      "    int n = 42;",
      '    System.out.println("Hello, " + who + "!");',
      '    System.out.println("n = " + n + ", twice = " + (n * 2));',
      '    System.out.println("char: " + \'X\' + " bool: " + true);',
      '    System.out.println(1 + 2 + " items");',
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });
    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("Hello, world!\nn = 42, twice = 84\nchar: X bool: true\n3 items\n");
  },
);

test(
  "field initializers and static constants run identically to javac",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Ini";
    const src = [
      "public class Ini {",
      "  int a = 5;",
      "  int b = a + 10;",
      "  static int s = 100;",
      "  static final int K = 42;",
      "  static final long BIG = 1000L;",
      "  int getA() { return a; }",
      "  int getB() { return b; }",
      "  public static void main(String[] args) {",
      "    Ini o = new Ini();",
      "    System.out.println(o.getA());",
      "    System.out.println(o.getB());",
      "    System.out.println(s);",
      "    System.out.println(K);",
      "    System.out.println(BIG);",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });
    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("5\n15\n100\n42\n1000\n");
  },
);

test(
  "object creation and field access run identically to javac",
  { skip: HAS_JAVAC && HAS_JAVA ? false : "no JDK" },
  () => {
    const name = "Obj";
    const src = [
      "public class Obj {",
      "  int x;",
      "  static int count;",
      "  void set(int v) { this.x = v; }",
      "  int get() { return x; }",
      "  static void bump() { count = count + 1; }",
      "  public static void main(String[] args) {",
      "    Obj o = new Obj();",
      "    o.set(42);",
      "    System.out.println(o.get());",
      "    o.x = 7;",
      "    System.out.println(o.x);",
      "    bump(); bump();",
      "    System.out.println(count);",
      "  }",
      "}",
    ].join("\n");
    const ref = mkdtempSync(join(tmpdir(), "emit-ref-"));
    writeFileSync(join(ref, `${name}.java`), src);
    execFileSync("javac", ["--release", "21", "-d", ref, join(ref, `${name}.java`)]);
    const refOut = execFileSync("java", ["-cp", ref, name], { encoding: "utf8" });

    const ours = mkdtempSync(join(tmpdir(), "emit-ours-"));
    writeFileSync(join(ours, `${name}.class`), emit(name, src));
    const ourOut = execFileSync("java", ["-cp", ours, name], { encoding: "utf8" });

    expect(ourOut).toBe(refOut);
    expect(refOut).toBe("42\n7\n2\n");
  },
);

for (const [name, { source: src, stdout }] of Object.entries(CONTROL)) {
  test(`control flow binary baseline: ${name}`, () => {
    const bytes = emit(name, src);
    const baseline = join(baselinesDir, `${name}.class`);
    if (shouldUpdate || !existsSync(baseline)) {
      mkdirSync(baselinesDir, { recursive: true });
      writeFileSync(baseline, bytes);
    }
    expect(Buffer.from(bytes).equals(readFileSync(baseline))).toBe(true);
  });

  test(`control flow verifies and runs: ${name}`, { skip: HAS_JAVA ? false : "no JDK" }, () => {
    const dir = mkdtempSync(join(tmpdir(), "emit-cf-"));
    writeFileSync(join(dir, `${name}.class`), emit(name, src));
    // The JVM verifier checks our StackMapTable on load; a wrong frame -> VerifyError.
    const out = execFileSync("java", ["-cp", dir, name], { encoding: "utf8" });
    expect(out).toBe(stdout);
  });
}
