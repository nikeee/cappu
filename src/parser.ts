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
import { isAssignmentOperator, isModifierKeyword, isPrimitiveTypeKeyword, tokenToString } from "./utilities.ts";
import {
	type Annotation,
	type AnnotationTypeDeclaration,
	type ArrayCreationExpression,
	type ArrayInitializer,
	type ArrayType,
	type AssertStatement,
	type AssignmentExpression,
	type BinaryExpression,
	type Block,
	type BreakStatement,
	type CastExpression,
	type CatchClause,
	type ClassDeclaration,
	type ClassLiteralExpression,
	type ConditionalExpression,
	type ConstructorDeclaration,
	type ContinueStatement,
	type Diagnostic,
	type DiagnosticMessage,
	type DoStatement,
	type ElementAccessExpression,
	type EmptyStatement,
	type EntityName,
	type EnumConstantDeclaration,
	type EnumDeclaration,
	type Expression,
	type ExpressionStatement,
	type FieldDeclaration,
	type ForEachStatement,
	type ForStatement,
	type Identifier,
	type IfStatement,
	type ImportDeclaration,
	type InitializerBlock,
	type InstanceofExpression,
	type InterfaceDeclaration,
	type LabeledStatement,
	type LambdaExpression,
	type LiteralExpression,
	type MethodReferenceExpression,
	type ModuleDeclaration,
	type ExportsDirective,
	type OpensDirective,
	type ProvidesDirective,
	type RequiresDirective,
	type UsesDirective,
	type LocalVariableDeclarationStatement,
	type MethodDeclaration,
	type ModifierLike,
	type Node,
	type NodeArray,
	NodeFlags,
	type ObjectCreationExpression,
	type PackageDeclaration,
	type Parameter,
	type ParenthesizedExpression,
	type PostfixUnaryExpression,
	type PrefixUnaryExpression,
	type PrimitiveType,
	type PropertyAccessExpression,
	type QualifiedName,
	type Resource,
	type ReturnStatement,
	type CallExpression,
	type Scanner,
	type SourceFile,
	type Statement,
	type SuperExpression,
	type SwitchClause,
	type SwitchStatement,
	type SynchronizedStatement,
	SyntaxKind,
	type ThisExpression,
	type ThrowStatement,
	type Token,
	type TryStatement,
	type TypeNode,
	type TypeParameter,
	type TypeReference,
	type VariableDeclarator,
	type WhileStatement,
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
	ModuleDirectives,
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
		case ParsingContext.ModuleDirectives:
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
		token() === SyntaxKind.QuestionToken ||
		token() === SyntaxKind.AtToken // type-use annotation (SE8)
	);
}

function isListElement(context: ParsingContext, _inErrorRecovery: boolean): boolean {
	switch (context) {
		case ParsingContext.SourceElements:
			return isStartOfStatement();
		case ParsingContext.BlockStatements:
			return isStartOfStatementToken();
		case ParsingContext.TypeArguments:
			return isStartOfType();
		case ParsingContext.TypeParameters:
			return token() === SyntaxKind.Identifier || token() === SyntaxKind.AtToken;
		case ParsingContext.ClassMembers:
			return isStartOfClassMember();
		case ParsingContext.Parameters:
			return isStartOfParameter();
		case ParsingContext.ArgumentExpressions:
		case ParsingContext.ArrayInitializerElements:
			return token() === SyntaxKind.OpenBraceToken || isStartOfExpression();
		case ParsingContext.SwitchClauses:
			return token() === SyntaxKind.CaseKeyword || token() === SyntaxKind.DefaultKeyword;
		case ParsingContext.ModuleDirectives:
			return isModuleDirectiveStart();
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
	// SE8 type-use annotations (JSR 308), e.g. @NonNull String. Consumed but not
	// yet attached to the type node.
	while (token() === SyntaxKind.AtToken && !isAnnotationTypeDeclarationStart()) {
		parseAnnotation();
	}
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
		initializer = parseVariableInitializer();
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
		body = parseBlock();
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
	const body = parseBlock();
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
	const body = parseBlock();
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

// Contextual keywords (var, yield, module, requires, ...) are scanned as
// identifiers; these helpers recognize them by text.
function isContextualKeyword(text: string): boolean {
	return token() === SyntaxKind.Identifier && scanner.getTokenValue() === text;
}

function parseContextualKeyword(text: string): boolean {
	if (isContextualKeyword(text)) {
		nextToken();
		return true;
	}
	return false;
}

// Module declarations (SE9, module-info.java)

function parseModuleName(): EntityName {
	return parseEntityName();
}

function parseRequiresDirective(): RequiresDirective {
	const pos = getNodePos();
	parseContextualKeyword("requires");
	let isTransitive = false;
	let isStatic = false;
	// 'static' is a real keyword, 'transitive' is contextual; either order.
	while (true) {
		if (token() === SyntaxKind.StaticKeyword) {
			isStatic = true;
			nextToken();
		} else if (isContextualKeyword("transitive")) {
			isTransitive = true;
			nextToken();
		} else {
			break;
		}
	}
	const name = parseModuleName();
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<RequiresDirective>(SyntaxKind.RequiresDirective, pos);
	node.isTransitive = isTransitive;
	node.isStatic = isStatic;
	node.name = name;
	return finishNode(node, pos);
}

function parseToModuleList(): NodeArray<EntityName> {
	const pos = getNodePos();
	const list: EntityName[] = [parseModuleName()];
	while (parseOptional(SyntaxKind.CommaToken)) {
		list.push(parseModuleName());
	}
	return createNodeArray(list, pos);
}

function parseExportsOrOpensDirective(
	keyword: "exports" | "opens",
	kind: SyntaxKind.ExportsDirective | SyntaxKind.OpensDirective,
): ExportsDirective | OpensDirective {
	const pos = getNodePos();
	parseContextualKeyword(keyword);
	const packageName = parseEntityName();
	const toModules = parseContextualKeyword("to") ? parseToModuleList() : undefined;
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<ExportsDirective | OpensDirective>(kind, pos);
	node.packageName = packageName;
	node.toModules = toModules;
	return finishNode(node, pos);
}

function parseUsesDirective(): UsesDirective {
	const pos = getNodePos();
	parseContextualKeyword("uses");
	const typeName = parseEntityName();
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<UsesDirective>(SyntaxKind.UsesDirective, pos);
	node.typeName = typeName;
	return finishNode(node, pos);
}

function parseProvidesDirective(): ProvidesDirective {
	const pos = getNodePos();
	parseContextualKeyword("provides");
	const typeName = parseEntityName();
	parseContextualKeyword("with");
	const withPos = getNodePos();
	const withTypes: EntityName[] = [parseEntityName()];
	while (parseOptional(SyntaxKind.CommaToken)) {
		withTypes.push(parseEntityName());
	}
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<ProvidesDirective>(SyntaxKind.ProvidesDirective, pos);
	node.typeName = typeName;
	node.withTypes = createNodeArray(withTypes, withPos);
	return finishNode(node, pos);
}

function isModuleDirectiveStart(): boolean {
	return (
		isContextualKeyword("requires") ||
		isContextualKeyword("exports") ||
		isContextualKeyword("opens") ||
		isContextualKeyword("uses") ||
		isContextualKeyword("provides")
	);
}

function parseModuleDirective(): Node {
	if (isContextualKeyword("requires")) return parseRequiresDirective();
	if (isContextualKeyword("exports")) return parseExportsOrOpensDirective("exports", SyntaxKind.ExportsDirective);
	if (isContextualKeyword("opens")) return parseExportsOrOpensDirective("opens", SyntaxKind.OpensDirective);
	if (isContextualKeyword("uses")) return parseUsesDirective();
	return parseProvidesDirective();
}

function parseModuleDeclaration(): ModuleDeclaration {
	const pos = getNodePos();
	const annotations = parseAnnotations();
	const isOpen = parseContextualKeyword("open");
	parseContextualKeyword("module");
	const name = parseModuleName();
	parseExpected(SyntaxKind.OpenBraceToken);
	const directives = parseList(ParsingContext.ModuleDirectives, parseModuleDirective);
	parseExpected(SyntaxKind.CloseBraceToken);
	const node = createNode<ModuleDeclaration>(SyntaxKind.ModuleDeclaration, pos);
	node.annotations = annotations;
	node.isOpen = isOpen;
	node.name = name;
	node.directives = directives;
	return finishNode(node, pos);
}

// Top-level elements: an empty statement or a type declaration.
function parseSourceElement(): Statement {
	if (token() === SyntaxKind.SemicolonToken) {
		return parseEmptyStatement();
	}
	return parseTypeDeclaration();
}

function parseEmptyStatement(): EmptyStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.SemicolonToken);
	return finishNode(createNode<EmptyStatement>(SyntaxKind.EmptyStatement, pos), pos);
}

