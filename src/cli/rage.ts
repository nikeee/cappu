// `cappu rage`: open the issue tracker in the default browser - for when a bug
// has worn you down enough to file it.

import { spawn } from "node:child_process";

import pkg from "../../package.json" with { type: "json" };

const ISSUE_TRACKER = pkg.bugs.url;

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

export function runRage(): Promise<never> {
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
