import { test } from "node:test";

import { expect } from "expect";

import { execFileSync } from "node:child_process";
import { existsSync, realpathSync } from "node:fs";
import { dirname, join } from "node:path";

import { createChecker } from "./checker.ts";
import { classFilesToStub } from "./classfileReader.ts";
import { createJdkImage } from "./jdkImage.ts";
import { createJdkTypeResolver } from "./jdkTypes.ts";
import { createProgram, type Fqn } from "./program.ts";
import { type Uri } from "../workspace.ts";

// A JDK home with jmods/ to read real classes from. Best-effort: JAVA_HOME, then
// javac resolved through PATH. The whole file skips when none is found (CI/Go).
function findJdkHomeWithJmods(): string | undefined {
  const candidates: string[] = [];
  if (process.env.JAVA_HOME) candidates.push(process.env.JAVA_HOME);
  try {
    const finder = process.platform === "win32" ? "where" : "which";
    const javac = execFileSync(finder, ["javac"]).toString().split("\n")[0].trim();
    if (javac) candidates.push(dirname(dirname(realpathSync(javac))));
  } catch {
    // no javac on PATH
  }
  return candidates.find(home => existsSync(join(home, "jmods")));
}

const jdkHome = findJdkHomeWithJmods();
const skip = jdkHome ? false : "no JDK with jmods/ on this machine";

test("the image reads a real JDK class out of its jmods", { skip }, () => {
  const image = createJdkImage(jdkHome!)!;
  expect(image).toBeDefined();

  const family = image.readClassFamily("java/util/List");
  expect(family).toBeDefined();
  const stub = classFilesToStub(family!)!;
  expect(stub.name).toBe("java/util/List");
  expect(stub.source).toContain("package java.util;");
  expect(stub.source).toContain("interface List");

  // A class with a nested type (Map.Entry) folds the nested in from its sibling
  // .class so members resolve.
  const mapFamily = image.readClassFamily("java/util/Map")!;
  expect(mapFamily.length).toBeGreaterThan(1);
  expect(classFilesToStub(mapFamily)!.source).toContain("Entry");

  // A type that does not exist is undefined (no crash, no false positive).
  expect(image.readClassFamily("java/util/NotARealType")).toBeUndefined();
});

test("a consumer resolves JDK types the synthetic stub omits", { skip }, () => {
  const image = createJdkImage(jdkHome!)!;
  const program = createProgram();
  program.setJdkTypeResolver(createJdkTypeResolver(image));
  const index = program.getGlobalIndex();

  // Streams and java.time are absent from jdkStub.ts but present in the image.
  expect(index.getType("java.util.stream.Stream" as Fqn)).toBeDefined();
  expect(index.getType("java.time.LocalDate" as Fqn)).toBeDefined();
  // Common types still resolve.
  expect(index.getType("java.util.List" as Fqn)).toBeDefined();
  expect(index.getType("java.lang.String" as Fqn)).toBeDefined();

  // End to end: a source file referencing a stub-omitted type type-checks with
  // no unresolved-type diagnostic.
  program.setOpenDocument(
    "file:///App.java" as Uri,
    "import java.time.LocalDate;\nclass App { LocalDate today() { return null; } }",
    1,
  );
  const checker = createChecker(program);
  const sourceFile = program.getSourceFile("file:///App.java" as Uri)!;
  const diagnostics = checker.getSemanticDiagnostics(sourceFile);
  const unresolved = diagnostics.filter(d => /resolve|unknown|cannot find/i.test(d.messageText));
  expect(unresolved).toEqual([]);
});
