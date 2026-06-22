// Package dapserver implements cappu's debug adapter: it bridges the Debug
// Adapter Protocol to JDWP, compiling and launching the project's main class
// under the JVM debugger and translating requests and events between the two.
package dapserver

// Start a Debug Adapter Protocol server: one DAP connection bound to one debug
// session, over stdio by default or any reader/writer pair (TCP from the CLI).
// Mirrors internal/lspserver/server.go. Port of src/services/dap/dapServer.ts.

import (
	"io"
	"os"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/dap"
)

// Run drives a DAP session over the given streams until the input closes.
func Run(cfg *config.Config, reader io.Reader, writer io.Writer) error {
	conn := dap.NewConn(reader, writer)
	NewSession(conn, cfg)
	return conn.Run()
}

// Serve starts the server over stdio.
func Serve(cfg *config.Config) error { return Run(cfg, os.Stdin, os.Stdout) }
