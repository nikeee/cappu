import { test } from "node:test";
import TempDir from "../TempDir.ts";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import { classFileToStub, loadClassPath } from "./classfileReader.ts";
import { emitSourceFile } from "./emitter.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import { type Uri } from "../workspace.ts";
import type { Fqn } from "./program.ts";

// Emit with OUR compiler, read the bytes back as a stub - a self-contained
// roundtrip with no JDK needed.
function emitClass(name: string, source: string): Uint8Array {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument(`file:///${name}.java` as Uri, source, 1);
  const checker = createChecker(program);
  const classes = emitSourceFile(
    program.getSourceFile(`file:///${name}.java` as Uri)!,
    program,
    checker,
  );
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
  program.addProjectFile("classpath:///lib/Greeter.java" as Uri, stub.source);
  program.setOpenDocument(
    "file:///App.java" as Uri,
    'import lib.Greeter;\nclass App { String m() { return Greeter.greet("x"); } }',
    1,
  );
  const checker = createChecker(program);
  const classes = emitSourceFile(
    program.getSourceFile("file:///App.java" as Uri)!,
    program,
    checker,
  );
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
    "file:///Outer.java" as Uri,
    "public class Outer { public static class In {} }",
    1,
  );
  const checker = createChecker(program);
  const classes = emitSourceFile(
    program.getSourceFile("file:///Outer.java" as Uri)!,
    program,
    checker,
  );
  const inner = classes.find(c => c.name === "Outer$In")!;
  expect(classFileToStub(inner.bytes)).toBeUndefined();
});

function hasJarTool(): boolean {
  try {
    execFileSync("jar", ["--version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

test(
  "a .jar classpath entry loads its classes",
  { skip: hasJarTool() ? false : "no jar tool" },
  () => {
    const bytes = emitClass(
      "Util",
      "package lib;\npublic class Util { public static int triple(int x) { return x * 3; } }",
    );
    using dir = TempDir.create("jar-");
    const classFile = join(dir.path, "lib", "Util.class");
    mkdirSync(dirname(classFile), { recursive: true });
    writeFileSync(classFile, bytes);
    execFileSync("jar", ["cf", join(dir.path, "util.jar"), "-C", dir.path, "lib/Util.class"]);

    const program = createProgram();
    loadJdkStub(program);
    const loaded = loadClassPath(program, [join(dir.path, "util.jar")]);
    expect(loaded).toBe(1);
    expect(program.getGlobalIndex().getType("lib.Util" as Fqn)).toBeDefined();
  },
);

test("generic signatures survive the stub roundtrip", () => {
  const bytes = emitClass(
    "Box",
    [
      "package lib;",
      "import java.util.List;",
      "public class Box<T extends CharSequence> implements Comparable<Box<T>> {",
      "  public T value;",
      "  public T get() { return value; }",
      "  public <U extends Comparable<U>> U pick(U a, List<? extends U> rest) { return a; }",
      "  public int compareTo(Box<T> o) { return 0; }",
      "}",
    ].join("\n"),
  );
  const stub = classFileToStub(bytes)!;
  expect(stub.source).toContain("class Box<T extends java.lang.CharSequence>");
  expect(stub.source).toContain("implements java.lang.Comparable<lib.Box<T>>");
  expect(stub.source).toContain("public T value;");
  expect(stub.source).toContain("public T get()");
  expect(stub.source).toContain(
    "<U extends java.lang.Comparable<U>> U pick(U p0, java.util.List<? extends U> p1)",
  );

  // A consumer resolves the stub generically: Box<String>.get() types as String.
  const program = createProgram();
  loadJdkStub(program);
  program.addProjectFile("classpath:///lib/Box.java" as Uri, stub.source);
  program.setOpenDocument(
    "file:///App.java" as Uri,
    "import lib.Box;\nclass App { int m(Box<String> b) { return b.get().length(); } }",
    1,
  );
  const checker = createChecker(program);
  const classes = emitSourceFile(
    program.getSourceFile("file:///App.java" as Uri)!,
    program,
    checker,
  );
  expect(classes).toHaveLength(1);
});

test("nested classes group into their outer stub and resolve from a consumer", () => {
  const program1 = createProgram();
  loadJdkStub(program1);
  program1.setOpenDocument(
    "file:///Outer.java" as Uri,
    [
      "package lib;",
      "public class Outer {",
      "  public static class Builder { public int knobs; public Builder set(int x){ knobs = x; return this; } }",
      "  public int run() { Runnable r = new Runnable(){ public void run(){} }; return 5; }",
      "}",
    ].join("\n"),
    1,
  );
  const checker1 = createChecker(program1);
  const classes = emitSourceFile(
    program1.getSourceFile("file:///Outer.java" as Uri)!,
    program1,
    checker1,
  );
  using dir = TempDir.create("nested-");
  for (const c of classes) {
    const file = join(dir.path, `${c.name}.class`);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, c.bytes);
  }

  const program = createProgram();
  loadJdkStub(program);
  expect(loadClassPath(program, [dir.path])).toBe(1); // one top-level stub
  const stub = program.getSourceFile("classpath:///lib/Outer.java" as Uri)!.text;
  expect(stub).toContain("public static class Builder");
  expect(stub).not.toContain("$1"); // the anonymous class never appears

  program.setOpenDocument(
    "file:///App.java" as Uri,
    "import lib.Outer;\nclass App { int m() { return new Outer.Builder().set(3).knobs; } }",
    1,
  );
  const checker = createChecker(program);
  const out = emitSourceFile(program.getSourceFile("file:///App.java" as Uri)!, program, checker);
  expect(out).toHaveLength(1);
});

// nikeee/cappu#70-hunt: a hostile or truncated jar entry must never hang the
// reader. Both corruptions below previously looped (the descriptor scan until
// a 2^32 RangeError, the signature scan forever).
test("corrupted descriptors and signatures terminate and still stub", () => {
  const program = createProgram();
  loadJdkStub(program);
  program.addProjectFile(
    "file:///G.java" as Uri,
    "public class G<T extends Comparable<T>> { public void m(int x) { } public T id(T t) { return t; } }",
  );
  const checker = createChecker(program);
  const [cls] = emitSourceFile(program.getSourceFile("file:///G.java" as Uri)!, program, checker);
  const original = Buffer.from(cls!.bytes);

  // descriptor "(I)V" -> "(IIV": the ')' the parameter scan looks for is gone
  const desc = Buffer.from(original);
  const descAt = desc.indexOf(Buffer.from("(I)V"));
  expect(descAt).toBeGreaterThan(0);
  desc.set(Buffer.from("(IIV"), descAt);
  expect(classFileToStub(new Uint8Array(desc))).toBeDefined();

  // class signature -> same-length colon soup with no closing '>'
  const sig = Buffer.from(original);
  const sigAt = sig.indexOf(Buffer.from("<T::Ljava/lang/Comparable<TT;>;>"));
  expect(sigAt).toBeGreaterThan(0);
  sig.set(Buffer.from("<T:<T:<T:<T:<T:<T:<T:<T:<T:<T:<T"), sigAt);
  expect(() => classFileToStub(new Uint8Array(sig))).not.toThrow();

  // method signature "(TT;)TT;" -> truncated T-refs and unclosed type args
  const methodSig = Buffer.from(original);
  const methodSigAt = methodSig.indexOf(Buffer.from("(TT;)TT;"));
  expect(methodSigAt).toBeGreaterThan(0);
  methodSig.set(Buffer.from("(TT;)LC<"), methodSigAt);
  expect(() => classFileToStub(new Uint8Array(methodSig))).not.toThrow();
});
