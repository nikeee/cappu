package services

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
)

// Port of src/services/callHierarchy.test.ts.

const chSrc = "class C {\n" +
	"  int target() { return 1; }\n" +
	"  int caller() { return target() + target(); }\n" +
	"  int other() { return caller(); }\n" +
	"}"

func chSetup(text string) (*compiler.Program, *compiler.Checker, *compiler.Node) {
	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	program.SetOpenDocument("file:///C.java", text, 1)
	return program, compiler.NewChecker(program), program.GetSourceFile("file:///C.java")
}

func TestPrepareCallHierarchy(t *testing.T) {
	_, checker, sf := chSetup(chSrc)
	items := PrepareCallHierarchy(checker, sf, strings.Index(chSrc, "target() { return 1"))
	if len(items) != 1 || items[0].Name != "target" {
		t.Fatalf("prepare = %+v, want [target]", items)
	}
}

func TestCallHierarchyIncoming(t *testing.T) {
	program, checker, sf := chSetup(chSrc)
	target := PrepareCallHierarchy(checker, sf, strings.Index(chSrc, "target() { return 1"))[0]
	incoming := CallHierarchyIncoming(program, checker, target)
	if len(incoming) != 1 || incoming[0].From.Name != "caller" {
		t.Fatalf("incoming = %+v, want one from caller", incoming)
	}
	if len(incoming[0].FromRanges) != 2 {
		t.Errorf("caller calls target twice, got %d ranges", len(incoming[0].FromRanges))
	}
}

func TestCallHierarchyOutgoing(t *testing.T) {
	program, checker, sf := chSetup(chSrc)
	caller := PrepareCallHierarchy(checker, sf, strings.Index(chSrc, "caller() { return target"))[0]
	outgoing := CallHierarchyOutgoing(program, checker, caller)
	if len(outgoing) != 1 || outgoing[0].To.Name != "target" {
		t.Fatalf("outgoing = %+v, want one to target", outgoing)
	}
	if len(outgoing[0].FromRanges) != 2 {
		t.Errorf("caller calls target twice, got %d ranges", len(outgoing[0].FromRanges))
	}
}

func TestPrepareCallHierarchyNotAMethod(t *testing.T) {
	_, checker, sf := chSetup(chSrc)
	if items := PrepareCallHierarchy(checker, sf, 0); len(items) != 0 {
		t.Errorf("prepare off any method = %+v, want empty", items)
	}
}
