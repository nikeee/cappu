#!/usr/bin/env node

// Unified entry point: parses arguments with Node's built-in util.parseArgs
// and dispatches each subcommand to its own module under cli/ (one file per
// command). The whole script runs as top-level await.

import { parseArgs } from "node:util";

import { loadConfig } from "../config.ts";
import { runAdd } from "./add.ts";
import { runRemove } from "./remove.ts";
import { runOutdated } from "./outdated.ts";
import { runCompileCommand } from "./compile.ts";
import { runCheckCommand } from "./check.ts";
import { runFormat } from "./format.ts";
import { runConfigSchema } from "./configSchema.ts";
import { runInit } from "./init.ts";
import { runInstall } from "./install.ts";
import { runLsp } from "./lsp.ts";
import { runMcp } from "./mcp.ts";
import { runDap } from "./dap.ts";
import { runSearch } from "./search.ts";
import { runCacheCommand } from "./cache.ts";
import { runSelfUpgrade } from "./selfUpgrade.ts";
import { runRage } from "./rage.ts";
import { runUpdate } from "./update.ts";
import { runAudit } from "./audit.ts";
import { runLicenses } from "./licenses.ts";
import { runPublish } from "./publish.ts";
import { runVersion } from "./version.ts";
import { runVerify } from "./verify.ts";
import { runTree } from "./tree.ts";
import { formatDuration, painter } from "./style.ts";
import { emitAnnotation } from "./annotations.ts";
import { agentEnabled } from "./agent.ts";
import { runTestCommand } from "./test.ts";
import { runRunCommand } from "./run.ts";
import pkg from "../../package.json" with { type: "json" };

// Help is data-driven so it can be grouped and coloured (bun-style). Each row is
// a command/flag, its arg syntax, and a description; descriptions are single
// strings and word-wrapped to the terminal at render time. painter() makes the
// colours a no-op under NO_COLOR / an agent / a pipe, so plain output is intact.
type HelpRow = { name: string; args?: string; desc: string };
type HelpColor = "cyan" | "green" | "magenta" | "yellow" | "blue";
type HelpGroup = { title: string; color: HelpColor; rows?: HelpRow[]; note?: string };

