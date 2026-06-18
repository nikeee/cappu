// `cappu install`: render the print-free installDependencies result - jars
// written, version conflicts (warnings), unresolvable packages (errors) -
// with a progress bar on the way (TTY only; piped output stays plain).

import { styleText } from "node:util";

import { SingleBar } from "cli-progress";

import type { CappuConfig } from "../config.ts";
import { installDependencies } from "../install.ts";
import { provisionJdk } from "../jdks/index.ts";
import { colorEnabled } from "./color.ts";
import { warnUnmappedLicenses } from "./licenses.ts";
import { downloadBar, painter } from "./style.ts";

/** Whether the install progress bar / resolving indicator may render. */
function progressEnabled(): boolean {
  return colorEnabled(process.stderr.isTTY);
}

// One animated bar on stderr while packages download (stdout stays the plain
// list of written jars, so piping it remains useful).
function progressBar(): SingleBar | undefined {
  return downloadBar(process.stderr);
}

export async function runInstall(
  config: CappuConfig,
  options: { updateLock?: boolean; verbose?: boolean } = {},
): Promise<never> {
  const out = painter(process.stdout);
  const err = painter(process.stderr);
  let bar: SingleBar | undefined;
  // Resolving (no lockfile) fetches a POM per package with no known total, so
  // it gets a count-up line rather than a bar; cleared once downloads start.
  let resolved = 0;
  const result = await installDependencies(config, undefined, {
    ...options,
    onResolve: current => {
      if (!progressEnabled()) return;
      resolved++;
      const label = `${styleText("cyan", "resolving")} ${styleText("bold", String(resolved))} ${styleText("dim", current)}`;
      process.stderr.write(`\r\x1b[2K${label}`); // carriage return + clear line
    },
    onProgress: (done, total, current) => {
      if (resolved > 0) {
        process.stderr.write("\r\x1b[2K"); // wipe the resolving line before the bar
        resolved = 0;
      }
      bar ??= (() => {
        const created = progressBar();
        created?.start(total, 0, { package: "" });
        return created;
      })();
      bar?.update(done, { package: current });
    },
  });
  if (resolved > 0) process.stderr.write("\r\x1b[2K"); // nothing to download after resolve
  bar?.stop();

  // JDK provisioning (nikeee/cappu#8): the configured "jdk" entry is
  // downloaded once into the per-user cache and unpacked into .cappu/jdks.
  let jdkFailed = false;
  if (config.jdk !== undefined) {
    let jdkBar: SingleBar | undefined;
    try {
      const jdk = await provisionJdk(config, config.jdk, (received, total) => {
        if (total === undefined) return;
        jdkBar ??= (() => {
          const created = downloadBar(process.stderr, { unit: "MiB" });
          created?.start(Math.round(total / 1024 / 1024), 0, { package: config.jdk });
          return created;
        })();
        jdkBar?.update(Math.round(received / 1024 / 1024), { package: config.jdk });
      });
      jdkBar?.stop();
      if (jdk.alreadyProvisioned) {
        process.stderr.write(err("dim", `jdk ${config.jdk}: already provisioned\n`));
      } else {
        if (jdk.fromCache)
          process.stderr.write(err("dim", `jdk ${config.jdk}: archive from the local cache\n`));
        process.stdout.write(`${jdk.jdkDir}\n`);
      }
    } catch (e) {
      jdkBar?.stop();
      process.stderr.write(`${err("red", "error:")} jdk ${config.jdk}: ${(e as Error).message}\n`);
      jdkFailed = true;
    }
  }
  if (result.fromLock) {
    process.stderr.write(err("dim", "using cappu-lock.json\n"));
  }
  if (result.fromStore.length > 0) {
    process.stderr.write(
      err("dim", `${result.fromStore.length} package(s) from the local store\n`),
    );
  }
  if (result.lockStale) {
    process.stderr.write(
      `${err("yellow", "warning:")} cappu.json's dependencies changed since cappu-lock.json was written;\n` +
        "         the locked set was installed anyway. Use `cappu add` (or delete the\n" +
        "         lock file) to re-resolve.\n",
    );
  }
  // --verbose lists every written jar (plain, so it stays pipeable); the
  // default is a colourful per-category count.
  if (options.verbose) {
    for (const file of result.installed) process.stdout.write(`${file}\n`);
  } else {
    const { compile, processor, test } = result.installedByCategory;
    const categories = [
      { n: compile.length, one: "compile dependency", many: "compile dependencies" },
      { n: processor.length, one: "annotation processor", many: "annotation processors" },
      { n: test.length, one: "test dependency", many: "test dependencies" },
    ];
    const parts = categories
      .filter(c => c.n > 0)
      .map(c => `${out(["bold", "cyan"], String(c.n))} ${c.n === 1 ? c.one : c.many}`);
    const summary = parts.length > 0 ? parts.join(", ") : out("dim", "no packages");
    process.stdout.write(`${out("green", "✓")} ${summary} installed\n`);
  }
  warnUnmappedLicenses(result.resolution.packages);
  for (const c of result.resolution.conflicts) {
    process.stderr.write(
      `${err("yellow", "warning:")} ${c.key}: version ${c.rejected} (via ${c.rejectedBy.artifactId}) loses to ${c.selected}\n`,
    );
  }
  let failed = false;
  for (const m of result.resolution.missing) {
    const via = m.requestedBy ? ` (required by ${m.requestedBy.artifactId})` : "";
    process.stderr.write(
      `${err("red", "error:")} ${m.coordinates.groupId}:${m.coordinates.artifactId}:${m.coordinates.version}: not found in any package source${via}\n`,
    );
    failed = true;
  }
  for (const c of result.noArtifact) {
    process.stderr.write(`${err("red", "error:")} ${c}: source provided no jar\n`);
    failed = true;
  }
  for (const c of result.integrityFailures) {
    process.stderr.write(
      `${err("red", "error:")} ${c}: downloaded jar does not match the SHA-256 in cappu-lock.json\n`,
    );
    failed = true;
  }
  process.exit(failed || jdkFailed ? 1 : 0);
}
