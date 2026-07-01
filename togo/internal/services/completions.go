package services

// Completion provider. Designed to stay useful on incomplete / broken code:
// member completion (`expr.|`) lists the members of the receiver's type when it
// is known and falls back to nothing (never a guess) when it is not; identifier
// completion always offers the names visible in the current scope, which works
// regardless of parse errors because the binder still produced scopes.
// Port of src/services/completions.ts.

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
)

// CompletionItemKind mirrors a subset of LSP CompletionItemKind (numeric values
// match the protocol).
type CompletionItemKind int

const (
	CompletionItemKindMethod        CompletionItemKind = 2
	CompletionItemKindField         CompletionItemKind = 5
	CompletionItemKindVariable      CompletionItemKind = 6
	CompletionItemKindClass         CompletionItemKind = 7
	CompletionItemKindInterface     CompletionItemKind = 8
	CompletionItemKindEnum          CompletionItemKind = 10
	CompletionItemKindFile          CompletionItemKind = 17
	CompletionItemKindEnumMember    CompletionItemKind = 20
	CompletionItemKindTypeParameter CompletionItemKind = 25
)

// CompletionItem is one completion suggestion.
type CompletionItem struct {
	Label string
	Kind  CompletionItemKind
	// Deprecated is true when the symbol carries a @Deprecated annotation
	// (client renders it struck out).
	Deprecated bool
}

func completionKind(flags compiler.SymbolFlags) CompletionItemKind {
	switch {
	case flags&(compiler.SymbolFlagsClass|compiler.SymbolFlagsRecord|compiler.SymbolFlagsAnnotation) != 0:
		return CompletionItemKindClass
	case flags&compiler.SymbolFlagsInterface != 0:
		return CompletionItemKindInterface
	case flags&compiler.SymbolFlagsEnum != 0:
		return CompletionItemKindEnum
	case flags&(compiler.SymbolFlagsMethod|compiler.SymbolFlagsConstructor) != 0:
		return CompletionItemKindMethod
	case flags&compiler.SymbolFlagsField != 0:
		return CompletionItemKindField
	case flags&compiler.SymbolFlagsEnumConstant != 0:
		return CompletionItemKindEnumMember
	case flags&compiler.SymbolFlagsTypeParameter != 0:
		return CompletionItemKindTypeParameter
	default:
		return CompletionItemKindVariable
	}
}

// orderedSymbols accumulates name->symbol with first-wins semantics and stable
// insertion order (matching the TS Map).
type orderedSymbols struct {
	names []string
	byKey map[string]*compiler.Symbol
}

func newOrderedSymbols() *orderedSymbols {
	return &orderedSymbols{byKey: map[string]*compiler.Symbol{}}
}

func (o *orderedSymbols) set(name string, symbol *compiler.Symbol) {
	if _, ok := o.byKey[name]; ok {
		return
	}
	o.byKey[name] = symbol
	o.names = append(o.names, name)
}

func (o *orderedSymbols) items() []CompletionItem {
	out := make([]CompletionItem, 0, len(o.names))
	for _, name := range o.names {
		symbol := o.byKey[name]
		_, deprecated := compiler.SymbolDeprecation(symbol)
		out = append(out, CompletionItem{Label: name, Kind: completionKind(symbol.Flags), Deprecated: deprecated})
	}
	return out
}

func isExpressionKind(kind compiler.SyntaxKind) bool {
	return kind == compiler.Identifier || (kind >= compiler.FirstExpression && kind <= compiler.LastExpression)
}

func completionsIsTypeDeclaration(kind compiler.SyntaxKind) bool {
	switch kind {
	case compiler.ClassDeclaration, compiler.InterfaceDeclaration, compiler.EnumDeclaration,
		compiler.AnnotationTypeDeclaration, compiler.RecordDeclaration:
		return true
	default:
		return false
	}
}

func gatherTypeMembers(typeSymbol *compiler.Symbol, program *compiler.Program, into *orderedSymbols, seen map[*compiler.Symbol]bool, includeTypeParameters bool) {
	if seen[typeSymbol] {
		return
	}
	seen[typeSymbol] = true
	for _, name := range typeSymbol.Members.OrderedKeys() {
		symbol := typeSymbol.Members[name]
		if !includeTypeParameters && symbol.Flags&compiler.SymbolFlagsTypeParameter != 0 {
			continue
		}
		if symbol.Flags&compiler.SymbolFlagsConstructor != 0 {
			continue
		}
		into.set(name, symbol)
	}
	for _, superSymbol := range compiler.GetDirectSuperTypeSymbols(typeSymbol, program) {
		gatherTypeMembers(superSymbol, program, into, seen, includeTypeParameters)
	}
}

