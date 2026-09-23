package mcp

// The file-shaped MCP tools: `cappu decompile` and `cappu format` as read-only
// tools. Neither needs the Java program - they work on one file (or one class
// on the configured classPath) and return text; nothing is written. Errors go
// back to the caller as the tool's error. Port of src/services/mcpFiles.ts.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/format"
)

// DecompileArgs are the `decompile` tool's arguments.
type DecompileArgs struct {
	// File is the path to a `.class` file.
	File *string `json:"file"`
	// ClassName is the binary name of a class on the configured classPath
	// (`com.acme.Foo$Bar`).
	ClassName *string `json:"className"`
	// Disasm asks for bytecode in `javap -c -p` layout instead of source.
	Disasm bool `json:"disasm"`
}

// DecompileResult is the `decompile` tool's result.
type DecompileResult struct {
	Source string `json:"source"`
}

// FormatResult is the `format` tool's result.
type FormatResult struct {
	Formatted string `json:"formatted"`
	Changed   bool   `json:"changed"`
}

// readErrorText words I/O failures the way the CLI does (the TS build maps
// Node's codes to the same text; see src/cli/decompile.ts).
func readErrorText(err error) string {
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

func readFile(file string) ([]byte, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %s", file, readErrorText(err))
	}
	return b, nil
}

// findClass is the bytes of className from the config's classPath: a jar entry
// (the jar given directly, or found anywhere under a directory entry, in path
// order), or the `.class` at its binary-name path under a directory entry.
func findClass(cfg *config.Config, className string) ([]byte, error) {
	if cfg == nil {
		return nil, errors.New("className needs a project config (cappu.json) with a classPath")
	}
	entryName := strings.ReplaceAll(className, ".", "/") + ".class"
	inJar := func(jar string) []byte {
		data, err := os.ReadFile(jar)
		if err != nil {
			return nil // an unreadable jar matches nothing
		}
		for _, entry := range compiler.ReadZipEntries(data) {
			if entry.Name == entryName {
				return entry.Read()
			}
		}
		return nil
	}
	for _, raw := range cfg.CompilerOptions.ClassPath {
		entry := cfg.ResolvePath(raw)
		if strings.HasSuffix(entry, ".jar") {
			if b := inJar(entry); b != nil {
				return b, nil
			}
			continue
		}
		if b, err := os.ReadFile(filepath.Join(entry, entryName)); err == nil {
			return b, nil
		}
		var found []byte
		// WalkDir is in path order, as the TS build sorts its glob.
		_ = filepath.WalkDir(entry, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jar") {
				return nil
			}
			if b := inJar(path); b != nil {
				found = b
				return fs.SkipAll
			}
			return nil
		})
		if found != nil {
			return found, nil
		}
	}
	return nil, fmt.Errorf("class %s not found on the classPath", className)
}

// decompileToSource is cli.DecompileToSource (that package wires `cappu mcp`,
// so this one cannot import it): the formatter lays out the decompiler's rough
// text, and a body it refuses stays unformatted.
func decompileToSource(b []byte, siblings compiler.Siblings) (string, error) {
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

// DecompileTool is the `decompile` tool.
func DecompileTool(cfg *config.Config, args DecompileArgs) (DecompileResult, error) {
	file, className := "", ""
	if args.File != nil {
		file = *args.File
	}
	if args.ClassName != nil {
		className = *args.ClassName
	}
	if (file == "") == (className == "") {
		return DecompileResult{}, errors.New("give exactly one of file or className")
	}
	var b []byte
	var err error
	// The classes javac wrote beside this one: next to the file, or on the
	// same classPath the class itself came from.
	var siblings compiler.Siblings
	if file != "" {
		b, err = readFile(file)
		dir := filepath.Dir(file)
		siblings = func(binaryName string) ([]byte, bool) {
			name := binaryName
			if slash := strings.LastIndex(name, "/"); slash >= 0 {
				name = name[slash+1:]
			}
			b, err := os.ReadFile(filepath.Join(dir, name+".class"))
			return b, err == nil
		}
	} else {
		b, err = findClass(cfg, className)
		siblings = func(binaryName string) ([]byte, bool) {
			b, err := findClass(cfg, strings.ReplaceAll(binaryName, "/", "."))
			return b, err == nil
		}
	}
	if err != nil {
		return DecompileResult{}, err
	}
	var text string
	if args.Disasm {
		text, err = compiler.Disassemble(b)
	} else {
		text, err = decompileToSource(b, siblings)
	}
	if err != nil {
		// A class-file error names what it was read from, like the I/O ones.
		return DecompileResult{}, fmt.Errorf("%s: %s", file+className, err.Error())
	}
	return DecompileResult{Source: text}, nil
}

// FormatTool is the `format` tool: the file as `cappu format --write` would
// leave it. Changed is false when it is already formatted.
func FormatTool(options format.FormatOptions, file string) (FormatResult, error) {
	b, err := readFile(file)
	if err != nil {
		return FormatResult{}, err
	}
	text := string(b)
	formatted, err := format.FormatSource(text, options, file)
	if errors.Is(err, format.ErrUnsupportedSyntax) {
		// Both builds word this the same, whichever unsupported construct it was.
		// Anything else is a formatter bug and stays one.
		return FormatResult{}, fmt.Errorf("%s: unsupported syntax", file)
	}
	if err != nil {
		return FormatResult{}, err
	}
	return FormatResult{Formatted: formatted, Changed: formatted != text}, nil
}
