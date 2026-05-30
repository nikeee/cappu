// Core type definitions for the Java parser.
//
// Structure and naming mirror the TypeScript compiler (src/compiler/types.ts):
// a single SyntaxKind enum covering tokens and node kinds, organized into ranges
// with First*/Last* markers, plus the base Node/NodeArray/Token interfaces.
//
// The enum is laid out for the full Java SE26 grammar up front so that the
// numeric values (and therefore the range markers) stay stable as later
// milestones add the parsing for each feature. Java contextual keywords
// (var, yield, record, sealed, permits, when, module directives) are NOT in the
// keyword range - they are scanned as Identifier and recognized positionally,
// matching how the TS compiler treats its contextual keywords.

export const enum SyntaxKind {
	Unknown,

	// Trivia
	EndOfFileToken,
	SingleLineCommentTrivia,
	MultiLineCommentTrivia,
	NewLineTrivia,
	WhitespaceTrivia,

	// Literals
	NumericLiteral,
	CharacterLiteral,
	StringLiteral,
	TextBlockLiteral,

	// Punctuation and operators
	OpenBraceToken,
	CloseBraceToken,
	OpenParenToken,
	CloseParenToken,
	OpenBracketToken,
	CloseBracketToken,
	DotToken,
	DotDotDotToken,
	SemicolonToken,
	CommaToken,
	AtToken,
	ColonColonToken, // ::  (SE8 method reference)
	ArrowToken, // ->  (SE8 lambda / SE14 switch rule)
	LessThanToken,
	GreaterThanToken,
	LessThanEqualsToken,
	GreaterThanEqualsToken,
	EqualsEqualsToken,
	ExclamationEqualsToken,
	AmpersandAmpersandToken,
	BarBarToken,
	ExclamationToken,
	AmpersandToken,
	BarToken,
	CaretToken,
	TildeToken,
	LessThanLessThanToken,
	GreaterThanGreaterThanToken,
	GreaterThanGreaterThanGreaterThanToken,
	PlusToken,
	MinusToken,
	AsteriskToken,
	SlashToken,
	PercentToken,
	PlusPlusToken,
	MinusMinusToken,
	EqualsToken,
	PlusEqualsToken,
	MinusEqualsToken,
	AsteriskEqualsToken,
	SlashEqualsToken,
	PercentEqualsToken,
	AmpersandEqualsToken,
	BarEqualsToken,
	CaretEqualsToken,
	LessThanLessThanEqualsToken,
	GreaterThanGreaterThanEqualsToken,
	GreaterThanGreaterThanGreaterThanEqualsToken,
	QuestionToken,
	ColonToken,

	// Reserved keywords (JLS 3.9)
	AbstractKeyword,
	AssertKeyword,
	BooleanKeyword,
	BreakKeyword,
	ByteKeyword,
	CaseKeyword,
	CatchKeyword,
	CharKeyword,
	ClassKeyword,
	ConstKeyword, // reserved, unused - parser errors on use
	ContinueKeyword,
	DefaultKeyword,
	DoKeyword,
	DoubleKeyword,
	ElseKeyword,
	EnumKeyword,
	ExtendsKeyword,
	FinalKeyword,
	FinallyKeyword,
	FloatKeyword,
	ForKeyword,
	GotoKeyword, // reserved, unused
	IfKeyword,
	ImplementsKeyword,
	ImportKeyword,
	InstanceofKeyword,
	IntKeyword,
	InterfaceKeyword,
	LongKeyword,
	NativeKeyword,
	NewKeyword,
	PackageKeyword,
	PrivateKeyword,
	ProtectedKeyword,
	PublicKeyword,
	ReturnKeyword,
	ShortKeyword,
	StaticKeyword,
	StrictfpKeyword,
	SuperKeyword,
	SwitchKeyword,
	SynchronizedKeyword,
	ThisKeyword,
	ThrowKeyword,
	ThrowsKeyword,
	TransientKeyword,
	TryKeyword,
	VoidKeyword,
	VolatileKeyword,
	WhileKeyword,
	// Reserved literals (JLS treats these as literals, but they lex like keywords)
	TrueKeyword,
	FalseKeyword,
	NullKeyword,

	Identifier,

	// Names
	QualifiedName,

	// Type nodes
	PrimitiveType,
	TypeReference,
	ArrayType,
	WildcardType,
	TypeParameter,
	VarType, // SE10 'var' inferred type

	// Compilation unit and top-level declarations
	SourceFile,
	PackageDeclaration,
	ImportDeclaration,
	ClassDeclaration,
	InterfaceDeclaration,
	EnumDeclaration,
	RecordDeclaration, // SE16
	AnnotationTypeDeclaration, // @interface

	// Members
	FieldDeclaration,
	MethodDeclaration,
	ConstructorDeclaration,
	CompactConstructorDeclaration, // SE16
	InitializerBlock,
	Parameter,
	ReceiverParameter,
	RecordComponent, // SE16
	EnumConstantDeclaration,
	AnnotationTypeElementDeclaration,
	VariableDeclarator,
	TypeParameterList, // unused placeholder kept for stable numbering

	// Modifiers and annotations
	Annotation,
	AnnotationArgument,

	// Module declarations (SE9)
	ModuleDeclaration,
	RequiresDirective,
	ExportsDirective,
	OpensDirective,
	UsesDirective,
	ProvidesDirective,

	// Statements
	Block,
	EmptyStatement,
	LocalVariableDeclarationStatement,
	LocalClassDeclarationStatement,
	ExpressionStatement,
	IfStatement,
	AssertStatement,
	SwitchStatement,
	SwitchClause,
	WhileStatement,
	DoStatement,
	ForStatement,
	ForEachStatement,
	BreakStatement,
	ContinueStatement,
	ReturnStatement,
	ThrowStatement,
	SynchronizedStatement,
	TryStatement,
	Resource,
	CatchClause,
	LabeledStatement,
	YieldStatement, // SE14

	// Expressions
	AssignmentExpression,
	ConditionalExpression,
	BinaryExpression,
	InstanceofExpression,
	PrefixUnaryExpression,
	PostfixUnaryExpression,
	CastExpression,
	PropertyAccessExpression,
	ElementAccessExpression,
	CallExpression,
	ObjectCreationExpression,
	ArrayCreationExpression,
	ArrayInitializer,
	ThisExpression,
	SuperExpression,
	ParenthesizedExpression,
	ClassLiteralExpression,
	LambdaExpression, // SE8
	MethodReferenceExpression, // SE8
	SwitchExpression, // SE14

	// Patterns (SE16/SE21)
	TypePattern,
	RecordPattern,
	MatchAllPattern, // '_'

	// Range markers
	FirstLiteralToken = NumericLiteral,
	LastLiteralToken = TextBlockLiteral,
	FirstPunctuation = OpenBraceToken,
	LastPunctuation = ColonToken,
	FirstAssignment = EqualsToken,
	LastAssignment = GreaterThanGreaterThanGreaterThanEqualsToken,
	FirstKeyword = AbstractKeyword,
	LastKeyword = NullKeyword,
	FirstReservedWord = AbstractKeyword,
	LastReservedWord = WhileKeyword,
	FirstNode = QualifiedName,
	FirstTypeNode = PrimitiveType,
	LastTypeNode = VarType,
	FirstStatement = Block,
	LastStatement = YieldStatement,
	FirstExpression = AssignmentExpression,
	LastExpression = SwitchExpression,
}

