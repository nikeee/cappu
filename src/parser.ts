// Recursive-descent parser. Mirrors the TypeScript compiler parser: module-level
// mutable state initialized per parseSourceFile call, finishNode stamping
// pos/end and error flags, and list parsing (parseList/parseDelimitedList) with
// context-aware error recovery that always makes forward progress.
//
// This file holds the parser CORE (M3): scanning helpers, node construction,
// diagnostics, the list infrastructure, forEachChild, and a parseSourceFile that
// so far only recognizes EmptyStatement (";"). Grammar is layered on in later
// milestones; everything else is recovered as "declaration or statement
// expected" without aborting.

import { createScanner } from "./scanner.ts";
import { createDiagnostic } from "./diagnostics.ts";
import { Diagnostics } from "./diagnostics.ts";
import { tokenToString } from "./utilities.ts";
import {
	type Diagnostic,
	type DiagnosticMessage,
	type EmptyStatement,
	type Identifier,
	type Node,
	type NodeArray,
	NodeFlags,
	type Scanner,
	type SourceFile,
	type Statement,
	SyntaxKind,
	type Token,
} from "./types.ts";

type Mutable<T> = { -readonly [P in keyof T]: T[P] };

const enum ParsingContext {
	SourceElements,
	BlockStatements,
	ClassMembers,
	EnumConstants,
	TypeArguments,
	TypeParameters,
	Parameters,
	ArgumentExpressions,
	VariableDeclarations,
	ArrayInitializerElements,
	SwitchClauses,
	CatchClauses,
	Count,
}

// Module-level parser state, reset at the start of every parseSourceFile call.
let scanner: Scanner;
let sourceText = "";
let fileName = "";
let currentToken: SyntaxKind = SyntaxKind.Unknown;
let parseDiagnostics: Diagnostic[] = [];
let parsingContext = 0;
let parseErrorBeforeNextFinishedNode = false;

function token(): SyntaxKind {
	return currentToken;
}

function nextToken(): SyntaxKind {
	return (currentToken = scanner.scan());
}

function getNodePos(): number {
	return scanner.getTokenFullStart();
}

function createNode<T extends Node>(kind: SyntaxKind, pos: number): Mutable<T> {
	return {
		kind,
		flags: NodeFlags.None,
		pos,
		end: pos,
		parent: undefined,
	} as unknown as Mutable<T>;
}

function finishNode<T extends Node>(node: Mutable<T>, pos: number, end = scanner.getTokenFullStart()): T {
	node.pos = pos;
	node.end = end;
	if (parseErrorBeforeNextFinishedNode) {
		parseErrorBeforeNextFinishedNode = false;
		node.flags |= NodeFlags.ThisNodeHasError;
	}
	return node as T;
}

function createNodeArray<T extends Node>(elements: T[], pos: number, end = getNodePos()): NodeArray<T> {
	const array = elements as unknown as Mutable<NodeArray<T>>;
	array.pos = pos;
	array.end = end;
	return array as NodeArray<T>;
}

// Diagnostics

function parseErrorAtPosition(
	start: number,
	length: number,
	message: DiagnosticMessage,
	...args: string[]
): void {
	const last = parseDiagnostics[parseDiagnostics.length - 1];
	if (!last || start !== last.pos) {
		parseDiagnostics.push(createDiagnostic(start, length, message, ...args));
	}
	// Tell the next finishNode that the node it completes spans a parse error.
	parseErrorBeforeNextFinishedNode = true;
}

function parseErrorAt(start: number, end: number, message: DiagnosticMessage, ...args: string[]): void {
	parseErrorAtPosition(start, end - start, message, ...args);
}

function parseErrorAtCurrentToken(message: DiagnosticMessage, ...args: string[]): void {
	parseErrorAt(scanner.getTokenStart(), scanner.getTokenEnd(), message, ...args);
}

// Token consumption helpers

