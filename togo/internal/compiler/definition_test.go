package compiler

import (
	"strings"
	"testing"
)

// Port of src/services/definition.test.ts: go-to-definition on the `var`
// keyword navigates to the inferred type's declaration.
func TestVarKeywordResolvesToInferredTypeDeclaration(t *testing.T) {
	src := "class Foo {} class V { void m() { var f = new Foo(); } }"
	program := NewProgram()
	program.SetOpenDocument("file:///V.java", src, 1)
	checker := NewChecker(program)
	sf := program.GetSourceFile("file:///V.java")

	node := GetNodeAtPosition(sf, strings.Index(src, "var"))
	if node.Kind != VarType {
		t.Fatalf("node kind = %v, want VarType", node.Kind)
	}
	nameID := node.Parent.AsLocalVariableDeclarationStatement().Declarators.Nodes[0].AsVariableDeclarator().Name
	typ := checker.GetTypeOfExpression(nameID)
	if typ.Kind != TypeKindClass {
		t.Fatalf("type kind = %v, want Class", typ.Kind)
	}
	decl := GetDeclarationNameNode(typ.Symbol)
	if decl.AsIdentifier().Text != "Foo" {
		t.Errorf("declaration name = %q, want Foo", decl.AsIdentifier().Text)
	}
	if GetSourceFileOfNode(decl).AsSourceFile().FileName != "file:///V.java" {
		t.Error("declaration should be in V.java")
	}
}
