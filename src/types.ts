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
	/** Symbol table for the scope this node introduces (set by the binder). */
	locals?: SymbolTable;
}

export interface NodeArray<T extends Node> extends ReadonlyArray<T>, ReadonlyTextRange {
	hasTrailingComma?: boolean;
}

export interface Token<TKind extends SyntaxKind> extends Node {
	readonly kind: TKind;
}

// Nodes
//
// Concrete node interfaces are added per milestone as the corresponding parser
// is written. The base set below is what the parser core (M3) needs.

export interface Statement extends Node {}

export interface EmptyStatement extends Statement {
	readonly kind: SyntaxKind.EmptyStatement;
}

export interface SourceFile extends Node {
	readonly kind: SyntaxKind.SourceFile;
	readonly packageDeclaration?: PackageDeclaration;
	readonly imports: NodeArray<ImportDeclaration>;
	/** Top-level type declarations (and any stray empty statements). */
	readonly statements: NodeArray<Statement>;
	readonly endOfFileToken: Token<SyntaxKind.EndOfFileToken>;
	fileName: string;
	text: string;
	parseDiagnostics: Diagnostic[];
	/** Diagnostics produced by the binder (duplicate declarations, ...). */
	bindDiagnostics?: Diagnostic[];
}

// Names

export interface Identifier extends Node {
	readonly kind: SyntaxKind.Identifier;
	/** The identifier text as written in the source. */
	readonly text: string;
}

export interface QualifiedName extends Node {
	readonly kind: SyntaxKind.QualifiedName;
	readonly left: EntityName;
	readonly right: Identifier;
}

export type EntityName = Identifier | QualifiedName;

// Type nodes

export type TypeNode = PrimitiveType | TypeReference | ArrayType | WildcardType;

export interface PrimitiveType extends Node {
	readonly kind: SyntaxKind.PrimitiveType;
	/** The primitive keyword kind (IntKeyword, BooleanKeyword, VoidKeyword, ...). */
	readonly keyword: SyntaxKind;
}

export interface TypeReference extends Node {
	readonly kind: SyntaxKind.TypeReference;
	readonly typeName: EntityName;
	readonly typeArguments?: NodeArray<TypeNode | WildcardType>;
}

export interface ArrayType extends Node {
	readonly kind: SyntaxKind.ArrayType;
	readonly elementType: TypeNode;
}

export interface WildcardType extends Node {
	readonly kind: SyntaxKind.WildcardType;
	readonly hasExtends: boolean;
	readonly hasSuper: boolean;
	readonly type?: TypeNode;
}

export interface TypeParameter extends Node {
	readonly kind: SyntaxKind.TypeParameter;
	readonly name: Identifier;
	/** Bounds: T extends A & B. */
	readonly constraint?: NodeArray<TypeNode>;
}

// Modifiers and annotations

/** A modifier is either a keyword token (public, static, ...) or an annotation. */
export type ModifierLike = Node;

export interface Annotation extends Node {
	readonly kind: SyntaxKind.Annotation;
	readonly typeName: EntityName;
	readonly args?: NodeArray<AnnotationArgument>;
}

export interface AnnotationArgument extends Node {
	readonly kind: SyntaxKind.AnnotationArgument;
	readonly name?: Identifier;
	readonly value: Node;
}

// Compilation unit pieces

export interface PackageDeclaration extends Node {
	readonly kind: SyntaxKind.PackageDeclaration;
	readonly annotations?: NodeArray<Annotation>;
	readonly name: EntityName;
}

export interface ImportDeclaration extends Node {
	readonly kind: SyntaxKind.ImportDeclaration;
	readonly isStatic: boolean;
	readonly name: EntityName;
	readonly isOnDemand: boolean;
}

// Type declarations. They are statements so they can appear both at the top
// level and (as local classes) inside blocks.

export interface ClassDeclaration extends Statement {
	readonly kind: SyntaxKind.ClassDeclaration;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly name: Identifier;
	readonly typeParameters?: NodeArray<TypeParameter>;
	readonly extendsType?: TypeNode;
	readonly implementsTypes?: NodeArray<TypeNode>;
	readonly members: NodeArray<Node>;
}

export interface InterfaceDeclaration extends Statement {
	readonly kind: SyntaxKind.InterfaceDeclaration;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly name: Identifier;
	readonly typeParameters?: NodeArray<TypeParameter>;
	readonly extendsTypes?: NodeArray<TypeNode>;
	readonly members: NodeArray<Node>;
}

export interface EnumDeclaration extends Statement {
	readonly kind: SyntaxKind.EnumDeclaration;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly name: Identifier;
	readonly implementsTypes?: NodeArray<TypeNode>;
	readonly enumConstants: NodeArray<EnumConstantDeclaration>;
	readonly members: NodeArray<Node>;
}

