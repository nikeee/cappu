// Package compiler is the Java front end: scanner, parser, binder, checker and
// the language services built on them. Port of src/compiler/.
//
// Structure and naming mirror the TypeScript compiler (and tsgo): a single
// SyntaxKind covering tokens and node kinds, organized into ranges with
// First*/Last* markers. The kind is a plain int with a hand-written name table
// (the range markers alias real kinds, which stringer cannot represent).
package compiler

import "strconv"

// SyntaxKind enumerates every token and node kind. The numeric layout matches
// src/compiler/types.ts exactly so positions and baselines stay identical to
// the Node build.
type SyntaxKind int

const (
	Unknown SyntaxKind = iota

	// Trivia
	EndOfFileToken
	SingleLineCommentTrivia
	MultiLineCommentTrivia
	NewLineTrivia
	WhitespaceTrivia

	// Literals
	NumericLiteral
	CharacterLiteral
	StringLiteral
	TextBlockLiteral

	// Punctuation and operators
	OpenBraceToken
	CloseBraceToken
	OpenParenToken
	CloseParenToken
	OpenBracketToken
	CloseBracketToken
	DotToken
	DotDotDotToken
	SemicolonToken
	CommaToken
	AtToken
	ColonColonToken
	ArrowToken
	LessThanToken
	GreaterThanToken
	LessThanEqualsToken
	GreaterThanEqualsToken
	EqualsEqualsToken
	ExclamationEqualsToken
	AmpersandAmpersandToken
	BarBarToken
	ExclamationToken
	AmpersandToken
	BarToken
	CaretToken
	TildeToken
	LessThanLessThanToken
	GreaterThanGreaterThanToken
	GreaterThanGreaterThanGreaterThanToken
	PlusToken
	MinusToken
	AsteriskToken
	SlashToken
	PercentToken
	PlusPlusToken
	MinusMinusToken
	EqualsToken
	PlusEqualsToken
	MinusEqualsToken
	AsteriskEqualsToken
	SlashEqualsToken
	PercentEqualsToken
	AmpersandEqualsToken
	BarEqualsToken
	CaretEqualsToken
	LessThanLessThanEqualsToken
	GreaterThanGreaterThanEqualsToken
	GreaterThanGreaterThanGreaterThanEqualsToken
	QuestionToken
	ColonToken

	// Reserved keywords (JLS 3.9)
	AbstractKeyword
	AssertKeyword
	BooleanKeyword
	BreakKeyword
	ByteKeyword
	CaseKeyword
	CatchKeyword
	CharKeyword
	ClassKeyword
	ConstKeyword
	ContinueKeyword
	DefaultKeyword
	DoKeyword
	DoubleKeyword
	ElseKeyword
	EnumKeyword
	ExtendsKeyword
	FinalKeyword
	FinallyKeyword
	FloatKeyword
	ForKeyword
	GotoKeyword
	IfKeyword
	ImplementsKeyword
	ImportKeyword
	InstanceofKeyword
	IntKeyword
	InterfaceKeyword
	LongKeyword
	NativeKeyword
	NewKeyword
	PackageKeyword
	PrivateKeyword
	ProtectedKeyword
	PublicKeyword
	ReturnKeyword
	ShortKeyword
	StaticKeyword
	StrictfpKeyword
	SuperKeyword
	SwitchKeyword
	SynchronizedKeyword
	ThisKeyword
	ThrowKeyword
	ThrowsKeyword
	TransientKeyword
	TryKeyword
	VoidKeyword
	VolatileKeyword
	WhileKeyword
	// Reserved literals (lex like keywords)
	TrueKeyword
	FalseKeyword
	NullKeyword

	Identifier

	// Names
	QualifiedName

	// Type nodes
	PrimitiveType
	TypeReference
	ArrayType
	WildcardType
	TypeParameter
	VarType

	// Compilation unit and top-level declarations
	SourceFile
	PackageDeclaration
	ImportDeclaration
	ClassDeclaration
	InterfaceDeclaration
	EnumDeclaration
	RecordDeclaration
	AnnotationTypeDeclaration

	// Members
	FieldDeclaration
	MethodDeclaration
	ConstructorDeclaration
	CompactConstructorDeclaration
	InitializerBlock
	Parameter
	ReceiverParameter
	RecordComponent
	EnumConstantDeclaration
	AnnotationTypeElementDeclaration
	VariableDeclarator
	TypeParameterList

	// Modifiers and annotations
	Annotation
	AnnotationArgument

	// Module declarations (SE9)
	ModuleDeclaration
	RequiresDirective
	ExportsDirective
	OpensDirective
	UsesDirective
	ProvidesDirective

	// Statements
	Block
	EmptyStatement
	LocalVariableDeclarationStatement
	LocalClassDeclarationStatement
	ExpressionStatement
	IfStatement
	AssertStatement
	SwitchStatement
	SwitchClause
	WhileStatement
	DoStatement
	ForStatement
	ForEachStatement
	BreakStatement
	ContinueStatement
	ReturnStatement
	ThrowStatement
	SynchronizedStatement
	TryStatement
	Resource
	CatchClause
	LabeledStatement
	YieldStatement

	// Expressions
	AssignmentExpression
	ConditionalExpression
	BinaryExpression
	InstanceofExpression
	PrefixUnaryExpression
	PostfixUnaryExpression
	CastExpression
	PropertyAccessExpression
	ElementAccessExpression
	CallExpression
	ObjectCreationExpression
	ArrayCreationExpression
	ArrayInitializer
	ThisExpression
	SuperExpression
	ParenthesizedExpression
	ClassLiteralExpression
	LambdaExpression
	MethodReferenceExpression
	SwitchExpression

	// Patterns (SE16/SE21)
	TypePattern
	RecordPattern
	MatchAllPattern

	kindCount // sentinel: number of real kinds
)

