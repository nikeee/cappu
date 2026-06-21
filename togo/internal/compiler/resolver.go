package compiler

// Name resolution: map a name-use Identifier to the Symbol it refers to.
//
// Lexical scope chain (JLS 6.5, simplified): block/method locals + type
// parameters -> type members (incl. inherited from super types) -> enclosing
// types -> single-type imports -> same package -> on-demand imports + java.lang
// -> fully-qualified names via the program's global index. Member access of an
// expression (a.b) needs the checker (P5) and is deferred.
// Port of src/compiler/resolver.ts.

import "strings"

type Meaning int

const (
	MeaningAny Meaning = iota
	MeaningType
	MeaningValue
)

func resolverIsTypeDeclaration(node *Node) bool {
	switch node.Kind {
	case ClassDeclaration, InterfaceDeclaration, EnumDeclaration, AnnotationTypeDeclaration, RecordDeclaration:
		return true
	default:
		return false
	}
}

func matchesMeaning(symbol *Symbol, meaning Meaning) bool {
	switch meaning {
	case MeaningType:
		return symbol.Flags&SymbolFlagsType != 0
	case MeaningValue:
		return symbol.Flags&(SymbolFlagsField|SymbolFlagsParameter|SymbolFlagsLocalVariable|SymbolFlagsEnumConstant|SymbolFlagsMethod) != 0
	default:
		return true
	}
}

// GetSourceFileOfNode walks up to the enclosing SourceFile node.
func GetSourceFileOfNode(node *Node) *Node {
	current := node
	for current.Kind != SourceFile {
		current = current.Parent
	}
	return current
}

func packageNameOf(sourceFile *Node) PackageName {
	data := sourceFile.AsSourceFile()
	if data.PackageDeclaration != nil {
		return PackageName(entityNameToString(data.PackageDeclaration.AsPackageDeclaration().Name))
	}
	return ""
}

func lastSegment(qualified string) string {
	dot := strings.LastIndex(qualified, ".")
	if dot < 0 {
		return qualified
	}
	return qualified[dot+1:]
}

// --- inheritance-aware member lookup -----------------------------------------

// resolvingSupertypes guards against cycles in the super-type graph.
var resolvingSupertypes = map[*Symbol]bool{}

func superTypeNodes(declaration *Node) []*Node {
	switch declaration.Kind {
	case ClassDeclaration:
		c := declaration.AsClassDeclaration()
		var out []*Node
		if c.ExtendsType != nil {
			out = append(out, c.ExtendsType)
		}
		if c.ImplementsTypes != nil {
			out = append(out, c.ImplementsTypes.Nodes...)
		}
		return out
	case InterfaceDeclaration:
		if e := declaration.AsInterfaceDeclaration().ExtendsTypes; e != nil {
			return e.Nodes
		}
	case EnumDeclaration:
		if i := declaration.AsEnumDeclaration().ImplementsTypes; i != nil {
			return i.Nodes
		}
	case RecordDeclaration:
		if i := declaration.AsRecordDeclaration().ImplementsTypes; i != nil {
			return i.Nodes
		}
	}
	return nil
}

// GetDirectSuperTypeSymbols returns the direct super-type symbols (extends/implements).
func GetDirectSuperTypeSymbols(typeSymbol *Symbol, program *Program) []*Symbol {
	return superTypeSymbols(typeSymbol, program)
}

