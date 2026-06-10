import { test } from "node:test";
import { expect } from "expect";

import { classFileToStub } from "./classfileReader.ts";
import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";

// Emit with OUR compiler, read the bytes back as a stub - a self-contained
// roundtrip with no JDK needed.
function emitClass(name: string, source: string): Uint8Array {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument(`file:///${name}.java`, source, 1);
  const checker = createChecker(program);
  const classes = emitSourceFile(program.getSourceFile(`file:///${name}.java`)!, program, checker);
  return classes.find(c => c.name.endsWith(name))!.bytes;
}

test("a compiled class reads back as a resolvable stub", () => {
  const bytes = emitClass(
    "Greeter",
    [
      "package lib;",
      "public class Greeter {",
      "  public int factor;",
      "  public Greeter(int factor) { this.factor = factor; }",
      "  public static String greet(String name) { return name; }",
      "  public int scale(int x) { return x * factor; }",
      "  private int hidden() { return 0; }",
      "}",
    ].join("\n"),
  );
  const stub = classFileToStub(bytes)!;
  expect(stub.name).toBe("lib/Greeter");
  expect(stub.source).toContain("package lib;");
  expect(stub.source).toContain("public class Greeter");
  expect(stub.source).toContain("public int factor;");
  expect(stub.source).toContain("public Greeter(int p0)");
  expect(stub.source).toContain("static java.lang.String greet(java.lang.String p0)");
  expect(stub.source).toContain("public int scale(int p0)");
  expect(stub.source).not.toContain("hidden"); // private members are omitted

  // The stub resolves through the normal pipeline: a caller compiles cleanly.
  const program = createProgram();
  loadJdkStub(program);
  program.addProjectFile("classpath:///lib/Greeter.java", stub.source);
  program.setOpenDocument(
    "file:///App.java",
    'import lib.Greeter;\nclass App { String m() { return Greeter.greet("x"); } }',
    1,
  );
  const checker = createChecker(program);
  const classes = emitSourceFile(program.getSourceFile("file:///App.java")!, program, checker);
  expect(classes).toHaveLength(1);
});

test("interfaces and enums read back in their own declaration forms", () => {
  const ifaceBytes = emitClass(
    "Speaker",
    "public interface Speaker { String speak(); default int volume() { return 5; } }",
  );
  const iface = classFileToStub(ifaceBytes)!;
  expect(iface.source).toContain("public interface Speaker");
  expect(iface.source).toContain("java.lang.String speak();"); // abstract: no body
  expect(iface.source).toContain("default int volume()"); // default keeps a body

  const enumBytes = emitClass("Color", "public enum Color { RED, GREEN, BLUE }");
  const e = classFileToStub(enumBytes)!;
  expect(e.source).toContain("public enum Color");
  expect(e.source).toContain("RED, GREEN, BLUE;");
  expect(e.source).not.toContain("valueOf"); // collides with synthesized statics
});

test("nested classes are skipped (not expressible as top-level stubs)", () => {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument(
    "file:///Outer.java",
    "public class Outer { public static class In {} }",
    1,
  );
  const checker = createChecker(program);
  const classes = emitSourceFile(program.getSourceFile("file:///Outer.java")!, program, checker);
  const inner = classes.find(c => c.name === "Outer$In")!;
  expect(classFileToStub(inner.bytes)).toBeUndefined();
});
