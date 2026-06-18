package compiler

// Token predicates shared by the parser. Port of the relevant parts of
// src/compiler/utilities.ts.

// isModifierKeyword reports whether kind is a declaration modifier keyword.
func isModifierKeyword(kind SyntaxKind) bool {
	switch kind {
	case PublicKeyword, ProtectedKeyword, PrivateKeyword, AbstractKeyword, StaticKeyword,
		FinalKeyword, NativeKeyword, SynchronizedKeyword, TransientKeyword, VolatileKeyword,
		StrictfpKeyword, DefaultKeyword:
		return true
	default:
		return false
	}
}

// isAssignmentOperator reports whether kind is `=` or a compound assignment.
func isAssignmentOperator(kind SyntaxKind) bool {
	switch kind {
	case EqualsToken, PlusEqualsToken, MinusEqualsToken, AsteriskEqualsToken, SlashEqualsToken,
		PercentEqualsToken, AmpersandEqualsToken, BarEqualsToken, CaretEqualsToken,
		LessThanLessThanEqualsToken, GreaterThanGreaterThanEqualsToken,
		GreaterThanGreaterThanGreaterThanEqualsToken:
		return true
	default:
		return false
	}
}

// isPrimitiveTypeKeyword reports whether kind is a primitive type keyword.
func isPrimitiveTypeKeyword(kind SyntaxKind) bool {
	switch kind {
	case BooleanKeyword, ByteKeyword, ShortKeyword, IntKeyword, LongKeyword,
		CharKeyword, FloatKeyword, DoubleKeyword:
		return true
	default:
		return false
	}
}
