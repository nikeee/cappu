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
import { isModifierKeyword, isPrimitiveTypeKeyword } from "./utilities.ts";
import {
	type Annotation,
	type AnnotationTypeDeclaration,
	type ArrayType,
	type Block,
	type ClassDeclaration,
	type ConstructorDeclaration,
	type Diagnostic,
	type DiagnosticMessage,
	type EmptyStatement,
	type EntityName,
	type EnumConstantDeclaration,
	type EnumDeclaration,
	type FieldDeclaration,
	type Identifier,
	type ImportDeclaration,
	type InitializerBlock,
	type InterfaceDeclaration,
	type MethodDeclaration,
	type ModifierLike,
	type Node,
	type NodeArray,
	NodeFlags,
	type PackageDeclaration,
	type Parameter,
	type PrimitiveType,
	type QualifiedName,
	type Scanner,
	type SourceFile,
	type Statement,
	SyntaxKind,
	type Token,
	type TypeNode,
	type TypeParameter,
	type TypeReference,
	type VariableDeclarator,
	type WildcardType,
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

// Speculation. Wraps the scanner's lookAhead/tryScan and additionally saves and
// restores the parser-level state (current token, diagnostics, error flag) so a
// failed/peeked parse leaves no trace. Mirrors the TS speculationHelper.
function speculationHelper<T>(callback: () => T, isLookahead: boolean): T {
	const saveToken = currentToken;
	const saveDiagnosticsLength = parseDiagnostics.length;
	const saveErrorBeforeNextFinishedNode = parseErrorBeforeNextFinishedNode;

	const result = isLookahead ? scanner.lookAhead(callback) : scanner.tryScan(callback);

	if (!result || isLookahead) {
		currentToken = saveToken;
		parseDiagnostics.length = saveDiagnosticsLength;
		parseErrorBeforeNextFinishedNode = saveErrorBeforeNextFinishedNode;
	}
	return result;
}

function lookAhead<T>(callback: () => T): T {
	return speculationHelper(callback, /*isLookahead*/ true);
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
	// M4: empty statements and type declarations. Full statement set in M7.
	switch (token()) {
		case SyntaxKind.SemicolonToken:
		case SyntaxKind.ClassKeyword:
		case SyntaxKind.InterfaceKeyword:
		case SyntaxKind.EnumKeyword:
		case SyntaxKind.AtToken:
			return true;
		default:
			return isModifierKeyword(token());
	}
}

function isStartOfType(): boolean {
	return (
		isPrimitiveTypeKeyword(token()) ||
		token() === SyntaxKind.VoidKeyword ||
		token() === SyntaxKind.Identifier ||
		token() === SyntaxKind.QuestionToken
	);
}

function isListElement(context: ParsingContext, _inErrorRecovery: boolean): boolean {
	switch (context) {
		case ParsingContext.SourceElements:
		case ParsingContext.BlockStatements:
			return isStartOfStatement();
		case ParsingContext.TypeArguments:
			return isStartOfType();
		case ParsingContext.TypeParameters:
			return token() === SyntaxKind.Identifier || token() === SyntaxKind.AtToken;
		case ParsingContext.ClassMembers:
			return isStartOfClassMember();
		case ParsingContext.Parameters:
			return isStartOfParameter();
		default:
			return false;
	}
}

function isStartOfClassMember(): boolean {
	switch (token()) {
		case SyntaxKind.SemicolonToken:
		case SyntaxKind.OpenBraceToken: // initializer block
		case SyntaxKind.LessThanToken: // generic method/constructor
		case SyntaxKind.AtToken:
		case SyntaxKind.ClassKeyword:
		case SyntaxKind.InterfaceKeyword:
		case SyntaxKind.EnumKeyword:
			return true;
		default:
			return isModifierKeyword(token()) || isStartOfType();
	}
}

function isStartOfParameter(): boolean {
	return (
		token() === SyntaxKind.AtToken ||
		token() === SyntaxKind.FinalKeyword ||
		isStartOfType()
	);
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

// A comma-separated list. Mirrors the TS parseDelimitedList, including the
// "skip a token if no progress" guard so malformed input cannot loop.
function parseDelimitedList<T extends Node>(context: ParsingContext, parseElement: () => T): NodeArray<T> {
	const saveParsingContext = parsingContext;
	parsingContext |= 1 << context;
	const list: T[] = [];
	const listPos = getNodePos();

	while (true) {
		if (isListElement(context, /*inErrorRecovery*/ false)) {
			const startPos = scanner.getTokenFullStart();
			list.push(parseElement());
			if (parseOptional(SyntaxKind.CommaToken)) {
				continue;
			}
			if (isListTerminator(context)) {
				break;
			}
			parseExpected(SyntaxKind.CommaToken);
			if (startPos === scanner.getTokenFullStart()) {
				nextToken();
			}
			continue;
		}
		if (isListTerminator(context)) {
			break;
		}
		if (abortParsingListOrMoveToNextToken(context)) {
			break;
		}
	}

	parsingContext = saveParsingContext;
	return createNodeArray(list, listPos);
}

// Names

function makeQualifiedName(left: EntityName, right: Identifier): QualifiedName {
	const node = createNode<QualifiedName>(SyntaxKind.QualifiedName, left.pos);
	node.left = left;
	node.right = right;
	return finishNode(node, left.pos);
}

function parseEntityName(): EntityName {
	let entity: EntityName = parseIdentifier();
	while (parseOptional(SyntaxKind.DotToken)) {
		entity = makeQualifiedName(entity, parseIdentifier());
	}
	return entity;
}

// Types

function parseType(): TypeNode {
	let type = parseNonArrayType();
	while (token() === SyntaxKind.OpenBracketToken) {
		const pos = type.pos;
		nextToken(); // '['
		parseExpected(SyntaxKind.CloseBracketToken);
		const array = createNode<ArrayType>(SyntaxKind.ArrayType, pos);
		array.elementType = type;
		type = finishNode(array, pos);
	}
	return type;
}

function parseNonArrayType(): TypeNode {
	const pos = getNodePos();
	if (isPrimitiveTypeKeyword(token()) || token() === SyntaxKind.VoidKeyword) {
		const keyword = token();
		nextToken();
		const node = createNode<PrimitiveType>(SyntaxKind.PrimitiveType, pos);
		node.keyword = keyword;
		return finishNode(node, pos);
	}
	if (token() === SyntaxKind.QuestionToken) {
		return parseWildcardType();
	}
	const typeName = parseEntityName();
	const typeArguments = token() === SyntaxKind.LessThanToken ? parseTypeArguments() : undefined;
	const node = createNode<TypeReference>(SyntaxKind.TypeReference, pos);
	node.typeName = typeName;
	node.typeArguments = typeArguments;
	return finishNode(node, pos);
}

function parseWildcardType(): WildcardType {
	const pos = getNodePos();
	parseExpected(SyntaxKind.QuestionToken);
	let hasExtends = false;
	let hasSuper = false;
	let type: TypeNode | undefined;
	if (parseOptional(SyntaxKind.ExtendsKeyword)) {
		hasExtends = true;
		type = parseType();
	} else if (parseOptional(SyntaxKind.SuperKeyword)) {
		hasSuper = true;
		type = parseType();
	}
	const node = createNode<WildcardType>(SyntaxKind.WildcardType, pos);
	node.hasExtends = hasExtends;
	node.hasSuper = hasSuper;
	node.type = type;
	return finishNode(node, pos);
}

function parseTypeArgument(): TypeNode | WildcardType {
	return token() === SyntaxKind.QuestionToken ? parseWildcardType() : parseType();
}

// The closing '>' is always a single GreaterThanToken (the scanner never munches
// '>>'), so nested type arguments close naturally one '>' at a time.
function parseTypeArguments(): NodeArray<TypeNode | WildcardType> {
	parseExpected(SyntaxKind.LessThanToken);
	const list = parseDelimitedList(ParsingContext.TypeArguments, parseTypeArgument);
	parseExpected(SyntaxKind.GreaterThanToken);
	return list;
}

function parseTypeParameter(): TypeParameter {
	const pos = getNodePos();
	const name = parseIdentifier();
	let constraint: NodeArray<TypeNode> | undefined;
	if (parseOptional(SyntaxKind.ExtendsKeyword)) {
		const bounds: TypeNode[] = [parseType()];
		const boundsPos = bounds[0]!.pos;
		while (parseOptional(SyntaxKind.AmpersandToken)) {
			bounds.push(parseType());
		}
		constraint = createNodeArray(bounds, boundsPos);
	}
	const node = createNode<TypeParameter>(SyntaxKind.TypeParameter, pos);
	node.name = name;
	node.constraint = constraint;
	return finishNode(node, pos);
}

function parseTypeParameters(): NodeArray<TypeParameter> | undefined {
	if (token() !== SyntaxKind.LessThanToken) {
		return undefined;
	}
	parseExpected(SyntaxKind.LessThanToken);
	const list = parseDelimitedList(ParsingContext.TypeParameters, parseTypeParameter);
	parseExpected(SyntaxKind.GreaterThanToken);
	return list;
}

function parseTypeList(): NodeArray<TypeNode> {
	const pos = getNodePos();
	const list: TypeNode[] = [parseType()];
	while (parseOptional(SyntaxKind.CommaToken)) {
		list.push(parseType());
	}
	return createNodeArray(list, pos);
}

// Modifiers and annotations

function isAnnotationTypeDeclarationStart(): boolean {
	// Current token is '@'. It introduces an @interface declaration (rather than
	// an annotation used as a modifier) when followed by 'interface'.
	return scanner.lookAhead(() => scanner.scan()) === SyntaxKind.InterfaceKeyword;
}

function parseModifiers(): NodeArray<ModifierLike> | undefined {
	const pos = getNodePos();
	const list: ModifierLike[] = [];
	while (true) {
		if (isModifierKeyword(token())) {
			list.push(parseTokenNode());
			continue;
		}
		if (token() === SyntaxKind.AtToken && !isAnnotationTypeDeclarationStart()) {
			list.push(parseAnnotation());
			continue;
		}
		break;
	}
	return list.length ? createNodeArray(list, pos) : undefined;
}

function parseAnnotation(): Annotation {
	const pos = getNodePos();
	parseExpected(SyntaxKind.AtToken);
	const typeName = parseEntityName();
	// Argument values are expressions; their parsing is added in M8. For now the
	// parenthesized argument list is skipped so headers parse cleanly.
	if (token() === SyntaxKind.OpenParenToken) {
		skipBalanced(SyntaxKind.OpenParenToken, SyntaxKind.CloseParenToken);
	}
	const node = createNode<Annotation>(SyntaxKind.Annotation, pos);
	node.typeName = typeName;
	node.args = undefined;
	return finishNode(node, pos);
}

// Consume a balanced run of open/close tokens, used to skip not-yet-parsed
// bodies (class members in M4, annotation arguments). The opening token is the
// current token.
function skipBalanced(open: SyntaxKind, close: SyntaxKind): void {
	nextToken(); // opening token
	let depth = 1;
	while (depth > 0 && token() !== SyntaxKind.EndOfFileToken) {
		if (token() === open) {
			depth++;
		} else if (token() === close) {
			depth--;
			if (depth === 0) {
				nextToken();
				return;
			}
		}
		nextToken();
	}
}

// Members

// A block whose statements are not yet parsed (skipped). M7 replaces this with
// real statement parsing.
function parseBlockStub(): Block {
	const pos = getNodePos();
	const statementsPos = getNodePos();
	if (token() === SyntaxKind.OpenBraceToken) {
		skipBalanced(SyntaxKind.OpenBraceToken, SyntaxKind.CloseBraceToken);
	} else {
		parseExpected(SyntaxKind.OpenBraceToken);
	}
	const node = createNode<Block>(SyntaxKind.Block, pos);
	node.statements = createNodeArray<Statement>([], statementsPos);
	return finishNode(node, pos);
}

// Trailing C-style array brackets after a declarator/parameter name (int a[]).
function parseArrayRankAfterName(): number {
	let rank = 0;
	while (token() === SyntaxKind.OpenBracketToken) {
		nextToken();
		parseExpected(SyntaxKind.CloseBracketToken);
		rank++;
	}
	return rank;
}

// Skip a variable initializer (after '='). Real expression parsing is M8; here
// we consume up to the next top-level ',' or ';'.
function skipInitializer(): void {
	let depth = 0;
	while (token() !== SyntaxKind.EndOfFileToken) {
		const t = token();
		if (depth === 0 && (t === SyntaxKind.CommaToken || t === SyntaxKind.SemicolonToken)) {
			return;
		}
		if (t === SyntaxKind.OpenParenToken || t === SyntaxKind.OpenBracketToken || t === SyntaxKind.OpenBraceToken) {
			depth++;
		} else if (
			t === SyntaxKind.CloseParenToken ||
			t === SyntaxKind.CloseBracketToken ||
			t === SyntaxKind.CloseBraceToken
		) {
			if (depth === 0) return;
			depth--;
		}
		nextToken();
	}
}

function parseVariableDeclarator(name: Identifier): VariableDeclarator {
	const arrayRankAfterName = parseArrayRankAfterName();
	let initializer: Node | undefined;
	if (parseOptional(SyntaxKind.EqualsToken)) {
		// M8 parses the real initializer expression; for now skip it.
		skipInitializer();
		initializer = undefined;
	}
	const node = createNode<VariableDeclarator>(SyntaxKind.VariableDeclarator, name.pos);
	node.name = name;
	node.arrayRankAfterName = arrayRankAfterName;
	node.initializer = initializer;
	return finishNode(node, name.pos);
}

function parseFieldDeclaration(
	pos: number,
	modifiers: NodeArray<ModifierLike> | undefined,
	type: TypeNode,
	firstName: Identifier,
): FieldDeclaration {
	const declaratorsPos = firstName.pos;
	const declarators: VariableDeclarator[] = [parseVariableDeclarator(firstName)];
	while (parseOptional(SyntaxKind.CommaToken)) {
		declarators.push(parseVariableDeclarator(parseIdentifier()));
	}
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<FieldDeclaration>(SyntaxKind.FieldDeclaration, pos);
	node.modifiers = modifiers;
	node.type = type;
	node.declarators = createNodeArray(declarators, declaratorsPos);
	return finishNode(node, pos);
}

function parseParameter(): Parameter {
	const pos = getNodePos();
	const modifiers = parseModifiers();
	const type = parseType();
	const isVarArgs = parseOptional(SyntaxKind.DotDotDotToken);
	const name = parseIdentifier();
	const arrayRankAfterName = parseArrayRankAfterName();
	const node = createNode<Parameter>(SyntaxKind.Parameter, pos);
	node.modifiers = modifiers;
	node.type = type;
	node.isVarArgs = isVarArgs;
	node.name = name;
	node.arrayRankAfterName = arrayRankAfterName;
	return finishNode(node, pos);
}

function parseFormalParameters(): NodeArray<Parameter> {
	parseExpected(SyntaxKind.OpenParenToken);
	const parameters = parseDelimitedList(ParsingContext.Parameters, parseParameter);
	parseExpected(SyntaxKind.CloseParenToken);
	return parameters;
}

function parseThrows(): NodeArray<TypeNode> | undefined {
	return parseOptional(SyntaxKind.ThrowsKeyword) ? parseTypeList() : undefined;
}

function parseMethodDeclaration(
	pos: number,
	modifiers: NodeArray<ModifierLike> | undefined,
	typeParameters: NodeArray<TypeParameter> | undefined,
	returnType: TypeNode,
	name: Identifier,
): MethodDeclaration {
	const parameters = parseFormalParameters();
	// C-style array return rank: int m()[]
	let actualReturnType = returnType;
	const extraRank = parseArrayRankAfterName();
	for (let i = 0; i < extraRank; i++) {
		const array = createNode<ArrayType>(SyntaxKind.ArrayType, actualReturnType.pos);
		array.elementType = actualReturnType;
		actualReturnType = finishNode(array, actualReturnType.pos);
	}
	const throwsClause = parseThrows();
	// Annotation-element default value (@interface): 'default <value>'. Skipped
	// until M8.
	if (parseOptional(SyntaxKind.DefaultKeyword)) {
		skipInitializer();
	}
	let body: Block | undefined;
	if (token() === SyntaxKind.OpenBraceToken) {
		body = parseBlockStub();
	} else {
		parseExpected(SyntaxKind.SemicolonToken);
	}
	const node = createNode<MethodDeclaration>(SyntaxKind.MethodDeclaration, pos);
	node.modifiers = modifiers;
	node.typeParameters = typeParameters;
	node.returnType = actualReturnType;
	node.name = name;
	node.parameters = parameters;
	node.throws = throwsClause;
	node.body = body;
	return finishNode(node, pos);
}

function parseConstructorDeclaration(
	pos: number,
	modifiers: NodeArray<ModifierLike> | undefined,
	typeParameters: NodeArray<TypeParameter> | undefined,
): ConstructorDeclaration {
	const name = parseIdentifier();
	const parameters = parseFormalParameters();
	const throwsClause = parseThrows();
	const body = parseBlockStub();
	const node = createNode<ConstructorDeclaration>(SyntaxKind.ConstructorDeclaration, pos);
	node.modifiers = modifiers;
	node.typeParameters = typeParameters;
	node.name = name;
	node.parameters = parameters;
	node.throws = throwsClause;
	node.body = body;
	return finishNode(node, pos);
}

function hasStaticModifier(modifiers: NodeArray<ModifierLike> | undefined): boolean {
	return !!modifiers?.some((m) => m.kind === SyntaxKind.StaticKeyword);
}

function parseInitializerBlock(
	pos: number,
	modifiers: NodeArray<ModifierLike> | undefined,
): InitializerBlock {
	const body = parseBlockStub();
	const node = createNode<InitializerBlock>(SyntaxKind.InitializerBlock, pos);
	node.isStatic = hasStaticModifier(modifiers);
	node.body = body;
	return finishNode(node, pos);
}

// After optional type parameters, a member is a constructor when it is
// 'Identifier (' with no return type.
function isConstructorDeclaration(): boolean {
	return (
		token() === SyntaxKind.Identifier &&
		lookAhead(() => {
			nextToken();
			return token() === SyntaxKind.OpenParenToken;
		})
	);
}

function parseClassMember(): Node {
	const pos = getNodePos();
	if (token() === SyntaxKind.SemicolonToken) {
		return parseEmptyStatement();
	}
	const modifiers = parseModifiers();

	if (token() === SyntaxKind.OpenBraceToken) {
		return parseInitializerBlock(pos, modifiers);
	}
	switch (token()) {
		case SyntaxKind.ClassKeyword:
			return parseClassDeclaration(pos, modifiers);
		case SyntaxKind.InterfaceKeyword:
			return parseInterfaceDeclaration(pos, modifiers);
		case SyntaxKind.EnumKeyword:
			return parseEnumDeclaration(pos, modifiers);
		case SyntaxKind.AtToken:
			return parseAnnotationTypeDeclaration(pos, modifiers);
	}

	const typeParameters = parseTypeParameters();
	if (isConstructorDeclaration()) {
		return parseConstructorDeclaration(pos, modifiers, typeParameters);
	}
	const type = parseType();
	const name = parseIdentifier();
	if (token() === SyntaxKind.OpenParenToken) {
		return parseMethodDeclaration(pos, modifiers, typeParameters, type, name);
	}
	return parseFieldDeclaration(pos, modifiers, type, name);
}

// Type declarations

function parseClassBody(): NodeArray<Node> {
	const pos = getNodePos();
	if (token() !== SyntaxKind.OpenBraceToken) {
		parseExpected(SyntaxKind.OpenBraceToken);
		return createNodeArray<Node>([], pos);
	}
	parseExpected(SyntaxKind.OpenBraceToken);
	const members = parseList(ParsingContext.ClassMembers, parseClassMember);
	parseExpected(SyntaxKind.CloseBraceToken);
	return members;
}

function parseAnnotations(): NodeArray<Annotation> | undefined {
	const pos = getNodePos();
	const list: Annotation[] = [];
	while (token() === SyntaxKind.AtToken && !isAnnotationTypeDeclarationStart()) {
		list.push(parseAnnotation());
	}
	return list.length ? createNodeArray(list, pos) : undefined;
}

function parseEnumConstant(): EnumConstantDeclaration {
	const pos = getNodePos();
	const annotations = parseAnnotations();
	const name = parseIdentifier();
	if (token() === SyntaxKind.OpenParenToken) {
		// Constructor arguments; real expressions in M8.
		skipBalanced(SyntaxKind.OpenParenToken, SyntaxKind.CloseParenToken);
	}
	const classBody = token() === SyntaxKind.OpenBraceToken ? parseClassBody() : undefined;
	const node = createNode<EnumConstantDeclaration>(SyntaxKind.EnumConstantDeclaration, pos);
	node.modifiers = annotations;
	node.name = name;
	node.arguments = undefined;
	node.classBody = classBody;
	return finishNode(node, pos);
}

function isStartOfEnumConstant(): boolean {
	return token() === SyntaxKind.Identifier || token() === SyntaxKind.AtToken;
}

function parseEnumBody(): { enumConstants: NodeArray<EnumConstantDeclaration>; members: NodeArray<Node> } {
	const constantsPos = getNodePos();
	parseExpected(SyntaxKind.OpenBraceToken);

	const constants: EnumConstantDeclaration[] = [];
	if (isStartOfEnumConstant()) {
		constants.push(parseEnumConstant());
		while (parseOptional(SyntaxKind.CommaToken)) {
			if (!isStartOfEnumConstant()) break; // trailing comma
			constants.push(parseEnumConstant());
		}
	}
	const enumConstants = createNodeArray(constants, constantsPos);

	let members: NodeArray<Node>;
	if (parseOptional(SyntaxKind.SemicolonToken)) {
		members = parseList(ParsingContext.ClassMembers, parseClassMember);
	} else {
		members = createNodeArray<Node>([], getNodePos());
	}
	parseExpected(SyntaxKind.CloseBraceToken);
	return { enumConstants, members };
}

function parseClassDeclaration(pos: number, modifiers: NodeArray<ModifierLike> | undefined): ClassDeclaration {
	parseExpected(SyntaxKind.ClassKeyword);
	const name = parseIdentifier();
	const typeParameters = parseTypeParameters();
	const extendsType = parseOptional(SyntaxKind.ExtendsKeyword) ? parseType() : undefined;
	const implementsTypes = parseOptional(SyntaxKind.ImplementsKeyword) ? parseTypeList() : undefined;
	const members = parseClassBody();
	const node = createNode<ClassDeclaration>(SyntaxKind.ClassDeclaration, pos);
	node.modifiers = modifiers;
	node.name = name;
	node.typeParameters = typeParameters;
	node.extendsType = extendsType;
	node.implementsTypes = implementsTypes;
	node.members = members;
	return finishNode(node, pos);
}

function parseInterfaceDeclaration(
	pos: number,
	modifiers: NodeArray<ModifierLike> | undefined,
): InterfaceDeclaration {
	parseExpected(SyntaxKind.InterfaceKeyword);
	const name = parseIdentifier();
	const typeParameters = parseTypeParameters();
	const extendsTypes = parseOptional(SyntaxKind.ExtendsKeyword) ? parseTypeList() : undefined;
	const members = parseClassBody();
	const node = createNode<InterfaceDeclaration>(SyntaxKind.InterfaceDeclaration, pos);
	node.modifiers = modifiers;
	node.name = name;
	node.typeParameters = typeParameters;
	node.extendsTypes = extendsTypes;
	node.members = members;
	return finishNode(node, pos);
}

function parseEnumDeclaration(pos: number, modifiers: NodeArray<ModifierLike> | undefined): EnumDeclaration {
	parseExpected(SyntaxKind.EnumKeyword);
	const name = parseIdentifier();
	const implementsTypes = parseOptional(SyntaxKind.ImplementsKeyword) ? parseTypeList() : undefined;
	const { enumConstants, members } = parseEnumBody();
	const node = createNode<EnumDeclaration>(SyntaxKind.EnumDeclaration, pos);
	node.modifiers = modifiers;
	node.name = name;
	node.implementsTypes = implementsTypes;
	node.enumConstants = enumConstants;
	node.members = members;
	return finishNode(node, pos);
}

function parseAnnotationTypeDeclaration(
	pos: number,
	modifiers: NodeArray<ModifierLike> | undefined,
): AnnotationTypeDeclaration {
	parseExpected(SyntaxKind.AtToken);
	parseExpected(SyntaxKind.InterfaceKeyword);
	const name = parseIdentifier();
	const members = parseClassBody();
	const node = createNode<AnnotationTypeDeclaration>(SyntaxKind.AnnotationTypeDeclaration, pos);
	node.modifiers = modifiers;
	node.name = name;
	node.members = members;
	return finishNode(node, pos);
}

function parseTypeDeclaration(): Statement {
	const pos = getNodePos();
	const modifiers = parseModifiers();
	switch (token()) {
		case SyntaxKind.ClassKeyword:
			return parseClassDeclaration(pos, modifiers);
		case SyntaxKind.InterfaceKeyword:
			return parseInterfaceDeclaration(pos, modifiers);
		case SyntaxKind.EnumKeyword:
			return parseEnumDeclaration(pos, modifiers);
		case SyntaxKind.AtToken:
			return parseAnnotationTypeDeclaration(pos, modifiers);
		default: {
			parseErrorAtCurrentToken(Diagnostics.Declaration_expected);
			if (token() !== SyntaxKind.EndOfFileToken) {
				nextToken();
			}
			return createMissingNode<Statement>(SyntaxKind.ClassDeclaration, /*reportAtCurrentPosition*/ false);
		}
	}
}

// Compilation unit pieces

function parsePackageDeclaration(): PackageDeclaration {
	const pos = getNodePos();
	parseExpected(SyntaxKind.PackageKeyword);
	const name = parseEntityName();
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<PackageDeclaration>(SyntaxKind.PackageDeclaration, pos);
	node.name = name;
	node.annotations = undefined;
	return finishNode(node, pos);
}

function parseImportDeclaration(): ImportDeclaration {
	const pos = getNodePos();
	parseExpected(SyntaxKind.ImportKeyword);
	const isStatic = parseOptional(SyntaxKind.StaticKeyword);
	let name: EntityName = parseIdentifier();
	let isOnDemand = false;
	while (parseOptional(SyntaxKind.DotToken)) {
		if (token() === SyntaxKind.AsteriskToken) {
			nextToken();
			isOnDemand = true;
			break;
		}
		name = makeQualifiedName(name, parseIdentifier());
	}
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<ImportDeclaration>(SyntaxKind.ImportDeclaration, pos);
	node.isStatic = isStatic;
	node.name = name;
	node.isOnDemand = isOnDemand;
	return finishNode(node, pos);
}

function parseImportDeclarations(): NodeArray<ImportDeclaration> {
	const pos = getNodePos();
	const list: ImportDeclaration[] = [];
	while (token() === SyntaxKind.ImportKeyword) {
		list.push(parseImportDeclaration());
	}
	return createNodeArray(list, pos);
}

// Statements (M4: empty statement + type declarations)

function parseEmptyStatement(): EmptyStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.SemicolonToken);
	return finishNode(createNode<EmptyStatement>(SyntaxKind.EmptyStatement, pos), pos);
}

