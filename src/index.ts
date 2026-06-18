// Public API of the Java parser. This is the entry point an LSP builds on:
// parse source into a SourceFile, bind it to populate symbol tables and parent
// pointers, and walk the tree with forEachChild.

export { createScanner } from "./compiler/scanner.ts";
export { forEachChild, parseSourceFile } from "./compiler/parser.ts";
export { bindSourceFile } from "./compiler/binder.ts";
export { createDiagnostic, Diagnostics, formatMessage } from "./compiler/diagnostics.ts";
export {
  isAssignmentOperator,
  isKeyword,
  isLiteralKind,
  isModifierKeyword,
  isPrimitiveTypeKeyword,
  isReservedWord,
  textToKeyword,
  tokenToString,
} from "./compiler/utilities.ts";
export * from "./compiler/types.ts";
