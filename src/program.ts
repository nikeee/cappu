// The multi-file project model. Holds the set of source files (open editor
// documents now; a workspace scan is layered on in P3), parses and binds them
// lazily, and caches the result per (uri, version) so repeated LSP requests do
// not re-parse. Cross-file indexing and the checker hang off this in later
// milestones.

import { bindSourceFile } from "./binder.ts";
import { parseSourceFile } from "./parser.ts";
import type { SourceFile } from "./types.ts";

interface OpenDocument {
  readonly text: string;
  readonly version: number;
}

interface CacheEntry {
  readonly version: number;
  readonly sourceFile: SourceFile;
}

export interface Program {
  /** Record (or update) an open editor document; bumps the effective version. */
  setOpenDocument(uri: string, text: string, version: number): void;
  closeDocument(uri: string): void;
  /** Parse + bind the file for a uri (cached by version), or undefined if unknown. */
  getSourceFile(uri: string): SourceFile | undefined;
  getOpenUris(): string[];
}

export function createProgram(): Program {
  const openDocuments = new Map<string, OpenDocument>();
  const cache = new Map<string, CacheEntry>();

  function getSourceFile(uri: string): SourceFile | undefined {
    const open = openDocuments.get(uri);
    if (!open) return undefined;

    const cached = cache.get(uri);
    if (cached && cached.version === open.version) {
      return cached.sourceFile;
    }

    const sourceFile = parseSourceFile(uri, open.text);
    bindSourceFile(sourceFile);
    cache.set(uri, { version: open.version, sourceFile });
    return sourceFile;
  }

  return {
    setOpenDocument(uri, text, version) {
      openDocuments.set(uri, { text, version });
    },
    closeDocument(uri) {
      openDocuments.delete(uri);
      cache.delete(uri);
    },
    getSourceFile,
    getOpenUris: () => [...openDocuments.keys()],
  };
}
