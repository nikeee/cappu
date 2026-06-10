#!/usr/bin/env node

// Unified entry point: `cappu lsp` runs the language server over stdio;
// `cappu compile` runs the javac-lite bytecode compiler. Argument parsing uses
// Node's built-in util.parseArgs (no third-party dependency).

import { existsSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseArgs } from "node:util";

import { runCompile } from "./compiler.ts";
import { CONFIG_TEMPLATE, DEFAULT_CONFIG_NAME, loadConfig } from "./config.ts";
import pkg from "../package.json" with { type: "json" };

const USAGE = `cappu ${pkg.version}

Usage:
  cappu init                         Write a starter cappu.json (commented, all options)
  cappu lsp [options]                Start the Java language server (JSON-RPC over stdio)
  cappu compile [options] <file...>  Compile .java files to .class bytecode

Options:
  -c, --config <file>   Project config (default: ./cappu.json, JSONC).
                        Sections: "compilerOptions" (classPath, sourcePaths,
                        outDir, quiet, failOnDegrade) and "lspOptions"
                        (inlayHints). Command-line flags take precedence.

Compile options:
  -d, --out-dir <dir>   Output root for the package tree (default: current directory)
  -q, --quiet           Do not print the path of each emitted .class file
      --fail-on-degrade Fail when a method body degrades to a placeholder
                        (an unsupported construct); degradations always warn

Global:
  -h, --help            Show this help
      --version         Show the version
`;

async function main(argv: string[]): Promise<void> {
  const { values, positionals } = parseArgs({
    args: argv,
    allowPositionals: true,
    options: {
      config: { type: "string", short: "c" },
      "out-dir": { type: "string", short: "d" },
      // No defaults: an absent flag must stay undefined so cappu.json
      // can supply the value (an explicit flag always wins).
      quiet: { type: "boolean", short: "q" },
      "fail-on-degrade": { type: "boolean" },
      help: { type: "boolean", short: "h", default: false },
      version: { type: "boolean", default: false },
    },
  });

  if (values.version) {
    process.stdout.write(`${pkg.version}\n`);
    return;
  }
  const [command, ...files] = positionals;
  if (values.help || command === undefined) {
    process.stdout.write(USAGE);
    process.exitCode = values.help ? 0 : 2;
    return;
  }

  // init runs before loadConfig: bootstrapping must not depend on (or be
  // blocked by) an existing, possibly broken config.
  if (command === "init") {
    const target = resolve(values.config ?? DEFAULT_CONFIG_NAME);
    if (existsSync(target)) {
      process.stderr.write(`cappu: ${target} already exists, not overwriting\n`);
      process.exit(1);
    }
    writeFileSync(target, CONFIG_TEMPLATE);
    process.stdout.write(`${target}\n`);
    return;
  }

  let config;
  try {
    config = loadConfig(values.config);
  } catch (e) {
    process.stderr.write(`cappu: ${(e as Error).message}\n`);
    process.exit(2);
  }

  switch (command) {
    case "lsp": {
      // Imported lazily so `cappu compile` never loads the LSP transport stack.
      // startServer() begins reading stdin and keeps the process alive; no exit.
      const { startServer } = await import("./server.ts");
      startServer(config);
      return;
    }
    case "compile":
      process.exit(
        runCompile(files, {
          outDir: values["out-dir"],
          quiet: values.quiet,
          failOnDegrade: values["fail-on-degrade"],
          config,
        }),
      );
    default:
      process.stderr.write(`cappu: unknown command '${command}'\n\n${USAGE}`);
      process.exit(2);
  }
}

await main(process.argv.slice(2));
