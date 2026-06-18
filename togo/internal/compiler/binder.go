package compiler

// Binder. Mirrors the TypeScript compiler binder: a single walk of the tree
// (via ForEachChild) that sets parent pointers, tracks the current container and
// block scope, and builds symbol tables. Declarations are attached to nodes
// (node.Symbol) and recorded in the enclosing container's table (a type's
// symbol.Members, or a container node's Locals). Duplicate declarations are
// reported as bind diagnostics.
//
// This is the foundation an LSP needs for goto-definition, find-references and
// completion. Scoping is deliberately simple for the SE7 baseline: parameters
// and the method body share separate tables (a param redeclared as a top-level
// body local is not flagged), and try-with-resources variables are not yet
// scoped. These are refined in later milestones. Port of src/compiler/binder.ts.

type binder struct {
	file            *Node
	parent          *Node
	container       *Node
	bindDiagnostics []Diagnostic
	factory         NodeFactory
}

// BindSourceFile binds a parsed source file in place, populating symbol tables,
// parent pointers and the file's BindDiagnostics.
func BindSourceFile(f *Node) {
	b := &binder{file: f, container: f, parent: f}
	data := f.AsSourceFile()
	f.Locals = SymbolTable{}
	f.Symbol = createSymbol(SymbolFlagsModule, data.FileName)

	b.bindChildren(f)

	data.BindDiagnostics = b.bindDiagnostics
}

func createSymbol(flags SymbolFlags, name string) *Symbol {
	return &Symbol{Flags: flags, EscapedName: name}
}

func isTypeDeclaration(node *Node) bool {
	switch node.Kind {
	case ClassDeclaration, InterfaceDeclaration, EnumDeclaration, AnnotationTypeDeclaration, RecordDeclaration:
		return true
	default:
		return false
	}
}

// isContainer reports whether the node owns a symbol table (type members, or locals).
func isContainer(node *Node) bool {
	switch node.Kind {
	case SourceFile, MethodDeclaration, ConstructorDeclaration, Block,
		ForStatement, ForEachStatement, CatchClause, LambdaExpression:
		return true
	default:
		return isTypeDeclaration(node)
	}
}

// containerTable is the table that declarations in this container record into.
func (b *binder) containerTable(node *Node) SymbolTable {
	if node.Kind == SourceFile {
		return b.file.Locals
	}
	if isTypeDeclaration(node) && node.Symbol != nil {
		if node.Symbol.Members == nil {
			node.Symbol.Members = SymbolTable{}
		}
		return node.Symbol.Members
	}
	// A type declaration whose symbol was never created (missing name after a
	// parse error) falls back to a plain locals table, so binding stays robust.
	if node.Locals == nil {
		node.Locals = SymbolTable{}
	}
	return node.Locals
}

func (b *binder) declareSymbol(table SymbolTable, name string, node, locationNode *Node, flags, excludes SymbolFlags) *Symbol {
	symbol := table[name]
	if symbol == nil {
		symbol = createSymbol(flags, name)
		table[name] = symbol
	} else {
		if symbol.Flags&excludes != 0 {
			b.bindDiagnostics = append(b.bindDiagnostics, CreateDiagnostic(
				locationNode.Pos, locationNode.End-locationNode.Pos, Diagnostics.DuplicateDeclaration0, name))
		}
		symbol.Flags |= flags
	}
	symbol.Declarations = append(symbol.Declarations, node)
	if symbol.ValueDeclaration == nil {
		symbol.ValueDeclaration = node
	}
	if symbol.Parent == nil {
		symbol.Parent = b.container.Symbol
	}
	node.Symbol = symbol
	return symbol
}

func (b *binder) declareIntoContainer(name, node *Node, flags, excludes SymbolFlags) {
	// Missing name, or the unnamed variable '_' (SE21): nothing to declare.
	if name == nil || name.Kind != Identifier {
		return
	}
	text := name.AsIdentifier().Text
	if text == "" || text == "_" {
		return
	}
	b.declareSymbol(b.containerTable(b.container), text, node, name, flags, excludes)
}

