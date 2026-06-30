//go:build darwin

package copyfile

import "golang.org/x/sys/unix"

// materializeImpl clones src into dst copy-on-write; falls back to a plain copy
// when the filesystem can't clone.
func materializeImpl(src, dst string) error {
	if err := unix.Clonefile(src, dst, 0); err != nil {
		return plainCopy(src, dst)
	}
	return nil
}
