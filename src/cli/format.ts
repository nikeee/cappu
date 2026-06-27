// `cappu format`: a google-java-format-compatible formatter (nikeee/cappu#24).
// By default it only CHECKS formatting - it lists the files that are not
// formatted and exits non-zero, changing nothing. With --write it rewrites
// those files in place. Files it cannot format without losing information (a
// syntax error, or a comment in an unsupported position) are skipped with a
// warning and never rewritten.

import { writeFileSync } from "node:fs";
import { availableParallelism } from "node:os";
import { resolve } from "node:path";
import { Worker } from "node:worker_threads";

import { type CappuConfig } from "../config.ts";
import { type FormatOptions } from "../format/index.ts";
import { findFormattableFiles } from "../workspace.ts";
import { emitAnnotation } from "./annotations.ts";
import { formatOne, type Outcome } from "./format-one.ts";
import { painter } from "./style.ts";

export interface FormatFlags {
  write?: boolean;
}

// Below this file count the worker_threads startup cost outweighs the gain, so
// format inline on the main thread; above it, fan out across a worker pool.
const PARALLEL_THRESHOLD = 64;

// Read + format every target, in parallel across a worker pool when there are
// enough files to amortize worker startup, else sequentially. Returns outcomes
// in target order (workers process contiguous chunks reassembled by index).
async function computeOutcomes(
  targets: string[],
  cwd: string,
  style: FormatOptions["style"],
): Promise<Outcome[]> {
  const maxWorkers = Math.min(availableParallelism(), targets.length);
  if (targets.length < PARALLEL_THRESHOLD || maxWorkers <= 1) {
    return targets.map(f => formatOne(f, cwd, style));
  }

  const results = new Array<Outcome>(targets.length);
  const chunkSize = Math.ceil(targets.length / maxWorkers);
  const workers: Promise<void>[] = [];
  for (let start = 0; start < targets.length; start += chunkSize) {
    const files = targets.slice(start, start + chunkSize);
    const base = start;
    workers.push(
      new Promise<void>((res, rej) => {
        const worker = new Worker(new URL("./format-worker.ts", import.meta.url), {
          workerData: { files, cwd, style },
        });
        worker.on("message", (out: Outcome[]) => {
          out.forEach((o, k) => (results[base + k] = o));
          res();
        });
        worker.on("error", rej);
        worker.on("exit", code => {
          if (code !== 0) rej(new Error(`format worker exited with code ${code}`));
        });
      }),
    );
  }
  await Promise.all(workers);
  return results;
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
  const cwd = process.cwd();
  const outcomes = await computeOutcomes(targets, cwd, style);

  // Serial phase: emit output and apply writes in target order (deterministic,
  // independent of which worker finished first).
  const unformatted: string[] = [];
  const changed: string[] = [];
  let skipped = 0;

  for (let i = 0; i < outcomes.length; i++) {
    const o = outcomes[i];
    if (o.readErr) {
      process.stderr.write(`cappu: cannot read ${o.rel}: ${o.readErr}\n`);
      process.exit(2);
    }
    if (o.fmtErr) {
      process.stderr.write(`cappu: ${o.fmtErr}\n`);
      process.exit(2);
    }
    if (o.skipped) {
      skipped++;
      process.stderr.write(paint("dim", `skipped ${o.rel} (unsupported syntax)\n`));
      continue;
    }
    if (!o.changed) continue;

    if (flags.write) {
      writeFileSync(targets[i], o.formatted!);
      changed.push(o.rel);
      // List each rewritten file on stdout (machine-readable, like the check
      // mode lists the unformatted ones); the count summary goes to stderr.
      process.stdout.write(`${o.rel}\n`);
    } else {
      unformatted.push(o.rel);
      process.stdout.write(`${o.rel}\n`);
      emitAnnotation("error", "not formatted (run `cappu format --write`)", { file: o.rel });
    }
  }

  if (flags.write) {
    process.stderr.write(
      `cappu: formatted ${paint("green", `${changed.length} of ${targets.length}`)} file(s)${
        skipped ? `, ${paint("yellow", `skipped ${skipped}`)}` : ""
      }\n`,
    );
    process.exit(0);
  }

  if (unformatted.length > 0) {
    process.stderr.write(
      `cappu: ${paint("red", `${unformatted.length} of ${targets.length}`)} file(s) not formatted; run ${paint("bold", "`cappu format --write`")}\n`,
    );
    process.exit(1);
  }
  process.stderr.write(
    `cappu: ${paint("green", `all ${targets.length} file(s) formatted`)}${
      skipped ? ` (${paint("yellow", `skipped ${skipped}`)})` : ""
    }\n`,
  );
  process.exit(0);
}
