package services

// Code lenses: a reference count over every type and method declaration in a
// file, and an implementation count over interfaces, abstract classes and their
// abstract methods. Reference counts come from one pass over the workspace;
// implementation counts come from the generation-memoized subtype index.
// Port of src/services/codeLens.ts.

import "github.com/nikeee/cappu/internal/compiler"

func isLensDeclaration(kind compiler.SyntaxKind) bool {
	switch kind {
	case compiler.ClassDeclaration, compiler.InterfaceDeclaration, compiler.EnumDeclaration,
		compiler.RecordDeclaration, compiler.AnnotationTypeDeclaration, compiler.MethodDeclaration:
		return true
	default:
		return false
	}
}

// CodeLensEntry is one lens anchored to a declaration's name.
type CodeLensEntry struct {
	Name  *compiler.Node // the declaration's name node
	Kind  string         // "references" | "implementations"
	Sites []*compiler.Node
}

func lensIsDeclarationName(node *compiler.Node, symbol *compiler.Symbol) bool {
	parent := node.Parent
	return parent != nil && parent.Symbol == symbol && declName(parent) == node
}

func hasAbstractModifier(node *compiler.Node) bool {
	mods := declModifiers(node)
	if mods == nil {
		return false
	}
	for _, m := range mods.Nodes {
		if m.Kind == compiler.AbstractKeyword {
			return true
		}
	}
	return false
}

// abstractMethodsOf returns the abstract methods of an interface or abstract class.
func abstractMethodsOf(declaration *compiler.Node) []*compiler.Node {
	isInterface := declaration.Kind == compiler.InterfaceDeclaration
	var out []*compiler.Node
	for _, m := range declMembers(declaration) {
		if m.Kind != compiler.MethodDeclaration {
			continue
		}
		if isInterface {
			if m.AsMethodDeclaration().Body == nil { // default/static methods have a body
				out = append(out, m)
			}
		} else if hasAbstractModifier(m) {
			out = append(out, m)
		}
	}
	return out
}

// GetCodeLenses returns reference and implementation lenses for a source file.
func GetCodeLenses(program *compiler.Program, checker *compiler.Checker, sourceFile *compiler.Node) []CodeLensEntry {
	refTargets := map[*compiler.Symbol]int{} // symbol -> index into entries
	var entries []CodeLensEntry
	subtypes := GetSubtypeIndex(program)

	var collect compiler.Visitor
	collect = func(node *compiler.Node) bool {
		name := declName(node)
		if name != nil && node.Symbol != nil {
			if isLensDeclaration(node.Kind) {
				if _, ok := refTargets[node.Symbol]; !ok {
					refTargets[node.Symbol] = len(entries)
					entries = append(entries, CodeLensEntry{Name: name, Kind: "references"})
				}
			}
			isImplTarget := node.Kind == compiler.InterfaceDeclaration ||
				(node.Kind == compiler.ClassDeclaration && hasAbstractModifier(node))
			if isImplTarget {
				var sites []*compiler.Node
				for _, sub := range subtypes.AllSubtypesOf(node.Symbol) {
					if n := DeclarationName(sub); n != nil {
						sites = append(sites, n)
					}
				}
				entries = append(entries, CodeLensEntry{Name: name, Kind: "implementations", Sites: sites})
				for _, method := range abstractMethodsOf(node) {
					var impls []*compiler.Node
					for _, m := range FindMethodImplementations(method, program) {
						impls = append(impls, m.AsMethodDeclaration().Name)
					}
					entries = append(entries, CodeLensEntry{Name: method.AsMethodDeclaration().Name, Kind: "implementations", Sites: impls})
				}
			}
		}
		node.ForEachChild(collect)
		return false
	}
	collect(sourceFile)
	if len(refTargets) == 0 {
		return entries
	}

	for _, uri := range program.GetAllUris() {
		if compiler.IsSyntheticURI(uri) {
			continue
		}
		file := program.GetSourceFile(uri)
		if file == nil {
			continue
		}
		var visit compiler.Visitor
		visit = func(node *compiler.Node) bool {
			if node.Kind == compiler.Identifier {
				symbol := checker.ResolveName(node)
				if symbol != nil {
					if idx, ok := refTargets[symbol]; ok && !lensIsDeclarationName(node, symbol) {
						entries[idx].Sites = append(entries[idx].Sites, node)
					}
				}
			}
			node.ForEachChild(visit)
			return false
		}
		visit(file)
	}
	return entries
}
