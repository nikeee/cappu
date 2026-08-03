// Golden tests for the Java formatter. Each test-fixtures/format/cases/*.input
// is formatted in both styles and compared to the checked-in *.output baselines
// under test-fixtures/format/baselines/<style>. The baselines are the REAL
// google-java-format output, so these tests measure actual compatibility over
// the subset of constructs the formatter covers.
//
// Normal runs only read the committed baselines (no JDK needed). To regenerate
// them after an intentional change - or when adding a case - run the real
// google-java-format. Either point GJF_JAR at the all-deps jar:
//   GJF_JAR=/path/to/google-java-format-all-deps.jar \
//     UPDATE_BASELINES=1 node_modules/.bin/tsx --test ./src/format/format.test.ts
// or, when only the maven repo jar is present, point GJF_CP at a resolved
// classpath (mvn dependency:build-classpath -Dmdep.outputFile=cp.txt):
//   GJF_CP=$(cat cp.txt) UPDATE_BASELINES=1 node_modules/.bin/tsx --test ...
// Download the jar from https://github.com/google/google-java-format/releases.

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { type FormatOptions, formatSource } from "./index.ts";

const here = import.meta.dirname;
const casesDir = join(here, "..", "..", "test-fixtures", "format", "cases");
const baselinesDir = join(here, "..", "..", "test-fixtures", "format", "baselines");
const shouldUpdate = process.env.UPDATE_BASELINES === "1";
const gjfJar = process.env.GJF_JAR;
// The maven repo jar is not all-deps; GJF_CP lets baseline regen run gjf off a
// resolved classpath instead (mvn dependency:build-classpath > cp.txt).
const gjfCp = process.env.GJF_CP;
const haveGjf = gjfJar !== undefined || gjfCp !== undefined;

const STYLES: FormatOptions["style"][] = ["google", "aosp"];

// google-java-format reaches into javac internals; these exports are required
// on a modern JDK. Mirrors the wrapper the README documents.
const GJF_JVM_ARGS = [
  "--add-exports",
  "jdk.compiler/com.sun.tools.javac.api=ALL-UNNAMED",
  "--add-exports",
  "jdk.compiler/com.sun.tools.javac.file=ALL-UNNAMED",
  "--add-exports",
  "jdk.compiler/com.sun.tools.javac.parser=ALL-UNNAMED",
  "--add-exports",
  "jdk.compiler/com.sun.tools.javac.tree=ALL-UNNAMED",
  "--add-exports",
  "jdk.compiler/com.sun.tools.javac.util=ALL-UNNAMED",
];

function runGoogleJavaFormat(source: string, style: FormatOptions["style"]): string {
  const styleArgs = style === "aosp" ? ["--aosp"] : [];
  const launch = gjfCp
    ? [...GJF_JVM_ARGS, "-cp", gjfCp, "com.google.googlejavaformat.java.Main", ...styleArgs, "-"]
    : [...GJF_JVM_ARGS, "-jar", gjfJar!, ...styleArgs, "-"];
  return execFileSync("java", launch, { input: source, encoding: "utf8" });
}

const cases = existsSync(casesDir)
  ? readdirSync(casesDir)
      .filter(f => f.endsWith(".input"))
      .sort()
  : [];

for (const file of cases) {
  const base = file.replace(/\.input$/, "");
  const source = readFileSync(join(casesDir, file), "utf8");

  for (const style of STYLES) {
    const baselinePath = join(baselinesDir, style, `${base}.output`);

    test(`format ${base} [${style}] matches google-java-format`, () => {
      if (shouldUpdate || !existsSync(baselinePath)) {
        if (!haveGjf) {
          throw new Error(
            `missing baseline ${baselinePath} and neither GJF_JAR nor GJF_CP is set; set one to ` +
              `(re)generate baselines (see this file's header).`,
          );
        }
        mkdirSync(join(baselinesDir, style), { recursive: true });
        writeFileSync(baselinePath, runGoogleJavaFormat(source, style));
      }
      const expected = readFileSync(baselinePath, "utf8");
      expect(formatSource(source, { style })).toBe(expected);
    });

    test(`format ${base} [${style}] is idempotent`, () => {
      if (!existsSync(baselinePath)) return; // generated above on the first pass
      const expected = readFileSync(baselinePath, "utf8");
      // Re-formatting already-formatted code must be a no-op.
      expect(formatSource(expected, { style })).toBe(expected);
    });
  }
}

// Not derivable from a golden case file: an array constructor reference parses
// as a class literal carrying the array type, and printing it as one produced
// "Foo[].class::new", i.e. the formatter rewrote valid code into code that does
// not compile.
test("array constructor references survive formatting", () => {
  const source = [
    "class T {",
    "  Object f = java.util.stream.Stream.of(1).toArray(Integer[]::new);",
    "  Object g = String[][]::new;",
    "  Object h = int[]::new;",
    "  Class<?> i = Integer[].class;",
    "  java.util.function.Function<Class<?>, String> j = Integer[].class::getName;",
    "}",
    "",
  ].join("\n");
  expect(formatSource(source, { style: "google" })).toBe(source);
});

