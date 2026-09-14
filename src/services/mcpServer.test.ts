// Integration tests for the MCP server's config/classpath live reload: a real
// client over an in-memory transport, real files on disk. The jar fixture is
// the same util.jar (lib.Util) the Go port's tests use.
import { copyFileSync, mkdirSync, readFileSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { test } from "node:test";
import { join } from "node:path";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { expect } from "expect";

import { readZipEntries } from "../compiler/zipReader.ts";
import { loadConfig } from "../config.ts";
import TempDir from "../TempDir.ts";
import { startMcpServer } from "./mcpServer.ts";

const UTIL_JAR = join(
  import.meta.dirname,
  "..",
  "..",
  "togo",
  "internal",
  "compiler",
  "testdata",
  "classfiles",
  "util.jar",
);
const UTIL_STUB_URI = "classpath:///lib/Util.java";

async function startClient(dir: string) {
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  const config = loadConfig(undefined, dir);
  await startMcpServer(config, serverTransport);
  const client = new Client({ name: "test", version: "0.0.0" });
  await client.connect(clientTransport);
  return client;
}

async function describeSymbol(client: Client, ref: string): Promise<string> {
  const result = await client.callTool({ name: "describe_symbol", arguments: { ref } });
  return (result.content as Array<{ text: string }>)[0].text;
}

/** Write cappu.json with the given classPath and a distinct mtime. */
function writeConfigFile(dir: string, classPath: string, mtime: Date): void {
  const path = join(dir, "cappu.json");
  writeFileSync(
    path,
    JSON.stringify({ compilerOptions: { classPath: [classPath], sourcePaths: [] } }),
  );
  utimesSync(path, mtime, mtime);
}

const base = new Date(Date.now() - 3_600_000);

test("a jar appearing on the classpath is picked up on the next call", async () => {
  using dir = TempDir.create("mcp-reload-");
  mkdirSync(join(dir.path, "lib"));
  writeConfigFile(dir.path, "lib", base);
  const client = await startClient(dir.path);
  expect(await describeSymbol(client, "lib.Util")).not.toContain(UTIL_STUB_URI);
  copyFileSync(UTIL_JAR, join(dir.path, "lib", "util.jar"));
  expect(await describeSymbol(client, "lib.Util")).toContain(UTIL_STUB_URI);
});

test("a removed jar drops its stale stubs (full rebuild)", async () => {
  using dir = TempDir.create("mcp-reload-");
  mkdirSync(join(dir.path, "lib"));
  copyFileSync(UTIL_JAR, join(dir.path, "lib", "util.jar"));
  writeConfigFile(dir.path, "lib", base);
  const client = await startClient(dir.path);
  expect(await describeSymbol(client, "lib.Util")).toContain(UTIL_STUB_URI);
  rmSync(join(dir.path, "lib", "util.jar"));
  expect(await describeSymbol(client, "lib.Util")).not.toContain(UTIL_STUB_URI);
});

test("a rewritten cappu.json is reloaded on the next call", async () => {
  using dir = TempDir.create("mcp-reload-");
  mkdirSync(join(dir.path, "libA"));
  mkdirSync(join(dir.path, "libB"));
  copyFileSync(UTIL_JAR, join(dir.path, "libB", "util.jar"));
  writeConfigFile(dir.path, "libA", base);
  const client = await startClient(dir.path);
  expect(await describeSymbol(client, "lib.Util")).not.toContain(UTIL_STUB_URI);
  writeConfigFile(dir.path, "libB", new Date(base.getTime() + 60_000));
  expect(await describeSymbol(client, "lib.Util")).toContain(UTIL_STUB_URI);
});

test("a malformed cappu.json edit keeps the last good config", async () => {
  using dir = TempDir.create("mcp-reload-");
  mkdirSync(join(dir.path, "lib"));
  copyFileSync(UTIL_JAR, join(dir.path, "lib", "util.jar"));
  writeConfigFile(dir.path, "lib", base);
  const client = await startClient(dir.path);
  expect(await describeSymbol(client, "lib.Util")).toContain(UTIL_STUB_URI);
  const configPath = join(dir.path, "cappu.json");
  writeFileSync(configPath, "{ nope");
  const later = new Date(base.getTime() + 60_000);
  utimesSync(configPath, later, later);
  // logged once, old state kept on every later call
  expect(await describeSymbol(client, "lib.Util")).toContain(UTIL_STUB_URI);
  expect(await describeSymbol(client, "lib.Util")).toContain(UTIL_STUB_URI);
});

// The tool surface must match the Go port's: same names, so a client can talk to
// either build. `organize_imports` is the source action `code_actions` cannot
// serve, since that one needs a selection.
test("the tool list includes organize_imports", async () => {
  using dir = TempDir.create("mcp-tools-");
  writeFileSync(join(dir.path, "C.java"), "package app;\nclass C {}\n");
  const client = await startClient(dir.path);
  const names = (await client.listTools()).tools.map(t => t.name);
  expect(names).toContain("organize_imports");
  expect(names).toContain("code_actions");
  expect(names).toContain("decompile");
  expect(names).toContain("format");
});

async function callTool(
  client: Client,
  name: string,
  args: Record<string, unknown>,
): Promise<{ text: string; isError: boolean }> {
  const result = await client.callTool({ name, arguments: args });
  return {
    text: (result.content as Array<{ text: string }>)[0].text,
    isError: result.isError === true,
  };
}

test("decompile reads a class from the classPath (a jar under a directory) or a file", async () => {
  using dir = TempDir.create("mcp-decompile-");
  mkdirSync(join(dir.path, "lib"));
  copyFileSync(UTIL_JAR, join(dir.path, "lib", "util.jar"));
  writeConfigFile(dir.path, "lib", base);
  const client = await startClient(dir.path);

  let r = await callTool(client, "decompile", { className: "lib.Util" });
  expect(r.isError).toBe(false);
  expect(r.text).toContain("package lib;");
  expect(r.text).toContain("class Util");
  r = await callTool(client, "decompile", { className: "lib.Util", disasm: true });
  expect(r.isError).toBe(false);
  expect(r.text).toContain("Code:");
  r = await callTool(client, "decompile", { className: "lib.Missing" });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("class lib.Missing not found on the classPath");
  r = await callTool(client, "decompile", { file: join(dir.path, "nope.class") });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("nope.class: no such file or directory");
  r = await callTool(client, "decompile", {});
  expect(r.isError).toBe(true);
  expect(r.text).toContain("give exactly one of file or className");
  r = await callTool(client, "decompile", { file: "", className: "lib.Util" });
  expect(r.isError).toBe(false);
  expect(r.text).toContain("class Util");
  const jar = join(dir.path, "lib", "util.jar");
  r = await callTool(client, "decompile", { file: jar, className: "lib.Util" });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("give exactly one of file or className");
  // A jar is not a class file; the error names what was read.
  r = await callTool(client, "decompile", { file: jar });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("util.jar: not a class file");
  // Arguments are typed (zod), as the Go build validates them.
  r = await callTool(client, "decompile", { className: "lib.Util", disasm: "true" });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("expected boolean");
  r = await callTool(client, "decompile", { file: null, className: "lib.Util" });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("expected string");

  const classFile = join(dir.path, "Util.class");
  writeFileSync(
    classFile,
    readZipEntries(readFileSync(UTIL_JAR))!
      .find(e => e.name === "lib/Util.class")!
      .read(),
  );
  r = await callTool(client, "decompile", { file: classFile });
  expect(r.isError).toBe(false);
  expect(r.text).toContain("class Util");
});

// The classPath is searched in order: a jar entry, then a directory entry
// holding the `.class` at its binary-name path or jars anywhere below it (in
// path order). Nested classes are named as in the bytecode.
test("decompile searches the classPath in order", async () => {
  using dir = TempDir.create("mcp-decompile-order-");
  const nested = join(
    import.meta.dirname,
    "..",
    "..",
    "togo",
    "internal",
    "compiler",
    "testdata",
    "classfiles",
    "nested",
  );
  const classes = join(dir.path, "classes", "lib");
  mkdirSync(classes, { recursive: true });
  writeFileSync(
    join(classes, "Util.class"),
    readZipEntries(readFileSync(UTIL_JAR))!
      .find(e => e.name === "lib/Util.class")!
      .read(),
  );
  mkdirSync(join(dir.path, "jars", "b"), { recursive: true });
  copyFileSync(UTIL_JAR, join(dir.path, "jars", "b", "util.jar"));
  copyFileSync(UTIL_JAR, join(dir.path, "jars", "zz.jar"));
  const withClassPath = async (classPath: string[]) => {
    writeFileSync(
      join(dir.path, "cappu.json"),
      JSON.stringify({ compilerOptions: { classPath, sourcePaths: [] } }),
    );
    return startClient(dir.path);
  };
  let client = await withClassPath([nested, UTIL_JAR, "classes", "jars"]);
  let r = await callTool(client, "decompile", { className: "lib.Outer$Builder" });
  expect(r.isError).toBe(false);
  expect(r.text).toContain("class Outer$Builder");
  r = await callTool(client, "decompile", { className: "lib.Util" });
  expect(r.isError).toBe(false);
  expect(r.text).toContain("class Util");
  // Drop the jar entry: the `.class` under the directory serves it.
  client = await withClassPath(["classes", "jars"]);
  r = await callTool(client, "decompile", { className: "lib.Util" });
  expect(r.isError).toBe(false);
  // Only jars under a directory: `b/util.jar` sorts before `zz.jar`, so a
  // class only in the latter is still found, and one in both comes from b/.
  client = await withClassPath(["jars"]);
  r = await callTool(client, "decompile", { className: "lib.Util" });
  expect(r.isError).toBe(false);
});

test("decompile by className needs a project config", async () => {
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await startMcpServer(undefined, serverTransport);
  const client = new Client({ name: "test", version: "0.0.0" });
  await client.connect(clientTransport);
  const r = await callTool(client, "decompile", { className: "lib.Util" });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("className needs a project config");
});

test("format returns the formatted file and writes nothing", async () => {
  using dir = TempDir.create("mcp-format-");
  writeConfigFile(dir.path, "lib", base);
  const client = await startClient(dir.path);
  const file = join(dir.path, "A.java");
  writeFileSync(file, "class A {int x;}\n");

  let r = await callTool(client, "format", { file });
  expect(r.isError).toBe(false);
  let got = JSON.parse(r.text) as { formatted: string; changed: boolean };
  expect(got).toEqual({ formatted: "class A {\n  int x;\n}\n", changed: true });
  expect(readFileSync(file, "utf8")).toBe("class A {int x;}\n");

  writeFileSync(file, got.formatted);
  r = await callTool(client, "format", { file });
  got = JSON.parse(r.text) as { formatted: string; changed: boolean };
  expect(got.changed).toBe(false);

  writeFileSync(file, "class A {\n");
  r = await callTool(client, "format", { file });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("A.java: unsupported syntax");
  r = await callTool(client, "format", { file: join(dir.path, "B.java") });
  expect(r.isError).toBe(true);
  expect(r.text).toContain("B.java: no such file or directory");
  // The project's formatterOptions are followed.
  writeFileSync(file, "class A {int x;}\n");
  writeFileSync(
    join(dir.path, "cappu.json"),
    JSON.stringify({
      compilerOptions: { classPath: [], sourcePaths: [] },
      formatterOptions: { style: "aosp" },
    }),
  );
  r = await callTool(await startClient(dir.path), "format", { file });
  got = JSON.parse(r.text) as { formatted: string; changed: boolean };
  expect(got.formatted).toBe("class A {\n    int x;\n}\n");
});
