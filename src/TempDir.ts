import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

/**
 * A temporary directory under the OS temp dir that removes itself on disposal.
 * Use with `using` so the directory is cleaned up when the scope exits:
 *
 * ```ts
 * using dir = TempDir.create("cappu-example-");
 * writeFileSync(join(dir.path, "f.txt"), "hi");
 * ```
 */
export default class TempDir {
  readonly path: string;
  private constructor(path: string) {
    this.path = path;
  }

  /** Creates `<os-tmp>/<prefix>XXXXXX` and returns a disposable handle to it. */
  static create(prefix: string): TempDir {
    return new TempDir(mkdtempSync(join(tmpdir(), prefix)));
  }

  [Symbol.dispose]() {
    rmSync(this.path, { recursive: true, force: true });
  }
}
