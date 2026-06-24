package compiler

// Port of src/compiler/nullness.ts: reading jspecify nullness annotations
// (@Nullable / @NonNull / @NullMarked / @NullUnmarked, https://jspecify.dev/docs/spec/)
// off declarations and type nodes. The checker turns these into a nullness facet on
// the type model (see checker_types.go) and warns when a possibly-null value reaches
// a non-null position. Purely syntactic: declared nullness is read, never narrowed.

import (
	"strings"

	"github.com/nikeee/cappu/internal/config"
)

// Nullness is a position's nullness; "" means unknown and stays silent.
type Nullness string

const (
	NullnessNonNull  Nullness = "nonNull"
	NullnessNullable Nullness = "nullable"
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
// fields, locals) or an `annotations` field (record components, package /
// module declarations, type-use nodes).
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
	case TypeReference:
		return arrayNodes(node.AsTypeReference().Annotations)
	case PrimitiveType:
		return arrayNodes(node.AsPrimitiveType().Annotations)
	}
	return nil
}

// hasNullnessAnnotation reports whether node carries an annotation in names.
func hasNullnessAnnotation(node *Node, names map[string]bool) bool {
	if node == nil {
		return false
	}
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

func nullnessFrom(node *Node, a *NullnessAnnotations) Nullness {
	if hasNullnessAnnotation(node, a.nullable) {
		return NullnessNullable
	}
	if hasNullnessAnnotation(node, a.nonNull) {
		return NullnessNonNull
	}
	return ""
}

// carrierOf returns the node carrying the modifiers for a symbol's declaration. A
// field or local's annotation sits on the enclosing declaration, not the declarator.
func carrierOf(decl *Node) *Node {
	if decl != nil && decl.Kind == VariableDeclarator {
		return decl.Parent
	}
	return decl
}

// readDeclaredNullness is the nullness declared by a declaration's own modifiers.
func readDeclaredNullness(decl *Node, a *NullnessAnnotations) Nullness {
	return nullnessFrom(carrierOf(decl), a)
}

// typeUseNullness is the nullness written as a type-use annotation on a type node.
func typeUseNullness(typeNode *Node, a *NullnessAnnotations) Nullness {
	return nullnessFrom(typeNode, a)
}

// isReferenceTypeNode reports whether a type node is a reference type (only those
// carry nullness; a primitive or `var` never does). Arrays are reference types.
func isReferenceTypeNode(t *Node) bool {
	return t != nil && (t.Kind == TypeReference || t.Kind == ArrayType)
}
