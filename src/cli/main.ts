#!/usr/bin/env node

// Unified entry point: parses arguments with Node's built-in util.parseArgs
// and dispatches each subcommand to its own module under cli/ (one file per
// command). The whole script runs as top-level await.

import { parseArgs } from "node:util";

import { loadConfig } from "../config.ts";
import { runAdd } from "./add.ts";
import { runCompileCommand } from "./compile.ts";
import { runInit } from "./init.ts";
import { runInstall } from "./install.ts";
import { runLsp } from "./lsp.ts";
import { runSearch } from "./search.ts";
import { runCacheCommand } from "./cache.ts";
import { runSelfUpgrade } from "./selfUpgrade.ts";
import { runUpdate } from "./update.ts";
import { runAudit } from "./audit.ts";
import { runLicenses } from "./licenses.ts";
import { runVerify } from "./verify.ts";
import { formatDuration, painter } from "./style.ts";
import { runTestCommand } from "./test.ts";
import pkg from "../../package.json" with { type: "json" };

const USAGE = `
cappu ${pkg.version}

Usage:
  cappu init [--with-schema]         Write a starter cappu.json (commented, all options);
                                     --with-schema also writes cappu.schema.json
  cappu install [-v]                 Download the cappu.json dependencies (transitively)
                                     into .cappu/lib/classes; prints a per-category
                                     count, or each jar path with -v/--verbose
  cappu update                       Bump declared dependencies to the newest stable
                                     versions that keep the tree conflict-free
  cappu verify                       Check the installed lib jars against the
                                     SHA-256 sums in cappu-lock.json
  cappu audit [--no-cache]           Scan resolved dependencies for known
                                     vulnerabilities (OSV); no fixing.
                                     --no-cache ignores all caches (fresh scan)
  cappu licenses [--json]            Print every resolved dependency and the
                                     license it ships under (best-effort SPDX);
                                     --json emits it machine-readable
  cappu add <configuration> <coord...>  Add one or more group:artifact[@version] to the
                                     dependencies section (api or implementation) and
                                     install them
  cappu search <query>               Search the configured package sources; prints
                                     group:artifact@latest-version per match
  cappu test                         Compile src/test/java and run the JUnit
                                     Platform console launcher over it
  cappu self-upgrade                 Replace this binary with the latest CD build
                                     (needs GITHUB_TOKEN or \`gh auth login\`)
  cappu cache clean                  Remove the global download cache
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
        "out-dir": { type: "string", short: "d" },
        output: { type: "string", short: "o" },
        // No defaults: an absent flag must stay undefined so cappu.json
        // can supply the value (an explicit flag always wins).
        quiet: { type: "boolean", short: "q" },
        verbose: { type: "boolean", short: "v" },
        "fail-on-degrade": { type: "boolean" },
        "with-schema": { type: "boolean", default: false },
        validate: { type: "boolean", default: false },
        "experimental-compiler": { type: "boolean" },
        json: { type: "boolean", default: false },
        "no-cache": { type: "boolean", default: false },
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

if (values.version) {
  process.stdout.write(`${pkg.version}\n`);
  process.exit(0);
}
if (values.help || command === undefined) {
  process.stdout.write(USAGE);
  process.exit(values.help ? 0 : 2);
}

// Print how long the dependency/build commands took, however they exit. lsp
// runs until the client disconnects, so a duration there is meaningless.
const TIMED_COMMANDS = new Set([
  "install",
  "update",
  "add",
  "audit",
  "licenses",
  "verify",
  "compile",
  "test",
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

// init, cache and self-upgrade run before loadConfig: none depends on (nor
// should be blocked by) an existing, possibly broken project config -
// self-upgrade is global and must work even when the cwd's cappu.json is bad.
if (command === "init") runInit(values.config, values["with-schema"]);
if (command === "cache") runCacheCommand(files);
if (command === "self-upgrade") await runSelfUpgrade();

let config;
try {
  config = loadConfig(values.config);
} catch (e) {
  process.stderr.write(`cappu: ${(e as Error).message}\n`);
  process.exit(2);
}

// verify needs config but returns `never`, so it runs here (not in the switch,
// where a never-returning case makes the break unreachable).
if (command === "verify") runVerify(config);

switch (command) {
  case "add":
    await runAdd(files[0], files.slice(1), values.config, config);
    break;
  case "install":
    await runInstall(config, { verbose: values.verbose });
    break;
  case "update":
    await runUpdate(values.config, config);
    break;
  case "audit":
    await runAudit(config, { noCache: values["no-cache"] });
    break;
  case "licenses":
    await runLicenses(config, { json: values.json });
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
