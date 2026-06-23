// `cappu self-upgrade`: replace the running compiled binary with the latest
// published release. Refuses to run from a dev launcher (tsx/node), where
// execPath is the runtime, not cappu.

import { basename } from "node:path";

import { SingleBar } from "cli-progress";

import pkg from "../../package.json" with { type: "json" };
import { platformTarget, selfUpgrade } from "../selfupgrade/index.ts";
import { downloadBar, painter } from "./style.ts";

export async function runSelfUpgrade(): Promise<never> {
  // CAPPU_UPGRADE_TARGET overrides the replaced path (tests / unusual installs).
  const targetPath = process.env.CAPPU_UPGRADE_TARGET ?? process.execPath;
  const name = basename(targetPath);
  if (!name.startsWith("cappu")) {
    process.stderr.write(
      `cappu: self-upgrade replaces the compiled cappu binary, but this is running via '${targetPath}'.\n` +
        "       Run the installed `cappu` binary, or set CAPPU_UPGRADE_TARGET to its path.\n",
    );
    process.exit(2);
  }

  const err = painter(process.stderr);
  const out = painter(process.stdout);
  const label = platformTarget() ?? "cappu";
  let bar: SingleBar | undefined;
  try {
    process.stderr.write(err(["bold", "cyan"], "fetching the latest release...\n"));
    const result = await selfUpgrade({
      targetPath,
      currentVersion: pkg.version,
      onDownloadProgress: (received, total) => {
        if (total === undefined) return;
        bar ??= (() => {
          const created = downloadBar(process.stderr, { unit: "MiB" });
          created?.start(Math.round(total / 1024 / 1024), 0, { package: label });
          return created;
        })();
        bar?.update(Math.round(received / 1024 / 1024), { package: label });
      },
    });
    bar?.stop();
    if (result.upToDate) {
      process.stdout.write(
        `${out("green", "✓")} already on the latest version (${out("cyan", result.release.tag)})\n`,
      );
      process.exit(0);
    }
    process.stdout.write(
      `${out("green", "✓")} upgraded ${result.targetPath} to ${out("bold", result.assetName)} ` +
        `(${out("cyan", result.release.tag)}, published ${result.release.publishedAt})\n`,
    );
    process.exit(0);
  } catch (e) {
    bar?.stop();
    process.stderr.write(`${err("red", "error:")} self-upgrade failed: ${(e as Error).message}\n`);
    process.exit(1);
  }
}
