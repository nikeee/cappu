// MCP server over stdio. Exposes the Java semantic engine to agents as tools.
// Mirrors server.ts (the LSP entry) but speaks the Model Context Protocol.
// Tool logic lives in mcp.ts (pure, tested); this module owns disk freshness
// and transport. Run with: cappu mcp

import { readFileSync, statSync } from "node:fs";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

import { createChecker } from "./checker.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createMcpTools } from "./mcp.ts";
import { createProgram } from "./program.ts";
import { findJavaFiles, pathToUri } from "./workspace.ts";

const program = createProgram();
loadJdkStub(program);
const checker = createChecker(program);
const tools = createMcpTools(program, checker);

// Agents edit files on disk between calls. Re-read any .java file whose mtime
// changed (or that is new) before each tool call so results stay current.
// addProjectFile clears that file's parse/bind cache, so the next query
// re-parses only what is stale.
const root = process.cwd();
const mtimes = new Map<string, number>();
function refresh(): void {
  for (const path of findJavaFiles(root)) {
    let mtime: number;
    try {
      mtime = statSync(path).mtimeMs;
    } catch {
      continue;
    }
    if (mtimes.get(path) === mtime) continue;
    try {
      program.addProjectFile(pathToUri(path), readFileSync(path, "utf8"));
      mtimes.set(path, mtime);
    } catch {
      // unreadable file: skip
    }
  }
}

const server = new McpServer({ name: "cappu", version: "1.0.0" });

function ok(data: unknown) {
  return { content: [{ type: "text" as const, text: JSON.stringify(data, null, 2) }] };
}

server.registerTool(
  "diagnostics",
  {
    description:
      "Java syntax, binding and type diagnostics. Omit `files` to check the whole workspace.",
    inputSchema: { files: z.array(z.string()).optional() },
  },
  async args => {
    refresh();
    return ok(tools.diagnostics(args));
  },
);

server.registerTool(
  "outline",
  {
    description: "Top-level type/member outline of one Java file.",
    inputSchema: { file: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.outline(args));
  },
);

server.registerTool(
  "search_symbols",
  {
    description: "Find indexed Java types whose fully-qualified name contains `query`.",
    inputSchema: { query: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.searchSymbols(args));
  },
);

server.registerTool(
  "describe_symbol",
  {
    description:
      "Describe a symbol (kind, signature, Javadoc, definition). `ref` is a type FQN or simple name, or `Type#member` (e.g. `java.util.List#add`).",
    inputSchema: { ref: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.describeSymbol(args));
  },
);

server.registerTool(
  "find_definition",
  {
    description: "Locate where a symbol is declared. `ref` as in describe_symbol.",
    inputSchema: { ref: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.findDefinition(args));
  },
);

server.registerTool(
  "find_references",
  {
    description: "Find every use of a symbol across the workspace. `ref` as in describe_symbol.",
    inputSchema: { ref: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.findReferences(args));
  },
);

await server.connect(new StdioServerTransport());