func superTypeSymbols(typeSymbol *Symbol, program *Program) []*Symbol {
	if resolvingSupertypes[typeSymbol] {
		return nil
	}
	declaration := declarationOfSymbol(typeSymbol)
	if declaration == nil {
		return nil
	}
	resolvingSupertypes[typeSymbol] = true
	defer delete(resolvingSupertypes, typeSymbol)

	var result []*Symbol
	// An enum implicitly extends java.lang.Enum (JLS 8.9).
	if declaration.Kind == EnumDeclaration {
		if enumSymbol := program.GetGlobalIndex().GetType("java.lang.Enum"); enumSymbol != nil {
			result = append(result, enumSymbol)
		}
	}
	for _, typeNode := range superTypeNodes(declaration) {
		if typeNode.Kind == TypeReference {
			if symbol := ResolveTypeEntityName(typeNode.AsTypeReference().TypeName, declaration, program); symbol != nil {
				result = append(result, symbol)
			}
		}
	}
	// Every class without an extends clause (and every record) implicitly extends
	// java.lang.Object (JLS 8.1.4); an interface's members include Object's public
	// ones (JLS 9.2).
	hasClassSuper := declaration.Kind == ClassDeclaration && declaration.AsClassDeclaration().ExtendsType != nil
	if !hasClassSuper {
		if objectSymbol := program.GetGlobalIndex().GetType("java.lang.Object"); objectSymbol != nil && objectSymbol != typeSymbol {
			result = append(result, objectSymbol)
		}
	}
	return result
}

func declarationOfSymbol(symbol *Symbol) *Node {
	if symbol.ValueDeclaration != nil {
		return symbol.ValueDeclaration
	}
	if len(symbol.Declarations) > 0 {
		return symbol.Declarations[0]
	}
	return nil
}

// LookupMember looks up a member by name in a type and its super types.
func LookupMember(typeSymbol *Symbol, name string, meaning Meaning, program *Program) *Symbol {
	return lookupMember(typeSymbol, name, meaning, program, map[*Symbol]bool{})
}

func lookupMember(typeSymbol *Symbol, name string, meaning Meaning, program *Program, seen map[*Symbol]bool) *Symbol {
	if seen[typeSymbol] {
		return nil
	}
	seen[typeSymbol] = true
	if own := typeSymbol.Members[name]; own != nil && matchesMeaning(own, meaning) {
		return own
	}
	for _, superSymbol := range superTypeSymbols(typeSymbol, program) {
		if inherited := lookupMember(superSymbol, name, meaning, program, seen); inherited != nil {
			return inherited
		}
	}
	return nil
}

// --- scope chain -------------------------------------------------------------

func lookupInScopes(start *Node, name string, meaning Meaning, program *Program) *Symbol {
	var node, prev *Node
	node = start
	for node != nil {
		switch {
		case resolverIsTypeDeclaration(node) && node.Symbol != nil:
			if member := LookupMember(node.Symbol, name, meaning, program); member != nil {
				return member
			}
		case node.Kind == ObjectCreationExpression && classBodyIncludes(node, prev):
			// Inside an anonymous class body: members are inherited from the type it
			// extends/implements, so look them up on the supertype.
			oce := node.AsObjectCreationExpression()
			var target *Symbol
			if oce.Type.Kind == TypeReference {
				target = ResolveTypeEntityName(oce.Type.AsTypeReference().TypeName, node, program)
			}
			if target != nil {
				if member := LookupMember(target, name, meaning, program); member != nil {
					return member
				}
			}
		default:
			if local := node.Locals[name]; local != nil && matchesMeaning(local, meaning) {
				return local
			}
		}
		prev = node
		node = node.Parent
	}
	return nil
}

func classBodyIncludes(node, prev *Node) bool {
	if prev == nil {
		return false
	}
	body := node.AsObjectCreationExpression().ClassBody
	if body == nil {
		return false
	}
	for _, m := range body.Nodes {
		if m == prev {
			return true
		}
	}
	return false
}

// --- cross-file type resolution ----------------------------------------------