// Expressions

function isStartOfExpression(): boolean {
	switch (token()) {
		case SyntaxKind.NumericLiteral:
		case SyntaxKind.StringLiteral:
		case SyntaxKind.CharacterLiteral:
		case SyntaxKind.TextBlockLiteral:
		case SyntaxKind.TrueKeyword:
		case SyntaxKind.FalseKeyword:
		case SyntaxKind.NullKeyword:
		case SyntaxKind.ThisKeyword:
		case SyntaxKind.SuperKeyword:
		case SyntaxKind.NewKeyword:
		case SyntaxKind.Identifier:
		case SyntaxKind.OpenParenToken:
		case SyntaxKind.ExclamationToken:
		case SyntaxKind.TildeToken:
		case SyntaxKind.PlusToken:
		case SyntaxKind.MinusToken:
		case SyntaxKind.PlusPlusToken:
		case SyntaxKind.MinusMinusToken:
			return true;
		default:
			return isPrimitiveTypeKeyword(token()) || token() === SyntaxKind.VoidKeyword;
	}
}

function reScanGreaterIfNeeded(): void {
	if (token() === SyntaxKind.GreaterThanToken) {
		currentToken = scanner.reScanGreaterToken();
	}
}

function getBinaryOperatorPrecedence(kind: SyntaxKind): number {
	switch (kind) {
		case SyntaxKind.BarBarToken:
			return 1;
		case SyntaxKind.AmpersandAmpersandToken:
			return 2;
		case SyntaxKind.BarToken:
			return 3;
		case SyntaxKind.CaretToken:
			return 4;
		case SyntaxKind.AmpersandToken:
			return 5;
		case SyntaxKind.EqualsEqualsToken:
		case SyntaxKind.ExclamationEqualsToken:
			return 6;
		case SyntaxKind.LessThanToken:
		case SyntaxKind.GreaterThanToken:
		case SyntaxKind.LessThanEqualsToken:
		case SyntaxKind.GreaterThanEqualsToken:
			return 7;
		case SyntaxKind.LessThanLessThanToken:
		case SyntaxKind.GreaterThanGreaterThanToken:
		case SyntaxKind.GreaterThanGreaterThanGreaterThanToken:
			return 8;
		case SyntaxKind.PlusToken:
		case SyntaxKind.MinusToken:
			return 9;
		case SyntaxKind.AsteriskToken:
		case SyntaxKind.SlashToken:
		case SyntaxKind.PercentToken:
			return 10;
		default:
			return 0;
	}
}

const RELATIONAL_PRECEDENCE = 7;

function parseExpression(): Expression {
	return parseAssignmentExpression();
}

// SE8 lambda detection: "x ->" (concise) or "( ... ) ->".
function isLambdaStart(): boolean {
	if (token() === SyntaxKind.Identifier) {
		return lookAhead(() => (nextToken(), token() === SyntaxKind.ArrowToken));
	}
	if (token() === SyntaxKind.OpenParenToken) {
		return lookAhead(() => {
			nextToken(); // '('
			let depth = 1;
			while (depth > 0 && token() !== SyntaxKind.EndOfFileToken) {
				if (token() === SyntaxKind.OpenParenToken) depth++;
				else if (token() === SyntaxKind.CloseParenToken) depth--;
				nextToken();
			}
			return token() === SyntaxKind.ArrowToken;
		});
	}
	return false;
}

function parseLambdaParameter(): Node {
	// Inferred parameter: a bare identifier followed by ',' or ')'.
	if (
		token() === SyntaxKind.Identifier &&
		lookAhead(() => (nextToken(), token() === SyntaxKind.CommaToken || token() === SyntaxKind.CloseParenToken))
	) {
		return parseIdentifier();
	}
	return parseParameter();
}