func addAll(table compiler.SymbolTable, into *orderedSymbols) {
	for _, name := range table.OrderedKeys() {
		into.set(name, table[name])
	}
}

func collectScopeSymbols(node *compiler.Node, program *compiler.Program) *orderedSymbols {
	result := newOrderedSymbols()
	current := node
	for current != nil {
		if current.Symbol != nil && current.Symbol.Members != nil && completionsIsTypeDeclaration(current.Kind) {
			gatherTypeMembers(current.Symbol, program, result, map[*compiler.Symbol]bool{}, true)
		} else {
			addAll(current.Locals, result)
		}
		current = current.Parent
	}
	sourceFile := compiler.GetSourceFileOfNode(node).AsSourceFile()
	index := program.GetGlobalIndex()
	pkg := compiler.PackageName("")
	if sourceFile.PackageDeclaration != nil {
		pkg = compiler.PackageName(compiler.EntityNameToString(sourceFile.PackageDeclaration.AsPackageDeclaration().Name))
	}
	addAll(index.GetPackageTypes(pkg), result)
	addAll(index.GetPackageTypes("java.lang"), result)
	for _, imp := range sourceFile.Imports.Nodes {
		d := imp.AsImportDeclaration()
		if d.IsStatic {
			continue
		}
		if d.IsOnDemand {
			addAll(index.GetPackageTypes(compiler.PackageName(compiler.EntityNameToString(d.Name))), result)
		} else {
			fqn := compiler.EntityNameToString(d.Name)
			if t := index.GetType(compiler.Fqn(fqn)); t != nil {
				result.set(fqn[strings.LastIndex(fqn, ".")+1:], t)
			}
		}
	}
	return result
}

// --- classpath-resource completion -------------------------------------------

var resourceMethods = map[string]bool{"getResource": true, "getResourceAsStream": true}

func enclosingStringLiteral(node *compiler.Node) *compiler.Node {
	if node != nil && node.Kind == compiler.StringLiteral {
		return node
	}
	return nil
}

func isResourceArgument(str *compiler.Node) bool {
	call := str.Parent
	if call == nil || call.Kind != compiler.CallExpression {
		return false
	}
	callee := call.AsCallExpression().Expression
	return callee.Kind == compiler.PropertyAccessExpression &&
		resourceMethods[callee.AsPropertyAccessExpression().Name.AsIdentifier().Text]
}

func classpathResources(cfg *config.Config) []CompletionItem {
	seen := map[string]bool{}
	for _, root := range cfg.CompilerOptions.ResourcePaths {
		base := cfg.ResolvePath(root)
		_ = filepath.WalkDir(base, func(path string, dEntry fs.DirEntry, err error) error {
			if err != nil || dEntry.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(base, path)
			if rerr != nil {
				return nil
			}
			seen["/"+filepath.ToSlash(rel)] = true
			return nil
		})
	}
	var labels []string
	for label := range seen {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	out := make([]CompletionItem, 0, len(labels))
	for _, label := range labels {
		out = append(out, CompletionItem{Label: label, Kind: CompletionItemKindFile})
	}
	return out
}

// --- entry point -------------------------------------------------------------

func isIdentChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '$'
}

// GetCompletions returns completions at an offset. cfg (may be nil) enables the
// classpath-resource completion inside getResource(AsStream) strings.
func GetCompletions(program *compiler.Program, checker *compiler.Checker, sourceFile *compiler.Node, offset int, cfg *config.Config) []CompletionItem {
	text := sourceFile.AsSourceFile().Text

	if cfg != nil {
		back := offset - 1
		if back < 0 {
			back = 0
		}
		str := enclosingStringLiteral(compiler.GetNodeAtPosition(sourceFile, back))
		if str != nil && isResourceArgument(str) {
			return classpathResources(cfg)
		}
	}

	i := offset - 1
	for i >= 0 && isIdentChar(text[i]) {
		i--
	}
	for i >= 0 && isSpace(text[i]) {
		i--
	}
	if i >= 0 && text[i] == '.' {
		dot := i
		if dot == 0 {
			return nil
		}
		expr := compiler.GetNodeAtPosition(sourceFile, dot-1)
		for expr.Parent != nil && expr.Parent.End == dot && isExpressionKind(expr.Parent.Kind) {
			expr = expr.Parent
		}
		typ := checker.GetTypeOfExpression(expr)
		if typ.Kind != compiler.TypeKindClass {
			return nil
		}
		members := newOrderedSymbols()
		gatherTypeMembers(typ.Symbol, program, members, map[*compiler.Symbol]bool{}, false)
		return members.items()
	}

	back := offset - 1
	if back < 0 {
		back = 0
	}
	node := compiler.GetNodeAtPosition(sourceFile, back)
	return collectScopeSymbols(node, program).items()
}
