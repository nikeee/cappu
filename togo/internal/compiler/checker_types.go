package compiler

// The type model used by the checker. Java types: primitives, class/interface
// types (with type arguments), arrays, type variables, wildcards, intersections,
// the null type and an error type for the unknown/unresolved (so analysis
// degrades gracefully instead of throwing or reporting false errors).
// Port of src/compiler/checkerTypes.ts.

import "strings"

type TypeKind int

const (
	TypeKindPrimitive TypeKind = iota
	TypeKindClass
	TypeKindArray
	TypeKindTypeVariable
	TypeKindWildcard
	TypeKindIntersection
	TypeKindNull
	TypeKindError
)

// Type is a Java type in the checker's model. The active fields depend on Kind.
type Type struct {
	Kind TypeKind

	Name          string  // Primitive: int, long, boolean, ..., void
	Symbol        *Symbol // Class, TypeVariable
	TypeArguments []*Type // Class
	ElementType   *Type   // Array
	// Bound is the leftmost declared bound of a TypeVariable (filled in lazily by
	// the checker, since it may reference the variable itself), or a Wildcard's bound.
	Bound     *Type
	IsExtends bool    // Wildcard
	IsSuper   bool    // Wildcard
	Types     []*Type // Intersection
}

var (
	errorType = &Type{Kind: TypeKindError}
	nullType  = &Type{Kind: TypeKindNull}
)

var primitiveCache = map[string]*Type{}

func primitiveType(name string) *Type {
	if t, ok := primitiveCache[name]; ok {
		return t
	}
	t := &Type{Kind: TypeKindPrimitive, Name: name}
	primitiveCache[name] = t
	return t
}

func classType(symbol *Symbol, typeArguments []*Type) *Type {
	return &Type{Kind: TypeKindClass, Symbol: symbol, TypeArguments: typeArguments}
}

func arrayType(elementType *Type) *Type {
	return &Type{Kind: TypeKindArray, ElementType: elementType}
}

func typeVariable(symbol *Symbol) *Type {
	return &Type{Kind: TypeKindTypeVariable, Symbol: symbol}
}

func isError(t *Type) bool {
	return t.Kind == TypeKindError
}

// typeToString renders a human-readable form for hover/diagnostics.
func typeToString(t *Type) string {
	switch t.Kind {
	case TypeKindPrimitive:
		return t.Name
	case TypeKindClass:
		name := t.Symbol.EscapedName
		if len(t.TypeArguments) == 0 {
			return name
		}
		parts := make([]string, len(t.TypeArguments))
		for i, a := range t.TypeArguments {
			parts[i] = typeToString(a)
		}
		return name + "<" + strings.Join(parts, ", ") + ">"
	case TypeKindArray:
		return typeToString(t.ElementType) + "[]"
	case TypeKindTypeVariable:
		return t.Symbol.EscapedName
	case TypeKindWildcard:
		if t.IsExtends && t.Bound != nil {
			return "? extends " + typeToString(t.Bound)
		}
		if t.IsSuper && t.Bound != nil {
			return "? super " + typeToString(t.Bound)
		}
		return "?"
	case TypeKindIntersection:
		parts := make([]string, len(t.Types))
		for i, x := range t.Types {
			parts[i] = typeToString(x)
		}
		return strings.Join(parts, " & ")
	case TypeKindNull:
		return "null"
	default:
		return "<error>"
	}
}
