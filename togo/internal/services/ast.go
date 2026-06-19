// Package services implements the language-service layer (hover, definition,
// references, completion, symbols, signature help, semantic tokens, lenses,
// code actions) over the compiler front end. Port of src/services/*.
package services

import "github.com/nikeee/cappu/internal/compiler"

func isTypeDeclaration(node *compiler.Node) bool {
	switch node.Kind {
	case compiler.ClassDeclaration, compiler.InterfaceDeclaration, compiler.EnumDeclaration,
		compiler.AnnotationTypeDeclaration, compiler.RecordDeclaration:
		return true
	default:
		return false
	}
}

// superTypeNodes returns the direct super-type references of a type declaration.
func superTypeNodes(node *compiler.Node) []*compiler.Node {
	var out []*compiler.Node
	switch node.Kind {
	case compiler.ClassDeclaration:
		c := node.AsClassDeclaration()
		if c.ExtendsType != nil {
			out = append(out, c.ExtendsType)
		}
		out = appendNodes(out, c.ImplementsTypes)
	case compiler.InterfaceDeclaration:
		out = appendNodes(out, node.AsInterfaceDeclaration().ExtendsTypes)
	case compiler.EnumDeclaration:
		out = appendNodes(out, node.AsEnumDeclaration().ImplementsTypes)
	case compiler.RecordDeclaration:
		out = appendNodes(out, node.AsRecordDeclaration().ImplementsTypes)
	}
	return out
}

// declMembers returns the member declarations of a type declaration.
func declMembers(decl *compiler.Node) []*compiler.Node {
	switch decl.Kind {
	case compiler.ClassDeclaration:
		return nodesOf(decl.AsClassDeclaration().Members)
	case compiler.InterfaceDeclaration:
		return nodesOf(decl.AsInterfaceDeclaration().Members)
	case compiler.EnumDeclaration:
		return nodesOf(decl.AsEnumDeclaration().Members)
	case compiler.RecordDeclaration:
		return nodesOf(decl.AsRecordDeclaration().Members)
	case compiler.AnnotationTypeDeclaration:
		return nodesOf(decl.AsAnnotationTypeDeclaration().Members)
	default:
		return nil
	}
}

// declName returns the name Identifier of a declaration, if it has one.
func declName(decl *compiler.Node) *compiler.Node {
	switch decl.Kind {
	case compiler.ClassDeclaration:
		return decl.AsClassDeclaration().Name
	case compiler.InterfaceDeclaration:
		return decl.AsInterfaceDeclaration().Name
	case compiler.EnumDeclaration:
		return decl.AsEnumDeclaration().Name
	case compiler.AnnotationTypeDeclaration:
		return decl.AsAnnotationTypeDeclaration().Name
	case compiler.RecordDeclaration:
		return decl.AsRecordDeclaration().Name
	case compiler.MethodDeclaration:
		return decl.AsMethodDeclaration().Name
	case compiler.ConstructorDeclaration:
		return decl.AsConstructorDeclaration().Name
	case compiler.FieldDeclaration:
		return nil
	case compiler.VariableDeclarator:
		return decl.AsVariableDeclarator().Name
	case compiler.Parameter:
		return decl.AsParameter().Name
	case compiler.EnumConstantDeclaration:
		return decl.AsEnumConstantDeclaration().Name
	case compiler.TypeParameter:
		return decl.AsTypeParameter().Name
	default:
		return nil
	}
}

// declModifiers returns the modifiers NodeArray of a declaration, or nil.
func declModifiers(node *compiler.Node) *compiler.NodeArray {
	switch node.Kind {
	case compiler.MethodDeclaration:
		return node.AsMethodDeclaration().Modifiers
	case compiler.ConstructorDeclaration:
		return node.AsConstructorDeclaration().Modifiers
	case compiler.FieldDeclaration:
		return node.AsFieldDeclaration().Modifiers
	case compiler.LocalVariableDeclarationStatement:
		return node.AsLocalVariableDeclarationStatement().Modifiers
	case compiler.ClassDeclaration:
		return node.AsClassDeclaration().Modifiers
	case compiler.InterfaceDeclaration:
		return node.AsInterfaceDeclaration().Modifiers
	case compiler.EnumDeclaration:
		return node.AsEnumDeclaration().Modifiers
	case compiler.RecordDeclaration:
		return node.AsRecordDeclaration().Modifiers
	case compiler.AnnotationTypeDeclaration:
		return node.AsAnnotationTypeDeclaration().Modifiers
	default:
		return nil
	}
}

func symbolDeclaration(symbol *compiler.Symbol) *compiler.Node {
	if symbol.ValueDeclaration != nil {
		return symbol.ValueDeclaration
	}
	if len(symbol.Declarations) > 0 {
		return symbol.Declarations[0]
	}
	return nil
}

func appendNodes(out []*compiler.Node, arr *compiler.NodeArray) []*compiler.Node {
	if arr != nil {
		out = append(out, arr.Nodes...)
	}
	return out
}

func nodesOf(arr *compiler.NodeArray) []*compiler.Node {
	if arr == nil {
		return nil
	}
	return arr.Nodes
}
