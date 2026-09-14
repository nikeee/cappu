// The file-shaped MCP tools: `cappu decompile` and `cappu format` as read-only
// tools. Neither needs the Java program - they work on one file (or one class
// on the configured classPath) and return text; nothing is written. Errors are
// thrown and the server reports them (togo/internal/mcp/files.go does the same).

import { globSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { decompileToSource, readErrorText } from "../cli/decompile.ts";
import { disassemble } from "../compiler/disasm.ts";
import { readZipEntries } from "../compiler/zipReader.ts";
import { type CappuConfig, resolveConfigPath } from "../config.ts";
import { type FormatOptions, formatSource } from "../format/index.ts";

export interface DecompileArgs {
  /** Path to a `.class` file. */
  file?: string;
  /** Binary name of a class on the configured classPath (`com.acme.Foo$Bar`). */
  className?: string;
  /** Bytecode in `javap -c -p` layout instead of reconstructed source. */
  disasm?: boolean;
}

/**
 * The bytes of `className` from the config's classPath: a `.class` file under a
 * directory entry, or an entry of a jar (given directly, or found under a
 * directory - the same places loadClassPath looks).
 */
function findClass(config: CappuConfig | undefined, className: string): Uint8Array {
  if (!config) throw new Error("className needs a project config (cappu.json) with a classPath");
  const entryName = `${className.replace(/\./g, "/")}.class`;
  const inJar = (jar: string): Uint8Array | undefined => {
    try {
      return readZipEntries(readFileSync(jar))
        ?.find(e => e.name === entryName)
        ?.read();
    } catch {
      return undefined; // an unreadable jar matches nothing
    }
  };
  for (const raw of config.compilerOptions.classPath) {
    const entry = resolveConfigPath(config, raw);
    if (entry.endsWith(".jar")) {
      const bytes = inJar(entry);
      if (bytes) return bytes;
      continue;
    }
    try {
      return readFileSync(join(entry, entryName));
    } catch {
      // not in this directory as a file; try its jars
    }
    let jars: string[];
    try {
      jars = globSync("**/*.jar", { cwd: entry });
    } catch {
      continue;
    }
    for (const jar of jars) {
      const bytes = inJar(join(entry, jar));
      if (bytes) return bytes;
    }
  }
  throw new Error(`class ${className} not found on the classPath`);
}

/** Read a file, wording I/O failures the way the CLI (and the Go build) does. */
function read(file: string): Buffer {
  try {
    return readFileSync(file);
  } catch (e) {
    throw new Error(`${file}: ${readErrorText(e)}`);
  }
}

export function decompileTool(
  config: CappuConfig | undefined,
  args: DecompileArgs,
): { source: string } {
  if ((args.file === undefined) === (args.className === undefined)) {
    throw new Error("give exactly one of file or className");
  }
  const bytes = args.file !== undefined ? read(args.file) : findClass(config, args.className!);
  return { source: args.disasm ? disassemble(bytes) : decompileToSource(bytes) };
}

/**
 * The file as `cappu format --write` would leave it. `changed` is false when it
 * is already formatted, so a caller need not diff.
 */
export function formatTool(
  options: FormatOptions,
  args: { file: string },
): { formatted: string; changed: boolean } {
  const text = read(args.file).toString("utf8");
  let formatted: string;
  try {
    formatted = formatSource(text, options, args.file);
  } catch {
    // Both builds word this the same, whichever unsupported construct it was.
    throw new Error(`${args.file}: unsupported syntax`);
  }
  return { formatted, changed: formatted !== text };
}
