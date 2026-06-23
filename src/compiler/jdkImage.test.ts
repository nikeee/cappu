import { test } from "node:test";
import TempDir from "../TempDir.ts";

import { expect } from "expect";

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

import { createChecker } from "./checker.ts";
import { classFilesToStub } from "./classfileReader.ts";
import { emitSourceFile } from "./emitter.ts";
import { createJdkImage } from "./jdkImage.ts";
import { createJdkTypeResolver, installJdkTypes } from "./jdkTypes.ts";
import { createProgram, type Fqn, type PackageName } from "./program.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { type Uri } from "../workspace.ts";
import { writeZip } from "./zipWriter.ts";

// --- hermetic JDK image: synthesize a .jmod from emitted classes, no JDK -------

// Compile Java to .class bytes with our own emitter (no JDK).
function emitClasses(name: string, source: string): { binaryName: string; bytes: Uint8Array }[] {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument(`file:///${name}.java` as Uri, source, 1);
  const checker = createChecker(program);
  const classes = emitSourceFile(
    program.getSourceFile(`file:///${name}.java` as Uri)!,
    program,
    checker,
  );
  return classes.map(c => ({ binaryName: c.name, bytes: c.bytes }));
}

// A .jmod is the 4-byte magic "JM\x01\x00" followed by a zip whose class entries
// live under classes/ - exactly what jdkImage strips and reads.
function makeJmod(classes: { binaryName: string; bytes: Uint8Array }[]): Uint8Array {
  const zip = writeZip(
    classes.map(c => ({ name: `classes/${c.binaryName}.class`, bytes: c.bytes })),
  );
  const out = new Uint8Array(4 + zip.length);
  out.set([0x4a, 0x4d, 0x01, 0x00], 0);
  out.set(zip, 4);
  return out;
}

// A throwaway JDK home: <tmp>/jmods/<each file>. Auto-removed at process exit.
const tempHomes: string[] = [];
function makeJdkHome(jmods: { name: string; bytes: Uint8Array }[]): string {
  using home = TempDir.create("jdkhome-");
  tempHomes.push(home.path);
  mkdirSync(join(home.path, "jmods"));
  for (const m of jmods) writeFileSync(join(home.path, "jmods", m.name), m.bytes);
  return home.path;
}
process.on("exit", () => {
  for (const home of tempHomes) rmSync(home, { recursive: true, force: true });
});

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

// --- hermetic edge cases (synthetic jmods, no JDK needed) ----------------------

test("createJdkImage returns undefined when there is nothing to read", () => {
  // No jmods/ directory at all.
  using noJmods = TempDir.create("nojmods-");
  tempHomes.push(noJmods.path);
  expect(createJdkImage(noJmods.path)).toBeUndefined();
  // A jmods/ directory with no .jmod files in it.
  expect(createJdkImage(makeJdkHome([]))).toBeUndefined();
});

test("a non-jmod file in jmods/ is skipped, not crashed on", () => {
  // The file ends in .jmod (so createJdkImage sees a candidate) but lacks the
  // JM magic, so reading it yields nothing rather than throwing.
  const home = makeJdkHome([{ name: "junk.jmod", bytes: new Uint8Array([1, 2, 3, 4, 5]) }]);
  const image = createJdkImage(home)!;
  expect(image).toBeDefined();
  expect(image.readClassFamily("lib/Whatever")).toBeUndefined();
});

test("readClassFamily resolves a class, misses cleanly, and handles the default package", () => {
  const home = makeJdkHome([
    makeJmodEntry(
      "lib/Widget",
      "package lib;\npublic class Widget { public int size() { return 0; } }",
    ),
    makeJmodEntry("Root", "public class Root { public int v() { return 0; } }"), // default package
  ]);
  const image = createJdkImage(home)!;

  const widget = image.readClassFamily("lib/Widget");
  expect(widget).toBeDefined();
  expect(classFilesToStub(widget!)!.name).toBe("lib/Widget");

  // Default-package class (no slash in the binary name).
  expect(classFilesToStub(image.readClassFamily("Root")!)!.name).toBe("Root");

  // A class that is not present.
  expect(image.readClassFamily("lib/Missing")).toBeUndefined();
});

