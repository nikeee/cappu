// The multi-file project model. Holds the set of source files (open editor
// documents overlaying workspace-scanned project files), parses and binds them
// lazily, and caches the result per (uri, version) so repeated LSP requests do
// not re-parse. The cross-file GlobalIndex and the checker hang off this.

import { bindSourceFile } from "./binder.ts";
import { type Brand } from "../brand.ts";
import { parseSourceFile } from "./parser.ts";
import {
  type Node,
  type SourceFile,
  type Symbol,
  SymbolFlags,
  type SymbolTable,
  SyntaxKind,
} from "./types.ts";
import { entityNameToString } from "./utilities.ts";
import { type Uri } from "../workspace.ts";

/** The program mutation counter derived caches key their memo on. */
export type Generation = Brand<number, "Generation">;
/** A dotted fully-qualified type name ("java.util.List"; bare in the default package). */
export type Fqn = Brand<string, "Fqn">;
/** A dotted package name ("" for the default package). */
export type PackageName = Brand<string, "PackageName">;

// Cross-file lookup of top-level types by package and fully-qualified name.
export interface GlobalIndex {
  /** Type symbol for a fully-qualified name (e.g. "java.util.List" or, in the default package, "C"). */
  getType(fqn: Fqn): Symbol | undefined;
  /** simpleName -> type symbol for all top-level types in a package. */
  getPackageTypes(packageName: PackageName): SymbolTable | undefined;
  getPackageSymbol(packageName: PackageName): Symbol | undefined;
  /** Fully-qualified names of all top-level types with the given simple name (for import suggestions). */
  getAllTypeFqns(): string[];
  findFqnsBySimpleName(simpleName: string): Fqn[];
  /** A package symbol for an exact package or any prefix of one (e.g. "java" or "java.util"). */
  getPackageByName(name: PackageName): Symbol | undefined;
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
  setOpenDocument(uri: Uri, text: string, version: number): void;
  closeDocument(uri: Uri): void;
  /** Register a workspace file read from disk (open documents take precedence). */
  addProjectFile(uri: Uri, text: string): void;
  /** Forget a project file deleted from disk (an open document for it survives). */
  removeProjectFile(uri: Uri): void;
  /** Parse + bind the file for a uri (cached), or undefined if unknown. */
  getSourceFile(uri: Uri): SourceFile | undefined;
  getOpenUris(): Uri[];
  /** All known uris (open documents + project files). */
  getAllUris(): Uri[];
  /** Cross-file type index over all current files (rebuilt when files change). */
  getGlobalIndex(): GlobalIndex;
  /**
   * Monotonically increasing counter, bumped on every file mutation. Derived
   * caches (subtype index, ...) key their memo on it to invalidate cheaply.
   */
  getGeneration(): Generation;
}

function named(node: Node): string | undefined {
  return (node as { name?: { text: string } }).name?.text;
}

interface VersionedCacheEntry extends CacheEntry {
  readonly key: string;
}

export function createProgram(): Program {
  const openDocuments = new Map<Uri, OpenDocument>();
  const projectFiles = new Map<Uri, string>();
  const cache = new Map<Uri, VersionedCacheEntry>();

  // Effective text + cache key for a uri; open documents win over project files.
  function resolveSource(uri: Uri): { text: string; key: string } | undefined {
    const open = openDocuments.get(uri);
    if (open) return { text: open.text, key: `o${open.version}` };
    const text = projectFiles.get(uri);
    if (text !== undefined) return { text, key: "p" };
    return undefined;
  }

  function getSourceFile(uri: Uri): SourceFile | undefined {
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

  function allUris(): Uri[] {
    return [...new Set([...projectFiles.keys(), ...openDocuments.keys()])];
  }

  // Incremental cross-file index. Each file's top-level types are extracted once
  // (re-binding only when the file itself changes); the derived FQN/package maps
  // are rebuilt cheaply from those cached per-file lists. A single edit therefore
  // re-binds only the edited file, not the whole workspace.
  interface TypeEntry {
    packageName: PackageName;
    simpleName: string;
    symbol: Symbol;
  }
  const fileTypes = new Map<Uri, TypeEntry[]>();
  const dirty = new Set<Uri>();
  let indexBuilt = false;

  const packages = new Map<PackageName, SymbolTable>();
  const packageSymbols = new Map<PackageName, Symbol>();
  const typesByFqn = new Map<Fqn, Symbol>();
  // Every package name plus every dotted prefix of one (so "java" resolves even
  // though only "java.util" holds types), mapping to a package symbol.
  const packagesByName = new Map<PackageName, Symbol>();

  function extractTypes(uri: Uri): TypeEntry[] {
    const sourceFile = getSourceFile(uri);
    if (!sourceFile) return [];
    const packageName = (
      sourceFile.packageDeclaration ? entityNameToString(sourceFile.packageDeclaration.name) : ""
    ) as PackageName;
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
    const packageSymbolFor = (packageName: PackageName): Symbol => {
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
        typesByFqn.set((packageName ? `${packageName}.${simpleName}` : simpleName) as Fqn, symbol);
      }
    }

    // Index every package and every dotted prefix of one. Real packages keep
    // their symbol; intermediate prefixes get a synthetic package symbol.
    packagesByName.clear();
    for (const [name, symbol] of packageSymbols) packagesByName.set(name, symbol);
    for (const name of packageSymbols.keys()) {
      const segments = name.split(".");
      for (let i = 1; i < segments.length; i++) {
        const prefix = segments.slice(0, i).join(".") as PackageName;
        if (!packagesByName.has(prefix)) {
          packagesByName.set(prefix, {
            flags: SymbolFlags.Package,
            escapedName: prefix,
            members: new Map(),
          });
        }
      }
    }
  }

  const globalIndex: GlobalIndex = {
    getType: fqn => typesByFqn.get(fqn),
    getPackageTypes: packageName => packages.get(packageName),
    getPackageSymbol: packageName => packageSymbols.get(packageName),
    findFqnsBySimpleName: simpleName => {
      const result: Fqn[] = [];
      for (const fqn of typesByFqn.keys()) {
        const dot = fqn.lastIndexOf(".");
        if ((dot < 0 ? fqn : fqn.slice(dot + 1)) === simpleName) result.push(fqn);
      }
      return result;
    },
    getAllTypeFqns: () => [...typesByFqn.keys()],
    getPackageByName: name => packagesByName.get(name),
  };

  let generation = 0;
  return {
    setOpenDocument(uri, text, version) {
      openDocuments.set(uri, { text, version });
      dirty.add(uri);
      generation++;
    },
    closeDocument(uri) {
      openDocuments.delete(uri);
      cache.delete(uri);
      dirty.add(uri);
      generation++;
    },
    addProjectFile(uri, text) {
      projectFiles.set(uri, text);
      cache.delete(uri);
      dirty.add(uri);
      generation++;
    },
    removeProjectFile(uri) {
      projectFiles.delete(uri);
      cache.delete(uri);
      dirty.add(uri); // refreshIndex drops its types once no source resolves
      generation++;
    },
    getSourceFile,
    getOpenUris: () => [...openDocuments.keys()],
    getAllUris: allUris,
    getGlobalIndex() {
      refreshIndex();
      return globalIndex;
    },
    getGeneration: () => generation as Generation,
  };
}
