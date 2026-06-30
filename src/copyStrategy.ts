// Materialize a jar from the global store into a project (nikeee/cappu#35).
// Picking the cheapest mechanism the platform offers, chosen once at module
// load (the "strategy chosen at startup" the issue asks for):
//   - macOS:        copy-on-write clone (clonefile, via COPYFILE_FICLONE)
//   - Linux:        hardlink, falling back to a plain copy (e.g. EXDEV when the
//                   store and project live on different filesystems)
//   - Windows/other: plain copy
// The result is made read-only (0444): a hardlink shares the store's inode, so
// an accidental in-place overwrite would otherwise corrupt the shared entry.

import { chmodSync, constants, copyFileSync, linkSync, rmSync } from "node:fs";

type Materialize = (src: string, dest: string) => void;

const copy: Materialize = (src, dest) => {
  rmSync(dest, { force: true });
  copyFileSync(src, dest);
};

const clone: Materialize = (src, dest) => {
  rmSync(dest, { force: true });
  // COPYFILE_FICLONE maps to copyfile(COPYFILE_CLONE) on macOS (CoW) and falls
  // back to a plain copy when the clone can't be made.
  copyFileSync(src, dest, constants.COPYFILE_FICLONE);
};

const hardlink: Materialize = (src, dest) => {
  rmSync(dest, { force: true });
  try {
    linkSync(src, dest);
  } catch {
    copyFileSync(src, dest);
  }
};

const pick = (): Materialize => {
  switch (process.platform) {
    case "darwin":
      return clone;
    case "linux":
      return hardlink;
    default:
      return copy;
  }
};

const strategy = pick();

export function materialize(src: string, dest: string): void {
  strategy(src, dest);
  chmodSync(dest, 0o444);
}
