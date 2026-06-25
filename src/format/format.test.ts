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
