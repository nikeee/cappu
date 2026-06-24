package compiler

// Port of src/compiler/nullness.ts: reading jspecify nullness annotations
// (@Nullable / @NonNull / @NullMarked / @NullUnmarked, https://jspecify.dev/docs/spec/)
// off a declaration, and deciding whether a target position (a parameter, return,
// field or local) is non-null. The checker uses these to warn when a possibly-null
// value reaches a non-null position. Purely syntactic: declared nullness is read,
// never narrowed (no flow analysis); generic/array-component nullness is out of scope.

import (
	"strings"

	"github.com/nikeee/cappu/internal/config"
)

// Nullness is the three states a target position can be in; "unknown" stays silent.
type Nullness string

const (
	NullnessNonNull  Nullness = "nonNull"
	NullnessNullable Nullness = "nullable"
	NullnessUnknown  Nullness = "unknown"
)

// NullnessAnnotations are the annotation simple-name sets the checker matches
// against, resolved once from the config (FQDN) lists.
type NullnessAnnotations struct {
	nullable     map[string]bool
	nonNull      map[string]bool
	nullMarked   map[string]bool
	nullUnmarked map[string]bool
}

// Match by simple name so both `@Nullable` and `@org.jspecify.annotations.Nullable`
// hit the same configured entry (the same trick readDeprecation uses).
func simpleName(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

func nameSet(xs []string) map[string]bool {
	s := make(map[string]bool, len(xs))
	for _, x := range xs {
		s[simpleName(x)] = true
	}
	return s
}

// resolveNullnessAnnotations turns the config lists into simple-name sets, or
// returns nil when nullness checking is disabled.
func resolveNullnessAnnotations(o *config.Nullness) *NullnessAnnotations {
	if o == nil || !o.Enabled {
		return nil
	}
	return &NullnessAnnotations{
		nullable:     nameSet(o.NullableAnnotations),
		nonNull:      nameSet(o.NonNullAnnotations),
		nullMarked:   nameSet(o.NullMarkedAnnotations),
		nullUnmarked: nameSet(o.NullUnmarkedAnnotations),
	}
}

// A declaration's annotations live on its modifiers (types, methods, params,
// fields, locals) or on an `annotations` field (record components, package /
// module declarations).
func nullnessAnnotationsOf(node *Node) []*Node {
	if node == nil {
		return nil
	}
	if mods := declModifiers(node); mods != nil {
		return mods.Nodes
	}
	switch node.Kind {
	case LocalVariableDeclarationStatement:
		return arrayNodes(node.AsLocalVariableDeclarationStatement().Modifiers)
	case RecordComponent:
		return arrayNodes(node.AsRecordComponent().Annotations)
	case PackageDeclaration:
		return arrayNodes(node.AsPackageDeclaration().Annotations)
	}
	return nil
}

func hasNullnessAnnotation(node *Node, names map[string]bool) bool {
	for _, m := range nullnessAnnotationsOf(node) {
		if m.Kind != Annotation {
			continue
		}
		if names[simpleName(entityNameToString(m.AsAnnotation().TypeName))] {
			return true
		}
	}
	return false
}

// carrierOf returns the node carrying the modifiers + type for a symbol's
// declaration. A field or local's annotation sits on the enclosing declaration,
// not the VariableDeclarator.
func carrierOf(decl *Node) *Node {
	if decl != nil && decl.Kind == VariableDeclarator {
		return decl.Parent
	}
	return decl
}

func typeNodeOf(carrier *Node) *Node {
	switch carrier.Kind {
	case MethodDeclaration:
		return carrier.AsMethodDeclaration().ReturnType
	case Parameter:
		return carrier.AsParameter().Type
	case FieldDeclaration:
		return carrier.AsFieldDeclaration().Type
	case LocalVariableDeclarationStatement:
		return carrier.AsLocalVariableDeclarationStatement().Type
	case RecordComponent:
		return carrier.AsRecordComponent().Type
	}
	return nil
}

// Only reference types carry nullness; a primitive (or an unresolved `var`) never
// does. Arrays are reference types (the variable itself, not its elements).
func isReferenceTypeNode(t *Node) bool {
	return t != nil && (t.Kind == TypeReference || t.Kind == ArrayType)
}

// isNullMarked reports whether node is inside a @NullMarked scope. The nearest
// enclosing @NullMarked / @NullUnmarked on the declaration, an enclosing type, or
// this file's package declaration wins. Cross-file package-info.java is not consulted.
func (a *NullnessAnnotations) isNullMarked(node *Node) bool {
	for n := node; n != nil; n = n.Parent {
		if hasNullnessAnnotation(n, a.nullUnmarked) {
			return false
		}
		if hasNullnessAnnotation(n, a.nullMarked) {
			return true
		}
		if n.Kind == SourceFile {
			pkg := n.AsSourceFile().PackageDeclaration
			if hasNullnessAnnotation(pkg, a.nullUnmarked) {
				return false
			}
			if hasNullnessAnnotation(pkg, a.nullMarked) {
				return true
			}
		}
	}
	return false
}

// targetNullness is the declared nullness of a target position (the carrier node
// holding the modifiers + type). Explicit @Nullable/@NonNull wins; otherwise a
// reference type in a @NullMarked scope is non-null; everything else is unknown.
func (a *NullnessAnnotations) targetNullness(carrier *Node) Nullness {
	if carrier == nil {
		return NullnessUnknown
	}
	if hasNullnessAnnotation(carrier, a.nullable) {
		return NullnessNullable
	}
	if hasNullnessAnnotation(carrier, a.nonNull) {
		return NullnessNonNull
	}
	if !isReferenceTypeNode(typeNodeOf(carrier)) {
		return NullnessUnknown
	}
	if a.isNullMarked(carrier) {
		return NullnessNonNull
	}
	return NullnessUnknown
}
