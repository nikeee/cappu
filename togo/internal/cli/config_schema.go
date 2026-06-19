package cli

import (
	"fmt"
	"os"

	"github.com/nikeee/cappu/internal/config"
)

// RunConfigSchema prints the JSON Schema for cappu.json to stdout. Useful for
// tooling (and agents) that want to validate or understand the config without a
// project present. Port of src/cli/configSchema.ts.
func RunConfigSchema() int {
	schema, err := config.JSONSchema()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: could not generate schema: %v\n", err)
		return 1
	}
	fmt.Fprint(os.Stdout, schema)
	return 0
}
