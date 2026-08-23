package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/nikeee/cappu/internal/compiler"
)

// RunDecompile handles `cappu decompile`: print the bytecode of .class files,
// in `javap -c -p` layout (#43). Reconstructing Java source is a later phase,
// which is why there is no output-format flag yet.
// Port of src/cli/decompile.ts.
func RunDecompile(files []string) int {
	if len(files) == 0 {
		fmt.Fprint(os.Stderr, "usage: cappu decompile <file.class> ...\n")
		return 2
	}
	failed := false
	for _, file := range files {
		bytes, err := os.ReadFile(file)
		if err == nil {
			var text string
			text, err = compiler.Disassemble(bytes)
			if err == nil {
				fmt.Print(text)
				continue
			}
		}
		// Kept identical to the TS build's wording (src/cli/decompile.ts).
		reason := err.Error()
		if errors.Is(err, fs.ErrNotExist) {
			reason = "no such file or directory"
		}
		fmt.Fprintf(os.Stderr, "cappu: %s: %s\n", file, reason)
		failed = true
	}
	if failed {
		return 1
	}
	return 0
}
