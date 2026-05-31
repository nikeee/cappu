// The multi-file project model. Holds the set of source files (open editor
// documents now; a workspace scan is layered on in P3), parses and binds them
// lazily, and caches the result per (uri, version) so repeated LSP requests do
// not re-parse. Cross-file indexing and the checker hang off this in later
// milestones.

import { bindSourceFile } from "./binder.ts";
import { parseSourceFile } from "./parser.ts";
import { entityNameToString } from "./utilities.ts";
import {
  type Node,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  type SymbolTable,
  SyntaxKind,
} from "./types.ts";

/** Cross-file lookup of top-level types by package and fully-qualified name. */
export interface GlobalIndex {
  /** Type symbol for a fully-qualified name (e.g. "java.util.List" or, in the default package, "C"). */
  getType(fqn: string): Symbol | undefined;
  /** simpleName -> type symbol for all top-level types in a package. */
  getPackageTypes(packageName: string): SymbolTable | undefined;
  getPackageSymbol(packageName: string): Symbol | undefined;
}

const TYPE_DECLARATION_KINDS = new Set<SyntaxKind>([
  SyntaxKind.ClassDeclaration,
  SyntaxKind.InterfaceDeclaration,
  SyntaxKind.EnumDeclaration,
  SyntaxKind.AnnotationTypeDeclaration,
  SyntaxKind.RecordDeclaration,
]);

interface OpenDocument {
  readonly text: string;
  readonly version: number;
}

interface CacheEntry {
  readonly version: number;
  readonly sourceFile: SourceFile;
}

export interface Program {
  /** Record (or update) an open editor document; overrides any project file. */
  setOpenDocument(uri: string, text: string, version: number): void;
  closeDocument(uri: string): void;
  /** Register a workspace file read from disk (open documents take precedence). */
  addProjectFile(uri: string, text: string): void;
  /** Parse + bind the file for a uri (cached), or undefined if unknown. */
  getSourceFile(uri: string): SourceFile | undefined;
  getOpenUris(): string[];
  /** All known uris (open documents + project files). */
  getAllUris(): string[];
  /** Cross-file type index over all current files (rebuilt when files change). */
  getGlobalIndex(): GlobalIndex;
}

function named(node: Node): string | undefined {
  return (node as { name?: { text: string } }).name?.text;
}

interface VersionedCacheEntry extends CacheEntry {
  readonly key: string;
}

export function createProgram(): Program {
  const openDocuments = new Map<string, OpenDocument>();
  const projectFiles = new Map<string, string>();
  const cache = new Map<string, VersionedCacheEntry>();

  // Effective text + cache key for a uri; open documents win over project files.
  function resolveSource(uri: string): { text: string; key: string } | undefined {
    const open = openDocuments.get(uri);
    if (open) return { text: open.text, key: `o${open.version}` };
    const text = projectFiles.get(uri);
    if (text !== undefined) return { text, key: "p" };
    return undefined;
  }

  function getSourceFile(uri: string): SourceFile | undefined {
    const source = resolveSource(uri);
    if (!source) return undefined;

    const cached = cache.get(uri);
    if (cached && cached.key === source.key) {
      return cached.sourceFile;
    }

    const sourceFile = parseSourceFile(uri, source.text);
    bindSourceFile(sourceFile);
    cache.set(uri, { version: 0, key: source.key, sourceFile });
    return sourceFile;
  }

  function allUris(): string[] {
    return [...new Set([...projectFiles.keys(), ...openDocuments.keys()])];
  }

  // Incremental cross-file index. Each file's top-level types are extracted once
  // (re-binding only when the file itself changes); the derived FQN/package maps
  // are rebuilt cheaply from those cached per-file lists. A single edit therefore
  // re-binds only the edited file, not the whole workspace.
  interface TypeEntry {
    packageName: string;
    simpleName: string;
    symbol: Symbol;
  }
  const fileTypes = new Map<string, TypeEntry[]>();
  const dirty = new Set<string>();
  let indexBuilt = false;

  const packages = new Map<string, SymbolTable>();
  const packageSymbols = new Map<string, Symbol>();
  const typesByFqn = new Map<string, Symbol>();

  function extractTypes(uri: string): TypeEntry[] {
    const sourceFile = getSourceFile(uri);
    if (!sourceFile) return [];
    const packageName = sourceFile.packageDeclaration
      ? entityNameToString(sourceFile.packageDeclaration.name)
      : "";
    const entries: TypeEntry[] = [];
    for (const statement of sourceFile.statements) {
      if (!TYPE_DECLARATION_KINDS.has(statement.kind) || !statement.symbol) continue;
      const simpleName = named(statement);
      if (simpleName) entries.push({ packageName, simpleName, symbol: statement.symbol });
    }
    return entries;
  }

  function refreshIndex(): void {
    if (indexBuilt && dirty.size === 0) return;
    // Re-extract only the changed (dirty) files; everything else keeps its cached
    // per-file type list (and its already-bound SourceFile). On the first build
    // every file is dirty by definition.
    const toVisit = indexBuilt ? dirty : new Set(allUris());
    for (const uri of toVisit) {
      if (resolveSource(uri)) fileTypes.set(uri, extractTypes(uri));
      else fileTypes.delete(uri);
    }
    dirty.clear();
    indexBuilt = true;

    // Rebuild the cheap derived maps from the per-file lists (no parsing/binding).
    packages.clear();
    packageSymbols.clear();
    typesByFqn.clear();
    const packageSymbolFor = (packageName: string): Symbol => {
      let symbol = packageSymbols.get(packageName);
      if (!symbol) {
        symbol = { flags: SymbolFlags.Package, escapedName: packageName, members: new Map() };
        packageSymbols.set(packageName, symbol);
        packages.set(packageName, symbol.members!);
      }
      return symbol;
    };
    for (const entries of fileTypes.values()) {
      for (const { packageName, simpleName, symbol } of entries) {
        const packageSymbol = packageSymbolFor(packageName);
        symbol.parent = packageSymbol;
        packageSymbol.members!.set(simpleName, symbol);
        typesByFqn.set(packageName ? `${packageName}.${simpleName}` : simpleName, symbol);
      }
    }
  }

  const globalIndex: GlobalIndex = {
    getType: fqn => typesByFqn.get(fqn),
    getPackageTypes: packageName => packages.get(packageName),
    getPackageSymbol: packageName => packageSymbols.get(packageName),
  };

  return {
    setOpenDocument(uri, text, version) {
      openDocuments.set(uri, { text, version });
      dirty.add(uri);
    },
    closeDocument(uri) {
      openDocuments.delete(uri);
      cache.delete(uri);
      dirty.add(uri);
    },
    addProjectFile(uri, text) {
      projectFiles.set(uri, text);
      cache.delete(uri);
      dirty.add(uri);
    },
    getSourceFile,
    getOpenUris: () => [...openDocuments.keys()],
    getAllUris: allUris,
    getGlobalIndex() {
      refreshIndex();
      return globalIndex;
    },
  };
}
