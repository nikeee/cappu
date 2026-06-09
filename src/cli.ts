#!/usr/bin/env node

// Unified entry point: `cappu lsp` runs the language server over stdio;
// `cappu compile` runs the javac-lite bytecode compiler. Argument parsing uses
// Node's built-in util.parseArgs (no third-party dependency).

import { parseArgs } from "node:util";

import { runCompile } from "./compiler.ts";
import pkg from "../package.json" with { type: "json" };

const USAGE = `cappu ${pkg.version}

Usage:
  cappu lsp                          Start the Java language server (JSON-RPC over stdio)
  cappu compile [options] <file...>  Compile .java files to .class bytecode

Compile options:
  -d, --out-dir <dir>   Output root for the package tree (default: current directory)
  -q, --quiet           Do not print the path of each emitted .class file

Global:
  -h, --help            Show this help
      --version         Show the version
`;

async function main(argv: string[]): Promise<void> {
  const { values, positionals } = parseArgs({
    args: argv,
    allowPositionals: true,
    options: {
      "out-dir": { type: "string", short: "d" },
      quiet: { type: "boolean", short: "q", default: false },
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

  switch (command) {
    case "lsp": {
      // Imported lazily so `cappu compile` never loads the LSP transport stack.
      // startServer() begins reading stdin and keeps the process alive; no exit.
      const { startServer } = await import("./server.ts");
      startServer();
      return;
    }
    case "compile":
      process.exit(runCompile(files, values["out-dir"], values.quiet));
    default:
      process.stderr.write(`cappu: unknown command '${command}'\n\n${USAGE}`);
      process.exit(2);
  }
}

await main(process.argv.slice(2));
