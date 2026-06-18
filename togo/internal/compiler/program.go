package compiler

// The multi-file project model. Holds the set of source files (open editor
// documents overlaying workspace-scanned project files), parses and binds them
// lazily, and caches the result per (uri, version) so repeated LSP requests do
// not re-parse. The cross-file GlobalIndex and the checker hang off this.
// Port of src/compiler/program.ts.

import (
	"strconv"
	"strings"
)

// URI identifies a document ("file:///A.java").
type URI string

// Generation is the program mutation counter derived caches key their memo on.
type Generation int

// Fqn is a dotted fully-qualified type name ("java.util.List"; bare in the default package).
type Fqn string

// PackageName is a dotted package name ("" for the default package).
type PackageName string

func isTypeDeclarationKind(kind SyntaxKind) bool {
	switch kind {
	case ClassDeclaration, InterfaceDeclaration, EnumDeclaration, AnnotationTypeDeclaration, RecordDeclaration:
		return true
	default:
		return false
	}
}

type openDocument struct {
	text    string
	version int
}

type versionedCacheEntry struct {
	key        string
	sourceFile *Node
}

type typeEntry struct {
	packageName PackageName
	simpleName  string
	symbol      *Symbol
}

// Program is the multi-file project model.
type Program struct {
	openDocuments map[URI]openDocument
	projectFiles  map[URI]string
	cache         map[URI]versionedCacheEntry

	fileTypes  map[URI][]typeEntry
	dirty      map[URI]bool
	indexBuilt bool

	packages       map[PackageName]SymbolTable
	packageSymbols map[PackageName]*Symbol
	typesByFqn     map[Fqn]*Symbol
	packagesByName map[PackageName]*Symbol

	generation Generation
}

// NewProgram creates an empty program.
func NewProgram() *Program {
	return &Program{
		openDocuments:  map[URI]openDocument{},
		projectFiles:   map[URI]string{},
		cache:          map[URI]versionedCacheEntry{},
		fileTypes:      map[URI][]typeEntry{},
		dirty:          map[URI]bool{},
		packages:       map[PackageName]SymbolTable{},
		packageSymbols: map[PackageName]*Symbol{},
		typesByFqn:     map[Fqn]*Symbol{},
		packagesByName: map[PackageName]*Symbol{},
	}
}

// resolveSource returns the effective text and cache key for a uri; open
// documents win over project files.
func (p *Program) resolveSource(uri URI) (text, key string, ok bool) {
	if open, has := p.openDocuments[uri]; has {
		return open.text, "o" + strconv.Itoa(open.version), true
	}
	if text, has := p.projectFiles[uri]; has {
		return text, "p", true
	}
	return "", "", false
}

// GetSourceFile parses and binds the file for a uri (cached), or nil if unknown.
func (p *Program) GetSourceFile(uri URI) *Node {
	text, key, ok := p.resolveSource(uri)
	if !ok {
		return nil
	}
	if cached, has := p.cache[uri]; has && cached.key == key {
		return cached.sourceFile
	}
	sourceFile := ParseSourceFile(string(uri), text)
	BindSourceFile(sourceFile)
	p.cache[uri] = versionedCacheEntry{key: key, sourceFile: sourceFile}
	return sourceFile
}

func (p *Program) allUris() []URI {
	seen := map[URI]bool{}
	out := []URI{}
	for uri := range p.projectFiles {
		if !seen[uri] {
			seen[uri] = true
			out = append(out, uri)
		}
	}
	for uri := range p.openDocuments {
		if !seen[uri] {
			seen[uri] = true
			out = append(out, uri)
		}
	}
	return out
}

func (p *Program) extractTypes(uri URI) []typeEntry {
	sourceFile := p.GetSourceFile(uri)
	if sourceFile == nil {
		return nil
	}
	data := sourceFile.AsSourceFile()
	packageName := PackageName("")
	if data.PackageDeclaration != nil {
		packageName = PackageName(entityNameToString(data.PackageDeclaration.AsPackageDeclaration().Name))
	}
	var entries []typeEntry
	for _, statement := range data.Statements.Nodes {
		if !isTypeDeclarationKind(statement.Kind) || statement.Symbol == nil {
			continue
		}
		if name := nodeName(statement); name != nil && name.Kind == Identifier {
			entries = append(entries, typeEntry{packageName: packageName, simpleName: name.AsIdentifier().Text, symbol: statement.Symbol})
		}
	}
	return entries
}

