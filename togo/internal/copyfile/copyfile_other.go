//go:build !darwin && !linux

package copyfile

// materializeImpl just copies; Windows and anything else get a plain copy.
func materializeImpl(src, dst string) error {
	return plainCopy(src, dst)
}
