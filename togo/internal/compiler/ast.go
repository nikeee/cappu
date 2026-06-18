package compiler

// AST core. Follows tsgo's representation: a single Node struct carrying the
// Kind discriminator, source range, parent link and a `data` interface holding
// the concrete per-kind payload (IdentifierData, BinaryExpressionData, ...).
// As<Kind> accessors recover the concrete type; ForEachChild walks children via
// the data type's forEachChild. The node-specific structs, factory constructors
// and forEachChild methods live in nodes.go. Port of the AST parts of
// src/compiler/types.ts + parser.ts (forEachChild).

// Visitor visits a node and returns true to stop the traversal.
type Visitor func(*Node) bool

// nodeData is the per-kind payload; every concrete node struct implements it.
type nodeData interface {
	forEachChild(v Visitor) bool
}

// Node is one AST node: a Kind, a source range [Pos,End), flags, a parent link
// (set by the binder/parser) and the concrete payload.
type Node struct {
	Kind   SyntaxKind
	Pos    int
	End    int
	Flags  NodeFlags
	Parent *Node
	data   nodeData
}

// ForEachChild invokes v on each direct child; it stops and returns true as soon
// as v returns true.
func (n *Node) ForEachChild(v Visitor) bool {
	if n == nil || n.data == nil {
		return false
	}
	return n.data.forEachChild(v)
}

// NodeArray is a positioned list of nodes (mirrors the TS NodeArray).
type NodeArray struct {
	Nodes            []*Node
	Pos              int
	End              int
	HasTrailingComma bool
}

// Len returns the number of nodes (0 for a nil array).
func (a *NodeArray) Len() int {
	if a == nil {
		return 0
	}
	return len(a.Nodes)
}

// visit calls v on n when n is non-nil.
func visit(v Visitor, n *Node) bool {
	if n != nil {
		return v(n)
	}
	return false
}

// visitNodes calls v on each node of a (possibly nil) array.
func visitNodes(v Visitor, a *NodeArray) bool {
	if a == nil {
		return false
	}
	for _, n := range a.Nodes {
		if n != nil && v(n) {
			return true
		}
	}
	return false
}

// NodeFactory creates nodes. It mirrors tsgo's factory; positions are stamped by
// the parser's finishNode, not here.
type NodeFactory struct{}

func (f *NodeFactory) newNode(kind SyntaxKind, data nodeData) *Node {
	return &Node{Kind: kind, data: data}
}
