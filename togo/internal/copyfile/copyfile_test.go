package copyfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMaterializeCopiesAndMakesReadOnly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jar")
	dst := filepath.Join(dir, "dst.jar")
	if err := os.WriteFile(src, []byte("jar-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Materialize(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "jar-bytes" {
		t.Errorf("content = %q, want %q", got, "jar-bytes")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o444 {
		t.Errorf("mode = %o, want 0444", got)
	}
}

func TestMaterializeOverwritesReadOnlyDestination(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.jar")
	first := filepath.Join(dir, "first.jar")
	second := filepath.Join(dir, "second.jar")
	if err := os.WriteFile(first, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("newer"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Materialize(first, dst); err != nil { // leaves dst at 0444
		t.Fatal(err)
	}
	if err := Materialize(second, dst); err != nil { // must not fail on 0444 dst
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "newer" {
		t.Errorf("content = %q, want %q", got, "newer")
	}
}

// On Linux the strategy hardlinks, so src and dst share an inode (one temp dir
// is one filesystem). Other platforms clone/copy into a fresh inode.
func TestMaterializeHardlinksOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("hardlink strategy is Linux-only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jar")
	dst := filepath.Join(dir, "dst.jar")
	if err := os.WriteFile(src, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Materialize(src, dst); err != nil {
		t.Fatal(err)
	}

	si, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(si, di) {
		t.Error("expected src and dst to share an inode (hardlink)")
	}
}
