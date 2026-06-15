// `cappu self-upgrade`: replace the running compiled binary with the latest
// CD build. Refuses to run from a dev launcher (tsx/node), where execPath is
// the runtime, not cappu.

import { basename } from "node:path";

import { resolveToken, selfUpgrade } from "../selfupgrade/index.ts";

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

  const token = resolveToken();
  if (!token) {
    process.stderr.write(
      "cappu: self-upgrade needs a GitHub token to read CD build artifacts.\n" +
        "       Set GITHUB_TOKEN (or run `gh auth login`).\n",
    );
    process.exit(2);
  }

  try {
    process.stderr.write("fetching the latest CD build...\n");
    const result = await selfUpgrade({ targetPath, token });
    const sha = result.artifact.runSha.slice(0, 7);
    process.stdout.write(
      `upgraded ${result.targetPath} to ${result.target.artifact} ` +
        `(${sha}, built ${result.artifact.runCreatedAt})\n`,
    );
    process.exit(0);
  } catch (e) {
    process.stderr.write(`cappu: self-upgrade failed: ${(e as Error).message}\n`);
    process.exit(1);
  }
}
