#!/usr/bin/env node

// Unified entry point: `cappu init` bootstraps a config, `cappu lsp` runs the
// language server (stdio or --port socket), `cappu compile` runs the
// javac-lite bytecode compiler. Argument parsing uses Node's built-in
// util.parseArgs; the whole script runs as top-level await.

import { once } from "node:events";
import { existsSync, writeFileSync } from "node:fs";
import type { Socket } from "node:net";
import { resolve } from "node:path";
import { parseArgs } from "node:util";

import { missingConfiguredPaths, runCompile } from "./compiler.ts";
import { CONFIG_TEMPLATE, DEFAULT_CONFIG_NAME, loadConfig } from "./config.ts";
import pkg from "../package.json" with { type: "json" };

const USAGE = `
cappu ${pkg.version}

Usage:
  cappu init                         Write a starter cappu.json (commented, all options)
  cappu lsp [options]                Start the Java language server (JSON-RPC over stdio)
  cappu compile [options] <file...>  Compile .java files to .class bytecode

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
  -d, --out-dir <dir>   Output root for the package tree (default: current directory)
  -q, --quiet           Do not print the path of each emitted .class file
      --fail-on-degrade Fail when a method body degrades to a placeholder
                        (an unsupported construct); degradations always warn

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
    // No defaults: an absent flag must stay undefined so cappu.json
    // can supply the value (an explicit flag always wins).
    quiet: { type: "boolean", short: "q" },
    "fail-on-degrade": { type: "boolean" },
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
if (command === "init") {
  const target = resolve(values.config ?? DEFAULT_CONFIG_NAME);
  if (existsSync(target)) {
    process.stderr.write(`cappu: ${target} already exists, not overwriting\n`);
    process.exit(1);
  }
  writeFileSync(target, CONFIG_TEMPLATE);
  process.stdout.write(`${target}\n`);
  process.exit(0);
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
    if (values.port !== undefined) {
      const port = Number(values.port);
      if (!Number.isInteger(port) || port < 0 || port > 65535) {
        process.stderr.write(`cappu: invalid port '${values.port}'\n`);
        process.exit(2);
      }
      // Socket mode: listen, hand the first accepted connection to the server
      // (configured before the lazy import, since server.ts creates its
      // JSON-RPC connection at module load), exit when it disconnects.
      const { createServer } = await import("node:net");
      const tcp = createServer().listen(port);
      await once(tcp, "listening");
      const address = tcp.address();
      const bound = typeof address === "object" && address ? address.port : port;
      process.stderr.write(`cappu lsp listening on port ${bound}\n`);

      const [socket] = (await once(tcp, "connection")) as [Socket];
      tcp.close(); // one session per process, like other socket-mode servers
      socket.once("close", () => process.exit(0));
      const { setTransport } = await import("./serverTransport.ts");
      setTransport(socket, socket);
    }
    // Imported lazily so `cappu compile` never loads the LSP transport stack.
    // startServer() begins reading the transport and keeps the process alive.
    const { startServer } = await import("./server.ts");
    startServer(config);
    break;
  }
  case "compile": {
    if (files.length === 0) {
      process.stderr.write("usage: cappu compile [-d <outdir>] <file.java> ...\n");
      process.exit(2);
    }
    // Missing configured dirs are treated as empty; warn only when they come
    // from an actual cappu.json.
    for (const path of missingConfiguredPaths(config)) {
      process.stderr.write(`warning: configured path not found (treated as empty): ${path}\n`);
    }
    const result = runCompile(files, {
      outDir: values["out-dir"],
      failOnDegrade: values["fail-on-degrade"],
      config,
    });
    // runCompile is print-free; render its outcome here.
    const quiet = values.quiet ?? config.compilerOptions.quiet ?? false;
    if (!quiet) for (const out of result.written) process.stdout.write(`${out}\n`);
    for (const entry of result.degraded) {
      process.stderr.write(
        `warning: ${entry}: unsupported construct, emitted a placeholder body\n`,
      );
    }
    if (!result.success) {
      for (const d of result.diagnostics) {
        const location = d.file ? `${d.file}:${d.line}:${d.column}: ` : "";
        const code = d.code !== undefined ? ` ${d.code}` : "";
        process.stderr.write(`${location}${d.severity}${code}: ${d.message}\n`);
      }
      process.exit(1);
    }
    process.exit(0);
  }
  default:
    process.stderr.write(`cappu: unknown command '${command}'\n\n${USAGE}`);
    process.exit(2);
}
