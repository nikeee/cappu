package compiler

// Locate the AST node at a character offset - the basis for position-driven LSP
// requests (definition, hover, references, completion). Walks down via
// ForEachChild into the deepest node whose [pos, end) span contains the offset.
// Port of src/services/nodeAtPosition.ts.

func containsOffset(node *Node, offset int) bool {
	return offset >= node.Pos && offset < node.End
}

// GetNodeAtPosition returns the deepest node whose span contains the offset (the
// SourceFile if none deeper).
func GetNodeAtPosition(root *Node, offset int) *Node {
	current := root
	for {
		var found *Node
		current.ForEachChild(func(c *Node) bool {
			if containsOffset(c, offset) {
				found = c
				return true
			}
			return false
		})
		if found == nil {
			return current
		}
		current = found
	}
}

// GetIdentifierAtPosition returns the Identifier at the offset, if the cursor is
// on a name, else nil. Trailing-edge offsets (cursor just past the name) are
// accepted so "foo|" resolves.
func GetIdentifierAtPosition(root *Node, offset int) *Node {
	node := GetNodeAtPosition(root, offset)
	if node.Kind == Identifier {
		return node
	}
	// Cursor at the trailing edge of an identifier (offset == end): retry one
	// position back so "name|" still resolves.
	if offset > 0 {
		node = GetNodeAtPosition(root, offset-1)
		if node.Kind == Identifier {
			return node
		}
	}
	return nil
}