// refreshIndex rebuilds the cross-file index incrementally; only dirty files are
// re-extracted (and re-bound), and the derived FQN/package maps are rebuilt
// cheaply from the cached per-file type lists.
func (p *Program) refreshIndex() {
	if p.indexBuilt && len(p.dirty) == 0 {
		return
	}
	var toVisit map[URI]bool
	if p.indexBuilt {
		toVisit = p.dirty
	} else {
		toVisit = map[URI]bool{}
		for _, uri := range p.allUris() {
			toVisit[uri] = true
		}
	}
	for uri := range toVisit {
		if _, _, ok := p.resolveSource(uri); ok {
			p.fileTypes[uri] = p.extractTypes(uri)
		} else {
			delete(p.fileTypes, uri)
		}
	}
	p.dirty = map[URI]bool{}
	p.indexBuilt = true

	// Rebuild the cheap derived maps from the per-file lists (no parsing/binding).
	p.packages = map[PackageName]SymbolTable{}
	p.packageSymbols = map[PackageName]*Symbol{}
	p.typesByFqn = map[Fqn]*Symbol{}
	packageSymbolFor := func(packageName PackageName) *Symbol {
		symbol := p.packageSymbols[packageName]
		if symbol == nil {
			symbol = &Symbol{Flags: SymbolFlagsPackage, EscapedName: string(packageName), Members: SymbolTable{}}
			p.packageSymbols[packageName] = symbol
			p.packages[packageName] = symbol.Members
		}
		return symbol
	}
	for _, entries := range p.fileTypes {
		for _, e := range entries {
			packageSymbol := packageSymbolFor(e.packageName)
			e.symbol.Parent = packageSymbol
			packageSymbol.Members[e.simpleName] = e.symbol
			fqn := Fqn(e.simpleName)
			if e.packageName != "" {
				fqn = Fqn(string(e.packageName) + "." + e.simpleName)
			}
			p.typesByFqn[fqn] = e.symbol
		}
	}

	// Index every package and every dotted prefix of one. Real packages keep
	// their symbol; intermediate prefixes get a synthetic package symbol.
	p.packagesByName = map[PackageName]*Symbol{}
	for name, symbol := range p.packageSymbols {
		p.packagesByName[name] = symbol
	}
	for name := range p.packageSymbols {
		segments := strings.Split(string(name), ".")
		for i := 1; i < len(segments); i++ {
			prefix := PackageName(strings.Join(segments[:i], "."))
			if _, has := p.packagesByName[prefix]; !has {
				p.packagesByName[prefix] = &Symbol{Flags: SymbolFlagsPackage, EscapedName: string(prefix), Members: SymbolTable{}}
			}
		}
	}
}

// GlobalIndex is the cross-file lookup of top-level types by package and FQN.
type GlobalIndex struct{ p *Program }

// GetType returns the type symbol for a fully-qualified name.
func (g *GlobalIndex) GetType(fqn Fqn) *Symbol { return g.p.typesByFqn[fqn] }

// GetPackageTypes returns simpleName -> type symbol for all top-level types in a package.
func (g *GlobalIndex) GetPackageTypes(packageName PackageName) SymbolTable {
	return g.p.packages[packageName]
}

// GetPackageSymbol returns the symbol for an exact package.
func (g *GlobalIndex) GetPackageSymbol(packageName PackageName) *Symbol {
	return g.p.packageSymbols[packageName]
}

// FindFqnsBySimpleName returns the FQNs of all top-level types with the simple name.
func (g *GlobalIndex) FindFqnsBySimpleName(simpleName string) []Fqn {
	var result []Fqn
	for fqn := range g.p.typesByFqn {
		s := string(fqn)
		dot := strings.LastIndex(s, ".")
		last := s
		if dot >= 0 {
			last = s[dot+1:]
		}
		if last == simpleName {
			result = append(result, fqn)
		}
	}
	return result
}

// GetPackageByName returns a package symbol for an exact package or any prefix of one.
func (g *GlobalIndex) GetPackageByName(name PackageName) *Symbol {
	return g.p.packagesByName[name]
}

// SetOpenDocument records (or updates) an open editor document; it overrides any project file.
func (p *Program) SetOpenDocument(uri URI, text string, version int) {
	p.openDocuments[uri] = openDocument{text: text, version: version}
	p.dirty[uri] = true
	p.generation++
}

// CloseDocument forgets an open editor document.
func (p *Program) CloseDocument(uri URI) {
	delete(p.openDocuments, uri)
	delete(p.cache, uri)
	p.dirty[uri] = true
	p.generation++
}

// AddProjectFile registers a workspace file read from disk (open documents take precedence).
func (p *Program) AddProjectFile(uri URI, text string) {
	p.projectFiles[uri] = text
	delete(p.cache, uri)
	p.dirty[uri] = true
	p.generation++
}

// RemoveProjectFile forgets a project file deleted from disk (an open document for it survives).
func (p *Program) RemoveProjectFile(uri URI) {
	delete(p.projectFiles, uri)
	delete(p.cache, uri)
	p.dirty[uri] = true
	p.generation++
}

// GetOpenUris returns the uris of all open documents.
func (p *Program) GetOpenUris() []URI {
	out := []URI{}
	for uri := range p.openDocuments {
		out = append(out, uri)
	}
	return out
}

// GetAllUris returns all known uris (open documents + project files).
func (p *Program) GetAllUris() []URI { return p.allUris() }

// GetGlobalIndex returns the cross-file type index over all current files.
func (p *Program) GetGlobalIndex() *GlobalIndex {
	p.refreshIndex()
	return &GlobalIndex{p: p}
}

// GetGeneration returns the mutation counter.
func (p *Program) GetGeneration() Generation { return p.generation }
