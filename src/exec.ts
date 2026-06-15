// A spawnSync wrapper shared by the domains that shell out to javac/java
// (processors, testing). Injectable so those modules stay testable without a
// real process.

import { spawnSync } from "node:child_process";

export interface ExecResult {
  /** null when the binary could not be spawned at all. */
  status: number | null;
  stderr: string;
  /** Set when spawning failed (ENOENT and friends). */
  error?: Error;
}

export type Exec = (bin: string, args: string[]) => ExecResult;

export const defaultExec: Exec = (bin, args) => {
  const result = spawnSync(bin, args, { stdio: ["ignore", "ignore", "pipe"] });
  return { status: result.status, stderr: result.stderr?.toString() ?? "", error: result.error };
};