export const enum NodeFlags {
	None = 0,
	/** The parser encountered an error while parsing this node directly. */
	ThisNodeHasError = 1 << 0,
	/** This node or one of its descendants contains a parse error. */
	ThisNodeOrAnySubNodesHasError = 1 << 1,
}

export const enum TokenFlags {
	None = 0,
	/** There was a line break between this token and the previous one. */
	PrecedingLineBreak = 1 << 0,
	/** A literal whose closing delimiter is missing (unterminated string/char/comment). */
	Unterminated = 1 << 1,
	HexSpecifier = 1 << 2, // 0x...
	BinarySpecifier = 1 << 3, // 0b...
	OctalSpecifier = 1 << 4, // leading 0
	ContainsUnderscore = 1 << 5, // 1_000 (SE7)
	LongSuffix = 1 << 6, // 123L
	FloatSuffix = 1 << 7, // 1.0f
	DoubleSuffix = 1 << 8, // 1.0d
	/** A /** ... *​/ block comment (Javadoc). */
	JavaDoc = 1 << 9,
}

export interface ReadonlyTextRange {
	readonly pos: number;
	readonly end: number;
}

export interface Node extends ReadonlyTextRange {
	readonly kind: SyntaxKind;
	flags: NodeFlags;
	parent: Node;
	symbol?: Symbol;
}

