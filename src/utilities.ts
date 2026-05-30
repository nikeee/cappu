// Shared helpers used by the scanner, parser and binder: the keyword/punctuation
// text tables and small SyntaxKind range predicates. Mirrors the role of
// utilities in the TS compiler.

import { SyntaxKind } from "./types.ts";

// Text -> kind for every reserved word (and the reserved literals true/false/null).
// Contextual keywords (var, yield, record, sealed, ...) are intentionally absent:
// they are scanned as identifiers and recognized positionally by the parser.
export const textToKeyword: ReadonlyMap<string, SyntaxKind> = new Map([
	["abstract", SyntaxKind.AbstractKeyword],
	["assert", SyntaxKind.AssertKeyword],
	["boolean", SyntaxKind.BooleanKeyword],
	["break", SyntaxKind.BreakKeyword],
	["byte", SyntaxKind.ByteKeyword],
	["case", SyntaxKind.CaseKeyword],
	["catch", SyntaxKind.CatchKeyword],
	["char", SyntaxKind.CharKeyword],
	["class", SyntaxKind.ClassKeyword],
	["const", SyntaxKind.ConstKeyword],
	["continue", SyntaxKind.ContinueKeyword],
	["default", SyntaxKind.DefaultKeyword],
	["do", SyntaxKind.DoKeyword],
	["double", SyntaxKind.DoubleKeyword],
	["else", SyntaxKind.ElseKeyword],
	["enum", SyntaxKind.EnumKeyword],
	["extends", SyntaxKind.ExtendsKeyword],
	["final", SyntaxKind.FinalKeyword],
	["finally", SyntaxKind.FinallyKeyword],
	["float", SyntaxKind.FloatKeyword],
	["for", SyntaxKind.ForKeyword],
	["goto", SyntaxKind.GotoKeyword],
	["if", SyntaxKind.IfKeyword],
	["implements", SyntaxKind.ImplementsKeyword],
	["import", SyntaxKind.ImportKeyword],
	["instanceof", SyntaxKind.InstanceofKeyword],
	["int", SyntaxKind.IntKeyword],
	["interface", SyntaxKind.InterfaceKeyword],
	["long", SyntaxKind.LongKeyword],
	["native", SyntaxKind.NativeKeyword],
	["new", SyntaxKind.NewKeyword],
	["package", SyntaxKind.PackageKeyword],
	["private", SyntaxKind.PrivateKeyword],
	["protected", SyntaxKind.ProtectedKeyword],
	["public", SyntaxKind.PublicKeyword],
	["return", SyntaxKind.ReturnKeyword],
	["short", SyntaxKind.ShortKeyword],
	["static", SyntaxKind.StaticKeyword],
	["strictfp", SyntaxKind.StrictfpKeyword],
	["super", SyntaxKind.SuperKeyword],
	["switch", SyntaxKind.SwitchKeyword],
	["synchronized", SyntaxKind.SynchronizedKeyword],
	["this", SyntaxKind.ThisKeyword],
	["throw", SyntaxKind.ThrowKeyword],
	["throws", SyntaxKind.ThrowsKeyword],
	["transient", SyntaxKind.TransientKeyword],
	["try", SyntaxKind.TryKeyword],
	["void", SyntaxKind.VoidKeyword],
	["volatile", SyntaxKind.VolatileKeyword],
	["while", SyntaxKind.WhileKeyword],
	["true", SyntaxKind.TrueKeyword],
	["false", SyntaxKind.FalseKeyword],
	["null", SyntaxKind.NullKeyword],
]);