function parseExpected(kind: SyntaxKind, message?: DiagnosticMessage): boolean {
	if (token() === kind) {
		nextToken();
		return true;
	}
	if (message) {
		parseErrorAtCurrentToken(message);
	} else {
		parseErrorAtCurrentToken(Diagnostics._0_expected, tokenToString(kind) ?? "");
	}
	return false;
}

function parseOptional(kind: SyntaxKind): boolean {
	if (token() === kind) {
		nextToken();
		return true;
	}
	return false;
}

function parseTokenNode<T extends Node>(): T {
	const pos = getNodePos();
	const kind = token();
	nextToken();
	return finishNode(createNode<T>(kind, pos), pos);
}

function parseOptionalToken<T extends Node>(kind: SyntaxKind): T | undefined {
	if (token() === kind) {
		return parseTokenNode<T>();
	}
	return undefined;
}

function parseExpectedToken<T extends Node>(kind: SyntaxKind, message?: DiagnosticMessage): T {
	return (
		parseOptionalToken<T>(kind) ??
		createMissingNode<T>(kind, /*reportAtCurrentPosition*/ false, message ?? Diagnostics._0_expected, tokenToString(kind) ?? "")
	);
}

function createMissingNode<T extends Node>(
	kind: SyntaxKind,
	reportAtCurrentPosition: boolean,
	message?: DiagnosticMessage,
	...args: string[]
): T {
	if (reportAtCurrentPosition) {
		parseErrorAtPosition(scanner.getTokenFullStart(), 0, message ?? Diagnostics._0_expected, ...args);
	} else if (message) {
		parseErrorAtCurrentToken(message, ...args);
	}
	const pos = getNodePos();
	const node = createNode<T>(kind, pos);
	if (kind === SyntaxKind.Identifier) {
		(node as unknown as Mutable<Identifier>).text = "";
	}
	return finishNode(node, pos);
}

function parseIdentifier(): Identifier {
	if (token() === SyntaxKind.Identifier) {
		const pos = getNodePos();
		const text = scanner.getTokenValue();
		nextToken();
		const node = createNode<Identifier>(SyntaxKind.Identifier, pos);
		node.text = text;
		return finishNode(node, pos);
	}
	return createMissingNode<Identifier>(SyntaxKind.Identifier, /*reportAtCurrentPosition*/ false, Diagnostics.Identifier_expected);
}

// List parsing with error recovery

function isListTerminator(context: ParsingContext): boolean {
	if (token() === SyntaxKind.EndOfFileToken) {
		return true;
	}
	switch (context) {
		case ParsingContext.BlockStatements:
		case ParsingContext.ClassMembers:
		case ParsingContext.EnumConstants:
		case ParsingContext.SwitchClauses:
		case ParsingContext.ArrayInitializerElements:
			return token() === SyntaxKind.CloseBraceToken;
		case ParsingContext.Parameters:
		case ParsingContext.ArgumentExpressions:
			return token() === SyntaxKind.CloseParenToken;
		case ParsingContext.TypeArguments:
		case ParsingContext.TypeParameters:
			return token() === SyntaxKind.GreaterThanToken;
		default:
			return false;
	}
}

function isStartOfStatement(): boolean {
	// M3 stub: only the empty statement is recognized. Extended in M7.
	return token() === SyntaxKind.SemicolonToken;
}

function isListElement(context: ParsingContext, _inErrorRecovery: boolean): boolean {
	switch (context) {
		case ParsingContext.SourceElements:
		case ParsingContext.BlockStatements:
			return isStartOfStatement();
		default:
			return false;
	}
}

function isInSomeParsingContext(): boolean {
	for (let context = 0; context < ParsingContext.Count; context++) {
		if (parsingContext & (1 << context)) {
			if (isListElement(context, /*inErrorRecovery*/ true) || isListTerminator(context)) {
				return true;
			}
		}
	}
	return false;
}