export interface NodeArray<T extends Node> extends ReadonlyArray<T>, ReadonlyTextRange {
	hasTrailingComma?: boolean;
}

export interface Token<TKind extends SyntaxKind> extends Node {
	readonly kind: TKind;
}

export interface Identifier extends Node {
	readonly kind: SyntaxKind.Identifier;
	/** The identifier text as written in the source. */
	readonly text: string;
}

// Diagnostics

export const enum DiagnosticCategory {
	Warning,
	Error,
	Suggestion,
	Message,
}

export interface DiagnosticMessage {
	readonly key: string;
	readonly code: number;
	readonly category: DiagnosticCategory;
	readonly message: string;
}

/** A diagnostic produced by the parser, located by an offset range in the source text. */
export interface Diagnostic extends ReadonlyTextRange {
	readonly code: number;
	readonly category: DiagnosticCategory;
	readonly messageText: string;
}

// Scanner

export type ErrorCallback = (message: DiagnosticMessage, length: number) => void;

export interface Scanner {
	scan(): SyntaxKind;
	getToken(): SyntaxKind;
	/** Raw token text including any escapes, as written in the source. */
	getTokenText(): string;
	/** Cooked token value (identifier name, decoded string contents, etc.). */
	getTokenValue(): string;
	/** Start of the token text, excluding preceding trivia. */
	getTokenStart(): number;
	/** Start of the token including preceding trivia. */
	getTokenFullStart(): number;
	/** End offset of the token (exclusive). */
	getTokenEnd(): number;
	getTokenFlags(): TokenFlags;
	hasPrecedingLineBreak(): boolean;
	setText(text: string, start?: number, length?: number): void;
	setOnError(onError: ErrorCallback | undefined): void;
	resetTokenState(pos: number): void;
	/** Re-scan a '>'-family token as a single '>' (used when closing type arguments). */
	reScanGreaterToken(): SyntaxKind;
	lookAhead<T>(callback: () => T): T;
	tryScan<T>(callback: () => T): T;
}

// Symbols (binder, M9)

export const enum SymbolFlags {
	None = 0,
	Package = 1 << 0,
	Class = 1 << 1,
	Interface = 1 << 2,
	Enum = 1 << 3,
	Annotation = 1 << 4,
	Record = 1 << 5,
	Method = 1 << 6,
	Constructor = 1 << 7,
	Field = 1 << 8,
	EnumConstant = 1 << 9,
	Parameter = 1 << 10,
	TypeParameter = 1 << 11,
	LocalVariable = 1 << 12,
	Module = 1 << 13,

	Type = Class | Interface | Enum | Annotation | Record | TypeParameter,
}

export type SymbolTable = Map<string, Symbol>;

export interface Symbol {
	flags: SymbolFlags;
	escapedName: string;
	declarations?: Node[];
	members?: SymbolTable;
	parent?: Symbol;
}
