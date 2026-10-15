package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// runFile is one file of the run: its class and what that class declares.
type runFile struct {
	path   string
	bytes  []byte
	class  string
	nested []string
}

// writers decides, for every file of the run, whether another file writes
// it: a file's class writes the classes it declares, and those theirs, down
// to compiler.NestedDepth - every one of them read through the lookup of the
// class at the top, whose file is the one written. A declared class is
// written inside only when that lookup reads this very file for it; one it
// reads from elsewhere - another build beside it - is written on its own.
type writers struct {
	files      []runFile
	byClass    map[string]int
	declaredBy map[string][]int
	resolver   map[int]compiler.Siblings
	named      map[string]string
	// inside[i] is the file whose class declares file i's and writes it, -1
	// for one written on its own; level[i] how deep under its top it sits.
	inside, level []int
	state         []int // 0 unknown, 1 deciding, 2 decided
}

func (w *writers) decide(i int) {
	if w.state[i] == 2 {
		return
	}
	w.inside[i], w.level[i] = -1, 0
	if w.state[i] == 1 {
		return // a class declaring itself through others: written on its own
	}
	w.state[i] = 1
	defer func() { w.state[i] = 2 }()
	class := w.files[i].class
	// A class whose declaring class is not a file of the run may still be
	// written by one that is, further up: Foo writes Foo.A, which it reads
	// for itself, and A writes A.B through Foo's lookup.
	if len(w.declaredBy[class]) == 0 {
		outer, ok := w.ancestor(i)
		if !ok {
			return
		}
		w.decide(outer)
		top := outer
		for w.inside[top] >= 0 {
			top = w.inside[top]
		}
		if level, _, written := w.chain(i, outer, w.lookup(top)); written &&
			level+w.level[outer] <= compiler.NestedDepth {
			w.inside[i], w.level[i] = outer, level+w.level[outer]
		}
		return
	}
	for _, outer := range w.declaredBy[class] {
		if outer == i {
			continue
		}
		w.decide(outer)
		if w.level[outer]+1 > compiler.NestedDepth {
			continue
		}
		top := outer
		for w.inside[top] >= 0 {
			top = w.inside[top]
		}
		if found, ok := w.lookup(top)(w.files[i].class); ok && bytes.Equal(found, w.files[i].bytes) {
			w.inside[i], w.level[i] = outer, w.level[outer]+1
			break
		}
	}
}

// declared reports a file whose class a class of the run declares, directly
// or further up - one that is expected to be written inside.
func (w *writers) declared(i int) bool {
	if len(w.declaredBy[w.files[i].class]) > 0 {
		return true
	}
	outer, ok := w.ancestor(i)
	if !ok {
		return false
	}
	top := outer
	for w.inside[top] >= 0 {
		top = w.inside[top]
	}
	if _, declared, _ := w.chain(i, outer, w.lookup(top)); declared {
		return true
	}
	// A class in between that cannot be read leaves the chain unread; the
	// first step down from the class of the run is still a declared one.
	class, at := w.files[i].class, len(w.files[outer].class)
	next := strings.IndexByte(class[at+1:], '$')
	if next < 0 {
		return false
	}
	return slices.Contains(w.files[outer].nested, class[:at+1+next])
}

// ancestor is the closest file of the run whose class's name, followed by a
// `$`, begins file i's.
func (w *writers) ancestor(i int) (int, bool) {
	class := w.files[i].class
	for cut := strings.LastIndex(class, "$"); cut > 0; cut = strings.LastIndex(class[:cut], "$") {
		if outer, ok := w.byClass[class[:cut]]; ok && outer != i {
			return outer, true
		}
	}
	return 0, false
}

