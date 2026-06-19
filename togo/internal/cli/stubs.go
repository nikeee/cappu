package cli

import (
	"fmt"
	"os"
)

// Stub reports that a command is not yet ported to the Go build and exits 2.
// These commands exist in the Node build (see ../../../src/cli) and are filled
// in milestone by milestone; the stub keeps the CLI surface complete so
// `cappu --help` and dispatch match the Node version meanwhile. Remaining
// stubs: compile (the bytecode emitter / JVM execution). The lsp language
// server (internal/lspserver) and the mcp agent server (internal/mcp) are both
// ported and wired in cmd/cappu.
func Stub(command string) int {
	fmt.Fprintf(os.Stderr, "cappu: '%s' is not yet ported to the Go build\n", command)
	return 2
}
