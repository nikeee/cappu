package compiler

// textToKeyword maps each reserved word (and the reserved literals
// true/false/null) to its kind. Contextual keywords (var, yield, record,
// sealed, ...) are intentionally absent: they are scanned as identifiers and
// recognized positionally by the parser. Port of utilities.ts textToKeyword.
var textToKeyword = map[string]SyntaxKind{
	"abstract":     AbstractKeyword,
	"assert":       AssertKeyword,
	"boolean":      BooleanKeyword,
	"break":        BreakKeyword,
	"byte":         ByteKeyword,
	"case":         CaseKeyword,
	"catch":        CatchKeyword,
	"char":         CharKeyword,
	"class":        ClassKeyword,
	"const":        ConstKeyword,
	"continue":     ContinueKeyword,
	"default":      DefaultKeyword,
	"do":           DoKeyword,
	"double":       DoubleKeyword,
	"else":         ElseKeyword,
	"enum":         EnumKeyword,
	"extends":      ExtendsKeyword,
	"final":        FinalKeyword,
	"finally":      FinallyKeyword,
	"float":        FloatKeyword,
	"for":          ForKeyword,
	"goto":         GotoKeyword,
	"if":           IfKeyword,
	"implements":   ImplementsKeyword,
	"import":       ImportKeyword,
	"instanceof":   InstanceofKeyword,
	"int":          IntKeyword,
	"interface":    InterfaceKeyword,
	"long":         LongKeyword,
	"native":       NativeKeyword,
	"new":          NewKeyword,
	"package":      PackageKeyword,
	"private":      PrivateKeyword,
	"protected":    ProtectedKeyword,
	"public":       PublicKeyword,
	"return":       ReturnKeyword,
	"short":        ShortKeyword,
	"static":       StaticKeyword,
	"strictfp":     StrictfpKeyword,
	"super":        SuperKeyword,
	"switch":       SwitchKeyword,
	"synchronized": SynchronizedKeyword,
	"this":         ThisKeyword,
	"throw":        ThrowKeyword,
	"throws":       ThrowsKeyword,
	"transient":    TransientKeyword,
	"try":          TryKeyword,
	"void":         VoidKeyword,
	"volatile":     VolatileKeyword,
	"while":        WhileKeyword,
	"true":         TrueKeyword,
	"false":        FalseKeyword,
	"null":         NullKeyword,
}

// punctuationText is the canonical spelling of each punctuation/operator token.
var punctuationText = map[SyntaxKind]string{
	OpenBraceToken: "{", CloseBraceToken: "}", OpenParenToken: "(", CloseParenToken: ")",
	OpenBracketToken: "[", CloseBracketToken: "]", DotToken: ".", DotDotDotToken: "...",
	SemicolonToken: ";", CommaToken: ",", AtToken: "@", ColonColonToken: "::", ArrowToken: "->",
	LessThanToken: "<", GreaterThanToken: ">", LessThanEqualsToken: "<=", GreaterThanEqualsToken: ">=",
	EqualsEqualsToken: "==", ExclamationEqualsToken: "!=", AmpersandAmpersandToken: "&&", BarBarToken: "||",
	ExclamationToken: "!", AmpersandToken: "&", BarToken: "|", CaretToken: "^", TildeToken: "~",
	LessThanLessThanToken: "<<", GreaterThanGreaterThanToken: ">>", GreaterThanGreaterThanGreaterThanToken: ">>>",
	PlusToken: "+", MinusToken: "-", AsteriskToken: "*", SlashToken: "/", PercentToken: "%",
	PlusPlusToken: "++", MinusMinusToken: "--", EqualsToken: "=", PlusEqualsToken: "+=", MinusEqualsToken: "-=",
	AsteriskEqualsToken: "*=", SlashEqualsToken: "/=", PercentEqualsToken: "%=", AmpersandEqualsToken: "&=",
	BarEqualsToken: "|=", CaretEqualsToken: "^=", LessThanLessThanEqualsToken: "<<=",
	GreaterThanGreaterThanEqualsToken: ">>=", GreaterThanGreaterThanGreaterThanEqualsToken: ">>>=",
	QuestionToken: "?", ColonToken: ":",
}

// keywordText is the reverse of textToKeyword (kind -> spelling).
var keywordText = func() map[SyntaxKind]string {
	m := make(map[SyntaxKind]string, len(textToKeyword))
	for text, kind := range textToKeyword {
		m[kind] = text
	}
	return m
}()

// tokenToString is the canonical spelling of a punctuation or keyword token, or
// "" for others (identifiers, literals, EOF). Port of utilities.ts tokenToString.
func tokenToString(kind SyntaxKind) string {
	if s, ok := punctuationText[kind]; ok {
		return s
	}
	return keywordText[kind]
}