// bindDeclaration declares the node (if it is a declaration) into the current container.
func (b *binder) bindDeclaration(node *Node) {
	switch node.Kind {
	case ClassDeclaration:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsClass, SymbolFlagsType)
	case InterfaceDeclaration:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsInterface, SymbolFlagsType)
	case EnumDeclaration:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsEnum, SymbolFlagsType)
	case AnnotationTypeDeclaration:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsAnnotation, SymbolFlagsType)
	case RecordDeclaration:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsRecord, SymbolFlagsType)
	case RecordComponent:
		c := node.AsRecordComponent()
		// The component's implicit private final field.
		b.declareIntoContainer(c.Name, node, SymbolFlagsField, SymbolFlagsField)
		// Its implicit accessor method `name()`, so `record.name()` resolves. A
		// synthetic zero-arg MethodDeclaration returning the component's type is
		// merged onto the same name symbol (which thus has Field | Method).
		if c.Name != nil {
			accessor := b.factory.NewMethodDeclaration(nil, nil, c.Type, c.Name, &NodeArray{}, nil, nil, nil)
			accessor.Pos = node.Pos
			accessor.End = node.End
			accessor.Parent = node.Parent
			b.declareIntoContainer(c.Name, accessor, SymbolFlagsMethod, SymbolFlagsNone)
		}
	case CompactConstructorDeclaration:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsConstructor, SymbolFlagsNone)
	case MethodDeclaration:
		// Overloading is allowed: methods do not exclude each other.
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsMethod, SymbolFlagsNone)
	case ConstructorDeclaration:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsConstructor, SymbolFlagsNone)
	case EnumConstantDeclaration:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsEnumConstant, SymbolFlagsEnumConstant)
	case Parameter:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsParameter, SymbolFlagsParameter)
	case Identifier:
		b.bindIdentifierDeclaration(node)
	case TypePattern:
		// SE21 pattern binding variable (case String s, instanceof patterns).
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsLocalVariable, SymbolFlagsNone)
	case TypeParameter:
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsTypeParameter, SymbolFlagsTypeParameter)
	case VariableDeclarator:
		flags := SymbolFlagsLocalVariable
		if node.Parent != nil && node.Parent.Kind == FieldDeclaration {
			flags = SymbolFlagsField
		}
		b.declareIntoContainer(nodeName(node), node, flags, flags)
	case Resource:
		// A try-with-resources resource `try (R r = ...)` declares `r` (JLS
		// 14.20.3). The declaration form carries a name; the variable-access form
		// (try (existingVar)) does not, so nodeName returns nil and nothing is
		// declared. The type lives on the Resource node itself.
		b.declareIntoContainer(nodeName(node), node, SymbolFlagsLocalVariable, SymbolFlagsLocalVariable)
	case CatchClause:
		// The catch variable is the clause's name, scoped to the catch block.
		name := nodeName(node)
		if name != nil && name.Kind == Identifier && name.AsIdentifier().Text != "" {
			node.Locals = SymbolTable{}
			b.declareSymbol(node.Locals, name.AsIdentifier().Text, name, name, SymbolFlagsParameter, SymbolFlagsParameter)
		}
	}
}

// bindIdentifierDeclaration handles the two bare-Identifier declaration forms: a
// concise lambda parameter and a type-pattern binding on an InstanceofExpression.
func (b *binder) bindIdentifierDeclaration(node *Node) {
	parent := node.Parent
	if parent == nil {
		return
	}
	if parent.Kind == LambdaExpression {
		params := parent.AsLambdaExpression().Parameters
		if params != nil {
			for _, p := range params.Nodes {
				if p == node {
					b.declareIntoContainer(node, node, SymbolFlagsParameter, SymbolFlagsParameter)
					return
				}
			}
		}
	} else if parent.Kind == InstanceofExpression && parent.AsInstanceofExpression().Name == node {
		// The binding variable of a type pattern `x instanceof T t` (JLS 14.30.1).
		b.declareIntoContainer(node, node, SymbolFlagsLocalVariable, SymbolFlagsLocalVariable)
	}
}

// nodeName pulls the name Identifier off a declaration node, if present.
func nodeName(node *Node) *Node {
	switch node.Kind {
	case ClassDeclaration:
		return node.AsClassDeclaration().Name
	case InterfaceDeclaration:
		return node.AsInterfaceDeclaration().Name
	case EnumDeclaration:
		return node.AsEnumDeclaration().Name
	case AnnotationTypeDeclaration:
		return node.AsAnnotationTypeDeclaration().Name
	case RecordDeclaration:
		return node.AsRecordDeclaration().Name
	case RecordComponent:
		return node.AsRecordComponent().Name
	case CompactConstructorDeclaration:
		return node.AsCompactConstructorDeclaration().Name
	case MethodDeclaration:
		return node.AsMethodDeclaration().Name
	case ConstructorDeclaration:
		return node.AsConstructorDeclaration().Name
	case EnumConstantDeclaration:
		return node.AsEnumConstantDeclaration().Name
	case Parameter:
		return node.AsParameter().Name
	case TypePattern:
		return node.AsTypePattern().Name
	case TypeParameter:
		return node.AsTypeParameter().Name
	case VariableDeclarator:
		return node.AsVariableDeclarator().Name
	case Resource:
		return node.AsResource().Name
	case CatchClause:
		return node.AsCatchClause().Name
	default:
		return nil
	}
}

func (b *binder) bind(node *Node) {
	if node == nil {
		return
	}
	node.Parent = b.parent
	b.bindDeclaration(node)

	savedParent := b.parent
	savedContainer := b.container

	b.parent = node
	if isContainer(node) {
		b.container = node
		b.containerTable(node) // ensure the table exists
	}

	b.bindChildren(node)

	b.parent = savedParent
	b.container = savedContainer
}

func (b *binder) bindChildren(node *Node) {
	node.ForEachChild(func(n *Node) bool {
		b.bind(n)
		return false
	})
}
