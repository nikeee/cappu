package cli

import (
	"fmt"
	"os"

	"github.com/nikeee/cappu/internal/cache"
)

// RunCache handles `cappu cache clean`: remove the global download cache
// (packages, JDKs, resolved metadata). Other subcommands are rejected. Port of
// src/cli/cache.ts.
func RunCache(args []string) int {
	if len(args) != 1 || args[0] != "clean" {
		fmt.Fprintln(os.Stderr, "usage: cappu cache clean")
		return 2
	}
	removed := cache.Clean()
	if len(removed) == 0 {
		fmt.Fprintln(os.Stderr, "cache already empty")
	} else {
		for _, dir := range removed {
			fmt.Fprintf(os.Stdout, "removed %s\n", dir)
		}
	}
	return 0
}
