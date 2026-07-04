package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg")
	if got, want := Root(), filepath.Join("/tmp/xdg", "cappu"); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestCleanRemovesExistingDirs(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", base)
	t.Setenv("CAPPU_PACKAGE_STORE", "")
	t.Setenv("CAPPU_JDK_STORE", "")

	root := Root()
	if err := os.MkdirAll(filepath.Join(root, "packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := Clean()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != root {
		t.Fatalf("Clean() = %v, want [%s]", removed, root)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("cache root should be gone after Clean")
	}
	// A second clean finds nothing.
	if got, err := Clean(); err != nil || len(got) != 0 {
		t.Errorf("second Clean() = %v (err %v), want empty", got, err)
	}
}
