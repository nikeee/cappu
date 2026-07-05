// Integration tests for the MCP server's config/classpath live reload: a real
// client over an in-memory transport, real files on disk. The jar fixture is
// the same util.jar (lib.Util) the Go port's tests use.
import { copyFileSync, mkdirSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { test } from "node:test";
import { join } from "node:path";

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { expect } from "expect";

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
  writeFileSync(path, JSON.stringify({ compilerOptions: { classPath: [classPath], sourcePaths: [] } }));
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