function parseLambdaExpression(): LambdaExpression {
	const pos = getNodePos();
	let parameters: NodeArray<Node>;
	if (token() === SyntaxKind.OpenParenToken) {
		parseExpected(SyntaxKind.OpenParenToken);
		parameters = parseDelimitedList(ParsingContext.Parameters, parseLambdaParameter);
		parseExpected(SyntaxKind.CloseParenToken);
	} else {
		const paramsPos = getNodePos();
		parameters = createNodeArray<Node>([parseIdentifier()], paramsPos);
	}
	parseExpected(SyntaxKind.ArrowToken);
	const body = token() === SyntaxKind.OpenBraceToken ? parseBlock() : parseExpression();
	const node = createNode<LambdaExpression>(SyntaxKind.LambdaExpression, pos);
	node.parameters = parameters;
	node.body = body;
	return finishNode(node, pos);
}

function parseAssignmentExpression(): Expression {
	if (isLambdaStart()) {
		return parseLambdaExpression();
	}
	const pos = getNodePos();
	const left = parseConditionalExpression();
	reScanGreaterIfNeeded();
	if (isAssignmentOperator(token())) {
		const operatorToken = token();
		nextToken();
		const right = parseAssignmentExpression();
		const node = createNode<AssignmentExpression>(SyntaxKind.AssignmentExpression, pos);
		node.left = left;
		node.operatorToken = operatorToken;
		node.right = right;
		return finishNode(node, pos);
	}
	return left;
}

function parseConditionalExpression(): Expression {
	const pos = getNodePos();
	const condition = parseBinaryExpression(1);
	if (parseOptional(SyntaxKind.QuestionToken)) {
		const whenTrue = parseAssignmentExpression();
		parseExpected(SyntaxKind.ColonToken);
		const whenFalse = parseAssignmentExpression();
		const node = createNode<ConditionalExpression>(SyntaxKind.ConditionalExpression, pos);
		node.condition = condition;
		node.whenTrue = whenTrue;
		node.whenFalse = whenFalse;
		return finishNode(node, pos);
	}
	return condition;
}

function parseBinaryExpression(minPrecedence: number): Expression {
	const pos = getNodePos();
	const left = parseUnaryExpression();
	return parseBinaryExpressionRest(left, minPrecedence, pos);
}

function parseBinaryExpressionRest(leftStart: Expression, minPrecedence: number, pos: number): Expression {
	let left = leftStart;
	while (true) {
		if (token() === SyntaxKind.InstanceofKeyword) {
			if (RELATIONAL_PRECEDENCE < minPrecedence) break;
			nextToken();
			const type = parseType();
			const node = createNode<InstanceofExpression>(SyntaxKind.InstanceofExpression, pos);
			node.expression = left;
			node.type = type;
			left = finishNode(node, pos);
			continue;
		}
		reScanGreaterIfNeeded();
		const precedence = getBinaryOperatorPrecedence(token());
		if (precedence === 0 || precedence < minPrecedence) break;
		const operatorToken = token();
		nextToken();
		const right = parseBinaryExpression(precedence + 1);
		const node = createNode<BinaryExpression>(SyntaxKind.BinaryExpression, pos);
		node.left = left;
		node.operatorToken = operatorToken;
		node.right = right;
		left = finishNode(node, pos);
	}
	return left;
}

function parseUnaryExpression(): Expression {
	const t = token();
	switch (t) {
		case SyntaxKind.PlusToken:
		case SyntaxKind.MinusToken:
		case SyntaxKind.TildeToken:
		case SyntaxKind.ExclamationToken:
		case SyntaxKind.PlusPlusToken:
		case SyntaxKind.MinusMinusToken: {
			const pos = getNodePos();
			nextToken();
			const operand = parseUnaryExpression();
			const node = createNode<PrefixUnaryExpression>(SyntaxKind.PrefixUnaryExpression, pos);
			node.operator = t;
			node.operand = operand;
			return finishNode(node, pos);
		}
	}
	if (t === SyntaxKind.OpenParenToken && isCastExpression()) {
		return parseCastExpression();
	}
	return parsePostfixExpression();
}

// (Type) operand. Distinguished from a parenthesized expression by lookahead.
function isCastExpression(): boolean {
	return lookAhead(() => {
		nextToken(); // '('
		if (token() === SyntaxKind.CloseParenToken) return false;
		const primitive = isPrimitiveTypeKeyword(token()) || token() === SyntaxKind.VoidKeyword;
		parseType();
		if (token() !== SyntaxKind.CloseParenToken) return false;
		nextToken(); // ')'
		// A primitive cast (int)x is unambiguous. A reference cast must be
		// followed by a token that begins a unary expression but is not a binary
		// operator prefix (+, -, ++, --), so "(a) - b" stays a subtraction.
		return primitive ? isStartOfExpression() : isStartOfReferenceCastOperand();
	});
}

function isStartOfReferenceCastOperand(): boolean {
	switch (token()) {
		case SyntaxKind.Identifier:
		case SyntaxKind.NumericLiteral:
		case SyntaxKind.StringLiteral:
		case SyntaxKind.CharacterLiteral:
		case SyntaxKind.TextBlockLiteral:
		case SyntaxKind.TrueKeyword:
		case SyntaxKind.FalseKeyword:
		case SyntaxKind.NullKeyword:
		case SyntaxKind.OpenParenToken:
		case SyntaxKind.ThisKeyword:
		case SyntaxKind.SuperKeyword:
		case SyntaxKind.NewKeyword:
		case SyntaxKind.ExclamationToken:
		case SyntaxKind.TildeToken:
			return true;
		default:
			return false;
	}
}

function parseCastExpression(): CastExpression {
	const pos = getNodePos();
	parseExpected(SyntaxKind.OpenParenToken);
	const type = parseType();
	parseExpected(SyntaxKind.CloseParenToken);
	const expression = parseUnaryExpression();
	const node = createNode<CastExpression>(SyntaxKind.CastExpression, pos);
	node.type = type;
	node.expression = expression;
	return finishNode(node, pos);
}

function parsePostfixExpression(): Expression {
	const expr = parsePrimaryExpression();
	if (token() === SyntaxKind.PlusPlusToken || token() === SyntaxKind.MinusMinusToken) {
		const operator = token();
		nextToken();
		const node = createNode<PostfixUnaryExpression>(SyntaxKind.PostfixUnaryExpression, expr.pos);
		node.operand = expr;
		node.operator = operator;
		return finishNode(node, expr.pos);
	}
	return expr;
}

function parsePrimaryExpression(): Expression {
	return parseExpressionSuffixes(parseAtom());
}

