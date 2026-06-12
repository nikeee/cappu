// `cappu install`: render the print-free installDependencies result - jars
// written, version conflicts (warnings), unresolvable packages (errors) -
// with a progress bar on the way (TTY only; piped output stays plain).

import { styleText } from "node:util";

import { SingleBar } from "cli-progress";

import type { CappuConfig } from "../config.ts";
import { installDependencies } from "../install.ts";

/**
 * Whether the animated progress bar may render: stderr must be a terminal,
 * and NO_COLOR (https://no-color.org - set and non-empty) turns the bar off
 * entirely, not just its colors.
 */
export function progressEnabled(
  isTTY: boolean | undefined = process.stderr.isTTY,
  env: NodeJS.ProcessEnv = process.env,
): boolean {
  return isTTY === true && !env.NO_COLOR;
}

// One animated bar on stderr while packages download (stdout stays the plain
// list of written jars, so piping it remains useful).
function progressBar(): SingleBar | undefined {
  if (!progressEnabled()) return undefined;
  // styleText validates against the stream the bar writes to, so colors also
  // drop out for a terminal that reports no color support
  const style = (format: Parameters<typeof styleText>[0], text: string): string =>
    styleText(format, text, { stream: process.stderr });
  return new SingleBar({
    format: `${style("cyan", "{bar}")} ${style("bold", "{value}/{total}")} ${style("dim", "{package}")}`,
    barCompleteChar: "█",
    barIncompleteChar: "░",
    hideCursor: true,
    clearOnComplete: true,
    stream: process.stderr,
  });
}

export async function runInstall(
  config: CappuConfig,
  options: { updateLock?: boolean } = {},
): Promise<never> {
  let bar: SingleBar | undefined;
  const result = await installDependencies(config, undefined, {
    ...options,
    onProgress: (done, total, current) => {
      bar ??= (() => {
        const created = progressBar();
        created?.start(total, 0, { package: "" });
        return created;
      })();
      bar?.update(done, { package: current });
    },
  });
  bar?.stop();
  if (result.fromLock) {
    process.stderr.write("using cappu-lock.json\n");
  }
  if (result.fromStore.length > 0) {
    process.stderr.write(`${result.fromStore.length} package(s) from the local store\n`);
  }
  if (result.lockStale) {
    process.stderr.write(
      "warning: cappu.json's dependencies changed since cappu-lock.json was written;\n" +
        "         the locked set was installed anyway. Use `cappu add` (or delete the\n" +
        "         lock file) to re-resolve.\n",
    );
  }
  for (const file of result.installed) process.stdout.write(`${file}\n`);
  for (const c of result.resolution.conflicts) {
    process.stderr.write(
      `warning: ${c.key}: version ${c.rejected} (via ${c.rejectedBy.artifactId}) loses to ${c.selected}\n`,
    );
  }
  let failed = false;
  for (const m of result.resolution.missing) {
    const via = m.requestedBy ? ` (required by ${m.requestedBy.artifactId})` : "";
    process.stderr.write(
      `error: ${m.coordinates.groupId}:${m.coordinates.artifactId}:${m.coordinates.version}: not found in any package source${via}\n`,
    );
    failed = true;
  }
  for (const c of result.noArtifact) {
    process.stderr.write(`error: ${c}: source provided no jar\n`);
    failed = true;
  }
  for (const c of result.integrityFailures) {
    process.stderr.write(
      `error: ${c}: downloaded jar does not match the SHA-256 in cappu-lock.json\n`,
    );
    failed = true;
  }
  process.exit(failed ? 1 : 0);
}
