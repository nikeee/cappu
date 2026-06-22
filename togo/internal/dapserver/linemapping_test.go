package dapserver

import (
	"testing"

	"github.com/nikeee/cappu/internal/jdwp"
)

func lines(pairs ...[2]int) []jdwp.LineTableEntry {
	out := make([]jdwp.LineTableEntry, len(pairs))
	for i, p := range pairs {
		out[i] = jdwp.LineTableEntry{LineCodeIndex: uint64(p[0]), LineNumber: int32(p[1])}
	}
	return out
}

func TestResolveExactLine(t *testing.T) {
	methods := []MethodLines{{MethodID: 1, Lines: lines([2]int{0, 3}, [2]int{5, 4}, [2]int{12, 6})}}
	got, ok := ResolveLine(methods, 4)
	if !ok || got.MethodID != 1 || got.Index != 5 || got.Line != 4 {
		t.Fatalf("got %+v ok %v", got, ok)
	}
}

func TestResolveAdjustsToNextLine(t *testing.T) {
	methods := []MethodLines{{MethodID: 1, Lines: lines([2]int{0, 3}, [2]int{5, 4}, [2]int{12, 6})}}
	got, ok := ResolveLine(methods, 5) // line 5 has no entry -> binds to line 6
	if !ok || got.Index != 12 || got.Line != 6 {
		t.Fatalf("got %+v ok %v", got, ok)
	}
}

func TestResolveAcrossMethods(t *testing.T) {
	methods := []MethodLines{
		{MethodID: 1, Lines: lines([2]int{0, 3}, [2]int{8, 10})},
		{MethodID: 2, Lines: lines([2]int{0, 6}, [2]int{4, 7})},
	}
	got, ok := ResolveLine(methods, 7)
	if !ok || got.MethodID != 2 || got.Index != 4 || got.Line != 7 {
		t.Fatalf("got %+v ok %v", got, ok)
	}
}

func TestResolveUnresolvable(t *testing.T) {
	methods := []MethodLines{{MethodID: 1, Lines: lines([2]int{0, 3}, [2]int{5, 4})}}
	if _, ok := ResolveLine(methods, 99); ok {
		t.Fatal("expected unresolvable")
	}
}

func TestResolveLowerIndexWinsOnTie(t *testing.T) {
	methods := []MethodLines{
		{MethodID: 1, Lines: lines([2]int{20, 8})},
		{MethodID: 2, Lines: lines([2]int{4, 8})},
	}
	got, ok := ResolveLine(methods, 8)
	if !ok || got.MethodID != 2 || got.Index != 4 {
		t.Fatalf("got %+v", got)
	}
}

func TestSignatureToType(t *testing.T) {
	cases := map[string]string{
		"I":                  "int",
		"Z":                  "boolean",
		"Ljava/lang/String;": "java.lang.String",
		"[I":                 "int[]",
		"[[Ljava/util/List;": "java.util.List[][]",
	}
	for sig, want := range cases {
		if got := SignatureToType(sig); got != want {
			t.Errorf("SignatureToType(%q) = %q, want %q", sig, got, want)
		}
	}
}

func TestSignatureTagByte(t *testing.T) {
	if SignatureTagByte("I") != 'I' || SignatureTagByte("Ljava/lang/String;") != 'L' || SignatureTagByte("[I") != '[' {
		t.Fatal("wrong tag byte")
	}
}
