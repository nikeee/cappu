package compiler

import "regexp"

// Source-file driver for the bytecode backend. Walks a parsed source file and
// emits one .class per top-level class via the class-file writer in bytecode.go.
// Port of src/compiler/emitter.ts.

var stringRefRe = regexp.MustCompile(`^(java\.lang\.)?String$`)

// DeclaresMainMethod reports whether a member list declares an entry point:
// public static void main(String[]).
func DeclaresMainMethod(members []*Node) bool {
	for _, member := range members {
		if member.Kind != MethodDeclaration {
			continue
		}
		method := member.AsMethodDeclaration()
		if method.Name.AsIdentifier().Text != "main" || len(arrayNodes(method.Parameters)) != 1 {
			continue
		}
		mods := method.Modifiers
		if !hasModifierKind(mods, PublicKeyword) || !hasModifierKind(mods, StaticKeyword) {
			continue
		}
		returnsVoid := method.ReturnType.Kind == PrimitiveType && method.ReturnType.AsPrimitiveType().Keyword == VoidKeyword
		if !returnsVoid {
			continue
		}
		parameter := arrayNodes(method.Parameters)[0].AsParameter()
		isStringRef := func(t *Node) bool {
			return t.Kind == TypeReference && stringRefRe.MatchString(entityNameToString(t.AsTypeReference().TypeName))
		}
		if parameter.IsVarArgs || parameter.ArrayRankAfterName == 1 {
			if isStringRef(parameter.Type) {
				return true
			}
			continue
		}
		if parameter.Type.Kind == ArrayType && isStringRef(parameter.Type.AsArrayType().ElementType) {
			return true
		}
	}
	return false
}

// EmitSourceFile emits a .class file for every class declaration in a source file.
func EmitSourceFile(sourceFile *Node, program *Program, checker *Checker, debugInfo bool) []EmittedClass {
	var result []EmittedClass
	nest := computeNestMembers(sourceFile, program)
	inner := computeInnerClassInfo(sourceFile, program)
	previousDebugInfo := SetEmitDebugInfo(debugInfo)
	// deferred so a panicking emit (unsupportedEmit) can't leak the flag into later files
	defer SetEmitDebugInfo(previousDebugInfo)
	var visit func(node *Node)
	visit = func(node *Node) {
		switch node.Kind {
		case ClassDeclaration:
			d := node.AsClassDeclaration()
			if node.Symbol != nil || d.Name != nil {
				ec := emitClass(node, program, checker, nest, inner)
				ec.HasMainMethod = DeclaresMainMethod(arrayNodes(d.Members))
				result = append(result, ec)
			}
		case InterfaceDeclaration:
			result = append(result, emitInterface(node, program, checker, nest, inner))
		case EnumDeclaration:
			result = append(result, emitEnum(node, program, checker, nest, inner)...)
		case RecordDeclaration:
			result = append(result, emitRecord(node, program, checker, nest, inner))
		case AnnotationTypeDeclaration:
			result = append(result, emitAnnotationType(node, program, checker, nest, inner))
		case ObjectCreationExpression:
			if anon, ok := emitAnonymousClassIfPossible(node, program, checker, nest, inner); ok {
				result = append(result, anon)
			}
		}
		node.ForEachChild(func(child *Node) bool {
			visit(child)
			return false
		})
	}
	visit(sourceFile)
	return result
}
