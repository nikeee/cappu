package compiler

import (
	"regexp"
	"strconv"
)

// Diagnostic message table and helpers. Mirrors the TS compiler's Diagnostics
// object: each message is a stable {code, key, category, message} record, with
// {0}, {1}, ... placeholders filled by FormatMessage. Codes are stable within
// this project. Port of src/compiler/diagnostics.ts.

// DiagnosticCategory is a diagnostic's severity.
type DiagnosticCategory int

// DiagnosticKey is a message's stable machine identifier (e.g. "Identifier_expected").
type DiagnosticKey string

// DiagnosticCode is a diagnostic's stable numeric code, distinct from a source
// offset or line.
type DiagnosticCode int

const (
	CategoryError DiagnosticCategory = iota
	CategoryWarning
	CategoryMessage
	// CategorySuggestion is appended here rather than mirroring the TS enum
	// order; codes are the stable cross-language contract, not ordinals.
	CategorySuggestion
)

// DiagnosticMessage is one entry of the message table.
type DiagnosticMessage struct {
	Code     DiagnosticCode
	Key      DiagnosticKey
	Category DiagnosticCategory
	Message  string
}

// Diagnostic is a message stamped with a source location.
type Diagnostic struct {
	Pos         int
	End         int
	Code        DiagnosticCode
	Category    DiagnosticCategory
	MessageText string
}

// ErrorCallback receives a scanner/parser error with its location.
type ErrorCallback func(message DiagnosticMessage, pos, length int)

func diag(code DiagnosticCode, key DiagnosticKey, message string, category DiagnosticCategory) DiagnosticMessage {
	return DiagnosticMessage{Code: code, Key: key, Category: category, Message: message}
}

