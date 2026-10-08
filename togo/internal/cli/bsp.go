package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nikeee/cappu/internal/bspserver"
	"github.com/nikeee/cappu/internal/config"
)

// RunBspInstall handles `cappu bsp --install`: write the BSP connection file
// .bsp/cappu.json beside cappu.json, pointing at this binary.
func RunBspInstall(cfg *config.Config) int {
	if err := writeBspConnectionFile(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}
	return 0
}

func writeBspConnectionFile(cfg *config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := bspserver.ConnectionFile(exe)
	if err != nil {
		return err
	}
	path := cfg.ResolvePath(filepath.Join(".bsp", "cappu.json"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
