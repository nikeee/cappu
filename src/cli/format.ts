// `cappu format`: a google-java-format-compatible formatter (nikeee/cappu#24).
// By default it only CHECKS formatting - it lists the files that are not
// formatted and exits non-zero, changing nothing. With --write it rewrites
// those files in place. Files it cannot format without losing information (a
// syntax error, or a comment in an unsupported position) are skipped with a
// warning and never rewritten.

import { readFileSync, writeFileSync } from "node:fs";
import { relative, resolve } from "node:path";

import { type CappuConfig } from "../config.ts";
import { formatSource, UnsupportedSyntaxError } from "../format/index.ts";
import { findFormattableFiles } from "../workspace.ts";
import { emitAnnotation } from "./annotations.ts";
import { painter } from "./style.ts";

export interface FormatFlags {
  write?: boolean;
}

export async function runFormat(
  files: string[],
  flags: FormatFlags,
  config: CappuConfig,
): Promise<never> {
  const style = config.formatterOptions.style;
  // Explicit file arguments win; otherwise format the whole project.
  const targets =
    files.length > 0
      ? files.map(f => resolve(process.cwd(), f))
      : findFormattableFiles(config).map(String);

  if (targets.length === 0) {
    process.stderr.write("cappu: no .java files to format\n");
    process.exit(0);
  }

  const paint = painter(process.stderr);
  const unformatted: string[] = [];
  let written = 0;
  let skipped = 0;

  for (const file of targets) {
    const rel = relative(process.cwd(), file);
    let source: string;
    try {
      source = readFileSync(file, "utf8");
    } catch (e) {
      process.stderr.write(`cappu: cannot read ${rel}: ${(e as Error).message}\n`);
      process.exit(2);
    }

    let formatted: string;
    try {
      formatted = formatSource(source, { style }, file);
    } catch (e) {
      if (e instanceof UnsupportedSyntaxError) {
        skipped++;
        process.stderr.write(paint("dim", `skipped ${rel} (${e.message})\n`));
        continue;
      }
      throw e;
    }

    if (formatted === source) continue;

    if (flags.write) {
      writeFileSync(file, formatted);
      written++;
      process.stderr.write(paint("dim", `formatted ${rel}\n`));
    } else {
      unformatted.push(rel);
      process.stdout.write(`${rel}\n`);
      emitAnnotation("error", "not formatted (run `cappu format --write`)", { file: rel });
    }
  }

  if (flags.write) {
    process.stderr.write(
      `cappu: formatted ${written} of ${targets.length} file(s)${
        skipped ? `, skipped ${skipped}` : ""
      }\n`,
    );
    process.exit(0);
  }

  if (unformatted.length > 0) {
    process.stderr.write(
      `cappu: ${unformatted.length} of ${targets.length} file(s) not formatted; run \`cappu format --write\`\n`,
    );
    process.exit(1);
  }
  process.stderr.write(
    `cappu: all ${targets.length} file(s) formatted${skipped ? ` (skipped ${skipped})` : ""}\n`,
  );
  process.exit(0);
}