function parsingContextErrors(context: ParsingContext): void {
	switch (context) {
		case ParsingContext.SourceElements:
		case ParsingContext.BlockStatements:
			parseErrorAtCurrentToken(Diagnostics.Declaration_or_statement_expected);
			return;
		case ParsingContext.Parameters:
			parseErrorAtCurrentToken(Diagnostics.Parameter_declaration_expected);
			return;
		default:
			parseErrorAtCurrentToken(Diagnostics.Unexpected_token);
	}
}

// Decide whether to abort the current list (because the token is valid in some
// enclosing context) or skip the offending token and keep going. Always makes
// progress, so list parsing cannot loop forever.
function abortParsingListOrMoveToNextToken(context: ParsingContext): boolean {
	parsingContextErrors(context);
	if (isInSomeParsingContext()) {
		return true;
	}
	nextToken();
	return false;
}

function parseList<T extends Node>(context: ParsingContext, parseElement: () => T): NodeArray<T> {
	const saveParsingContext = parsingContext;
	parsingContext |= 1 << context;
	const list: T[] = [];
	const listPos = getNodePos();

	while (!isListTerminator(context)) {
		if (isListElement(context, /*inErrorRecovery*/ false)) {
			list.push(parseElement());
			continue;
		}
		if (abortParsingListOrMoveToNextToken(context)) {
			break;
		}
	}

	parsingContext = saveParsingContext;
	return createNodeArray(list, listPos);
}

// Statements (M3: empty statement only)

function parseEmptyStatement(): EmptyStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.SemicolonToken);
	return finishNode(createNode<EmptyStatement>(SyntaxKind.EmptyStatement, pos), pos);
}

function parseStatement(): Statement {
	// M3 stub matching isStartOfStatement.
	return parseEmptyStatement();
}

// Entry point

export function parseSourceFile(fileNameArg: string, text: string): SourceFile {
	fileName = fileNameArg;
	sourceText = text;
	parseDiagnostics = [];
	parsingContext = 0;
	parseErrorBeforeNextFinishedNode = false;
	scanner = createScanner(text, (message, errPos, length) => parseErrorAtPosition(errPos, length, message));

	nextToken();
	const pos = getNodePos();
	const statements = parseList(ParsingContext.SourceElements, parseStatement);
	const endOfFileToken = parseExpectedToken<Token<SyntaxKind.EndOfFileToken>>(SyntaxKind.EndOfFileToken);

	const node = createNode<SourceFile>(SyntaxKind.SourceFile, pos);
	node.statements = statements;
	node.endOfFileToken = endOfFileToken;
	node.fileName = fileName;
	node.text = sourceText;
	finishNode(node, pos);
	node.parseDiagnostics = parseDiagnostics;
	return node;
}

// Tree walking. Mirrors the TS compiler forEachChild: visit each child node (and
// child NodeArray) in source order, returning the first truthy callback result.
// The binder and every LSP traversal go through this.

function visitNode<T>(cbNode: (node: Node) => T | undefined, node: Node | undefined): T | undefined {
	return node ? cbNode(node) : undefined;
}

function visitNodes<T>(
	cbNode: (node: Node) => T | undefined,
	cbNodes: ((nodes: NodeArray<Node>) => T | undefined) | undefined,
	nodes: NodeArray<Node> | undefined,
): T | undefined {
	if (!nodes) return undefined;
	if (cbNodes) return cbNodes(nodes);
	for (const node of nodes) {
		const result = cbNode(node);
		if (result) return result;
	}
	return undefined;
}

export function forEachChild<T>(
	node: Node,
	cbNode: (node: Node) => T | undefined,
	cbNodes?: (nodes: NodeArray<Node>) => T | undefined,
): T | undefined {
	switch (node.kind) {
		case SyntaxKind.SourceFile: {
			const sf = node as SourceFile;
			return visitNodes(cbNode, cbNodes, sf.statements) || visitNode(cbNode, sf.endOfFileToken);
		}
		default:
			// Tokens and childless nodes (EmptyStatement, Identifier, ...).
			return undefined;
	}
}