const COMMAND_GROUPS: HelpGroup[] = [
  {
    title: "Project",
    color: "cyan",
    rows: [
      {
        name: "init",
        args: "[-y] [--with-schema]",
        desc: "Scaffold a project: ask for the coordinates and build output and write cappu.json (-y/--yes takes defaults); --with-schema also writes cappu.schema.json",
      },
      { name: "config-schema", desc: "Print the JSON Schema for cappu.json to stdout" },
    ],
  },
  {
    title: "Dependencies",
    color: "cyan",
    rows: [
      {
        name: "install",
        args: "[-v] [--locked]",
        desc: "Download the cappu.json dependencies (transitively) into .cappu/lib/classes; prints a per-category count, or each jar path with -v/--verbose. --locked fails (without downloading) if cappu-lock.json is stale or missing (for CI)",
      },
      {
        name: "update",
        desc: "Bump declared dependencies to the newest stable versions that keep the tree conflict-free",
      },
      {
        name: "outdated",
        desc: "List declared dependencies that have a newer published version (current/wanted/latest)",
      },
      {
        name: "tree",
        args: "[--json]",
        desc: "Print the resolved dependency graph as an indented tree, one section per configuration (api, implementation, annotationProcessor, testImplementation); --json emits the forest machine-readable",
      },
      {
        name: "add",
        args: "<configuration> <coord...>",
        desc: "Add one or more group:artifact[:version] to the dependencies section (api, implementation, annotationProcessor or testImplementation) and install them",
      },
      {
        name: "remove",
        args: "<configuration> <coord...>",
        desc: "Remove one or more group:artifact from the named dependencies section and re-resolve",
      },
      {
        name: "audit",
        args: "[--no-cache] [--format text|sarif]",
        desc: "Scan resolved dependencies for known vulnerabilities (OSV); no fixing. --no-cache ignores all caches (fresh scan); --format sarif emits a SARIF 2.1.0 log for code-scanning upload (default: text)",
      },
      {
        name: "licenses",
        args: "[--json]",
        desc: "Print every resolved dependency and the license it ships under (best-effort SPDX); --json emits it machine-readable",
      },
      {
        name: "verify",
        desc: "Check the installed lib jars against the SHA-256 sums in cappu-lock.json",
      },
      {
        name: "search",
        args: "<query> [--json]",
        desc: "Search the configured package sources; prints group:artifact@latest-version per match; --json emits the matches machine-readable",
      },
      {
        name: "publish",
        args: "[--repo <url>]",
        desc: "Build the jar, generate its POM, and upload both to a Maven registry (needs groupId/artifactId/version in cappu.json + creds). Registry: --repo, else $CAPPU_PUBLISH_REGISTRY, else publishRepository, else Maven Central",
      },
    ],
  },
  {
    title: "Build & run",
    color: "green",
    rows: [
      {
        name: "compile",
        args: "[options] [file...]",
        desc: "Compile .java files to .class bytecode; with no files, compile everything under the configured sourcePaths (a project build)",
      },
      {
        name: "check",
        args: "[file...]",
        desc: "Type-check with cappu's own checker (the LSP's diagnostics, more than javac emits) without writing any class files; with no files, check everything under the configured sourcePaths",
      },
      {
        name: "format",
        args: "[-w] [file...]",
        desc: "Check Java formatting (google-java-format compatible); with no files, every file under the configured sourcePaths. Lists unformatted files and exits non-zero; -w/--write rewrites them in place. Style and ignore globs: formatterOptions.",
      },
      {
        name: "run",
        args: "[-- <args>]",
        desc: "Compile the project and run it on the JVM: the configured compilerOptions.mainClass, else the single class declaring main(String[]). Arguments after -- are passed to the program",
      },
      {
        name: "test",
        desc: "Compile src/test/java and run the JUnit Platform console launcher over it",
      },
    ],
  },
  {
    title: "Servers",
    color: "magenta",
    rows: [
      {
        name: "lsp",
        args: "[options]",
        desc: "Start the Java language server (JSON-RPC over stdio)",
      },
      {
        name: "dap",
        args: "[options]",
        desc: "Start the debug adapter (Debug Adapter Protocol over stdio): compile the project with debug info, launch its mainClass under JDWP, and bridge breakpoints, stepping, stacks and locals to a DAP client (e.g. an editor)",
      },
      {
        name: "mcp",
        desc: "Start the MCP server for agents: name-addressed semantic tools (diagnostics, outline, describe/find symbols, members, callers, type hierarchy, import resolution, rename) plus project tools (audit, licenses, search/latest/outdated packages, dependency tree) over stdio",
      },
    ],
  },
  {
    title: "Maintenance",
    color: "yellow",
    rows: [
      {
        name: "version",
        args: "<major|minor|patch>",
        desc: "Bump the project version in cappu.json; at a git repo root, also commit it and tag v<version>",
      },
      {
        name: "self-upgrade",
        desc: "Replace this binary with the latest CD build (needs GITHUB_TOKEN or `gh auth login`)",
      },
      {
        name: "rage",
        args: "[--open]",
        desc: "Print version/environment info and the issue tracker URL; --open also opens it in your browser",
      },
      { name: "cache clean", desc: "Remove the global download cache" },
      {
        name: "cache verify",
        desc: "Check cached artifacts against the hashes recorded beside them",
      },
    ],
  },
];

const OPTION_GROUPS: HelpGroup[] = [
  {
    title: "Options",
    color: "blue",
    rows: [
      {
        name: "-c, --config <file>",
        desc: 'Project config (default: ./cappu.json, JSONC). Sections: "compilerOptions" (classPath, sourcePaths, quiet, experimentalCompiler) and "lspOptions" (inlayHints). Command-line flags take precedence.',
      },
    ],
  },
  {
    title: "Lsp options",
    color: "blue",
    rows: [
      {
        name: "-p, --port <port>",
        desc: "Listen on a TCP port instead of stdio; the first client to connect gets the session (the server exits when it disconnects)",
      },
    ],
  },
  {
    title: "Compile options",
    color: "blue",
    rows: [
      {
        name: "-o, --output <kind>",
        desc: 'What to produce in ./dist: "classes" (a package tree usable as java -cp <dir>), "jar", or "fat-jar" (includes the dependency jars\' contents)',
      },
      {
        name: "    --artifact <name>",
        desc: 'Jar base name in ./dist (e.g. "app" -> dist/app.jar); default <artifactId>-<version> or the project dir name',
      },
      { name: "-q, --quiet", desc: "Do not print the path of each emitted .class file" },
    ],
    note: "(cappu's experimental compiler and its failOnDegrade / validate options are configured in cappu.json under compilerOptions.experimentalCompiler.)",
  },
  {
    title: "Global",
    color: "blue",
    rows: [
      { name: "-h, --help", desc: "Show this help" },
      { name: "    --version", desc: "Show the version" },
    ],
    note: "When an AI agent drives cappu (AGENT, CLAUDECODE, CURSOR_AGENT, ... set), colour and animations are off and machine-readable output is implied where supported (audit emits SARIF; licenses, tree, search emit --json).",
  },
];

