package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// `cappu compile --validate`: compile the same sources with javac and compare
// the bytecode via normalized disassembly (javap -c -p, constant-pool indices
// stripped). Print-free; the CLI renders the result. Port of
// src/compiler/validateJavac.ts.

// ValidationMismatch is one class that differed from javac.
type ValidationMismatch struct {
	ClassName string
	Detail    string
}

// ValidationResult is the outcome of validateAgainstJavac. Error != "" means
// javac/javap could not be run at all; otherwise OK reports whether every class
// compared equal (degraded placeholder bodies are skipped).
type ValidationResult struct {
	OK         bool
	Compared   int
	Mismatches []ValidationMismatch
	Error      string
}

// javapFor returns javap next to a configured javac, or plain "javap".
func javapFor(javacBin string) string {
	if strings.Contains(javacBin, string(filepath.Separator)) || strings.Contains(javacBin, "/") {
		return filepath.Join(filepath.Dir(javacBin), "javap")
	}
	return "javap"
}

func compareClass(ours, theirs *Disasm) string {
	if strings.Join(ours.Members, "\n") != strings.Join(theirs.Members, "\n") {
		return "declared members differ"
	}
	theirCode := map[string][]string{}
	for _, m := range theirs.Code {
		theirCode[m.Signature] = m.Instructions
	}
	for _, m := range ours.Code {
		if IsPlaceholderBody(m.Instructions) {
			continue
		}
		reference, ok := theirCode[m.Signature]
		if !ok {
			return "no javac counterpart for " + m.Signature
		}
		if len(m.Instructions) != len(reference) {
			return m.Signature + ": " + itoaV(len(m.Instructions)) + " vs " + itoaV(len(reference)) + " instructions"
		}
		for i := range m.Instructions {
			if m.Instructions[i] != reference[i] {
				return m.Signature + ": instruction " + itoaV(i) + ": '" + m.Instructions[i] + "' vs '" + reference[i] + "'"
			}
		}
	}
	return ""
}

func itoaV(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ValidateAgainstJavac compiles sourceFiles with javac into a temp dir and
// compares every class we wrote against javac's output for the same binary name.
func ValidateAgainstJavac(sourceFiles, written []string, javacBin string) ValidationResult {
	if javacBin == "" {
		javacBin = "javac"
	}
	tmp, err := os.MkdirTemp("", "cappu-validate-")
	if err != nil {
		return ValidationResult{Error: err.Error()}
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	args := append([]string{"-d", tmp, "--release", "21"}, sourceFiles...)
	cmd := exec.Command(javacBin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return ValidationResult{Error: javacBin + " failed: " + detail}
	}

	var javacClasses []string
	_ = filepath.WalkDir(tmp, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".class") {
			javacClasses = append(javacClasses, path)
		}
		return nil
	})
	javap := javapFor(javacBin)
	ours, err := DisasmFiles(written, javap)
	if err != nil {
		return ValidationResult{Error: "javap failed: " + err.Error()}
	}
	theirs, err := DisasmFiles(javacClasses, javap)
	if err != nil {
		return ValidationResult{Error: "javap failed: " + err.Error()}
	}

	var mismatches []ValidationMismatch
	compared := 0
	for name, disasm := range ours {
		reference, ok := theirs[name]
		if !ok {
			mismatches = append(mismatches, ValidationMismatch{ClassName: name, Detail: "javac produced no such class"})
			continue
		}
		compared++
		if detail := compareClass(disasm, reference); detail != "" {
			mismatches = append(mismatches, ValidationMismatch{ClassName: name, Detail: detail})
		}
	}
	if len(mismatches) > 0 {
		return ValidationResult{OK: false, Compared: compared, Mismatches: mismatches}
	}
	return ValidationResult{OK: true, Compared: compared}
}