function parseAtom(): Expression {
	const pos = getNodePos();
	switch (token()) {
		case SyntaxKind.NumericLiteral:
		case SyntaxKind.StringLiteral:
		case SyntaxKind.CharacterLiteral:
		case SyntaxKind.TextBlockLiteral: {
			const kind = token();
			const value = scanner.getTokenValue();
			nextToken();
			const node = createNode<LiteralExpression>(kind, pos);
			node.value = value;
			return finishNode(node, pos);
		}
		case SyntaxKind.TrueKeyword:
		case SyntaxKind.FalseKeyword:
		case SyntaxKind.NullKeyword:
			return parseTokenNode();
		case SyntaxKind.ThisKeyword:
			nextToken();
			return finishNode(createNode<ThisExpression>(SyntaxKind.ThisExpression, pos), pos);
		case SyntaxKind.SuperKeyword:
			nextToken();
			return finishNode(createNode<SuperExpression>(SyntaxKind.SuperExpression, pos), pos);
		case SyntaxKind.OpenParenToken: {
			nextToken();
			const expression = parseExpression();
			parseExpected(SyntaxKind.CloseParenToken);
			const node = createNode<ParenthesizedExpression>(SyntaxKind.ParenthesizedExpression, pos);
			node.expression = expression;
			return finishNode(node, pos);
		}
		case SyntaxKind.NewKeyword:
			return parseNewExpression();
		case SyntaxKind.Identifier:
			return parseIdentifier();
		default:
			if (isPrimitiveTypeKeyword(token()) || token() === SyntaxKind.VoidKeyword) {
				// Primitive class literal: int.class, int[].class, void.class
				const type = parseType();
				parseExpected(SyntaxKind.DotToken);
				parseExpected(SyntaxKind.ClassKeyword);
				const node = createNode<ClassLiteralExpression>(SyntaxKind.ClassLiteralExpression, pos);
				node.type = type;
				return finishNode(node, pos);
			}
			return createMissingNode<Expression>(SyntaxKind.Identifier, /*reportAtCurrentPosition*/ false, Diagnostics.Expression_expected);
	}
}

function expressionToEntityName(expr: Expression): EntityName | undefined {
	if (expr.kind === SyntaxKind.Identifier) {
		return expr as Identifier;
	}
	if (expr.kind === SyntaxKind.PropertyAccessExpression) {
		const access = expr as PropertyAccessExpression;
		const left = expressionToEntityName(access.expression);
		if (left) return makeQualifiedName(left, access.name);
	}
	return undefined;
}

function makePropertyAccess(expr: Expression, name: Identifier): PropertyAccessExpression {
	const node = createNode<PropertyAccessExpression>(SyntaxKind.PropertyAccessExpression, expr.pos);
	node.expression = expr;
	node.name = name;
	return finishNode(node, expr.pos);
}

function parseArgumentList(): NodeArray<Expression> {
	parseExpected(SyntaxKind.OpenParenToken);
	const args = parseDelimitedList(ParsingContext.ArgumentExpressions, parseExpression);
	parseExpected(SyntaxKind.CloseParenToken);
	return args;
}

function parseExpressionSuffixes(start: Expression): Expression {
	let expr = start;
	while (true) {
		const exprPos = expr.pos;
		if (token() === SyntaxKind.DotToken) {
			nextToken();
			if (token() === SyntaxKind.ClassKeyword) {
				nextToken();
				const entity = expressionToEntityName(expr) ??
					createMissingNode<Identifier>(SyntaxKind.Identifier, false);
				const typeRef = createNode<TypeReference>(SyntaxKind.TypeReference, exprPos);
				typeRef.typeName = entity;
				typeRef.typeArguments = undefined;
				const typeNode = finishNode(typeRef, exprPos);
				const node = createNode<ClassLiteralExpression>(SyntaxKind.ClassLiteralExpression, exprPos);
				node.type = typeNode;
				expr = finishNode(node, exprPos);
				continue;
			}
			// Qualified this/super (Outer.this) are consumed but not modeled yet.
			if (token() === SyntaxKind.ThisKeyword || token() === SyntaxKind.SuperKeyword) {
				nextToken();
				continue;
			}
			const typeArguments = token() === SyntaxKind.LessThanToken ? parseTypeArguments() : undefined;
			const name = parseIdentifier();
			if (token() === SyntaxKind.OpenParenToken) {
				const target = makePropertyAccess(expr, name);
				const args = parseArgumentList();
				const call = createNode<CallExpression>(SyntaxKind.CallExpression, exprPos);
				call.expression = target;
				call.typeArguments = typeArguments;
				call.arguments = args;
				expr = finishNode(call, exprPos);
			} else {
				expr = makePropertyAccess(expr, name);
			}
			continue;
		}
		if (token() === SyntaxKind.OpenBracketToken) {
			nextToken();
			const argumentExpression = parseExpression();
			parseExpected(SyntaxKind.CloseBracketToken);
			const node = createNode<ElementAccessExpression>(SyntaxKind.ElementAccessExpression, exprPos);
			node.expression = expr;
			node.argumentExpression = argumentExpression;
			expr = finishNode(node, exprPos);
			continue;
		}
		if (token() === SyntaxKind.OpenParenToken) {
			const args = parseArgumentList();
			const node = createNode<CallExpression>(SyntaxKind.CallExpression, exprPos);
			node.expression = expr;
			node.typeArguments = undefined;
			node.arguments = args;
			expr = finishNode(node, exprPos);
			continue;
		}
		if (token() === SyntaxKind.ColonColonToken) {
			// SE8 method reference: expr :: [typeArgs] (Identifier | new)
			nextToken();
			const typeArguments = token() === SyntaxKind.LessThanToken ? parseTypeArguments() : undefined;
			const node = createNode<MethodReferenceExpression>(SyntaxKind.MethodReferenceExpression, exprPos);
			node.expression = expr;
			node.typeArguments = typeArguments;
			if (parseOptional(SyntaxKind.NewKeyword)) {
				node.isConstructorRef = true;
				node.name = undefined;
			} else {
				node.isConstructorRef = false;
				node.name = parseIdentifier();
			}
			expr = finishNode(node, exprPos);
			continue;
		}
		break;
	}
	return expr;
}

function parseNewExpression(): Expression {
	const pos = getNodePos();
	parseExpected(SyntaxKind.NewKeyword);
	const type = parseNonArrayType();
	if (token() === SyntaxKind.OpenBracketToken) {
		return parseArrayCreationRest(pos, type);
	}
	const args = parseArgumentList();
	const classBody = token() === SyntaxKind.OpenBraceToken ? parseClassBody() : undefined;
	const node = createNode<ObjectCreationExpression>(SyntaxKind.ObjectCreationExpression, pos);
	node.type = type;
	node.arguments = args;
	node.classBody = classBody;
	return finishNode(node, pos);
}