function parseStatement(): Statement {
	if (token() === SyntaxKind.SemicolonToken) {
		return parseEmptyStatement();
	}
	return parseTypeDeclaration();
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
	const packageDeclaration = token() === SyntaxKind.PackageKeyword ? parsePackageDeclaration() : undefined;
	const imports = parseImportDeclarations();
	const statements = parseList(ParsingContext.SourceElements, parseStatement);
	const endOfFileToken = parseExpectedToken<Token<SyntaxKind.EndOfFileToken>>(SyntaxKind.EndOfFileToken);

	const node = createNode<SourceFile>(SyntaxKind.SourceFile, pos);
	node.packageDeclaration = packageDeclaration;
	node.imports = imports;
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
			return (
				visitNode(cbNode, sf.packageDeclaration) ||
				visitNodes(cbNode, cbNodes, sf.imports) ||
				visitNodes(cbNode, cbNodes, sf.statements) ||
				visitNode(cbNode, sf.endOfFileToken)
			);
		}
		case SyntaxKind.PackageDeclaration: {
			const n = node as PackageDeclaration;
			return visitNodes(cbNode, cbNodes, n.annotations) || visitNode(cbNode, n.name);
		}
		case SyntaxKind.ImportDeclaration:
			return visitNode(cbNode, (node as ImportDeclaration).name);
		case SyntaxKind.QualifiedName: {
			const n = node as QualifiedName;
			return visitNode(cbNode, n.left) || visitNode(cbNode, n.right);
		}
		case SyntaxKind.ClassDeclaration: {
			const n = node as ClassDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.name) ||
				visitNodes(cbNode, cbNodes, n.typeParameters) ||
				visitNode(cbNode, n.extendsType) ||
				visitNodes(cbNode, cbNodes, n.implementsTypes) ||
				visitNodes(cbNode, cbNodes, n.members)
			);
		}
		case SyntaxKind.InterfaceDeclaration: {
			const n = node as InterfaceDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.name) ||
				visitNodes(cbNode, cbNodes, n.typeParameters) ||
				visitNodes(cbNode, cbNodes, n.extendsTypes) ||
				visitNodes(cbNode, cbNodes, n.members)
			);
		}
		case SyntaxKind.EnumDeclaration: {
			const n = node as EnumDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.name) ||
				visitNodes(cbNode, cbNodes, n.implementsTypes) ||
				visitNodes(cbNode, cbNodes, n.enumConstants) ||
				visitNodes(cbNode, cbNodes, n.members)
			);
		}
		case SyntaxKind.FieldDeclaration: {
			const n = node as FieldDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.type) ||
				visitNodes(cbNode, cbNodes, n.declarators)
			);
		}
		case SyntaxKind.VariableDeclarator: {
			const n = node as VariableDeclarator;
			return visitNode(cbNode, n.name) || visitNode(cbNode, n.initializer);
		}
		case SyntaxKind.MethodDeclaration: {
			const n = node as MethodDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNodes(cbNode, cbNodes, n.typeParameters) ||
				visitNode(cbNode, n.returnType) ||
				visitNode(cbNode, n.name) ||
				visitNodes(cbNode, cbNodes, n.parameters) ||
				visitNodes(cbNode, cbNodes, n.throws) ||
				visitNode(cbNode, n.body)
			);
		}
		case SyntaxKind.ConstructorDeclaration: {
			const n = node as ConstructorDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNodes(cbNode, cbNodes, n.typeParameters) ||
				visitNode(cbNode, n.name) ||
				visitNodes(cbNode, cbNodes, n.parameters) ||
				visitNodes(cbNode, cbNodes, n.throws) ||
				visitNode(cbNode, n.body)
			);
		}
		case SyntaxKind.InitializerBlock:
			return visitNode(cbNode, (node as InitializerBlock).body);
		case SyntaxKind.Parameter: {
			const n = node as Parameter;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.type) ||
				visitNode(cbNode, n.name)
			);
		}
		case SyntaxKind.EnumConstantDeclaration: {
			const n = node as EnumConstantDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.name) ||
				visitNodes(cbNode, cbNodes, n.arguments) ||
				visitNodes(cbNode, cbNodes, n.classBody)
			);
		}
		case SyntaxKind.Block:
			return visitNodes(cbNode, cbNodes, (node as Block).statements);
		case SyntaxKind.AnnotationTypeDeclaration: {
			const n = node as AnnotationTypeDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.name) ||
				visitNodes(cbNode, cbNodes, n.members)
			);
		}
		case SyntaxKind.TypeReference: {
			const n = node as TypeReference;
			return visitNode(cbNode, n.typeName) || visitNodes(cbNode, cbNodes, n.typeArguments);
		}
		case SyntaxKind.ArrayType:
			return visitNode(cbNode, (node as ArrayType).elementType);
		case SyntaxKind.WildcardType:
			return visitNode(cbNode, (node as WildcardType).type);
		case SyntaxKind.TypeParameter: {
			const n = node as TypeParameter;
			return visitNode(cbNode, n.name) || visitNodes(cbNode, cbNodes, n.constraint);
		}
		case SyntaxKind.Annotation: {
			const n = node as Annotation;
			return visitNode(cbNode, n.typeName) || visitNodes(cbNode, cbNodes, n.args);
		}
		default:
			// Tokens and childless nodes (EmptyStatement, Identifier, PrimitiveType).
			return undefined;
	}
}
