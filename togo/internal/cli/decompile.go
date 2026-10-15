package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
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

// writtenInsideAnother reports a class another file of this run declares and
// writes: one of the classes declaring it reads this very file for it. One
// they read from elsewhere - another build beside them - is written on its
// own instead, which is said, or it would be lost without a word.
func writtenInsideAnother(file string, b []byte, declaredBy map[string][]compiler.Siblings) bool {
	read, err := compiler.ReadClassFile(b)
	if err != nil {
		return false
	}
	outers, ok := declaredBy[read.ThisClass]
	if !ok {
		return false
	}
	for _, siblings := range outers {
		if found, ok := siblings(read.ThisClass); ok && bytes.Equal(found, b) {
			return true
		}
	}
	fmt.Fprintf(os.Stderr, "cappu: %s: written on its own: the class that declares it reads another copy, or none\n", file)
	return false
}

func RunDecompile(files []string, disasm bool) int {
	if len(files) == 0 {
		fmt.Fprint(os.Stderr, "usage: cappu decompile <file.class> ...\n")
		return 2
	}
	// A file named twice - by another path, a link - is one file.
	var kept []os.FileInfo
	unique := files[:0:0]
	for _, file := range files {
		info, err := os.Stat(file)
		if err == nil && slices.ContainsFunc(kept, func(one os.FileInfo) bool { return os.SameFile(one, info) }) {
			continue
		}
		if err == nil {
			kept = append(kept, info)
		}
		unique = append(unique, file)
	}
	files = unique
	// A class the file of another one declares is not written twice: two
	// declarations of one type do not compile together. Only what that other
	// file actually writes is skipped - a name that merely looks nested is
	// not, or the class would be lost without a word.
	type outer struct {
		file  string
		bytes []byte
	}
	var outers []outer
	declared, named := map[string][]int{}, map[string]string{}
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
			if nested := compiler.NestedClasses(read); len(nested) > 0 {
				outers = append(outers, outer{file, b})
				for _, name := range nested {
					declared[name] = append(declared[name], len(outers)-1)
				}
			}
		}
	}
	// Each declaring class's own lookup, built once - every one of its
	// nested classes asks it.
	resolvers := make([]compiler.Siblings, len(outers))
	for i, one := range outers {
		resolvers[i] = siblingsFor(one.file, one.bytes, named)
	}
	declaredBy := map[string][]compiler.Siblings{}
	for name, indices := range declared {
		for _, i := range indices {
			declaredBy[name] = append(declaredBy[name], resolvers[i])
		}
	}
	failed := false
	for _, file := range files {
		bytes, err := os.ReadFile(file)
		if err == nil && writtenInsideAnother(file, bytes, declaredBy) {
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