export interface AnnotationTypeDeclaration extends Statement {
	readonly kind: SyntaxKind.AnnotationTypeDeclaration;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly name: Identifier;
	readonly members: NodeArray<Node>;
}

export type TypeDeclaration =
	| ClassDeclaration
	| InterfaceDeclaration
	| EnumDeclaration
	| AnnotationTypeDeclaration;

// Members

export interface VariableDeclarator extends Node {
	readonly kind: SyntaxKind.VariableDeclarator;
	readonly name: Identifier;
	/** Extra array rank from C-style brackets after the name (int a[]). */
	readonly arrayRankAfterName: number;
	readonly initializer?: Node;
}

export interface FieldDeclaration extends Node {
	readonly kind: SyntaxKind.FieldDeclaration;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly type: TypeNode;
	readonly declarators: NodeArray<VariableDeclarator>;
}

export interface Parameter extends Node {
	readonly kind: SyntaxKind.Parameter;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly type: TypeNode;
	readonly isVarArgs: boolean;
	readonly name: Identifier;
	readonly arrayRankAfterName: number;
}

export interface MethodDeclaration extends Node {
	readonly kind: SyntaxKind.MethodDeclaration;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly typeParameters?: NodeArray<TypeParameter>;
	readonly returnType: TypeNode;
	readonly name: Identifier;
	readonly parameters: NodeArray<Parameter>;
	readonly throws?: NodeArray<TypeNode>;
	/** Undefined for abstract/interface/annotation-element methods. */
	readonly body?: Block;
}

export interface ConstructorDeclaration extends Node {
	readonly kind: SyntaxKind.ConstructorDeclaration;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly typeParameters?: NodeArray<TypeParameter>;
	readonly name: Identifier;
	readonly parameters: NodeArray<Parameter>;
	readonly throws?: NodeArray<TypeNode>;
	readonly body: Block;
}

export interface InitializerBlock extends Node {
	readonly kind: SyntaxKind.InitializerBlock;
	readonly isStatic: boolean;
	readonly body: Block;
}

export interface EnumConstantDeclaration extends Node {
	readonly kind: SyntaxKind.EnumConstantDeclaration;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly name: Identifier;
	readonly arguments?: NodeArray<Expression>;
	readonly classBody?: NodeArray<Node>;
}

// Expressions

export interface Expression extends Node {}

export interface LiteralExpression extends Expression {
	readonly kind:
		| SyntaxKind.NumericLiteral
		| SyntaxKind.StringLiteral
		| SyntaxKind.CharacterLiteral
		| SyntaxKind.TextBlockLiteral;
	readonly value: string;
}

export interface ParenthesizedExpression extends Expression {
	readonly kind: SyntaxKind.ParenthesizedExpression;
	readonly expression: Expression;
}

export interface PrefixUnaryExpression extends Expression {
	readonly kind: SyntaxKind.PrefixUnaryExpression;
	readonly operator: SyntaxKind;
	readonly operand: Expression;
}

export interface PostfixUnaryExpression extends Expression {
	readonly kind: SyntaxKind.PostfixUnaryExpression;
	readonly operand: Expression;
	readonly operator: SyntaxKind;
}

export interface BinaryExpression extends Expression {
	readonly kind: SyntaxKind.BinaryExpression;
	readonly left: Expression;
	readonly operatorToken: SyntaxKind;
	readonly right: Expression;
}

export interface AssignmentExpression extends Expression {
	readonly kind: SyntaxKind.AssignmentExpression;
	readonly left: Expression;
	readonly operatorToken: SyntaxKind;
	readonly right: Expression;
}

export interface ConditionalExpression extends Expression {
	readonly kind: SyntaxKind.ConditionalExpression;
	readonly condition: Expression;
	readonly whenTrue: Expression;
	readonly whenFalse: Expression;
}

export interface InstanceofExpression extends Expression {
	readonly kind: SyntaxKind.InstanceofExpression;
	readonly expression: Expression;
	readonly type: TypeNode;
}

export interface CastExpression extends Expression {
	readonly kind: SyntaxKind.CastExpression;
	readonly type: TypeNode;
	readonly expression: Expression;
}

export interface PropertyAccessExpression extends Expression {
	readonly kind: SyntaxKind.PropertyAccessExpression;
	readonly expression: Expression;
	readonly name: Identifier;
}

export interface ElementAccessExpression extends Expression {
	readonly kind: SyntaxKind.ElementAccessExpression;
	readonly expression: Expression;
	readonly argumentExpression: Expression;
}

export interface CallExpression extends Expression {
	readonly kind: SyntaxKind.CallExpression;
	readonly expression: Expression;
	readonly typeArguments?: NodeArray<TypeNode | WildcardType>;
	readonly arguments: NodeArray<Expression>;
}

