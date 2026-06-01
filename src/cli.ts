#!/usr/bin/env node

// Unified entry point: `cappu lsp` runs the language server over stdio;
// `cappu compile` runs the javac-lite bytecode compiler.

import yargs from "yargs";
import { hideBin } from "yargs/helpers";

import { runCompile } from "./compiler.ts";
import pkg from "../package.json" with { type: "json" };

await yargs(hideBin(process.argv))
  .scriptName("cappu")
  .command(
    "lsp",
    "Start the Java language server (JSON-RPC over stdio)",
    {},
    async () => {
      // Importing starts the server via its module side effects.
      await import("./server.ts");
    },
  )
  .command(
    "compile [files..]",
    "Compile .java files to .class bytecode",
    y =>
      y
        .positional("files", {
          describe: ".java source files to compile",
          type: "string",
          array: true,
          default: [] as string[],
        })
        .option("out-dir", {
          alias: "d",
          describe: "Output root for the package tree (default: current directory)",
          type: "string",
        }),
    args => {
      process.exit(runCompile(args.files, args.outDir));
    },
  )
  .demandCommand(1, "Specify a command: lsp or compile")
  .strict()
  .version(pkg.version)
  .help()
  .parse();
