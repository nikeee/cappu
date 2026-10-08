package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/format"
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

// DecompileToSource reconstructs Java source from one class file. The
// decompiler emits rough text; the formatter lays it out. A body this phase
// cannot reconstruct carries its disassembly as a comment, which the formatter
// may refuse - the unformatted source is still the right answer then.
func DecompileToSource(b []byte) (string, error) { return DecompileToSourceWith(b, nil) }

// DecompileToSourceWith is DecompileToSource with the classes beside this one
// to read.
func DecompileToSourceWith(b []byte, siblings compiler.Siblings) (string, error) {
	source, err := compiler.DecompileWith(b, siblings)
	if err != nil {
		return "", err
	}
	formatted, err := format.FormatSource(source, format.FormatOptions{}, "")
	if err != nil {
		return source, nil
	}
	return formatted, nil
}

// siblingsFor is SiblingsBeside for a class whose bytes are already read: the
// name in them says which directory is the package root.
func siblingsFor(file string, b []byte) compiler.Siblings {
	name := ""
	if read, err := compiler.ReadClassFile(b); err == nil {
		name = read.ThisClass
	}
	return compiler.SiblingsBeside(file, name)
}

func RunDecompile(files []string, disasm bool) int {
	if len(files) == 0 {
		fmt.Fprint(os.Stderr, "usage: cappu decompile <file.class> ...\n")
		return 2
	}
	failed := false
	for _, file := range files {
		bytes, err := os.ReadFile(file)
		if err == nil {
			var text string
			if disasm {
				text, err = compiler.Disassemble(bytes)
			} else {
				text, err = DecompileToSourceWith(bytes, siblingsFor(file, bytes))
			}
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
