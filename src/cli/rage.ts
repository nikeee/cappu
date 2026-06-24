// `cappu rage`: print version/environment info plus the issue tracker URL - so
// a worn-down bug filer can paste the context into a report. `--open` also opens
// the tracker in the default browser.

import { spawn } from "node:child_process";

import pkg from "../../package.json" with { type: "json" };

const ISSUE_TRACKER = pkg.bugs.url;

// Environment block the user can paste into a bug report.
export function rageReport(): string {
  return (
    `cappu ${pkg.version}\n` +
    `runtime: node ${process.version}\n` +
    `platform: ${process.platform} ${process.arch}\n` +
    `\nfile an issue at ${ISSUE_TRACKER}\n`
  );
}

// The platform's "open this with whatever's registered" launcher.
function browserOpener(): { command: string; args: string[] } {
  switch (process.platform) {
    case "darwin":
      return { command: "open", args: [ISSUE_TRACKER] };
    case "win32":
      // `start` is a cmd builtin, not an executable; the empty "" is its title arg.
      return { command: "cmd", args: ["/c", "start", "", ISSUE_TRACKER] };
    default:
      return { command: "xdg-open", args: [ISSUE_TRACKER] };
  }
}

export function runRage(open: boolean): Promise<never> {
  process.stdout.write(rageReport());

  if (!open) process.exit(0);

  const { command, args } = browserOpener();
  const child = spawn(command, args, { stdio: "ignore", detached: true });
  // Let the launcher outlive us; we exit explicitly once it spawned or failed.
  child.unref();
  return new Promise<never>(() => {
    child.on("spawn", () => {
      process.stderr.write(`opening ${ISSUE_TRACKER}\n`);
      process.exit(0);
    });
    child.on("error", () => {
      process.stderr.write(`cappu: could not open a browser; file it at ${ISSUE_TRACKER}\n`);
      process.exit(1);
    });
  });
}
