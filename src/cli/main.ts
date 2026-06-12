#!/usr/bin/env node

// Unified entry point: parses arguments with Node's built-in util.parseArgs
// and dispatches to one command module per subcommand (init.ts, install.ts,
// lsp.ts, compile.ts). The whole script runs as top-level await.

import { parseArgs } from "node:util";

import { loadConfig } from "../config.ts";
import { runAdd } from "./add.ts";
import { runCompileCommand } from "./compile.ts";
import { runInit } from "./init.ts";
import { runInstall } from "./install.ts";
import { runLsp } from "./lsp.ts";
import { runSearch } from "./search.ts";
import { runTestCommand } from "./test.ts";
import pkg from "../../package.json" with { type: "json" };

const USAGE = `
cappu ${pkg.version}

Usage:
  cappu init [--with-schema]         Write a starter cappu.json (commented, all options);
                                     --with-schema also writes cappu.schema.json
  cappu install                      Download the cappu.json dependencies (transitively)
                                     into lib/classes
  cappu add <configuration> <coord...>  Add one or more group:artifact[@version] to the
                                     dependencies section (api or implementation) and
                                     install them
  cappu search <query>               Search the configured package sources; prints
                                     group:artifact@latest-version per match
  cappu test                         Compile src/test/java and run the JUnit
                                     Platform console launcher over it
  cappu lsp [options]                Start the Java language server (JSON-RPC over stdio)
  cappu compile [options] [file...]  Compile .java files to .class bytecode; with no
                                     files, compile everything under the configured
                                     sourcePaths (a project build)

Options:
  -c, --config <file>   Project config (default: ./cappu.json, JSONC).
                        Sections: "compilerOptions" (classPath, sourcePaths,
                        outDir, quiet, failOnDegrade) and "lspOptions"
                        (inlayHints). Command-line flags take precedence.

Lsp options:
  -p, --port <port>     Listen on a TCP port instead of stdio; the first client
                        to connect gets the session (the server exits when it
                        disconnects)

Compile options:
  -d, --out-dir <dir>   Output root for the build artifacts (default:
                        compilerOptions.outDir, then ./dist)
  -o, --output <kind>   What to produce in the output root: "classes" (a
                        package tree usable as java -cp <dir>), "jar", or
                        "fat-jar" (includes the dependency jars' contents)
  -q, --quiet           Do not print the path of each emitted .class file
      --fail-on-degrade Fail when a method body degrades to a placeholder
                        (an unsupported construct; needs --experimental-compiler)
      --validate        Also compile with javac and fail unless the normalized
                        bytecode matches (needs --experimental-compiler)
      --experimental-compiler
                        Compile with cappu's own compiler instead of javac
                        (the default delegates to the configured/provisioned
                        javac entirely)

Global:
  -h, --help            Show this help
      --version         Show the version
`.trimStart();

const { values, positionals } = parseArgs({
  args: process.argv.slice(2),
  allowPositionals: true,
  options: {
    config: { type: "string", short: "c" },
    port: { type: "string", short: "p" },
    "out-dir": { type: "string", short: "d" },
    output: { type: "string", short: "o" },
    // No defaults: an absent flag must stay undefined so cappu.json
    // can supply the value (an explicit flag always wins).
    quiet: { type: "boolean", short: "q" },
    "fail-on-degrade": { type: "boolean" },
    "with-schema": { type: "boolean", default: false },
    validate: { type: "boolean", default: false },
    "experimental-compiler": { type: "boolean" },
    help: { type: "boolean", short: "h", default: false },
    version: { type: "boolean", default: false },
  },
});

const [command, ...files] = positionals;

if (values.version) {
  process.stdout.write(`${pkg.version}\n`);
  process.exit(0);
}
if (values.help || command === undefined) {
  process.stdout.write(USAGE);
  process.exit(values.help ? 0 : 2);
}

// init runs before loadConfig: bootstrapping must not depend on (or be
// blocked by) an existing, possibly broken config.
if (command === "init") runInit(values.config, values["with-schema"]);

let config;
try {
  config = loadConfig(values.config);
} catch (e) {
  process.stderr.write(`cappu: ${(e as Error).message}\n`);
  process.exit(2);
}

switch (command) {
  case "add":
    await runAdd(files[0], files.slice(1), values.config, config);
    break;
  case "install":
    await runInstall(config);
    break;
  case "search": {
    const query = files.join(" ").trim();
    if (query === "") {
      process.stderr.write("cappu: search needs a query, e.g. `cappu search gson`\n");
      process.exit(2);
    }
    await runSearch(query, config);
    break;
  }
  case "lsp":
    await runLsp(config, values.port);
    break;
  case "test":
    await runTestCommand(config);
    break;
  case "compile":
    await runCompileCommand(
      files,
      {
        outDir: values["out-dir"],
        output: values.output,
        quiet: values.quiet,
        failOnDegrade: values["fail-on-degrade"],
        experimentalCompiler: values["experimental-compiler"],
        validate: values.validate,
      },
      config,
    );
    break;
  default:
    process.stderr.write(`cappu: unknown command '${command}'\n\n${USAGE}`);
    process.exit(2);
}
