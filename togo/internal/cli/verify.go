package cli

import (
	"fmt"
	"os"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/lockfile"
)

// RunVerify handles `cappu verify`: check the jars currently in the lib
// directories against the SHA-256 sums in cappu-lock.json. Read-only; exits
// non-zero on any mismatch or missing jar. Port of src/cli/verify.ts.
func RunVerify(cfg *config.Config) int {
	result := lockfile.VerifyInstalled(cfg)
	if !result.FromLock {
		fmt.Fprintln(os.Stderr, "cappu: no cappu-lock.json to verify against; run `cappu install` first")
		return 1
	}
	for _, id := range result.Modified {
		fmt.Fprintf(os.Stderr, "error: %s: installed jar does not match cappu-lock.json\n", id)
	}
	for _, id := range result.Missing {
		fmt.Fprintf(os.Stderr, "error: %s: locked but not installed (run `cappu install`)\n", id)
	}
	fmt.Fprintf(os.Stderr, "%d ok, %d modified, %d missing\n",
		len(result.OK), len(result.Modified), len(result.Missing))
	if len(result.Modified)+len(result.Missing) > 0 {
		return 1
	}
	return 0
}