function parseArrayCreationRest(pos: number, elementType: TypeNode): ArrayCreationExpression {
	const dims: Expression[] = [];
	const dimsPos = getNodePos();
	let additionalRank = 0;
	while (token() === SyntaxKind.OpenBracketToken) {
		nextToken();
		if (token() === SyntaxKind.CloseBracketToken) {
			additionalRank++;
			nextToken();
		} else {
			dims.push(parseExpression());
			parseExpected(SyntaxKind.CloseBracketToken);
		}
	}
	const initializer = token() === SyntaxKind.OpenBraceToken ? parseArrayInitializer() : undefined;
	const node = createNode<ArrayCreationExpression>(SyntaxKind.ArrayCreationExpression, pos);
	node.elementType = elementType;
	node.dimensions = createNodeArray(dims, dimsPos);
	node.additionalRank = additionalRank;
	node.initializer = initializer;
	return finishNode(node, pos);
}

function parseArrayInitializerElement(): Expression {
	return token() === SyntaxKind.OpenBraceToken ? parseArrayInitializer() : parseExpression();
}

function parseArrayInitializer(): ArrayInitializer {
	const pos = getNodePos();
	parseExpected(SyntaxKind.OpenBraceToken);
	const elements = parseDelimitedList(ParsingContext.ArrayInitializerElements, parseArrayInitializerElement);
	parseExpected(SyntaxKind.CloseBraceToken);
	const node = createNode<ArrayInitializer>(SyntaxKind.ArrayInitializer, pos);
	node.elements = elements;
	return finishNode(node, pos);
}

function parseVariableInitializer(): Expression {
	return token() === SyntaxKind.OpenBraceToken ? parseArrayInitializer() : parseExpression();
}

// Statements

function parseBlock(): Block {
	const pos = getNodePos();
	parseExpected(SyntaxKind.OpenBraceToken);
	const statements = parseList(ParsingContext.BlockStatements, parseStatement);
	parseExpected(SyntaxKind.CloseBraceToken);
	const node = createNode<Block>(SyntaxKind.Block, pos);
	node.statements = statements;
	return finishNode(node, pos);
}

function isStartOfStatementToken(): boolean {
	switch (token()) {
		case SyntaxKind.SemicolonToken:
		case SyntaxKind.OpenBraceToken:
		case SyntaxKind.IfKeyword:
		case SyntaxKind.WhileKeyword:
		case SyntaxKind.DoKeyword:
		case SyntaxKind.ForKeyword:
		case SyntaxKind.TryKeyword:
		case SyntaxKind.SwitchKeyword:
		case SyntaxKind.ReturnKeyword:
		case SyntaxKind.ThrowKeyword:
		case SyntaxKind.BreakKeyword:
		case SyntaxKind.ContinueKeyword:
		case SyntaxKind.SynchronizedKeyword:
		case SyntaxKind.AssertKeyword:
		case SyntaxKind.ClassKeyword:
		case SyntaxKind.InterfaceKeyword:
		case SyntaxKind.EnumKeyword:
		case SyntaxKind.AtToken:
		case SyntaxKind.FinalKeyword:
			return true;
		default:
			return isStartOfExpression();
	}
}

function isLocalVariableDeclarationStart(): boolean {
	return lookAhead(() => {
		parseType();
		return token() === SyntaxKind.Identifier;
	});
}

function parseStatement(): Statement {
	switch (token()) {
		case SyntaxKind.SemicolonToken:
			return parseEmptyStatement();
		case SyntaxKind.OpenBraceToken:
			return parseBlock();
		case SyntaxKind.IfKeyword:
			return parseIfStatement();
		case SyntaxKind.WhileKeyword:
			return parseWhileStatement();
		case SyntaxKind.DoKeyword:
			return parseDoStatement();
		case SyntaxKind.ForKeyword:
			return parseForStatement();
		case SyntaxKind.TryKeyword:
			return parseTryStatement();
		case SyntaxKind.SwitchKeyword:
			return parseSwitchStatement();
		case SyntaxKind.ReturnKeyword:
			return parseReturnStatement();
		case SyntaxKind.ThrowKeyword:
			return parseThrowStatement();
		case SyntaxKind.BreakKeyword:
			return parseBreakOrContinue(SyntaxKind.BreakStatement);
		case SyntaxKind.ContinueKeyword:
			return parseBreakOrContinue(SyntaxKind.ContinueStatement);
		case SyntaxKind.SynchronizedKeyword:
			return parseSynchronizedStatement();
		case SyntaxKind.AssertKeyword:
			return parseAssertStatement();
		case SyntaxKind.ClassKeyword:
		case SyntaxKind.InterfaceKeyword:
		case SyntaxKind.EnumKeyword:
			return parseTypeDeclaration();
	}
	if (token() === SyntaxKind.Identifier && lookAhead(() => (nextToken(), token() === SyntaxKind.ColonToken))) {
		return parseLabeledStatement();
	}
	if (isModifierKeyword(token()) || token() === SyntaxKind.AtToken || isLocalVariableDeclarationStart()) {
		return parseLocalDeclarationStatement();
	}
	const pos = getNodePos();
	const expression = parseExpression();
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<ExpressionStatement>(SyntaxKind.ExpressionStatement, pos);
	node.expression = expression;
	return finishNode(node, pos);
}

function parseLocalVariableDeclarationRest(
	pos: number,
	modifiers: NodeArray<ModifierLike> | undefined,
): LocalVariableDeclarationStatement {
	const type = parseType();
	const declaratorsPos = getNodePos();
	const declarators: VariableDeclarator[] = [parseVariableDeclarator(parseIdentifier())];
	while (parseOptional(SyntaxKind.CommaToken)) {
		declarators.push(parseVariableDeclarator(parseIdentifier()));
	}
	const node = createNode<LocalVariableDeclarationStatement>(SyntaxKind.LocalVariableDeclarationStatement, pos);
	node.modifiers = modifiers;
	node.type = type;
	node.declarators = createNodeArray(declarators, declaratorsPos);
	return finishNode(node, pos);
}

function parseLocalDeclarationStatement(): Statement {
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
	}
	const node = parseLocalVariableDeclarationRest(pos, modifiers);
	parseExpected(SyntaxKind.SemicolonToken);
	return node;
}