// Word-wrap to a width; never breaks a single long token.
function wrapText(text: string, width: number): string[] {
  const lines: string[] = [];
  let line = "";
  for (const word of text.split(" ")) {
    if (line && line.length + 1 + word.length > width) {
      lines.push(line);
      line = word;
    } else {
      line = line ? `${line} ${word}` : word;
    }
  }
  if (line) lines.push(line);
  return lines;
}

const HELP_NAME_COL = 30; // left column width (after the 2-space indent)

function renderUsage(stream: NodeJS.WriteStream): string {
  const paint = painter(stream);
  const cols = stream.columns && stream.columns > 50 ? Math.min(stream.columns, 100) : 80;
  const descCol = 2 + HELP_NAME_COL;
  const descWidth = Math.max(24, cols - descCol);

  const renderGroup = (group: HelpGroup): string => {
    let out = `\n${paint("bold", `${group.title}:`)}\n`;
    for (const row of group.rows ?? []) {
      const plainLeft = row.args ? `${row.name} ${row.args}` : row.name;
      const left = row.args
        ? `${paint([group.color, "bold"], row.name)} ${paint("dim", row.args)}`
        : paint([group.color, "bold"], row.name);
      const descLines = wrapText(row.desc, descWidth);
      if (plainLeft.length <= HELP_NAME_COL - 1) {
        out += `  ${left}${" ".repeat(HELP_NAME_COL - plainLeft.length)}${descLines[0]}\n`;
        for (const l of descLines.slice(1)) out += `${" ".repeat(descCol)}${l}\n`;
      } else {
        out += `  ${left}\n`;
        for (const l of descLines) out += `${" ".repeat(descCol)}${l}\n`;
      }
    }
    if (group.note) {
      out += "\n";
      for (const l of wrapText(group.note, cols - 2)) out += `  ${paint("dim", l)}\n`;
    }
    return out;
  };

  let out = `${paint("bold", "cappu")} ${paint("dim", pkg.version)} - a Java toolchain: package manager, compiler, formatter, language & debug servers\n\n`;
  out += `${paint("bold", "Usage:")} cappu ${paint("dim", "<command> [...flags] [...args]")}\n`;
  for (const group of [...COMMAND_GROUPS, ...OPTION_GROUPS]) out += renderGroup(group);
  return out;
}

// The IIFE keeps parseArgs's precise inferred result type (the catch path is
// `never`); a top-level `let` annotation would widen `values` to the generic
// union and lose the per-option string/boolean types.
const { values, positionals } = (() => {
  try {
    return parseArgs({
      args: process.argv.slice(2),
      allowPositionals: true,
      options: {
        config: { type: "string", short: "c" },
        port: { type: "string", short: "p" },
        output: { type: "string", short: "o" },
        artifact: { type: "string" },
        // No defaults: an absent flag must stay undefined so cappu.json
        // can supply the value (an explicit flag always wins).
        quiet: { type: "boolean", short: "q" },
        verbose: { type: "boolean", short: "v" },
        locked: { type: "boolean", default: false },
        write: { type: "boolean", short: "w", default: false },
        "with-schema": { type: "boolean", default: false },
        yes: { type: "boolean", short: "y", default: false },
        json: { type: "boolean", default: false },
        format: { type: "string" },
        "no-cache": { type: "boolean", default: false },
        repo: { type: "string" },
        open: { type: "boolean", default: false },
        help: { type: "boolean", short: "h", default: false },
        version: { type: "boolean", default: false },
      },
    });
  } catch (e) {
    // parseArgs throws on an unknown flag or a value that looks like a flag
    // (e.g. --port=-1); turn its message into a friendly one rather than a
    // bundled-source stack trace.
    process.stderr.write(`cappu: ${(e as Error).message}\nRun \`cappu --help\` for usage.\n`);
    process.exit(2);
  }
})();

const [command, ...files] = positionals;