// Punctuation/operator kind -> spelling. Combined with the keyword spellings to
// drive tokenToString (used for "'_0_' expected" diagnostics).
const punctuationToText: ReadonlyMap<SyntaxKind, string> = new Map([
	[SyntaxKind.OpenBraceToken, "{"],
	[SyntaxKind.CloseBraceToken, "}"],
	[SyntaxKind.OpenParenToken, "("],
	[SyntaxKind.CloseParenToken, ")"],
	[SyntaxKind.OpenBracketToken, "["],
	[SyntaxKind.CloseBracketToken, "]"],
	[SyntaxKind.DotToken, "."],
	[SyntaxKind.DotDotDotToken, "..."],
	[SyntaxKind.SemicolonToken, ";"],
	[SyntaxKind.CommaToken, ","],
	[SyntaxKind.AtToken, "@"],
	[SyntaxKind.ColonColonToken, "::"],
	[SyntaxKind.ArrowToken, "->"],
	[SyntaxKind.LessThanToken, "<"],
	[SyntaxKind.GreaterThanToken, ">"],
	[SyntaxKind.LessThanEqualsToken, "<="],
	[SyntaxKind.GreaterThanEqualsToken, ">="],
	[SyntaxKind.EqualsEqualsToken, "=="],
	[SyntaxKind.ExclamationEqualsToken, "!="],
	[SyntaxKind.AmpersandAmpersandToken, "&&"],
	[SyntaxKind.BarBarToken, "||"],
	[SyntaxKind.ExclamationToken, "!"],
	[SyntaxKind.AmpersandToken, "&"],
	[SyntaxKind.BarToken, "|"],
	[SyntaxKind.CaretToken, "^"],
	[SyntaxKind.TildeToken, "~"],
	[SyntaxKind.LessThanLessThanToken, "<<"],
	[SyntaxKind.GreaterThanGreaterThanToken, ">>"],
	[SyntaxKind.GreaterThanGreaterThanGreaterThanToken, ">>>"],
	[SyntaxKind.PlusToken, "+"],
	[SyntaxKind.MinusToken, "-"],
	[SyntaxKind.AsteriskToken, "*"],
	[SyntaxKind.SlashToken, "/"],
	[SyntaxKind.PercentToken, "%"],
	[SyntaxKind.PlusPlusToken, "++"],
	[SyntaxKind.MinusMinusToken, "--"],
	[SyntaxKind.EqualsToken, "="],
	[SyntaxKind.PlusEqualsToken, "+="],
	[SyntaxKind.MinusEqualsToken, "-="],
	[SyntaxKind.AsteriskEqualsToken, "*="],
	[SyntaxKind.SlashEqualsToken, "/="],
	[SyntaxKind.PercentEqualsToken, "%="],
	[SyntaxKind.AmpersandEqualsToken, "&="],
	[SyntaxKind.BarEqualsToken, "|="],
	[SyntaxKind.CaretEqualsToken, "^="],
	[SyntaxKind.LessThanLessThanEqualsToken, "<<="],
	[SyntaxKind.GreaterThanGreaterThanEqualsToken, ">>="],
	[SyntaxKind.GreaterThanGreaterThanGreaterThanEqualsToken, ">>>="],
	[SyntaxKind.QuestionToken, "?"],
	[SyntaxKind.ColonToken, ":"],
]);

const tokenToTextMap: ReadonlyMap<SyntaxKind, string> = new Map<SyntaxKind, string>([
	...punctuationToText,
	...Array.from(textToKeyword, ([text, kind]) => [kind, text] as const),
]);

/** The canonical spelling of a punctuation or keyword token, or undefined for others. */
export function tokenToString(kind: SyntaxKind): string | undefined {
	return tokenToTextMap.get(kind);
}

export function isKeyword(kind: SyntaxKind): boolean {
	return kind >= SyntaxKind.FirstKeyword && kind <= SyntaxKind.LastKeyword;
}

export function isReservedWord(kind: SyntaxKind): boolean {
	return kind >= SyntaxKind.FirstReservedWord && kind <= SyntaxKind.LastReservedWord;
}

export function isModifierKeyword(kind: SyntaxKind): boolean {
	switch (kind) {
		case SyntaxKind.PublicKeyword:
		case SyntaxKind.ProtectedKeyword:
		case SyntaxKind.PrivateKeyword:
		case SyntaxKind.AbstractKeyword:
		case SyntaxKind.StaticKeyword:
		case SyntaxKind.FinalKeyword:
		case SyntaxKind.NativeKeyword:
		case SyntaxKind.SynchronizedKeyword:
		case SyntaxKind.TransientKeyword:
		case SyntaxKind.VolatileKeyword:
		case SyntaxKind.StrictfpKeyword:
		case SyntaxKind.DefaultKeyword:
			return true;
		default:
			return false;
	}
}

export function isPrimitiveTypeKeyword(kind: SyntaxKind): boolean {
	switch (kind) {
		case SyntaxKind.BooleanKeyword:
		case SyntaxKind.ByteKeyword:
		case SyntaxKind.ShortKeyword:
		case SyntaxKind.IntKeyword:
		case SyntaxKind.LongKeyword:
		case SyntaxKind.CharKeyword:
		case SyntaxKind.FloatKeyword:
		case SyntaxKind.DoubleKeyword:
			return true;
		default:
			return false;
	}
}

export function isLiteralKind(kind: SyntaxKind): boolean {
	return kind >= SyntaxKind.FirstLiteralToken && kind <= SyntaxKind.LastLiteralToken;
}

export function isAssignmentOperator(kind: SyntaxKind): boolean {
	return kind >= SyntaxKind.FirstAssignment && kind <= SyntaxKind.LastAssignment;
}