function parseIfStatement(): IfStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.IfKeyword);
	parseExpected(SyntaxKind.OpenParenToken);
	const condition = parseExpression();
	parseExpected(SyntaxKind.CloseParenToken);
	const thenStatement = parseStatement();
	const elseStatement = parseOptional(SyntaxKind.ElseKeyword) ? parseStatement() : undefined;
	const node = createNode<IfStatement>(SyntaxKind.IfStatement, pos);
	node.condition = condition;
	node.thenStatement = thenStatement;
	node.elseStatement = elseStatement;
	return finishNode(node, pos);
}

function parseWhileStatement(): WhileStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.WhileKeyword);
	parseExpected(SyntaxKind.OpenParenToken);
	const condition = parseExpression();
	parseExpected(SyntaxKind.CloseParenToken);
	const statement = parseStatement();
	const node = createNode<WhileStatement>(SyntaxKind.WhileStatement, pos);
	node.condition = condition;
	node.statement = statement;
	return finishNode(node, pos);
}

function parseDoStatement(): DoStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.DoKeyword);
	const statement = parseStatement();
	parseExpected(SyntaxKind.WhileKeyword);
	parseExpected(SyntaxKind.OpenParenToken);
	const condition = parseExpression();
	parseExpected(SyntaxKind.CloseParenToken);
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<DoStatement>(SyntaxKind.DoStatement, pos);
	node.statement = statement;
	node.condition = condition;
	return finishNode(node, pos);
}

function isForEachHeader(): boolean {
	return lookAhead(() => {
		parseModifiers();
		if (token() === SyntaxKind.SemicolonToken) return false;
		parseType();
		if (token() !== SyntaxKind.Identifier) return false;
		nextToken();
		return token() === SyntaxKind.ColonToken;
	});
}

function parseForStatement(): Statement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.ForKeyword);
	parseExpected(SyntaxKind.OpenParenToken);

	if (isForEachHeader()) {
		const parameter = parseParameter();
		parseExpected(SyntaxKind.ColonToken);
		const expression = parseExpression();
		parseExpected(SyntaxKind.CloseParenToken);
		const statement = parseStatement();
		const node = createNode<ForEachStatement>(SyntaxKind.ForEachStatement, pos);
		node.parameter = parameter;
		node.expression = expression;
		node.statement = statement;
		return finishNode(node, pos);
	}

	let initializer: Node | undefined;
	if (token() === SyntaxKind.SemicolonToken) {
		nextToken();
	} else {
		if (isModifierKeyword(token()) || token() === SyntaxKind.AtToken || isLocalVariableDeclarationStart()) {
			const initPos = getNodePos();
			initializer = parseLocalVariableDeclarationRest(initPos, parseModifiers());
		} else {
			initializer = parseExpression();
			// Additional comma-separated init expressions are parsed but only the
			// first is retained.
			while (parseOptional(SyntaxKind.CommaToken)) parseExpression();
		}
		parseExpected(SyntaxKind.SemicolonToken);
	}

	const condition = token() === SyntaxKind.SemicolonToken ? undefined : parseExpression();
	parseExpected(SyntaxKind.SemicolonToken);

	let incrementors: NodeArray<Expression> | undefined;
	if (token() !== SyntaxKind.CloseParenToken) {
		const incPos = getNodePos();
		const list: Expression[] = [parseExpression()];
		while (parseOptional(SyntaxKind.CommaToken)) list.push(parseExpression());
		incrementors = createNodeArray(list, incPos);
	}
	parseExpected(SyntaxKind.CloseParenToken);
	const statement = parseStatement();

	const node = createNode<ForStatement>(SyntaxKind.ForStatement, pos);
	node.initializer = initializer;
	node.condition = condition;
	node.incrementors = incrementors;
	node.statement = statement;
	return finishNode(node, pos);
}

function parseReturnStatement(): ReturnStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.ReturnKeyword);
	const expression = token() === SyntaxKind.SemicolonToken ? undefined : parseExpression();
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<ReturnStatement>(SyntaxKind.ReturnStatement, pos);
	node.expression = expression;
	return finishNode(node, pos);
}

function parseThrowStatement(): ThrowStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.ThrowKeyword);
	const expression = parseExpression();
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<ThrowStatement>(SyntaxKind.ThrowStatement, pos);
	node.expression = expression;
	return finishNode(node, pos);
}

function parseBreakOrContinue(kind: SyntaxKind.BreakStatement | SyntaxKind.ContinueStatement): Statement {
	const pos = getNodePos();
	nextToken(); // 'break' / 'continue'
	const label = token() === SyntaxKind.Identifier ? parseIdentifier() : undefined;
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<BreakStatement | ContinueStatement>(kind, pos);
	node.label = label;
	return finishNode(node, pos);
}

function parseSynchronizedStatement(): SynchronizedStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.SynchronizedKeyword);
	parseExpected(SyntaxKind.OpenParenToken);
	const expression = parseExpression();
	parseExpected(SyntaxKind.CloseParenToken);
	const body = parseBlock();
	const node = createNode<SynchronizedStatement>(SyntaxKind.SynchronizedStatement, pos);
	node.expression = expression;
	node.body = body;
	return finishNode(node, pos);
}

function parseAssertStatement(): AssertStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.AssertKeyword);
	const condition = parseExpression();
	const message = parseOptional(SyntaxKind.ColonToken) ? parseExpression() : undefined;
	parseExpected(SyntaxKind.SemicolonToken);
	const node = createNode<AssertStatement>(SyntaxKind.AssertStatement, pos);
	node.condition = condition;
	node.message = message;
	return finishNode(node, pos);
}

function parseLabeledStatement(): LabeledStatement {
	const pos = getNodePos();
	const label = parseIdentifier();
	parseExpected(SyntaxKind.ColonToken);
	const statement = parseStatement();
	const node = createNode<LabeledStatement>(SyntaxKind.LabeledStatement, pos);
	node.label = label;
	node.statement = statement;
	return finishNode(node, pos);
}

function parseResource(): Resource {
	const pos = getNodePos();
	const modifiers = parseModifiers();
	const type = parseType();
	const name = parseIdentifier();
	parseExpected(SyntaxKind.EqualsToken);
	const initializer = parseExpression();
	const node = createNode<Resource>(SyntaxKind.Resource, pos);
	node.modifiers = modifiers;
	node.type = type;
	node.name = name;
	node.initializer = initializer;
	return finishNode(node, pos);
}