// Range markers (aliases of real kinds; kept out of the name table).
const (
	FirstLiteralToken = NumericLiteral
	LastLiteralToken  = TextBlockLiteral
	FirstPunctuation  = OpenBraceToken
	LastPunctuation   = ColonToken
	FirstAssignment   = EqualsToken
	LastAssignment    = GreaterThanGreaterThanGreaterThanEqualsToken
	FirstKeyword      = AbstractKeyword
	LastKeyword       = NullKeyword
	FirstReservedWord = AbstractKeyword
	LastReservedWord  = WhileKeyword
	FirstNode         = QualifiedName
	FirstTypeNode     = PrimitiveType
	LastTypeNode      = VarType
	FirstStatement    = Block
	LastStatement     = YieldStatement
	FirstExpression   = AssignmentExpression
	LastExpression    = SwitchExpression
)

// syntaxKindNames maps each kind to its canonical member name, in iota order
// (mirrors the TS enum's reverse mapping, range markers excluded).
var syntaxKindNames = [...]string{
	"Unknown",
	"EndOfFileToken", "SingleLineCommentTrivia", "MultiLineCommentTrivia", "NewLineTrivia", "WhitespaceTrivia",
	"NumericLiteral", "CharacterLiteral", "StringLiteral", "TextBlockLiteral",
	"OpenBraceToken", "CloseBraceToken", "OpenParenToken", "CloseParenToken", "OpenBracketToken", "CloseBracketToken",
	"DotToken", "DotDotDotToken", "SemicolonToken", "CommaToken", "AtToken", "ColonColonToken", "ArrowToken",
	"LessThanToken", "GreaterThanToken", "LessThanEqualsToken", "GreaterThanEqualsToken", "EqualsEqualsToken",
	"ExclamationEqualsToken", "AmpersandAmpersandToken", "BarBarToken", "ExclamationToken", "AmpersandToken",
	"BarToken", "CaretToken", "TildeToken", "LessThanLessThanToken", "GreaterThanGreaterThanToken",
	"GreaterThanGreaterThanGreaterThanToken", "PlusToken", "MinusToken", "AsteriskToken", "SlashToken", "PercentToken",
	"PlusPlusToken", "MinusMinusToken", "EqualsToken", "PlusEqualsToken", "MinusEqualsToken", "AsteriskEqualsToken",
	"SlashEqualsToken", "PercentEqualsToken", "AmpersandEqualsToken", "BarEqualsToken", "CaretEqualsToken",
	"LessThanLessThanEqualsToken", "GreaterThanGreaterThanEqualsToken", "GreaterThanGreaterThanGreaterThanEqualsToken",
	"QuestionToken", "ColonToken",
	"AbstractKeyword", "AssertKeyword", "BooleanKeyword", "BreakKeyword", "ByteKeyword", "CaseKeyword", "CatchKeyword",
	"CharKeyword", "ClassKeyword", "ConstKeyword", "ContinueKeyword", "DefaultKeyword", "DoKeyword", "DoubleKeyword",
	"ElseKeyword", "EnumKeyword", "ExtendsKeyword", "FinalKeyword", "FinallyKeyword", "FloatKeyword", "ForKeyword",
	"GotoKeyword", "IfKeyword", "ImplementsKeyword", "ImportKeyword", "InstanceofKeyword", "IntKeyword",
	"InterfaceKeyword", "LongKeyword", "NativeKeyword", "NewKeyword", "PackageKeyword", "PrivateKeyword",
	"ProtectedKeyword", "PublicKeyword", "ReturnKeyword", "ShortKeyword", "StaticKeyword", "StrictfpKeyword",
	"SuperKeyword", "SwitchKeyword", "SynchronizedKeyword", "ThisKeyword", "ThrowKeyword", "ThrowsKeyword",
	"TransientKeyword", "TryKeyword", "VoidKeyword", "VolatileKeyword", "WhileKeyword",
	"TrueKeyword", "FalseKeyword", "NullKeyword",
	"Identifier",
	"QualifiedName",
	"PrimitiveType", "TypeReference", "ArrayType", "WildcardType", "TypeParameter", "VarType",
	"SourceFile", "PackageDeclaration", "ImportDeclaration", "ClassDeclaration", "InterfaceDeclaration",
	"EnumDeclaration", "RecordDeclaration", "AnnotationTypeDeclaration",
	"FieldDeclaration", "MethodDeclaration", "ConstructorDeclaration", "CompactConstructorDeclaration",
	"InitializerBlock", "Parameter", "ReceiverParameter", "RecordComponent", "EnumConstantDeclaration",
	"AnnotationTypeElementDeclaration", "VariableDeclarator", "TypeParameterList",
	"Annotation", "AnnotationArgument",
	"ModuleDeclaration", "RequiresDirective", "ExportsDirective", "OpensDirective", "UsesDirective", "ProvidesDirective",
	"Block", "EmptyStatement", "LocalVariableDeclarationStatement", "LocalClassDeclarationStatement",
	"ExpressionStatement", "IfStatement", "AssertStatement", "SwitchStatement", "SwitchClause", "WhileStatement",
	"DoStatement", "ForStatement", "ForEachStatement", "BreakStatement", "ContinueStatement", "ReturnStatement",
	"ThrowStatement", "SynchronizedStatement", "TryStatement", "Resource", "CatchClause", "LabeledStatement",
	"YieldStatement",
	"AssignmentExpression", "ConditionalExpression", "BinaryExpression", "InstanceofExpression",
	"PrefixUnaryExpression", "PostfixUnaryExpression", "CastExpression", "PropertyAccessExpression",
	"ElementAccessExpression", "CallExpression", "ObjectCreationExpression", "ArrayCreationExpression",
	"ArrayInitializer", "ThisExpression", "SuperExpression", "ParenthesizedExpression", "ClassLiteralExpression",
	"LambdaExpression", "MethodReferenceExpression", "SwitchExpression",
	"TypePattern", "RecordPattern", "MatchAllPattern",
}

