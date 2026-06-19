// Package mcp implements the agent-facing (Model Context Protocol) symbol tools
// over the compiler front end. Port of src/services/mcp*.
package mcp

// Turns an agent-supplied symbol reference into engine symbols. References are
// name-addressed (agents do not have file offsets): either a type (FQN
// "com.foo.Bar" or a bare simple name "Bar") or a member "Type#member". Only
// declared members are resolved; inherited members are out of scope for now.
// Port of src/services/mcpResolve.ts.

import (
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
)

func resolveType(typeRef string, index *compiler.GlobalIndex) []*compiler.Symbol {
	if direct := index.GetType(compiler.Fqn(typeRef)); direct != nil {
		return []*compiler.Symbol{direct}
	}
	var out []*compiler.Symbol
	for _, fqn := range index.FindFqnsBySimpleName(typeRef) {
		if s := index.GetType(fqn); s != nil {
			out = append(out, s)
		}
	}
	return out
}

// ResolveSymbolRef resolves an agent-supplied reference ("Type" or "Type#member").
func ResolveSymbolRef(ref string, index *compiler.GlobalIndex) []*compiler.Symbol {
	hash := strings.Index(ref, "#")
	if hash < 0 {
		return resolveType(ref, index)
	}
	typeRef := ref[:hash]
	memberName := ref[hash+1:]
	var members []*compiler.Symbol
	for _, t := range resolveType(typeRef, index) {
		if member := t.Members[memberName]; member != nil {
			members = append(members, member)
		}
	}
	return members
}
