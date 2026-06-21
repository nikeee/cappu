// Wire a provisioned JDK's real classes into the type checker, lazily. When a
// project configures a "jdk", its jmods/ become the source of truth for JDK
// types (the full standard library); otherwise we fall back to the synthetic
// jdkStub.ts (which works with no JDK present at all).
//
// The provider resolves one class at a time on a project-index miss: read the
// class family from the image, regenerate a stub source, parse+bind it in
// isolation (NOT addProjectFile - that would pull every JDK class through the
// eager cross-file index), cache the symbol. Transitive supertypes resolve the
// same way on their own later lookups, so only the classes a project touches are
// ever bound. Resolution-only: this feeds getType, not completion/import lists.

import type { CappuConfig } from "../config.ts";
import { provisionedJdkHome } from "../jdks/index.ts";
import { type Uri } from "../workspace.ts";
import { bindSourceFile } from "./binder.ts";
import { classFilesToStub } from "./classfileReader.ts";
import { createJdkImage, type JdkImage } from "./jdkImage.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { parseSourceFile } from "./parser.ts";
import type { Fqn, PackageName, Program } from "./program.ts";
import { type Symbol, SymbolFlags } from "./types.ts";

/** A lazy `getType` fallback backed by a provisioned JDK's jmods. */
export function createJdkTypeResolver(image: JdkImage): (fqn: Fqn) => Symbol | undefined {
  // null = looked up, not in the image (so we do not re-read the jmods for it).
  const cache = new Map<Fqn, Symbol | null>();
  const packageSymbols = new Map<PackageName, Symbol>();

  const packageSymbolFor = (packageName: PackageName): Symbol => {
    let symbol = packageSymbols.get(packageName);
    if (!symbol) {
      symbol = { flags: SymbolFlags.Package, escapedName: packageName, members: new Map() };
      packageSymbols.set(packageName, symbol);
    }
    return symbol;
  };

  return fqn => {
    const cached = cache.get(fqn);
    if (cached !== undefined) return cached ?? undefined;

    const lastDot = fqn.lastIndexOf(".");
    const packageName = (lastDot < 0 ? "" : fqn.slice(0, lastDot)) as PackageName;
    const simpleName = lastDot < 0 ? fqn : fqn.slice(lastDot + 1);
    const binaryName = fqn.replaceAll(".", "/");

    const family = image.readClassFamily(binaryName);
    const stub = family && classFilesToStub(family);
    if (!stub) {
      cache.set(fqn, null);
      return undefined;
    }

    const sourceFile = parseSourceFile(`jdk:///${binaryName}.java` as Uri, stub.source);
    bindSourceFile(sourceFile);
    // The stub has exactly one top-level type; take its bound symbol.
    const declaration = sourceFile.statements.find(
      s => (s as { symbol?: Symbol }).symbol?.escapedName === simpleName,
    );
    const symbol = (declaration as { symbol?: Symbol } | undefined)?.symbol;
    if (!symbol) {
      cache.set(fqn, null);
      return undefined;
    }

    const packageSymbol = packageSymbolFor(packageName);
    symbol.parent = packageSymbol;
    packageSymbol.members!.set(simpleName, symbol);
    cache.set(fqn, symbol);
    return symbol;
  };
}

/**
 * Make JDK types resolvable for `program`. The synthetic stub is always loaded:
 * it is the curated common-type list that completion and auto-import enumerate
 * (getPackageTypes/findFqnsBySimpleName/getAllTypeFqns) - the lazy image resolver
 * feeds getType only, not enumeration. When a JDK is provisioned, the image
 * additionally resolves whole types the stub omits (streams, java.time, ...),
 * with the stub winning for the common types it already covers. Tolerates a
 * missing config (the LSP can run without one).
 */
export function installJdkTypes(program: Program, config: CappuConfig | undefined): void {
  loadJdkStub(program);
  const home = config && provisionedJdkHome(config);
  const image = home ? createJdkImage(home) : undefined;
  if (image) program.setJdkTypeResolver(createJdkTypeResolver(image));
}