test("a nested class folds into its outer's family", () => {
  const classes = emitClasses(
    "Outer",
    [
      "package lib;",
      "public class Outer {",
      "  public static class Builder { public int knobs; }",
      "}",
    ].join("\n"),
  );
  // Sanity: the emitter produced both the outer and the nested class.
  expect(classes.map(c => c.binaryName).sort()).toEqual(["lib/Outer", "lib/Outer$Builder"]);

  const image = createJdkImage(makeJdkHome([{ name: "lib.jmod", bytes: makeJmod(classes) }]))!;
  const family = image.readClassFamily("lib/Outer")!;
  expect(family.length).toBe(2); // outer + nested
  expect(classFilesToStub(family)!.source).toContain("class Builder");
});

test("a project type shadows a JDK type of the same name", () => {
  // The image carries lib.Thing with imageOnly(); the project declares its own
  // lib.Thing with projectOnly(). getType must return the project's.
  const image = createJdkImage(
    makeJdkHome([
      makeJmodEntry(
        "lib/Thing",
        "package lib;\npublic class Thing { public int imageOnly() { return 0; } }",
      ),
    ]),
  )!;
  const program = createProgram();
  program.setJdkTypeResolver(createJdkTypeResolver(image));
  program.addProjectFile(
    "file:///lib/Thing.java" as Uri,
    "package lib;\npublic class Thing { public int projectOnly() { return 1; } }",
  );
  const thing = program.getGlobalIndex().getType("lib.Thing" as Fqn);
  expect(thing?.members?.has("projectOnly")).toBe(true);
  expect(thing?.members?.has("imageOnly")).toBe(false);
});

test("a JDK-type miss is idempotent (cached, no re-read)", () => {
  const home = makeJdkHome([makeJmodEntry("lib/Only", "package lib;\npublic class Only {}")]);
  const resolve = createJdkTypeResolver(createJdkImage(home)!);
  expect(resolve("lib.Absent" as Fqn)).toBeUndefined();
  expect(resolve("lib.Absent" as Fqn)).toBeUndefined(); // second call hits the null cache
  expect(resolve("lib.Only" as Fqn)).toBeDefined();
});

test("installJdkTypes falls back to the synthetic stub with no provisioned JDK", () => {
  // No config (the LSP can run without one): the stub must still resolve.
  const program = createProgram();
  installJdkTypes(program, undefined);
  expect(program.getGlobalIndex().getType("java.lang.String" as Fqn)).toBeDefined();
});

test("a provisioned JDK keeps the stub for enumeration and adds image resolution", () => {
  // The real install path loads the stub (so completion / auto-import keep
  // enumerating) AND sets the image resolver (so stub-omitted types resolve).
  // Mirror that composition with a synthetic image carrying an extra type.
  const home = makeJdkHome([makeJmodEntry("lib/Extra", "package lib;\npublic class Extra {}")]);
  const program = createProgram();
  loadJdkStub(program);
  program.setJdkTypeResolver(createJdkTypeResolver(createJdkImage(home)!));
  const index = program.getGlobalIndex();

  // Enumeration (completion / auto-import) still sees the stub's common types.
  expect(index.getPackageTypes("java.lang" as PackageName)?.size ?? 0).toBeGreaterThan(0);
  expect(index.findFqnsBySimpleName("String").length).toBeGreaterThan(0);
  // Resolution reaches the image for types the stub does not carry.
  expect(index.getType("lib.Extra" as Fqn)).toBeDefined();
});

// Build one synthetic jmod holding a single emitted class, named after it.
function makeJmodEntry(binaryName: string, source: string): { name: string; bytes: Uint8Array } {
  const simple = binaryName.slice(binaryName.lastIndexOf("/") + 1);
  return { name: `${simple}.jmod`, bytes: makeJmod(emitClasses(simple, source)) };
}