// String returns the SyntaxKind member name (for debug output and baselines).
func (k SyntaxKind) String() string {
	if k >= 0 && int(k) < len(syntaxKindNames) {
		return syntaxKindNames[k]
	}
	return "Unknown(" + strconv.Itoa(int(k)) + ")"
}

// IsKeyword reports whether kind is a reserved keyword (or reserved literal).
func IsKeyword(k SyntaxKind) bool { return k >= FirstKeyword && k <= LastKeyword }

// IsReservedWord reports whether kind is a reserved word (excludes the literals).
func IsReservedWord(k SyntaxKind) bool { return k >= FirstReservedWord && k <= LastReservedWord }

// NodeFlags are per-node parse-state flags.
type NodeFlags uint32

const (
	NodeFlagsNone                      NodeFlags = 0
	NodeFlagThisNodeHasError           NodeFlags = 1 << 0
	NodeFlagThisNodeOrAnySubNodesError NodeFlags = 1 << 1
)

// TokenFlags are per-token lexical flags.
type TokenFlags uint32

const (
	TokenFlagsNone     TokenFlags = 0
	PrecedingLineBreak TokenFlags = 1 << 0
	Unterminated       TokenFlags = 1 << 1
	HexSpecifier       TokenFlags = 1 << 2
	BinarySpecifier    TokenFlags = 1 << 3
	OctalSpecifier     TokenFlags = 1 << 4
	ContainsUnderscore TokenFlags = 1 << 5
	LongSuffix         TokenFlags = 1 << 6
	FloatSuffix        TokenFlags = 1 << 7
	DoubleSuffix       TokenFlags = 1 << 8
	JavaDoc            TokenFlags = 1 << 9
)
