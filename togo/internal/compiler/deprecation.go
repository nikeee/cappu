package compiler

// Port of src/compiler/deprecation.ts: reading the @Deprecated annotation (JLS
// 9.6.4.6) off a declaration, and the shape of a deprecated *use* reported by the
// checker and the MCP server.

import "strings"

// Deprecation is the information a @Deprecated(since=..., forRemoval=...) carries.
type Deprecation struct {
	Since      string
	HasSince   bool
	ForRemoval bool
}

// DeprecatedUse is one use of a deprecated declaration.
type DeprecatedUse struct {
	Pos        int    // span of the referenced name
	End        int    //
	Name       string // the referenced name (method or type)
	Kind       string // "method" or "type"
	Since      string
	HasSince   bool
	ForRemoval bool
}

// ReadDeprecation reads a @Deprecated annotation off a declaration's modifiers,
// returning its since/forRemoval; ok is false when not deprecated. Matches the
// annotation by simple name (the standard java.lang.Deprecated).
func ReadDeprecation(declaration *Node) (Deprecation, bool) {
	for _, m := range arrayNodes(declModifiers(declaration)) {
		if m.Kind != Annotation {
			continue
		}
		ann := m.AsAnnotation()
		name := entityNameToString(ann.TypeName)
		if name != "Deprecated" && !strings.HasSuffix(name, ".Deprecated") {
			continue
		}
		dep := Deprecation{}
		for _, arg := range arrayNodes(ann.Args) {
			a := arg.AsAnnotationArgument()
			argName := "value"
			if a.Name != nil {
				argName = a.Name.AsIdentifier().Text
			}
			switch {
			case argName == "since" && a.Value.Kind == StringLiteral:
				dep.Since = a.Value.AsLiteralExpression().Value
				dep.HasSince = true
			case argName == "forRemoval" && a.Value.Kind == TrueKeyword:
				dep.ForRemoval = true
			}
		}
		return dep, true
	}
	return Deprecation{}, false
}

// SymbolDeprecation returns the deprecation of a symbol's declaration; ok is
// false when not deprecated. A field's declaration node is the VariableDeclarator
// while its @Deprecated sits on the enclosing FieldDeclaration, so read the
// annotation from the parent in that case.
func SymbolDeprecation(symbol *Symbol) (Deprecation, bool) {
	if symbol == nil {
		return Deprecation{}, false
	}
	decl := symbol.ValueDeclaration
	if decl == nil && len(symbol.Declarations) > 0 {
		decl = symbol.Declarations[0]
	}
	if decl != nil && decl.Kind == VariableDeclarator {
		decl = decl.Parent
	}
	return ReadDeprecation(decl)
}