// Diagnostics is the message table.
var Diagnostics = struct {
	Expected0                             DiagnosticMessage
	IdentifierExpected                    DiagnosticMessage
	DeclarationOrStatementExpected        DiagnosticMessage
	StatementExpected                     DiagnosticMessage
	ExpressionExpected                    DiagnosticMessage
	TypeExpected                          DiagnosticMessage
	UnexpectedToken                       DiagnosticMessage
	TrailingCommaNotAllowed               DiagnosticMessage
	DeclarationExpected                   DiagnosticMessage
	ParameterDeclarationExpected          DiagnosticMessage
	UnterminatedStringLiteral             DiagnosticMessage
	UnterminatedCharacterLiteral          DiagnosticMessage
	UnterminatedComment                   DiagnosticMessage
	DigitExpected                         DiagnosticMessage
	HexadecimalDigitExpected              DiagnosticMessage
	BinaryDigitExpected                   DiagnosticMessage
	InvalidCharacter                      DiagnosticMessage
	DuplicateDeclaration0                 DiagnosticMessage
	IncompatibleTypes01                   DiagnosticMessage
	MethodDoesNotOverrideASupertypeMethod DiagnosticMessage
	SwitchExpressionNotExhaustive0        DiagnosticMessage
	CannotResolveMember0In1               DiagnosticMessage
	InvalidNumberOfArgumentsExpected0Got1 DiagnosticMessage
	UnusedImport0                         DiagnosticMessage
	Deprecated0                           DiagnosticMessage
	PossiblyNullValueAssignedToNonNull0   DiagnosticMessage
	DereferenceOfPossiblyNullValue0       DiagnosticMessage
	FormatNotEnoughArguments01            DiagnosticMessage
	FormatTooManyArguments01              DiagnosticMessage
	FormatConversionIncompatible01        DiagnosticMessage
	InvalidRegularExpression0             DiagnosticMessage
	InvalidDateTimePatternLetter0         DiagnosticMessage
	SuspiciousDateTimePatternLetter012    DiagnosticMessage
	String0IsNotAValid1                   DiagnosticMessage
	Radix0OutOfRange                      DiagnosticMessage
	Field0CanBeFinal                      DiagnosticMessage
}{
	Expected0:                             diag(1001, "_0_expected", "'{0}' expected.", CategoryError),
	IdentifierExpected:                    diag(1002, "Identifier_expected", "Identifier expected.", CategoryError),
	DeclarationOrStatementExpected:        diag(1003, "Declaration_or_statement_expected", "Declaration or statement expected.", CategoryError),
	StatementExpected:                     diag(1004, "Statement_expected", "Statement expected.", CategoryError),
	ExpressionExpected:                    diag(1005, "Expression_expected", "Expression expected.", CategoryError),
	TypeExpected:                          diag(1006, "Type_expected", "Type expected.", CategoryError),
	UnexpectedToken:                       diag(1007, "Unexpected_token", "Unexpected token.", CategoryError),
	TrailingCommaNotAllowed:               diag(1008, "Trailing_comma_not_allowed", "Trailing comma not allowed.", CategoryError),
	DeclarationExpected:                   diag(1020, "Declaration_expected", "Declaration expected.", CategoryError),
	ParameterDeclarationExpected:          diag(1021, "Parameter_declaration_expected", "Parameter declaration expected.", CategoryError),
	UnterminatedStringLiteral:             diag(1100, "Unterminated_string_literal", "Unterminated string literal.", CategoryError),
	UnterminatedCharacterLiteral:          diag(1101, "Unterminated_character_literal", "Unterminated character literal.", CategoryError),
	UnterminatedComment:                   diag(1102, "Unterminated_comment", "'*/' expected.", CategoryError),
	DigitExpected:                         diag(1103, "Digit_expected", "Digit expected.", CategoryError),
	HexadecimalDigitExpected:              diag(1104, "Hexadecimal_digit_expected", "Hexadecimal digit expected.", CategoryError),
	BinaryDigitExpected:                   diag(1105, "Binary_digit_expected", "Binary digit expected.", CategoryError),
	InvalidCharacter:                      diag(1106, "Invalid_character", "Invalid character.", CategoryError),
	DuplicateDeclaration0:                 diag(1200, "Duplicate_declaration_0", "Duplicate declaration '{0}'.", CategoryError),
	IncompatibleTypes01:                   diag(1300, "Incompatible_types_0_1", "Incompatible types: '{0}' cannot be converted to '{1}'.", CategoryError),
	MethodDoesNotOverrideASupertypeMethod: diag(1301, "Method_does_not_override_a_supertype_method", "Method does not override or implement a method from a supertype.", CategoryError),
	SwitchExpressionNotExhaustive0:        diag(1302, "Switch_expression_not_exhaustive_0", "Switch expression does not cover all values of '{0}' (no default).", CategoryError),
	CannotResolveMember0In1:               diag(1303, "Cannot_resolve_member_0_in_1", "Cannot resolve symbol '{0}' in type '{1}'.", CategoryError),
	InvalidNumberOfArgumentsExpected0Got1: diag(1304, "Invalid_number_of_arguments_expected_0_got_1", "Invalid number of arguments: expected {0}, got {1}.", CategoryError),
	UnusedImport0:                         diag(1305, "Unused_import_0", "Unused import '{0}'.", CategoryWarning),
	Deprecated0:                           diag(1306, "_0_is_deprecated", "'{0}' is deprecated.", CategoryWarning),
	PossiblyNullValueAssignedToNonNull0:   diag(1307, "Possibly_null_value_assigned_to_non_null_0", "'{0}' is non-null but the assigned value may be null.", CategoryWarning),
	DereferenceOfPossiblyNullValue0:       diag(1308, "Dereference_of_possibly_null_value_0", "'{0}' may be null when dereferenced here.", CategoryWarning),
	FormatNotEnoughArguments01:            diag(1309, "Format_not_enough_arguments_0_1", "Not enough arguments for format string: it references {0} but only {1} were provided.", CategoryWarning),
	FormatTooManyArguments01:              diag(1310, "Format_too_many_arguments_0_1", "Too many arguments for format string: it uses {0} but {1} were provided.", CategoryWarning),
	FormatConversionIncompatible01:        diag(1311, "Format_conversion_incompatible_0_1", "Format conversion '%{0}' cannot accept an argument of type '{1}'.", CategoryWarning),
	InvalidRegularExpression0:             diag(1312, "Invalid_regular_expression_0", "Invalid regular expression: {0}.", CategoryWarning),
	InvalidDateTimePatternLetter0:         diag(1313, "Invalid_date_time_pattern_letter_0", "Invalid date/time pattern letter '{0}'.", CategoryWarning),
	SuspiciousDateTimePatternLetter012:    diag(1314, "Suspicious_date_time_pattern_letter_0_1_2", "Pattern letter '{0}' means {1}; did you mean '{2}'?", CategoryWarning),
	String0IsNotAValid1:                   diag(1315, "String_0_is_not_a_valid_1", "'{0}' is not a valid {1}.", CategoryWarning),
	Radix0OutOfRange:                      diag(1316, "Radix_0_out_of_range", "Radix {0} is out of range (must be between 2 and 36).", CategoryWarning),
	Field0CanBeFinal:                      diag(1317, "Field_0_can_be_final", "Field '{0}' can be 'final'.", CategorySuggestion),
}

var placeholderRe = regexp.MustCompile(`\{(\d+)\}`)

// FormatMessage replaces {0}, {1}, ... placeholders in a message template.
func FormatMessage(message DiagnosticMessage, args ...string) string {
	return placeholderRe.ReplaceAllStringFunc(message.Message, func(whole string) string {
		index, _ := strconv.Atoi(whole[1 : len(whole)-1])
		if index < len(args) {
			return args[index]
		}
		return whole
	})
}

// CreateDiagnostic stamps a message with a source location.
func CreateDiagnostic(start, length int, message DiagnosticMessage, args ...string) Diagnostic {
	return Diagnostic{
		Pos:         start,
		End:         start + length,
		Code:        message.Code,
		Category:    message.Category,
		MessageText: FormatMessage(message, args...),
	}
}