function parseResourceSpecification(): NodeArray<Resource> {
	const pos = getNodePos();
	parseExpected(SyntaxKind.OpenParenToken);
	const resources: Resource[] = [parseResource()];
	while (parseOptional(SyntaxKind.SemicolonToken)) {
		if (token() === SyntaxKind.CloseParenToken) break; // trailing ';'
		resources.push(parseResource());
	}
	parseExpected(SyntaxKind.CloseParenToken);
	return createNodeArray(resources, pos);
}

function parseCatchClause(): CatchClause {
	const pos = getNodePos();
	parseExpected(SyntaxKind.CatchKeyword);
	parseExpected(SyntaxKind.OpenParenToken);
	parseModifiers(); // 'final' is allowed but not retained
	const typesPos = getNodePos();
	const catchTypes: TypeNode[] = [parseType()];
	while (parseOptional(SyntaxKind.BarToken)) {
		catchTypes.push(parseType());
	}
	const name = parseIdentifier();
	parseExpected(SyntaxKind.CloseParenToken);
	const block = parseBlock();
	const node = createNode<CatchClause>(SyntaxKind.CatchClause, pos);
	node.catchTypes = createNodeArray(catchTypes, typesPos);
	node.name = name;
	node.block = block;
	return finishNode(node, pos);
}

function parseTryStatement(): TryStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.TryKeyword);
	const resources = token() === SyntaxKind.OpenParenToken ? parseResourceSpecification() : undefined;
	const tryBlock = parseBlock();
	const catchPos = getNodePos();
	const catchClauses: CatchClause[] = [];
	while (token() === SyntaxKind.CatchKeyword) {
		catchClauses.push(parseCatchClause());
	}
	const finallyBlock = parseOptional(SyntaxKind.FinallyKeyword) ? parseBlock() : undefined;
	const node = createNode<TryStatement>(SyntaxKind.TryStatement, pos);
	node.resources = resources;
	node.tryBlock = tryBlock;
	node.catchClauses = createNodeArray(catchClauses, catchPos);
	node.finallyBlock = finallyBlock;
	return finishNode(node, pos);
}

function parseSwitchClause(): SwitchClause {
	const pos = getNodePos();
	let isDefault = false;
	let labelExpression: Expression | undefined;
	if (parseOptional(SyntaxKind.CaseKeyword)) {
		labelExpression = parseExpression();
	} else {
		parseExpected(SyntaxKind.DefaultKeyword);
		isDefault = true;
	}
	parseExpected(SyntaxKind.ColonToken);

	const statementsPos = getNodePos();
	const statements: Statement[] = [];
	while (
		token() !== SyntaxKind.CaseKeyword &&
		token() !== SyntaxKind.DefaultKeyword &&
		token() !== SyntaxKind.CloseBraceToken &&
		token() !== SyntaxKind.EndOfFileToken
	) {
		if (!isStartOfStatementToken()) break;
		statements.push(parseStatement());
	}
	const node = createNode<SwitchClause>(SyntaxKind.SwitchClause, pos);
	node.isDefault = isDefault;
	node.labelExpression = labelExpression;
	node.statements = createNodeArray(statements, statementsPos);
	return finishNode(node, pos);
}