// A trailing comment on an enum constant used to be printed BEFORE the
// separator, so "A(1), // one" came back as "A(1) // one," - the comma was
// commented out and the file no longer compiled.
test("enum constant separators stay in front of a trailing comment", () => {
  const source = [
    "enum T {",
    "  A(1), // one",
    "  B(2); // two",
    "",
    "  private final int n;",
    "",
    "  T(int n) {",
    "    this.n = n;",
    "  }",
    "}",
    "",
  ].join("\n");
  const once = formatSource(source, { style: "google" });
  expect(once).toBe(source);
});

// A declaration the printer degrades to a verbatim source slice (an @interface)
// carries its members' comments inside that slice. They stayed queued and were
// flushed again at the end of the file, so every run appended another copy.
test("comments inside a verbatim-printed declaration are not duplicated", () => {
  const source = [
    "public @interface A {",
    "  /** doc. */",
    "  boolean on() default true;",
    "}",
    "",
  ].join("\n");
  const once = formatSource(source, { style: "google" });
  expect(once.match(/doc\./g)).toHaveLength(1);
  expect(formatSource(once, { style: "google" })).toBe(once);
});

// A comment holding a non-ASCII character used to wrap early in the Go build,
// which measured bytes: the column budget is UTF-16 code units (this line is
// exactly 100 of them), so the two builds broke real files differently.
test("comment wrapping counts UTF-16 units, not bytes", () => {
  const source = [
    "class T {",
    "  void m() {",
    "    // Euler is low-order \u2014 allow a small tolerance but assert it remains close for small dt + short time.",
    "    int x = 0;",
    "  }",
    "}",
    "",
  ].join("\n");
  const expected = [
    "class T {",
    "  void m() {",
    "    // Euler is low-order \u2014 allow a small tolerance but assert it remains close for small dt + short",
    "    // time.",
    "    int x = 0;",
    "  }",
    "}",
    "",
  ].join("\n");
  expect(formatSource(source, { style: "google" })).toBe(expected);
});

// Types whose spelling the AST does not model are printed from source; the
// qualified this/super forms and receiver parameters used to lose their
// qualifier ("o.super()" came back as "super()").
test("JSR-308 types, qualified inner types and qualified this/super survive formatting", () => {
  const source = [
    "class T {",
    "  String @A [] @B [] arr;",
    "  Outer<Number>.B field;",
    "  Outer.@A Middle.@B Inner deep;",
    "",
    "  static class P<@A U> {",
    "    public void receiver(@F P<U> this) {}",
    "  }",
    "",
    "  class Inner {",
    "    int outer() {",
    "      return T.this.hashCode();",
    "    }",
    "",
    "    String parent() {",
    "      return T.super.toString();",
    "    }",
    "  }",
    "",
    "  static class Sub extends T.Inner {",
    "    Sub(T t) {",
    "      t.super();",
    "    }",
    "  }",
    "}",
    "",
  ].join("\n");
  expect(formatSource(source, { style: "google" })).toBe(source);
});

// Formatting must be a fixpoint. Both cases below changed on a second run:
// the trailing comment was attached to the innermost literal (forcing the
// initializer open), and a leading block comment that had come to start its
// line was then treated as a standalone line comment.
test("a trailing comment after a nested initializer stays with the statement", () => {
  const source = [
    "class T {",
    "  void m() {",
    "    int[][] edges = {{0, 1}, {1, 2}, {2, 3}, {3, 0}}; // Even cycle",
    "  }",
    "}",
    "",
  ].join("\n");
  expect(formatSource(source, { style: "google" })).toBe(source);
});

test("a leading block comment stays on its item's line when the list is broken", () => {
  const source = [
    "class T {",
    "  Object[] m() {",
    "    return new Object[] {",
    '      "for", "then", "despite", /* of */ "space", "I", "would", "be", "brought", "from",',
    '      "limits", "far", "remote", "where", "thou", "dost", "stay"',
    "    };",
    "  }",
    "}",
    "",
  ].join("\n");
  const once = formatSource(source, { style: "google" });
  expect(once).toContain('/* of */ "space",');
  expect(formatSource(once, { style: "google" })).toBe(once);
});

// The module printer consumed no comments at all, so any comment inside a
// module-info made the whole file unformattable ("comment in an unsupported
// position") and the CLI left it untouched.
test("comments inside a module declaration are formatted, not refused", () => {
  const source = [
    '@SuppressWarnings("requires-automatic") // automatic module names',
    "module com.acme.app {",
    "  exports com.acme.api;",
    "",
    "  // Optional dependency",
    "  requires static java.sql;",
    "  requires java.base; // the platform module",
    "  // dangling",
    "}",
    "",
  ].join("\n");
  expect(formatSource(source, { style: "google" })).toBe(source);
});

// google-java-format writes its output with the line separator the source uses
// (Newlines.guessLineSeparator); we always emitted LF, so every line of a CRLF
// file differed.
test("the source's line separator is preserved", () => {
  const lines = ["package p;", "", "class T {", "  int x;", "}", ""];
  expect(formatSource(lines.join("\r\n"), { style: "google" })).toBe(lines.join("\r\n"));
  expect(formatSource(lines.join("\n"), { style: "google" })).toBe(lines.join("\n"));
});