export interface ObjectCreationExpression extends Expression {
	readonly kind: SyntaxKind.ObjectCreationExpression;
	readonly type: TypeNode;
	readonly arguments: NodeArray<Expression>;
	readonly classBody?: NodeArray<Node>;
}

export interface ArrayCreationExpression extends Expression {
	readonly kind: SyntaxKind.ArrayCreationExpression;
	readonly elementType: TypeNode;
	readonly dimensions: NodeArray<Expression>;
	readonly additionalRank: number;
	readonly initializer?: ArrayInitializer;
}

export interface ArrayInitializer extends Expression {
	readonly kind: SyntaxKind.ArrayInitializer;
	readonly elements: NodeArray<Expression>;
}

export interface ThisExpression extends Expression {
	readonly kind: SyntaxKind.ThisExpression;
}

export interface SuperExpression extends Expression {
	readonly kind: SyntaxKind.SuperExpression;
}

export interface ClassLiteralExpression extends Expression {
	readonly kind: SyntaxKind.ClassLiteralExpression;
	readonly type: TypeNode;
}

// Statements

export interface Block extends Statement {
	readonly kind: SyntaxKind.Block;
	readonly statements: NodeArray<Statement>;
}

export interface LocalVariableDeclarationStatement extends Statement {
	readonly kind: SyntaxKind.LocalVariableDeclarationStatement;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly type: TypeNode;
	readonly declarators: NodeArray<VariableDeclarator>;
}

export interface ExpressionStatement extends Statement {
	readonly kind: SyntaxKind.ExpressionStatement;
	readonly expression: Expression;
}

export interface IfStatement extends Statement {
	readonly kind: SyntaxKind.IfStatement;
	readonly condition: Expression;
	readonly thenStatement: Statement;
	readonly elseStatement?: Statement;
}

export interface WhileStatement extends Statement {
	readonly kind: SyntaxKind.WhileStatement;
	readonly condition: Expression;
	readonly statement: Statement;
}

export interface DoStatement extends Statement {
	readonly kind: SyntaxKind.DoStatement;
	readonly statement: Statement;
	readonly condition: Expression;
}

export interface ForStatement extends Statement {
	readonly kind: SyntaxKind.ForStatement;
	readonly initializer?: Node;
	readonly condition?: Expression;
	readonly incrementors?: NodeArray<Expression>;
	readonly statement: Statement;
}

export interface ForEachStatement extends Statement {
	readonly kind: SyntaxKind.ForEachStatement;
	readonly parameter: Parameter;
	readonly expression: Expression;
	readonly statement: Statement;
}

export interface BreakStatement extends Statement {
	readonly kind: SyntaxKind.BreakStatement;
	readonly label?: Identifier;
}

export interface ContinueStatement extends Statement {
	readonly kind: SyntaxKind.ContinueStatement;
	readonly label?: Identifier;
}

export interface ReturnStatement extends Statement {
	readonly kind: SyntaxKind.ReturnStatement;
	readonly expression?: Expression;
}

export interface ThrowStatement extends Statement {
	readonly kind: SyntaxKind.ThrowStatement;
	readonly expression: Expression;
}

export interface SynchronizedStatement extends Statement {
	readonly kind: SyntaxKind.SynchronizedStatement;
	readonly expression: Expression;
	readonly body: Block;
}

export interface AssertStatement extends Statement {
	readonly kind: SyntaxKind.AssertStatement;
	readonly condition: Expression;
	readonly message?: Expression;
}

export interface LabeledStatement extends Statement {
	readonly kind: SyntaxKind.LabeledStatement;
	readonly label: Identifier;
	readonly statement: Statement;
}

export interface Resource extends Node {
	readonly kind: SyntaxKind.Resource;
	readonly modifiers?: NodeArray<ModifierLike>;
	readonly type?: TypeNode;
	readonly name: Identifier;
	readonly initializer: Expression;
}

export interface CatchClause extends Node {
	readonly kind: SyntaxKind.CatchClause;
	/** Catch type, possibly a multi-catch union (A | B). */
	readonly catchTypes: NodeArray<TypeNode>;
	readonly name: Identifier;
	readonly block: Block;
}

export interface TryStatement extends Statement {
	readonly kind: SyntaxKind.TryStatement;
	readonly resources?: NodeArray<Resource>;
	readonly tryBlock: Block;
	readonly catchClauses: NodeArray<CatchClause>;
	readonly finallyBlock?: Block;
}

export interface SwitchClause extends Node {
	readonly kind: SyntaxKind.SwitchClause;
	readonly isDefault: boolean;
	readonly labelExpression?: Expression;
	readonly statements: NodeArray<Statement>;
}

export interface SwitchStatement extends Statement {
	readonly kind: SyntaxKind.SwitchStatement;
	readonly expression: Expression;
	readonly clauses: NodeArray<SwitchClause>;
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

export type ErrorCallback = (message: DiagnosticMessage, pos: number, length: number) => void;

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
