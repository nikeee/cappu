// MCP server over stdio. Exposes the Java semantic engine to agents as tools.
// Mirrors server.ts (the LSP entry) but speaks the Model Context Protocol.
// Tool logic lives in mcp.ts (pure, tested); this module owns config-aware
// workspace loading, disk freshness and transport. Nothing happens at module
// load - the cli (`cappu mcp`) calls startMcpServer with the project config.

import { readFileSync, statSync } from "node:fs";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import type { Transport } from "@modelcontextprotocol/sdk/shared/transport.js";
import { z } from "zod";

import { createChecker } from "../compiler/checker.ts";
import { loadConfiguredPaths } from "../compiler/compiler.ts";
import { installJdkTypes } from "../compiler/jdkTypes.ts";
import { createProgram } from "../compiler/program.ts";
import { type CappuConfig, loadConfig } from "../config.ts";
import { classpathFingerprint, findSourceJavaFiles, pathToUri } from "../workspace.ts";
import { languageFeatures } from "./codeActions.ts";
import { createMcpTools } from "./mcp.ts";
import { createProjectTools } from "./mcpProject.ts";

/**
 * Build the program/checker from the project config, register every tool, and
 * connect the transport (stdio by default). Returns once connected; the
 * transport keeps the process alive.
 */
export async function startMcpServer(
  config?: CappuConfig,
  transport: Transport = new StdioServerTransport(),
): Promise<void> {
  let program = createProgram();
  let checker = createChecker(program);
  // Language-level features are fixed by the configured release; recomputed on
  // each rebuild so a cappu.json release change takes effect.
  let features = languageFeatures(config?.compilerOptions.release);
  let tools = createMcpTools(program, checker, features);
  let project: ReturnType<typeof createProjectTools> | undefined;
  const mtimes = new Map<string, number>();
  let cpFingerprint = new Map<string, number>();
  let configMtime: number | undefined;

  // rebuild replaces the whole semantic state from cfg. A classpath or config
  // change cannot be patched incrementally (loadClassPath never removes stubs
  // for classes that disappeared), so program, checker and tools are rebuilt
  // from scratch exactly like at startup. Tool handlers read the bindings at
  // call time, so registration need not re-run.
  function rebuild(cfg?: CappuConfig): void {
    config = cfg;
    program = createProgram();
    installJdkTypes(program, cfg);
    if (cfg) loadConfiguredPaths(program, cfg);
    checker = createChecker(program);
    features = languageFeatures(cfg?.compilerOptions.release);
    tools = createMcpTools(program, checker, features);
    if (cfg) project = createProjectTools(cfg);
    mtimes.clear();
    cpFingerprint = new Map();
    configMtime = undefined;
    if (cfg) {
      cpFingerprint = classpathFingerprint(cfg);
      if (cfg.configPath !== undefined) {
        try {
          configMtime = statSync(cfg.configPath).mtimeMs;
        } catch {
          // config file unreadable: treated as unchanged until it reappears
        }
      }
    }
  }
  rebuild(config);

  // refresh keeps tool results current with on-disk changes between calls.
  // A cappu.json edit reloads the config and rebuilds everything (malformed
  // edits keep the last good config, logged once); a classpath change (jar
  // added/removed/replaced, e.g. by `cappu install`) rebuilds too. Source
  // .java files are re-read individually by mtime. A config file appearing
  // (server started without one) or disappearing is deliberately not handled:
  // the tool list is a startup snapshot.
  function refresh(): void {
    if (!config) return;
    if (config.configPath !== undefined) {
      let mtime: number | undefined;
      try {
        mtime = statSync(config.configPath).mtimeMs;
      } catch {
        // config deleted mid-run: keep the last good state
      }
      if (mtime !== undefined && mtime !== configMtime) {
        // Record first: a broken file logs once, not per call, and is retried
        // only when it changes again.
        configMtime = mtime;
        try {
          rebuild(loadConfig(config.configPath));
        } catch (e) {
          process.stderr.write(`cappu: ${(e as Error).message} (keeping previous config)\n`);
        }
      }
    }
    const fp = classpathFingerprint(config);
    if (fp.size !== cpFingerprint.size || [...fp].some(([k, v]) => cpFingerprint.get(k) !== v)) {
      rebuild(config);
    }
    for (const path of findSourceJavaFiles(config)) {
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

  // Surfaced to the host in the initialize response (and typically given to the
  // model as system context). The server is deliberately read-only: it never
  // compiles, runs code, or writes files. Operations that produce artifacts or
  // execute the JVM are left to the cappu CLI so the user stays in control of
  // when writes happen.
  const instructions = [
    "cappu server read-only. Look at Java code and dependency tree. Never write file,",
    "never compile, never run code.",
    "",
    "Need write disk or run JVM? Use cappu CLI in shell:",
    "  - Build (.class / jar / fat-jar in ./dist):  cappu compile",
    "  - Run JUnit test:                            cappu test",
    "rename_symbol give you edits. You apply edits. Server not write them.",
    "",
    "Config file = cappu.json. Want schema? Run: cappu config-schema",
    "All commands: cappu help",
  ].join("\n");

  const server = new McpServer({ name: "cappu", version: "1.0.0" }, { instructions });

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
    "deprecated_uses",
    {
      description:
        "Find uses of @Deprecated methods and types, with each declaration's since/forRemoval. Omit `files` to scan the whole workspace.",
      inputSchema: { files: z.array(z.string()).optional() },
    },
    async args => {
      refresh();
      return ok(tools.deprecatedUses(args));
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

  server.registerTool(
    "find_implementations",
    {
      description:
        "For an interface/class: its subtypes. For a method: the overrides in those subtypes. `ref` as in describe_symbol.",
      inputSchema: { ref: z.string() },
    },
    async args => {
      refresh();
      return ok(tools.findImplementations(args));
    },
  );

  server.registerTool(
    "list_members",
    {
      description:
        "List a type's members (fields/methods/...), declared and inherited, each flagged. `ref` is a type FQN or simple name.",
      inputSchema: { ref: z.string() },
    },
    async args => {
      refresh();
      return ok(tools.listMembers(args));
    },
  );

  server.registerTool(
    "find_callers",
    {
      description: "Find the call sites of a method (call hierarchy). `ref` as in describe_symbol.",
      inputSchema: { ref: z.string() },
    },
    async args => {
      refresh();
      return ok(tools.findCallers(args));
    },
  );

  server.registerTool(
    "type_hierarchy",
    {
      description:
        "Supertypes (extends/implements, walked up) and subtypes of a type. `ref` as in describe_symbol.",
      inputSchema: { ref: z.string() },
    },
    async args => {
      refresh();
      return ok(tools.typeHierarchy(args));
    },
  );

  server.registerTool(
    "resolve_import",
    {
      description: 'Fully-qualified import candidates for an unqualified type name (e.g. "List").',
      inputSchema: { name: z.string() },
    },
    async args => {
      refresh();
      return ok(tools.resolveImport(args));
    },
  );

  server.registerTool(
    "rename_symbol",
    {
      description:
        "The workspace edits a rename would make (returned for you to apply; nothing is written). `ref` as in describe_symbol.",
      inputSchema: { ref: z.string(), newName: z.string() },
    },
    async args => {
      refresh();
      return ok(tools.renameSymbol(args));
    },
  );

  server.registerTool(
    "code_actions",
    {
      description:
        "The refactorings and quick fixes offered for a selection in a file (edits returned for you to apply; nothing is written). Positions are 1-based; endLine/endColumn default to the start.",
      inputSchema: {
        file: z.string(),
        startLine: z.number(),
        startColumn: z.number(),
        endLine: z.number().optional(),
        endColumn: z.number().optional(),
      },
    },
    async args => {
      refresh();
      return ok(tools.codeActions(args));
    },
  );

  // Project tools resolve dependencies from the configured sources, so they
  // only make sense with a loaded project config. They do not touch the Java
  // program (no refresh()).
  if (config) {
    server.registerTool(
      "audit",
      {
        description:
          "Scan the project's resolved dependencies (transitive) for known vulnerabilities (OSV).",
        inputSchema: {},
      },
      async () => ok(await project!.audit()),
    );

    server.registerTool(
      "licenses",
      {
        description:
          "List every resolved dependency and the license it ships under (best-effort SPDX).",
        inputSchema: {},
      },
      async () => ok(await project!.licenses()),
    );

    server.registerTool(
      "search_packages",
      {
        description:
          "Search the configured package sources; returns group:artifact:version coords.",
        inputSchema: { query: z.string() },
      },
      async args => ok(await project!.searchPackages(args)),
    );

    server.registerTool(
      "outdated",
      {
        description:
          "Declared dependencies with a newer conflict-free stable version available (preview of `cappu update`; writes nothing).",
        inputSchema: {},
      },
      async () => ok(await project!.outdated()),
    );

    server.registerTool(
      "latest_version",
      {
        description: "The newest published version of a `group:artifact` across the sources.",
        inputSchema: { coord: z.string() },
      },
      async args => ok(await project!.latestVersion(args)),
    );

    server.registerTool(
      "dependency_tree",
      {
        description:
          "The resolved transitive dependency graph, or - with `coord` (group:artifact:version) - the path that pulls it onto the classpath.",
        inputSchema: { coord: z.string().optional() },
      },
      async args => ok(await project!.dependencyTree(args)),
    );
  }

  await server.connect(transport);
}
