package cli

import (
	"fmt"
	"os"
)

// Stub reports that a command is not yet ported to the Go build and exits 2.
// These commands exist in the Node build (see ../../../src/cli) and are filled
// in milestone by milestone; the stub keeps the CLI surface complete so
// `cappu --help` and dispatch match the Node version meanwhile. Remaining
// stubs: compile and lsp (the compiler / language-server core), and mcp (the
// agent server, TS-only for now).
func Stub(command string) int {
	fmt.Fprintf(os.Stderr, "cappu: '%s' is not yet ported to the Go build\n", command)
	return 2
}
