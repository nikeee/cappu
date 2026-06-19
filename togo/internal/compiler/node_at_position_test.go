package compiler

import (
	"strings"
	"testing"
)

// Port of src/services/nodeAtPosition.test.ts.

const napSource = "class C {\n  int field;\n  void m() { int local = 1; }\n}\n"

func napFile() *Node { return ParseSourceFile("T.java", napSource) }

func TestGetNodeAtPositionDeepest(t *testing.T) {
	node := GetNodeAtPosition(napFile(), strings.Index(napSource, "field"))
	if node.Kind != Identifier || node.AsIdentifier().Text != "field" {
		t.Errorf("got kind %v text %q, want Identifier 'field'", node.Kind, nodeText(node))
	}
}

func TestGetIdentifierAtPositionResolves(t *testing.T) {
	id := GetIdentifierAtPosition(napFile(), strings.Index(napSource, "local"))
	if id == nil || id.AsIdentifier().Text != "local" {
		t.Errorf("got %v, want 'local'", id)
	}
}

func TestGetIdentifierAtPositionTrailingEdge(t *testing.T) {
	end := strings.Index(napSource, "field") + len("field")
	id := GetIdentifierAtPosition(napFile(), end)
	if id == nil || id.AsIdentifier().Text != "field" {
		t.Errorf("got %v, want 'field' at trailing edge", id)
	}
}

func TestGetIdentifierAtPositionOffName(t *testing.T) {
	if id := GetIdentifierAtPosition(napFile(), strings.Index(napSource, "{")); id != nil {
		t.Errorf("got %v, want nil off any name", id)
	}
}

func nodeText(n *Node) string {
	if n.Kind == Identifier {
		return n.AsIdentifier().Text
	}
	return ""
}