// An AI agent driving cappu implies machine-readable output, the same way it
// implies NO_COLOR (see agentEnabled). --json stays an explicit opt-in for
// humans; under an agent it is on by default for the commands that support it.
// ponytail: presence-only flag, so there is no `--json=false` escape hatch under
// an agent; add one if a consumer ever needs text output from an agent context.
const json = values.json || agentEnabled();

if (values.version) {
  process.stdout.write(`${pkg.version}\n`);
  process.exit(0);
}
if (values.help || command === undefined) {
  process.stdout.write(renderUsage(process.stdout));
  process.exit(values.help ? 0 : 2);
}

// Print how long the dependency/build commands took, however they exit. lsp
// runs until the client disconnects, so a duration there is meaningless.
const TIMED_COMMANDS = new Set([
  "install",
  "update",
  "add",
  "remove",
  "audit",
  "licenses",
  "tree",
  "publish",
  "verify",
  "compile",
  "check",
  "test",
  "format",
]);
if (TIMED_COMMANDS.has(command)) {
  const startedAt = performance.now();
  const paint = painter(process.stderr);
  process.on("exit", () => {
    process.stderr.write(
      paint("dim", `done in ${formatDuration(performance.now() - startedAt)}\n`),
    );
  });
}

// init, cache, self-upgrade and rage run before loadConfig: none depends on
// (nor should be blocked by) an existing, possibly broken project config -
// self-upgrade is global and must work even when the cwd's cappu.json is bad.
// Each handler exits the process, so control never falls through to loadConfig.
switch (command) {
  case "init":
    await runInit(values.config, values["with-schema"], { yes: values.yes });
    break; // runInit exits; break keeps the (async) case from "falling through"
  case "cache":
    runCacheCommand(files);
  // falls through: runCacheCommand exits the process (never returns)
  case "self-upgrade":
    await runSelfUpgrade();
    break;
  case "rage":
    await runRage(values.open);
    break;
  case "config-schema":
    runConfigSchema();
  // falls through: runConfigSchema exits the process (never returns)
}

let config;
try {
  config = loadConfig(values.config);
} catch (e) {
  process.stderr.write(`cappu: ${(e as Error).message}\n`);
  emitAnnotation("error", (e as Error).message);
  process.exit(2);
}

switch (command) {
  case "verify":
    runVerify(config);
  case "check":
    runCheckCommand(files, config);
  case "add":
    await runAdd(files[0], files.slice(1), values.config, config);
    break;
  case "remove":
    await runRemove(files[0], files.slice(1), values.config, config);
    break;
  case "outdated":
    await runOutdated(config);
    break;
  case "install":
    await runInstall(config, { verbose: values.verbose, locked: values.locked });
    break;
  case "update":
    await runUpdate(values.config, config);
    break;
  case "version":
    await runVersion(files[0], values.config, config);
    break;
  case "audit": {
    if (values.json) {
      process.stderr.write("cappu: `audit` uses --format (text|sarif), not --json\n");
      process.exit(2);
    }
    const format = values.format ?? (agentEnabled() ? "sarif" : "text");
    if (format !== "text" && format !== "sarif") {
      process.stderr.write(`cappu: unknown --format '${format}' (expected: text, sarif)\n`);
      process.exit(2);
    }
    await runAudit(config, { noCache: values["no-cache"], format });
    break;
  }
  case "licenses":
    await runLicenses(config, { json });
    break;
  case "tree":
    await runTree(config, { json });
    break;
  case "publish":
    await runPublish(config, { repo: values.repo });
    break;
  case "search": {
    const query = files.join(" ").trim();
    if (query === "") {
      process.stderr.write("cappu: search needs a query, e.g. `cappu search gson`\n");
      process.exit(2);
    }
    await runSearch(query, config, { json });
    break;
  }
  case "lsp":
    await runLsp(config, values.port);
    break;
  case "mcp":
    await runMcp(config);
    break;
  case "dap":
    await runDap(config, values.port);
    break;
  case "test":
    await runTestCommand(config);
    break;
  case "run":
    await runRunCommand(files, config);
    break;
  case "compile":
    await runCompileCommand(
      files,
      {
        output: values.output,
        artifact: values.artifact,
        quiet: values.quiet,
      },
      config,
    );
    break;
  case "format":
    await runFormat(files, { write: values.write }, config);
    break;
  default:
    process.stderr.write(`cappu: unknown command '${command}'\n\n${renderUsage(process.stderr)}`);
    process.exit(2);
}