// chain follows file i's class down from the class of outer, one `$` at a
// time, each a class the one before declares, read through lookup. It
// reports how many levels down file i's class is, whether the declarations
// lead there, and whether the lookup reads this very file for it.
func (w *writers) chain(i, outer int, lookup compiler.Siblings) (level int, declared, same bool) {
	class, declares := w.files[i].class, w.files[outer].nested
	at := len(w.files[outer].class)
	for level = 1; ; level++ {
		next := strings.IndexByte(class[at+1:], '$')
		name := class
		if next >= 0 {
			name = class[:at+1+next]
		}
		if !slices.Contains(declares, name) {
			return 0, false, false
		}
		found, ok := lookup(name)
		if name == class {
			return level, true, ok && bytes.Equal(found, w.files[i].bytes)
		}
		if !ok {
			return 0, false, false
		}
		read, err := compiler.ReadClassFile(found)
		if err != nil {
			return 0, false, false
		}
		declares, at = compiler.NestedClasses(read), at+1+next
	}
}

func (w *writers) lookup(i int) compiler.Siblings {
	if resolver, ok := w.resolver[i]; ok {
		return resolver
	}
	resolver := siblingsFor(w.files[i].path, w.files[i].bytes, w.named)
	w.resolver[i] = resolver
	return resolver
}

// distinctFiles drops a file named again - by the same path, another one, or
// a link. Files of different sizes are never one, so only those of one size
// are compared.
func distinctFiles(files []string) []string {
	bySize := map[int64][]os.FileInfo{}
	paths := map[string]bool{}
	var out []string
	for _, file := range files {
		key := file
		if abs, err := filepath.Abs(file); err == nil {
			key = abs
		}
		if paths[key] {
			continue
		}
		paths[key] = true
		if info, err := os.Stat(file); err == nil {
			if slices.ContainsFunc(bySize[info.Size()], func(one os.FileInfo) bool { return os.SameFile(one, info) }) {
				continue
			}
			bySize[info.Size()] = append(bySize[info.Size()], info)
		}
		out = append(out, file)
	}
	return out
}

func RunDecompile(files []string, disasm bool) int {
	if len(files) == 0 {
		fmt.Fprint(os.Stderr, "usage: cappu decompile <file.class> ...\n")
		return 2
	}
	files = distinctFiles(files)
	// A class the file of another one declares is not written twice: two
	// declarations of one type do not compile together. Only what that other
	// file actually writes is skipped - a name that merely looks nested is
	// not, or the class would be lost without a word.
	w := &writers{byClass: map[string]int{}, declaredBy: map[string][]int{}, resolver: map[int]compiler.Siblings{}, named: map[string]string{}}
	index := map[string]int{}
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
			if _, twice := w.named[read.ThisClass]; twice {
				w.named[read.ThisClass] = ""
			} else {
				w.named[read.ThisClass] = file
			}
			index[file] = len(w.files)
			if _, ok := w.byClass[read.ThisClass]; !ok {
				w.byClass[read.ThisClass] = len(w.files)
			}
			w.files = append(w.files, runFile{file, b, read.ThisClass, compiler.NestedClasses(read)})
		}
		for i, one := range w.files {
			for _, name := range one.nested {
				w.declaredBy[name] = append(w.declaredBy[name], i)
			}
		}
		w.inside, w.level, w.state = make([]int, len(w.files)), make([]int, len(w.files)), make([]int, len(w.files))
		for i := range w.files {
			w.decide(i)
		}
	}
	failed := false
	for _, file := range files {
		i, known := index[file]
		if known && w.inside[i] >= 0 {
			continue
		}
		if known && w.declared(i) {
			fmt.Fprintf(os.Stderr, "cappu: %s: written on its own, outside the class that declares it\n", file)
		}
		bytes, err := os.ReadFile(file)
		if err == nil {
			var text string
			if disasm {
				text, err = compiler.Disassemble(bytes)
			} else if known {
				text, err = DecompileToSourceWith(bytes, w.lookup(i))
			} else {
				text, err = DecompileToSourceWith(bytes, siblingsFor(file, bytes, w.named))
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
