//go:build linux

package copyfile

import "os"

// materializeImpl hardlinks src into dst; falls back to a plain copy when the
// link can't be made (e.g. EXDEV across filesystems).
func materializeImpl(src, dst string) error {
	if err := os.Link(src, dst); err != nil {
		return plainCopy(src, dst)
	}
	return nil
}
