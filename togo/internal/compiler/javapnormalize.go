package compiler

import (
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// Normalized disassembly via `javap -c -p`, used to compare our emitted bytecode
// against javac's. Constant-pool indices are stripped so only mnemonics +
// symbolic operands remain (stable across compilers); this is the form checked
// into the *-baselines fixtures and what `cappu compile --validate` compares.
// Port of src/compiler/javapNormalize.ts.

// Disasm is one class's normalized disassembly.
type Disasm struct {
	Members []string
	Code    []DisasmMethod // [methodSignature, instructionLines]
}

// DisasmMethod is one method's signature line and its instruction mnemonics.
type DisasmMethod struct {
	Signature    string
	Instructions []string
}

var (
	javapHeaderRe  = regexp.MustCompile(`(?:class|interface|enum)\s+[\w$.]+`)
	javapNameRe    = regexp.MustCompile(`(?:class|interface|enum)\s+([\w$.]+)`)
	javapInstrRe   = regexp.MustCompile(`^\d+:`)
	javapPcPrefix  = regexp.MustCompile(`^\d+:\s*`)
	javapCpIndexRe = regexp.MustCompile(`#\d+`)
	javapWsRe      = regexp.MustCompile(`\s+`)
)

// DisasmFiles disassembles one or more class files in a single javap invocation,
// keyed by the (binary) class name javap prints.
func DisasmFiles(classFiles []string, javapBin string) (map[string]*Disasm, error) {
	if javapBin == "" {
		javapBin = "javap"
	}
	out, err := exec.Command(javapBin, append([]string{"-c", "-p"}, classFiles...)...).Output()
	if err != nil {
		return nil, err
	}
	m := map[string]*Disasm{}
	var cur *Disasm
	var method *DisasmMethod
	for _, raw := range strings.Split(string(out), "\n") {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		header := raw == t && strings.HasSuffix(t, "{") && javapHeaderRe.MatchString(t)
		switch {
		case header:
			name := javapNameRe.FindStringSubmatch(t)[1]
			cur = &Disasm{}
			m[name] = cur
			method = nil
		case cur == nil:
			continue
		case javapInstrRe.MatchString(t):
			if method != nil {
				instr := javapPcPrefix.ReplaceAllString(t, "")
				instr = javapCpIndexRe.ReplaceAllString(instr, "#")
				instr = strings.TrimSpace(javapWsRe.ReplaceAllString(instr, " "))
				method.Instructions = append(method.Instructions, instr)
			}
		case strings.HasSuffix(t, ";") && !strings.HasPrefix(t, "//"):
			cur.Members = append(cur.Members, t)
			if strings.Contains(t, "(") {
				cur.Code = append(cur.Code, DisasmMethod{Signature: t})
				method = &cur.Code[len(cur.Code)-1]
			} else {
				method = nil // a field (or `static {};`): no comparable code
			}
		}
	}
	for _, d := range m {
		slices.Sort(d.Members)
	}
	return m, nil
}

// placeholderBodies are the trivial method bodies the emitter falls back to for
// an unsupported construct; a method whose disassembly matches one is degraded
// and skipped when comparing to javac.
var placeholderBodies = [][]string{
	{"return"},
	{"iconst_0", "ireturn"},
	{"lconst_0", "lreturn"},
	{"fconst_0", "freturn"},
	{"dconst_0", "dreturn"},
	{"aconst_null", "areturn"},
}

// IsPlaceholderBody reports whether an instruction stream is a degraded placeholder.
func IsPlaceholderBody(instrs []string) bool {
	for _, p := range placeholderBodies {
		if len(p) != len(instrs) {
			continue
		}
		match := true
		for i := range p {
			if p[i] != instrs[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