func resolveTypeNameCrossFile(name string, sourceFile *Node, index *GlobalIndex) *Symbol {
	data := sourceFile.AsSourceFile()
	// single-type imports
	for _, imp := range data.Imports.Nodes {
		d := imp.AsImportDeclaration()
		if !d.IsStatic && !d.IsOnDemand {
			fqn := entityNameToString(d.Name)
			if lastSegment(fqn) == name {
				if t := index.GetType(Fqn(fqn)); t != nil {
					return t
				}
			}
		}
	}
	// same package
	if pkg := index.GetPackageTypes(packageNameOf(sourceFile)); pkg != nil {
		if samePackage := pkg[name]; samePackage != nil {
			return samePackage
		}
	}
	// on-demand imports. The package-types map only covers project/classpath
	// types; GetType also reaches the lazy JDK provider for a star-imported JDK
	// package (e.g. `import java.util.*` -> List).
	for _, imp := range data.Imports.Nodes {
		d := imp.AsImportDeclaration()
		if !d.IsStatic && d.IsOnDemand {
			pkgName := entityNameToString(d.Name)
			if pkg := index.GetPackageTypes(PackageName(pkgName)); pkg != nil {
				if t := pkg[name]; t != nil {
					return t
				}
			}
			if t := index.GetType(Fqn(pkgName + "." + name)); t != nil {
				return t
			}
		}
	}
	// implicit java.lang.* (GetType reaches the lazy JDK provider too)
	if pkg := index.GetPackageTypes("java.lang"); pkg != nil {
		if t := pkg[name]; t != nil {
			return t
		}
	}
	return index.GetType(Fqn("java.lang." + name))
}

// resolveStaticImport resolves a value/method imported via `import static`.
func resolveStaticImport(name string, sourceFile *Node, program *Program) *Symbol {
	index := program.GetGlobalIndex()
	for _, imp := range sourceFile.AsSourceFile().Imports.Nodes {
		d := imp.AsImportDeclaration()
		if !d.IsStatic {
			continue
		}
		fqn := entityNameToString(d.Name)
		if d.IsOnDemand {
			t := index.GetType(Fqn(fqn))
			if t != nil {
				if member := LookupMember(t, name, MeaningAny, program); member != nil {
					return member
				}
			}
		} else if lastSegment(fqn) == name {
			dot := strings.LastIndex(fqn, ".")
			if dot >= 0 {
				t := index.GetType(Fqn(fqn[:dot]))
				if t != nil {
					if member := LookupMember(t, name, MeaningAny, program); member != nil {
						return member
					}
				}
			}
		}
	}
	return nil
}

func resolveTypeName(name string, fromNode *Node, program *Program) *Symbol {
	if lexical := lookupInScopes(fromNode, name, MeaningType, program); lexical != nil {
		return lexical
	}
	return resolveTypeNameCrossFile(name, GetSourceFileOfNode(fromNode), program.GetGlobalIndex())
}

// typeNameLinks memoizes type-entity-name resolution per node (the nodeLinks
// pattern). A reparse creates fresh nodes, so stale entries die with their keys.
// A recorded resolvedNothing distinguishes "resolved to nothing" from "not computed".
var typeNameLinks = map[*Node]resolvedLink{}

type resolvedLink struct {
	symbol   *Symbol
	computed bool
}

// ResolveTypeEntityName resolves an entity name used as a type.
func ResolveTypeEntityName(name *Node, fromNode *Node, program *Program) *Symbol {
	if cached, ok := typeNameLinks[name]; ok && cached.computed {
		return cached.symbol
	}
	result := resolveTypeEntityNameWorker(name, fromNode, program)
	typeNameLinks[name] = resolvedLink{symbol: result, computed: true}
	return result
}

func resolveTypeEntityNameWorker(name *Node, fromNode *Node, program *Program) *Symbol {
	if name.Kind == Identifier {
		return resolveTypeName(name.AsIdentifier().Text, fromNode, program)
	}
	fqn := entityNameToString(name)
	if byFqn := program.GetGlobalIndex().GetType(Fqn(fqn)); byFqn != nil {
		return byFqn
	}
	// nested type: resolve the left as a type, then look up the right member type
	q := name.AsQualifiedName()
	if leftType := ResolveTypeEntityName(q.Left, fromNode, program); leftType != nil {
		return LookupMember(leftType, q.Right.AsIdentifier().Text, MeaningType, program)
	}
	return nil
}

// --- identifier classification -----------------------------------------------

func declarationOf(identifier *Node) *Node {
	parent := identifier.Parent
	if parent != nil && parent.Symbol != nil && nodeName(parent) == identifier {
		return parent
	}
	return nil
}

func isQualifiedTypeNameTail(identifier *Node) bool {
	parent := identifier.Parent
	return parent != nil && parent.Kind == QualifiedName &&
		parent.AsQualifiedName().Right == identifier &&
		meaningOf(identifier) == MeaningType
}

