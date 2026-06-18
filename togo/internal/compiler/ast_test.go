package compiler

import "testing"

// childKinds collects the kinds of a node's direct children, in traversal order.
func childKinds(n *Node) []SyntaxKind {
	var ks []SyntaxKind
	n.ForEachChild(func(c *Node) bool {
		ks = append(ks, c.Kind)
		return false
	})
	return ks
}

func eqKinds(t *testing.T, label string, got, want []SyntaxKind) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v, want %v", label, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: got %v, want %v", label, got, want)
			return
		}
	}
}

// Builds `package com.example; import Foo; class C { int x = 1 + 2; }` by hand
// and checks the Node model, factory, As accessors and ForEachChild order.
func TestASTFactoryAndTraversal(t *testing.T) {
	f := &NodeFactory{}

	pkgName := f.NewQualifiedName(f.NewIdentifier("com"), f.NewIdentifier("example"))
	pkg := f.NewPackageDeclaration(nil, pkgName)
	imp := f.NewImportDeclaration(false, f.NewIdentifier("Foo"), false)

	one := f.NewLiteralExpression(NumericLiteral, "1")
	two := f.NewLiteralExpression(NumericLiteral, "2")
	sum := f.NewBinaryExpression(one, PlusToken, two)
	declarator := f.NewVariableDeclarator(f.NewIdentifier("x"), 0, sum)
	field := f.NewFieldDeclaration(nil, f.NewPrimitiveType(IntKeyword),
		&NodeArray{Nodes: []*Node{declarator}})
	class := f.NewClassDeclaration(nil, f.NewIdentifier("C"), nil, nil, nil, nil,
		&NodeArray{Nodes: []*Node{field}})

	sf := f.NewSourceFile(pkg, &NodeArray{Nodes: []*Node{imp}}, &NodeArray{Nodes: []*Node{class}}, nil, nil)

	// Kinds and As accessors.
	if sf.Kind != SourceFile {
		t.Fatalf("root kind = %v", sf.Kind)
	}
	if sf.AsSourceFile().PackageDeclaration != pkg {
		t.Error("AsSourceFile().PackageDeclaration mismatch")
	}
	if got := pkgName.AsQualifiedName().Left.AsIdentifier().Text; got != "com" {
		t.Errorf("qualified-name left = %q, want com", got)
	}
	if field.AsFieldDeclaration().Type.AsPrimitiveType().Keyword != IntKeyword {
		t.Error("field type keyword mismatch")
	}
	if sum.AsBinaryExpression().OperatorToken != PlusToken {
		t.Error("binary operator mismatch")
	}
	if one.AsLiteralExpression().Value != "1" {
		t.Error("literal value mismatch")
	}

	// ForEachChild traversal order (nil children skipped).
	eqKinds(t, "sourceFile", childKinds(sf), []SyntaxKind{PackageDeclaration, ImportDeclaration, ClassDeclaration})
	eqKinds(t, "class", childKinds(class), []SyntaxKind{Identifier, FieldDeclaration})
	eqKinds(t, "field", childKinds(field), []SyntaxKind{PrimitiveType, VariableDeclarator})
	eqKinds(t, "declarator", childKinds(declarator), []SyntaxKind{Identifier, BinaryExpression})
	eqKinds(t, "sum", childKinds(sum), []SyntaxKind{NumericLiteral, NumericLiteral})
	eqKinds(t, "package", childKinds(pkg), []SyntaxKind{QualifiedName})
}

// ForEachChild must stop early when the visitor returns true.
func TestForEachChildStops(t *testing.T) {
	f := &NodeFactory{}
	sum := f.NewBinaryExpression(f.NewLiteralExpression(NumericLiteral, "1"), PlusToken, f.NewLiteralExpression(NumericLiteral, "2"))
	visited := 0
	stopped := sum.ForEachChild(func(*Node) bool {
		visited++
		return true // stop after the first child
	})
	if !stopped || visited != 1 {
		t.Errorf("stopped=%v visited=%d, want true/1", stopped, visited)
	}
}