function parseSwitchStatement(): SwitchStatement {
	const pos = getNodePos();
	parseExpected(SyntaxKind.SwitchKeyword);
	parseExpected(SyntaxKind.OpenParenToken);
	const expression = parseExpression();
	parseExpected(SyntaxKind.CloseParenToken);
	parseExpected(SyntaxKind.OpenBraceToken);
	const clauses = parseList(ParsingContext.SwitchClauses, parseSwitchClause);
	parseExpected(SyntaxKind.CloseBraceToken);
	const node = createNode<SwitchStatement>(SyntaxKind.SwitchStatement, pos);
	node.expression = expression;
	node.clauses = clauses;
	return finishNode(node, pos);
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

	let moduleDeclaration: ModuleDeclaration | undefined;
	let statements: NodeArray<Statement>;
	if ((isContextualKeyword("open") || isContextualKeyword("module")) && !packageDeclaration) {
		moduleDeclaration = parseModuleDeclaration();
		statements = createNodeArray<Statement>([], getNodePos());
	} else {
		statements = parseList(ParsingContext.SourceElements, parseSourceElement);
	}
	const endOfFileToken = parseExpectedToken<Token<SyntaxKind.EndOfFileToken>>(SyntaxKind.EndOfFileToken);

	const node = createNode<SourceFile>(SyntaxKind.SourceFile, pos);
	node.packageDeclaration = packageDeclaration;
	node.imports = imports;
	node.moduleDeclaration = moduleDeclaration;
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
				visitNode(cbNode, sf.moduleDeclaration) ||
				visitNodes(cbNode, cbNodes, sf.statements) ||
				visitNode(cbNode, sf.endOfFileToken)
			);
		}
		case SyntaxKind.ModuleDeclaration: {
			const n = node as ModuleDeclaration;
			return (
				visitNodes(cbNode, cbNodes, n.annotations) ||
				visitNode(cbNode, n.name) ||
				visitNodes(cbNode, cbNodes, n.directives)
			);
		}
		case SyntaxKind.RequiresDirective:
			return visitNode(cbNode, (node as RequiresDirective).name);
		case SyntaxKind.ExportsDirective: {
			const n = node as ExportsDirective;
			return visitNode(cbNode, n.packageName) || visitNodes(cbNode, cbNodes, n.toModules);
		}
		case SyntaxKind.OpensDirective: {
			const n = node as OpensDirective;
			return visitNode(cbNode, n.packageName) || visitNodes(cbNode, cbNodes, n.toModules);
		}
		case SyntaxKind.UsesDirective:
			return visitNode(cbNode, (node as UsesDirective).typeName);
		case SyntaxKind.ProvidesDirective: {
			const n = node as ProvidesDirective;
			return visitNode(cbNode, n.typeName) || visitNodes(cbNode, cbNodes, n.withTypes);
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
		case SyntaxKind.LocalVariableDeclarationStatement: {
			const n = node as LocalVariableDeclarationStatement;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.type) ||
				visitNodes(cbNode, cbNodes, n.declarators)
			);
		}
		case SyntaxKind.ExpressionStatement:
			return visitNode(cbNode, (node as ExpressionStatement).expression);
		case SyntaxKind.IfStatement: {
			const n = node as IfStatement;
			return (
				visitNode(cbNode, n.condition) ||
				visitNode(cbNode, n.thenStatement) ||
				visitNode(cbNode, n.elseStatement)
			);
		}
		case SyntaxKind.WhileStatement: {
			const n = node as WhileStatement;
			return visitNode(cbNode, n.condition) || visitNode(cbNode, n.statement);
		}
		case SyntaxKind.DoStatement: {
			const n = node as DoStatement;
			return visitNode(cbNode, n.statement) || visitNode(cbNode, n.condition);
		}
		case SyntaxKind.ForStatement: {
			const n = node as ForStatement;
			return (
				visitNode(cbNode, n.initializer) ||
				visitNode(cbNode, n.condition) ||
				visitNodes(cbNode, cbNodes, n.incrementors) ||
				visitNode(cbNode, n.statement)
			);
		}
		case SyntaxKind.ForEachStatement: {
			const n = node as ForEachStatement;
			return (
				visitNode(cbNode, n.parameter) ||
				visitNode(cbNode, n.expression) ||
				visitNode(cbNode, n.statement)
			);
		}
		case SyntaxKind.BreakStatement:
			return visitNode(cbNode, (node as BreakStatement).label);
		case SyntaxKind.ContinueStatement:
			return visitNode(cbNode, (node as ContinueStatement).label);
		case SyntaxKind.ReturnStatement:
			return visitNode(cbNode, (node as ReturnStatement).expression);
		case SyntaxKind.ThrowStatement:
			return visitNode(cbNode, (node as ThrowStatement).expression);
		case SyntaxKind.SynchronizedStatement: {
			const n = node as SynchronizedStatement;
			return visitNode(cbNode, n.expression) || visitNode(cbNode, n.body);
		}
		case SyntaxKind.AssertStatement: {
			const n = node as AssertStatement;
			return visitNode(cbNode, n.condition) || visitNode(cbNode, n.message);
		}
		case SyntaxKind.LabeledStatement: {
			const n = node as LabeledStatement;
			return visitNode(cbNode, n.label) || visitNode(cbNode, n.statement);
		}
		case SyntaxKind.TryStatement: {
			const n = node as TryStatement;
			return (
				visitNodes(cbNode, cbNodes, n.resources) ||
				visitNode(cbNode, n.tryBlock) ||
				visitNodes(cbNode, cbNodes, n.catchClauses) ||
				visitNode(cbNode, n.finallyBlock)
			);
		}
		case SyntaxKind.Resource: {
			const n = node as Resource;
			return (
				visitNodes(cbNode, cbNodes, n.modifiers) ||
				visitNode(cbNode, n.type) ||
				visitNode(cbNode, n.name) ||
				visitNode(cbNode, n.initializer)
			);
		}
		case SyntaxKind.CatchClause: {
			const n = node as CatchClause;
			return (
				visitNodes(cbNode, cbNodes, n.catchTypes) ||
				visitNode(cbNode, n.name) ||
				visitNode(cbNode, n.block)
			);
		}
		case SyntaxKind.SwitchStatement: {
			const n = node as SwitchStatement;
			return visitNode(cbNode, n.expression) || visitNodes(cbNode, cbNodes, n.clauses);
		}
		case SyntaxKind.SwitchClause: {
			const n = node as SwitchClause;
			return visitNode(cbNode, n.labelExpression) || visitNodes(cbNode, cbNodes, n.statements);
		}
		case SyntaxKind.ParenthesizedExpression:
			return visitNode(cbNode, (node as ParenthesizedExpression).expression);
		case SyntaxKind.PrefixUnaryExpression:
			return visitNode(cbNode, (node as PrefixUnaryExpression).operand);
		case SyntaxKind.PostfixUnaryExpression:
			return visitNode(cbNode, (node as PostfixUnaryExpression).operand);
		case SyntaxKind.BinaryExpression: {
			const n = node as BinaryExpression;
			return visitNode(cbNode, n.left) || visitNode(cbNode, n.right);
		}
		case SyntaxKind.AssignmentExpression: {
			const n = node as AssignmentExpression;
			return visitNode(cbNode, n.left) || visitNode(cbNode, n.right);
		}
		case SyntaxKind.ConditionalExpression: {
			const n = node as ConditionalExpression;
			return (
				visitNode(cbNode, n.condition) ||
				visitNode(cbNode, n.whenTrue) ||
				visitNode(cbNode, n.whenFalse)
			);
		}
		case SyntaxKind.InstanceofExpression: {
			const n = node as InstanceofExpression;
			return visitNode(cbNode, n.expression) || visitNode(cbNode, n.type);
		}
		case SyntaxKind.CastExpression: {
			const n = node as CastExpression;
			return visitNode(cbNode, n.type) || visitNode(cbNode, n.expression);
		}
		case SyntaxKind.PropertyAccessExpression: {
			const n = node as PropertyAccessExpression;
			return visitNode(cbNode, n.expression) || visitNode(cbNode, n.name);
		}
		case SyntaxKind.ElementAccessExpression: {
			const n = node as ElementAccessExpression;
			return visitNode(cbNode, n.expression) || visitNode(cbNode, n.argumentExpression);
		}
		case SyntaxKind.CallExpression: {
			const n = node as CallExpression;
			return (
				visitNode(cbNode, n.expression) ||
				visitNodes(cbNode, cbNodes, n.typeArguments) ||
				visitNodes(cbNode, cbNodes, n.arguments)
			);
		}
		case SyntaxKind.ObjectCreationExpression: {
			const n = node as ObjectCreationExpression;
			return (
				visitNode(cbNode, n.type) ||
				visitNodes(cbNode, cbNodes, n.arguments) ||
				visitNodes(cbNode, cbNodes, n.classBody)
			);
		}
		case SyntaxKind.ArrayCreationExpression: {
			const n = node as ArrayCreationExpression;
			return (
				visitNode(cbNode, n.elementType) ||
				visitNodes(cbNode, cbNodes, n.dimensions) ||
				visitNode(cbNode, n.initializer)
			);
		}
		case SyntaxKind.ArrayInitializer:
			return visitNodes(cbNode, cbNodes, (node as ArrayInitializer).elements);
		case SyntaxKind.ClassLiteralExpression:
			return visitNode(cbNode, (node as ClassLiteralExpression).type);
		case SyntaxKind.LambdaExpression: {
			const n = node as LambdaExpression;
			return visitNodes(cbNode, cbNodes, n.parameters) || visitNode(cbNode, n.body);
		}
		case SyntaxKind.MethodReferenceExpression: {
			const n = node as MethodReferenceExpression;
			return (
				visitNode(cbNode, n.expression) ||
				visitNodes(cbNode, cbNodes, n.typeArguments) ||
				visitNode(cbNode, n.name)
			);
		}
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
