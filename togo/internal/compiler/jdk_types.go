package compiler

// Wire a provisioned JDK's real classes into the type checker, lazily. Port of
// src/compiler/jdkTypes.ts. When a project configures a "jdk", its jmods/ become
// the source of truth for JDK types (the full standard library); otherwise we
// fall back to the synthetic jdkstub.go (which works with no JDK present).
//
// The provider resolves one class at a time on a project-index miss: read the
// class family from the image, regenerate a stub source, parse+bind it in
// isolation (NOT AddProjectFile - that would pull every JDK class through the
// eager cross-file index), cache the symbol. Transitive supertypes resolve the
// same way on their own later lookups, so only the classes a project touches are
// ever bound. Resolution-only: this feeds GetType, not completion/import lists.

import (
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/jdks"
)

// createJdkTypeResolver returns a lazy GetType fallback backed by a provisioned
// JDK's jmods.
func createJdkTypeResolver(image *JdkImage) func(Fqn) *Symbol {
	// resolved records lookups (including misses, as nil) so the jmods are not
	// re-read for the same FQN.
	resolved := map[Fqn]*Symbol{}
	packageSymbols := map[PackageName]*Symbol{}

	packageSymbolFor := func(packageName PackageName) *Symbol {
		symbol := packageSymbols[packageName]
		if symbol == nil {
			symbol = &Symbol{Flags: SymbolFlagsPackage, EscapedName: string(packageName), Members: SymbolTable{}}
			packageSymbols[packageName] = symbol
		}
		return symbol
	}

	return func(fqn Fqn) *Symbol {
		if sym, ok := resolved[fqn]; ok {
			return sym
		}

		s := string(fqn)
		packageName := PackageName("")
		simpleName := s
		if lastDot := strings.LastIndexByte(s, '.'); lastDot >= 0 {
			packageName = PackageName(s[:lastDot])
			simpleName = s[lastDot+1:]
		}
		binaryName := strings.ReplaceAll(s, ".", "/")

		family := image.ReadClassFamily(binaryName)
		stub, ok := ClassFilesToStub(family)
		if family == nil || !ok {
			resolved[fqn] = nil
			return nil
		}

		sourceFile := ParseSourceFile("jdk:///"+binaryName+".java", stub.Source)
		BindSourceFile(sourceFile)
		// The stub has exactly one top-level type; take its bound symbol.
		var symbol *Symbol
		for _, statement := range sourceFile.AsSourceFile().Statements.Nodes {
			if statement.Symbol != nil && statement.Symbol.EscapedName == simpleName {
				symbol = statement.Symbol
				break
			}
		}
		if symbol == nil {
			resolved[fqn] = nil
			return nil
		}

		packageSymbol := packageSymbolFor(packageName)
		symbol.Parent = packageSymbol
		packageSymbol.Members[simpleName] = symbol
		resolved[fqn] = symbol
		return symbol
	}
}

// InstallJdkTypes makes JDK types resolvable for program: from the configured
// JDK's real classes when one is provisioned, else from the synthetic stub.
// Tolerates a nil config (the LSP can run without one).
func InstallJdkTypes(program *Program, cfg *config.Config) {
	var image *JdkImage
	if cfg != nil {
		if home := jdks.ProvisionedJdkHome(cfg); home != "" {
			image = NewJdkImage(home)
		}
	}
	if image != nil {
		program.SetJdkTypeResolver(createJdkTypeResolver(image))
	} else {
		LoadJdkStub(program)
	}
}
