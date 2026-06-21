// Read compiled classes out of a provisioned JDK's jmods/ directory, on demand.
// A real JDK gives the type checker the full standard-library API surface that
// the hand-written jdkStub.ts only approximates. We only ever touch the jmods
// of a JDK cappu itself provisioned (config "jdk"); a .jmod is an ordinary zip
// with a 4-byte magic header ("JM" + version), so the existing zip reader reads
// it once the header is stripped, and classes live under a "classes/" prefix.
//
// Everything here is lazy: nothing is read until the first JDK type is actually
// resolved (so startup is untouched), and only the modules a project touches are
// held in memory.

import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { readZipEntries, type ZipEntry } from "./zipReader.ts";

// .jmod magic: 0x4A 0x4D ("JM") followed by a 2-byte version; the rest is a zip.
const JMOD_MAGIC = [0x4a, 0x4d];

export interface JdkImage {
  /**
   * The compiled bytes of `binaryName` (e.g. "java/util/List") plus every
   * `binaryName$*` nested class in the same module, so a stub built from them
   * folds the nested types in. Undefined when the class is not in the image.
   */
  readClassFamily(binaryName: string): Uint8Array[] | undefined;
}

/** Read a .jmod file and return its zip entries, or undefined if not a jmod. */
function readJmodEntries(path: string): ZipEntry[] | undefined {
  const bytes = readFileSync(path);
  if (bytes.length < 4 || bytes[0] !== JMOD_MAGIC[0] || bytes[1] !== JMOD_MAGIC[1]) {
    return undefined;
  }
  return readZipEntries(bytes.subarray(4));
}

/** The package of a "classes/java/util/List.class" entry, as "java/util". */
function packageOfEntry(name: string): string | undefined {
  if (!name.startsWith("classes/") || !name.endsWith(".class")) return undefined;
  const path = name.slice("classes/".length, -".class".length);
  const slash = path.lastIndexOf("/");
  return slash < 0 ? "" : path.slice(0, slash);
}

/**
 * A reader over the jmods/ of the JDK at `jdkHome`, or undefined when there are
 * no jmods (e.g. a JRE, or a stripped image) - the caller then keeps the stub.
 */
export function createJdkImage(jdkHome: string): JdkImage | undefined {
  const jmodDir = join(jdkHome, "jmods");
  let jmodFiles: string[];
  try {
    jmodFiles = readdirSync(jmodDir).filter(f => f.endsWith(".jmod"));
  } catch {
    return undefined;
  }
  if (jmodFiles.length === 0) return undefined;
  // java.base first: it holds java.lang/util/io/... which is almost everything a
  // project resolves, so the package scan usually stops after one module.
  jmodFiles.sort((a, b) => (a === "java.base.jmod" ? -1 : b === "java.base.jmod" ? 1 : 0));

  // package ("java/util") -> module file ("java.base.jmod"). Built once, on the
  // first miss, then retained (strings only). The module byte buffers used to
  // build it are released; only modules we actually read classes from are kept.
  let packageToModule: Map<string, string> | undefined;
  // module file -> its entries (holds that module's byte buffer alive).
  // ponytail: keeps the buffers of modules we serve classes from (typically just
  // java.base); if resident memory ever matters, switch to seek-based reads.
  const openModules = new Map<string, ZipEntry[] | null>();

  function entriesFor(modFile: string): ZipEntry[] | undefined {
    const cached = openModules.get(modFile);
    if (cached !== undefined) return cached ?? undefined;
    let entries: ZipEntry[] | undefined;
    try {
      entries = readJmodEntries(join(jmodDir, modFile));
    } catch {
      entries = undefined;
    }
    openModules.set(modFile, entries ?? null);
    return entries;
  }

  function buildPackageMap(): Map<string, string> {
    const map = new Map<string, string>();
    for (const modFile of jmodFiles) {
      let entries: ZipEntry[] | undefined;
      try {
        entries = readJmodEntries(join(jmodDir, modFile));
      } catch {
        continue;
      }
      for (const entry of entries ?? []) {
        const pkg = packageOfEntry(entry.name);
        if (pkg !== undefined && !map.has(pkg)) map.set(pkg, modFile); // first module wins
      }
      // buffer for this module is dropped here unless entriesFor later reopens it
    }
    return map;
  }

  return {
    readClassFamily(binaryName) {
      packageToModule ??= buildPackageMap();
      const slash = binaryName.lastIndexOf("/");
      const pkg = slash < 0 ? "" : binaryName.slice(0, slash);
      const modFile = packageToModule.get(pkg);
      if (modFile === undefined) return undefined;
      const entries = entriesFor(modFile);
      if (!entries) return undefined;
      const outerName = `classes/${binaryName}.class`;
      const outer = entries.find(e => e.name === outerName);
      if (!outer) return undefined;
      const nestedPrefix = `classes/${binaryName}$`;
      const family = [outer.read()];
      for (const entry of entries) {
        if (entry.name.startsWith(nestedPrefix) && entry.name.endsWith(".class")) {
          family.push(entry.read());
        }
      }
      return family;
    },
  };
}
