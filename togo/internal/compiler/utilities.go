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

// skipTrivia advances past leading trivia (whitespace, line breaks, line and
// block comments) starting at pos. Node positions include leading trivia, so
// this maps a node's pos to where its actual token text begins. Port of
// src/compiler/utilities.ts.
func skipTrivia(text string, pos int) int {
	length := len(text)
	for pos < length {
		ch := text[pos]
		// space, tab, vertical tab, form feed, line feed, carriage return
		if ch == 0x20 || ch == 0x09 || ch == 0x0b || ch == 0x0c || ch == 0x0a || ch == 0x0d {
			pos++
			continue
		}
		if ch == '/' {
			var next byte
			if pos+1 < length {
				next = text[pos+1]
			}
			if next == '/' {
				pos += 2
				for pos < length && text[pos] != 0x0a && text[pos] != 0x0d {
					pos++
				}
				continue
			}
			if next == '*' {
				pos += 2
				for pos < length && (text[pos] != '*' || pos+1 >= length || text[pos+1] != '/') {
					pos++
				}
				pos += 2
				continue
			}
		}
		break
	}
	if pos > length {
		return length
	}
	return pos
}

// entityNameToString renders an EntityName (Identifier or QualifiedName) as a
// dotted string. Port of src/compiler/utilities.ts.
func entityNameToString(name *Node) string {
	if name.Kind == Identifier {
		return name.AsIdentifier().Text
	}
	q := name.AsQualifiedName()
	return entityNameToString(q.Left) + "." + q.Right.AsIdentifier().Text
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
