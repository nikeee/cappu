package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/nikeee/cappu/internal/compiler"
)

// Node and Go word their I/O errors differently, so both builds map the cases
// that matter to the same text (src/cli/decompile.ts does the same).
func readErrorText(err error) string {
	var pathError *fs.PathError
	if !errors.As(err, &pathError) {
		return err.Error() // a class-file error, not an I/O failure
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "no such file or directory"
	case errors.Is(err, syscall.EISDIR):
		return "is a directory"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}
	return "cannot read file"
}

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
		fmt.Fprintf(os.Stderr, "cappu: %s: %s\n", file, readErrorText(err))
		failed = true
	}
	if failed {
		return 1
	}
	return 0
}
