package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
// name in them says which directory is the package root. A class not beside
// it but named on the command line is read from the file that names it - the
// run skips it as written inside its outer class, which has to find it then.
// A name two files of the run carry says nothing about which one is meant,
// so only the one beside counts for it.
func siblingsFor(file string, b []byte, named map[string]string) compiler.Siblings {
	name := ""
	if read, err := compiler.ReadClassFile(b); err == nil {
		name = read.ThisClass
	}
	beside := compiler.SiblingsBeside(file, name)
	return func(binaryName string) ([]byte, bool) {
		if b, ok := beside(binaryName); ok {
			return b, true
		}
		if path := named[binaryName]; path != "" {
			if b, err := os.ReadFile(path); err == nil {
				return b, true
			}
		}
		return nil, false
	}
}

// declaringFile is the file of this run whose class declares a nested one.
type declaringFile struct {
	file  string
	bytes []byte
}

// writtenInsideAnother reports a class another file of this run declares and
// writes: the one that class reads for it is this very file. One it reads
// from elsewhere - another build beside it - leaves this one to be written
// on its own, or it would be lost without a word.
func writtenInsideAnother(b []byte, declared map[string]declaringFile, named map[string]string) bool {
	read, err := compiler.ReadClassFile(b)
	if err != nil {
		return false
	}
	outer, ok := declared[read.ThisClass]
	if !ok {
		return false
	}
	found, ok := siblingsFor(outer.file, outer.bytes, named)(read.ThisClass)
	return ok && bytes.Equal(found, b)
}

func RunDecompile(files []string, disasm bool) int {
	if len(files) == 0 {
		fmt.Fprint(os.Stderr, "usage: cappu decompile <file.class> ...\n")
		return 2
	}
	// A file named twice is one file.
	seen := map[string]bool{}
	unique := files[:0:0]
	for _, file := range files {
		key := file
		if abs, err := filepath.Abs(file); err == nil {
			key = abs
		}
		if !seen[key] {
			seen[key] = true
			unique = append(unique, file)
		}
	}
	files = unique
	// A class the file of another one declares is not written twice: two
	// declarations of one type do not compile together. Only what that other
	// file actually writes is skipped - a name that merely looks nested is
	// not, or the class would be lost without a word.
	declared, named := map[string]declaringFile{}, map[string]string{}
	if !disasm {
		for _, file := range files {
			b, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			read, err := compiler.ReadClassFile(b)
			if err != nil {
				continue
			}
			if _, twice := named[read.ThisClass]; twice {
				named[read.ThisClass] = ""
			} else {
				named[read.ThisClass] = file
			}
			for _, name := range compiler.NestedClasses(read) {
				if _, ok := declared[name]; !ok {
					declared[name] = declaringFile{file, b}
				}
			}
		}
	}
	failed := false
	for _, file := range files {
		bytes, err := os.ReadFile(file)
		if err == nil && writtenInsideAnother(bytes, declared, named) {
			continue
		}
		if err == nil {
			var text string
			if disasm {
				text, err = compiler.Disassemble(bytes)
			} else {
				text, err = DecompileToSourceWith(bytes, siblingsFor(file, bytes, named))
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