func isExpressionMemberAccess(identifier *Node) bool {
	parent := identifier.Parent
	if parent == nil {
		return false
	}
	if parent.Kind == PropertyAccessExpression {
		return parent.AsPropertyAccessExpression().Name == identifier
	}
	if parent.Kind == MethodReferenceExpression {
		return parent.AsMethodReferenceExpression().Name == identifier
	}
	return false
}

func meaningOf(identifier *Node) Meaning {
	node := identifier
	for node != nil {
		if node.Kind == TypeReference {
			return MeaningType
		}
		if node.Kind != QualifiedName && node.Kind != Identifier {
			break
		}
		node = node.Parent
	}
	return MeaningAny
}

// identifierLinks memoizes identifier resolution per node.
var identifierLinks = map[*Node]resolvedLink{}

// ResolveIdentifier resolves a name-use identifier to its declaration symbol, or nil.
func ResolveIdentifier(identifier *Node, program *Program) *Symbol {
	if cached, ok := identifierLinks[identifier]; ok && cached.computed {
		return cached.symbol
	}
	result := resolveIdentifierWorker(identifier, program)
	identifierLinks[identifier] = resolvedLink{symbol: result, computed: true}
	return result
}

func resolveIdentifierWorker(identifier *Node, program *Program) *Symbol {
	if declaration := declarationOf(identifier); declaration != nil {
		return declaration.Symbol
	}
	if isQualifiedTypeNameTail(identifier) {
		return ResolveTypeEntityName(identifier.Parent, identifier, program)
	}
	if isExpressionMemberAccess(identifier) {
		return nil // needs the checker (P5)
	}
	meaning := meaningOf(identifier)
	if lexical := lookupInScopes(identifier, identifier.AsIdentifier().Text, meaning, program); lexical != nil {
		return lexical
	}
	if meaning != MeaningValue {
		if t := resolveTypeNameCrossFile(identifier.AsIdentifier().Text, GetSourceFileOfNode(identifier), program.GetGlobalIndex()); t != nil {
			return t
		}
	}
	// A statically-imported field or method used by its simple name.
	return resolveStaticImport(identifier.AsIdentifier().Text, GetSourceFileOfNode(identifier), program)
}

// GetDeclarationNameNode returns the name node of a symbol's declaration.
func GetDeclarationNameNode(symbol *Symbol) *Node {
	declaration := declarationOfSymbol(symbol)
	if declaration == nil {
		return nil
	}
	if name := nodeName(declaration); name != nil {
		return name
	}
	return declaration
}

// --- find references ---------------------------------------------------------

func forEachDescendant(node *Node, cb func(*Node)) {
	cb(node)
	node.ForEachChild(func(child *Node) bool {
		forEachDescendant(child, cb)
		return false
	})
}

// fileLocalFlags marks symbols that cannot be referenced outside their file.
const fileLocalFlags = SymbolFlagsLocalVariable | SymbolFlagsParameter | SymbolFlagsTypeParameter

func candidateUris(symbol *Symbol, program *Program) []URI {
	if symbol.Flags&fileLocalFlags != 0 {
		if declaration := declarationOfSymbol(symbol); declaration != nil {
			return []URI{URI(GetSourceFileOfNode(declaration).AsSourceFile().FileName)}
		}
	}
	return program.GetAllUris()
}

// FindReferences returns all identifier nodes (uses and declaration names) that
// refer to a symbol. resolve maps an identifier to its symbol; pass nil to use
// the default lexical resolver.
func FindReferences(symbol *Symbol, program *Program, resolve func(*Node) *Symbol) []*Node {
	if resolve == nil {
		resolve = func(id *Node) *Symbol { return ResolveIdentifier(id, program) }
	}
	var result []*Node
	for _, uri := range candidateUris(symbol, program) {
		sourceFile := program.GetSourceFile(uri)
		if sourceFile == nil {
			continue
		}
		forEachDescendant(sourceFile, func(node *Node) {
			if node.Kind != Identifier {
				return
			}
			if resolve(node) == symbol {
				result = append(result, node)
			}
		})
	}
	return result
}
