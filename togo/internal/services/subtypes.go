package services

// Reverse subtype index: super type symbol -> the type declarations that
// directly extend/implement it, built in one workspace pass and memoized on the
// program generation. Backs textDocument/implementation, transitive
// implementation counts in code lenses, and (later) type/call hierarchy.
// Port of src/services/subtypes.ts.

import "github.com/nikeee/cappu/internal/compiler"

// SubtypeIndex is a reverse map from super types to their declared subtypes.
type SubtypeIndex struct {
	direct map[*compiler.Symbol][]*compiler.Symbol
}

// DirectSubtypesOf returns declarations whose extends/implements names the symbol.
func (s *SubtypeIndex) DirectSubtypesOf(superType *compiler.Symbol) []*compiler.Symbol {
	return s.direct[superType]
}

// AllSubtypesOf returns all transitive subtypes, direct ones first (BFS order).
func (s *SubtypeIndex) AllSubtypesOf(superType *compiler.Symbol) []*compiler.Symbol {
	seen := map[*compiler.Symbol]bool{}
	queue := append([]*compiler.Symbol{}, s.direct[superType]...)
	var out []*compiler.Symbol
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if seen[next] {
			continue
		}
		seen[next] = true
		out = append(out, next)
		queue = append(queue, s.direct[next]...)
	}
	return out
}

type subtypeCacheEntry struct {
	generation compiler.Generation
	index      *SubtypeIndex
}

var subtypeCache = map[*compiler.Program]subtypeCacheEntry{}

// GetSubtypeIndex builds (or returns the memoized) subtype index for a program.
func GetSubtypeIndex(program *compiler.Program) *SubtypeIndex {
	generation := program.GetGeneration()
	if cached, ok := subtypeCache[program]; ok && cached.generation == generation {
		return cached.index
	}

	direct := map[*compiler.Symbol][]*compiler.Symbol{}
	for _, uri := range program.GetAllUris() {
		if compiler.IsSyntheticURI(uri) {
			continue // stub types never extend user code
		}
		sourceFile := program.GetSourceFile(uri)
		if sourceFile == nil {
			continue
		}
		var visit compiler.Visitor
		visit = func(node *compiler.Node) bool {
			if isTypeDeclaration(node) && node.Symbol != nil {
				for _, superType := range superTypeNodes(node) {
					if superType.Kind != compiler.TypeReference {
						continue
					}
					superSymbol := compiler.ResolveTypeEntityName(superType.AsTypeReference().TypeName, node, program)
					if superSymbol == nil {
						continue
					}
					direct[superSymbol] = append(direct[superSymbol], node.Symbol)
				}
			}
			node.ForEachChild(visit)
			return false
		}
		visit(sourceFile)
	}

	index := &SubtypeIndex{direct: direct}
	subtypeCache[program] = subtypeCacheEntry{generation: generation, index: index}
	return index
}

// FindMethodImplementations returns the concrete method declarations
// implementing/overriding method (matched by name and arity) in the transitive
// subtypes of its declaring type.
func FindMethodImplementations(method *compiler.Node, program *compiler.Program) []*compiler.Node {
	if method.Parent == nil || method.Parent.Symbol == nil {
		return nil
	}
	owner := method.Parent.Symbol
	md := method.AsMethodDeclaration()
	var result []*compiler.Node
	for _, subtype := range GetSubtypeIndex(program).AllSubtypesOf(owner) {
		declaration := symbolDeclaration(subtype)
		if declaration == nil {
			continue
		}
		for _, member := range declMembers(declaration) {
			if member.Kind != compiler.MethodDeclaration {
				continue
			}
			cm := member.AsMethodDeclaration()
			if cm.Body != nil && cm.Name.AsIdentifier().Text == md.Name.AsIdentifier().Text &&
				cm.Parameters.Len() == md.Parameters.Len() {
				result = append(result, member)
			}
		}
	}
	return result
}

// DeclarationName returns the name node of a symbol's primary declaration.
func DeclarationName(symbol *compiler.Symbol) *compiler.Node {
	declaration := symbolDeclaration(symbol)
	if declaration == nil {
		return nil
	}
	return declName(declaration)
}
