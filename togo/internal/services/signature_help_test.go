package services

import (
	"sort"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/signatureHelp.test.ts. Signature help is the checker's
// resolveCallCandidates / parameterLabelsOf / signatureOfDeclaration over the
// call under the cursor; the server handler is a thin position-to-call wrapper.

func callAtMarker(t *testing.T, text string) (*compiler.Node, *compiler.Checker) {
	t.Helper()
	const marker = "/*|*/"
	offset := strings.Index(text, marker)
	clean := strings.Replace(text, marker, "", 1)
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.SetOpenDocument("file:///T.java", clean, 1)
	checker := compiler.NewChecker(program)
	sf := program.GetSourceFile("file:///T.java")
	for _, at := range []int{offset, offset - 1} {
		node := compiler.GetNodeAtPosition(sf, at)
		for ; node != nil; node = node.Parent {
			if node.Kind != compiler.CallExpression {
				continue
			}
			call := node.AsCallExpression()
			if offset > call.Expression.End && offset <= node.End {
				return node, checker
			}
		}
	}
	t.Fatal("no call at marker")
	return nil, nil
}

func TestCallCandidatesListOverloads(t *testing.T) {
	call, checker := callAtMarker(t, "class C { int f(int x){return x;} int f(String s){return 0;} void m(){ f(/*|*/ } }")
	var sigs []string
	for _, d := range checker.ResolveCallCandidates(call) {
		s, _ := checker.SignatureOfDeclaration(d)
		sigs = append(sigs, s)
	}
	sort.Strings(sigs)
	if len(sigs) != 2 || sigs[0] != "int f(String s)" || sigs[1] != "int f(int x)" {
		t.Errorf("candidates = %v", sigs)
	}
}

func TestParameterLabelsWrittenText(t *testing.T) {
	call, checker := callAtMarker(t, "class C { int add(int first, long second){return 0;} void m(){ add(1,/*|*/ } }")
	decl := checker.ResolveCall(call)
	labels := checker.ParameterLabelsOf(decl)
	if len(labels) != 2 || labels[0] != "int first" || labels[1] != "long second" {
		t.Errorf("labels = %v", labels)
	}
}

func TestCandidatesThroughReceiverInherited(t *testing.T) {
	call, checker := callAtMarker(t, "class B { void g(int x){} } class D extends B { void g(String s){} void m(){ this.g(/*|*/ } }")
	var labels []string
	for _, d := range checker.ResolveCallCandidates(call) {
		s, _ := checker.SignatureOfDeclaration(d)
		labels = append(labels, s)
	}
	sort.Strings(labels)
	if len(labels) != 2 || labels[0] != "void g(String s)" || labels[1] != "void g(int x)" {
		t.Errorf("candidates = %v", labels)
	}
}

func TestChosenOverloadMatchesArgs(t *testing.T) {
	call, checker := callAtMarker(t, "class C { int f(int x){return x;} int f(String s){return 0;} void m(){ f(\"a\"/*|*/) } }")
	resolved := checker.ResolveCall(call)
	if sig, _ := checker.SignatureOfDeclaration(resolved); sig != "int f(String s)" {
		t.Errorf("resolved = %q, want int f(String s)", sig)
	}
}
