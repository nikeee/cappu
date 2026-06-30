// Package copyfile materializes a jar from the global store into a project
// (nikeee/cappu#35), picking the cheapest mechanism the platform offers. The
// strategy is selected at compile time via per-OS build tags (the Go analogue
// of the issue's "strategy chosen at startup"):
//   - macOS:         copy-on-write clone (clonefile)
//   - Linux:         hardlink, falling back to a plain copy (e.g. EXDEV when the
//     store and project live on different filesystems)
//   - Windows/other: plain copy
//
// The result is made read-only (0444): a hardlink shares the store's inode, so
// an accidental in-place overwrite would otherwise corrupt the shared entry.
package copyfile

import (
	"io"
	"os"
)

// Materialize places the file at src into dst using the platform strategy
// (link/clone/copy from materializeImpl) and makes dst read-only. dst is
// removed first so a hardlink can't hit EEXIST and a copy can't hit EACCES on a
// prior 0444 entry.
func Materialize(src, dst string) error {
	_ = os.Remove(dst)
	if err := materializeImpl(src, dst); err != nil {
		return err
	}
	return os.Chmod(dst, 0o444)
}

// plainCopy is the universal fallback: stream src into a fresh dst.
func plainCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
