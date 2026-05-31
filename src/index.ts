// Public API of the Java parser. This is the entry point an LSP builds on:
// parse source into a SourceFile, bind it to populate symbol tables and parent
// pointers, and walk the tree with forEachChild.

export { createScanner } from "./scanner.ts";
export { forEachChild, parseSourceFile } from "./parser.ts";
export { bindSourceFile } from "./binder.ts";
export { createDiagnostic, Diagnostics, formatMessage } from "./diagnostics.ts";
export {
	isAssignmentOperator,
	isKeyword,
	isLiteralKind,
	isModifierKeyword,
	isPrimitiveTypeKeyword,
	isReservedWord,
	textToKeyword,
	tokenToString,
} from "./utilities.ts";
export * from "./types.ts";
