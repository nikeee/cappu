package cli

import (
	"fmt"
	"os"

	"github.com/nikeee/cappu/internal/cache"
	"github.com/nikeee/cappu/internal/install"
)

// RunCache handles `cappu cache clean` (remove the global download cache) and
// `cappu cache verify` (check cached artifacts against the hashes recorded
// beside them). Other subcommands are rejected. Port of src/cli/cache.ts.
func RunCache(args []string) int {
	if len(args) == 1 && args[0] == "clean" {
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
	if len(args) == 1 && args[0] == "verify" {
		result := install.VerifyCache()
		for _, f := range result.Modified {
			fmt.Fprintf(os.Stderr, "error: %s: cached bytes do not match the recorded hash\n", f)
		}
		for _, f := range result.Missing {
			fmt.Fprintf(os.Stderr, "error: %s: a hash is recorded but the file is gone\n", f)
		}
		fmt.Fprintf(os.Stderr, "%d ok, %d modified, %d missing\n",
			len(result.OK), len(result.Modified), len(result.Missing))
		if len(result.Modified)+len(result.Missing) > 0 {
			return 1
		}
		return 0
	}
	fmt.Fprintln(os.Stderr, "usage: cappu cache <clean|verify>")
	return 2
}
