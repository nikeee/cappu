// Diagnostic message table and construction helpers. Mirrors the TS compiler's
// Diagnostics object: each message is a stable {key, code, category, message}
// record, and createDiagnostic stamps one with a source location.
//
// Messages use {0}, {1}, ... placeholders filled by formatMessage. Codes are
// arbitrary but stable within this project (no relation to javac/tsc codes).

import {
  type Diagnostic,
  DiagnosticCategory,
  type DiagnosticCode,
  type DiagnosticKey,
  type DiagnosticMessage,
} from "./types.ts";

function diag(
  code: number,
  key: string,
  message: string,
  category: DiagnosticCategory = DiagnosticCategory.Error,
): DiagnosticMessage {
  // The single brand boundary: the table literals stay plain number/string.
  return { code: code as DiagnosticCode, key: key as DiagnosticKey, category, message };
}

export const Diagnostics = {
  _0_expected: diag(1001, "_0_expected", "'{0}' expected."),
  Identifier_expected: diag(1002, "Identifier_expected", "Identifier expected."),
  Declaration_or_statement_expected: diag(
    1003,
    "Declaration_or_statement_expected",
    "Declaration or statement expected.",
  ),
  Statement_expected: diag(1004, "Statement_expected", "Statement expected."),
  Expression_expected: diag(1005, "Expression_expected", "Expression expected."),
  Type_expected: diag(1006, "Type_expected", "Type expected."),
  Unexpected_token: diag(1007, "Unexpected_token", "Unexpected token."),
  Trailing_comma_not_allowed: diag(
    1008,
    "Trailing_comma_not_allowed",
    "Trailing comma not allowed.",
  ),

  // Type declaration members / class body
  Declaration_expected: diag(1020, "Declaration_expected", "Declaration expected."),
  Parameter_declaration_expected: diag(
    1021,
    "Parameter_declaration_expected",
    "Parameter declaration expected.",
  ),

  // Scanner
  Unterminated_string_literal: diag(
    1100,
    "Unterminated_string_literal",
    "Unterminated string literal.",
  ),
  Unterminated_character_literal: diag(
    1101,
    "Unterminated_character_literal",
    "Unterminated character literal.",
  ),
  Unterminated_comment: diag(1102, "Unterminated_comment", "'*/' expected."),
  Digit_expected: diag(1103, "Digit_expected", "Digit expected."),
  Hexadecimal_digit_expected: diag(
    1104,
    "Hexadecimal_digit_expected",
    "Hexadecimal digit expected.",
  ),
  Binary_digit_expected: diag(1105, "Binary_digit_expected", "Binary digit expected."),
  Invalid_character: diag(1106, "Invalid_character", "Invalid character."),

  // Binder
  Duplicate_declaration_0: diag(1200, "Duplicate_declaration_0", "Duplicate declaration '{0}'."),

  // Checker (semantic)
  Incompatible_types_0_1: diag(
    1300,
    "Incompatible_types_0_1",
    "Incompatible types: '{0}' cannot be converted to '{1}'.",
  ),
  Method_does_not_override_a_supertype_method: diag(
    1301,
    "Method_does_not_override_a_supertype_method",
    "Method does not override or implement a method from a supertype.",
  ),
  Switch_expression_not_exhaustive_0: diag(
    1302,
    "Switch_expression_not_exhaustive_0",
    "Switch expression does not cover all values of '{0}' (no default).",
  ),
  Cannot_resolve_member_0_in_1: diag(
    1303,
    "Cannot_resolve_member_0_in_1",
    "Cannot resolve symbol '{0}' in type '{1}'.",
  ),
  Invalid_number_of_arguments_expected_0_got_1: diag(
    1304,
    "Invalid_number_of_arguments_expected_0_got_1",
    "Invalid number of arguments: expected {0}, got {1}.",
  ),
  Unused_import_0: diag(
    1305,
    "Unused_import_0",
    "Unused import '{0}'.",
    DiagnosticCategory.Warning,
  ),
  _0_is_deprecated: diag(
    1306,
    "_0_is_deprecated",
    "'{0}' is deprecated.",
    DiagnosticCategory.Warning,
  ),
  Possibly_null_value_assigned_to_non_null_0: diag(
    1307,
    "Possibly_null_value_assigned_to_non_null_0",
    "'{0}' is non-null but the assigned value may be null.",
    DiagnosticCategory.Warning,
  ),
  Dereference_of_possibly_null_value_0: diag(
    1308,
    "Dereference_of_possibly_null_value_0",
    "'{0}' may be null when dereferenced here.",
    DiagnosticCategory.Warning,
  ),
  Format_not_enough_arguments_0_1: diag(
    1309,
    "Format_not_enough_arguments_0_1",
    "Not enough arguments for format string: it references {0} but only {1} were provided.",
    DiagnosticCategory.Warning,
  ),
  Format_too_many_arguments_0_1: diag(
    1310,
    "Format_too_many_arguments_0_1",
    "Too many arguments for format string: it uses {0} but {1} were provided.",
    DiagnosticCategory.Warning,
  ),
  Format_conversion_incompatible_0_1: diag(
    1311,
    "Format_conversion_incompatible_0_1",
    "Format conversion '%{0}' cannot accept an argument of type '{1}'.",
    DiagnosticCategory.Warning,
  ),
  Invalid_regular_expression_0: diag(
    1312,
    "Invalid_regular_expression_0",
    "Invalid regular expression: {0}.",
    DiagnosticCategory.Warning,
  ),
  Invalid_date_time_pattern_letter_0: diag(
    1313,
    "Invalid_date_time_pattern_letter_0",
    "Invalid date/time pattern letter '{0}'.",
    DiagnosticCategory.Warning,
  ),
  Suspicious_date_time_pattern_letter_0_1_2: diag(
    1314,
    "Suspicious_date_time_pattern_letter_0_1_2",
    "Pattern letter '{0}' means {1}; did you mean '{2}'?",
    DiagnosticCategory.Warning,
  ),
  String_0_is_not_a_valid_1: diag(
    1315,
    "String_0_is_not_a_valid_1",
    "'{0}' is not a valid {1}.",
    DiagnosticCategory.Warning,
  ),
  Radix_0_out_of_range: diag(
    1316,
    "Radix_0_out_of_range",
    "Radix {0} is out of range (must be between 2 and 36).",
    DiagnosticCategory.Warning,
  ),
  Field_0_can_be_final: diag(
    1317,
    "Field_0_can_be_final",
    "Field '{0}' can be 'final'.",
    DiagnosticCategory.Suggestion,
  ),
  Optional_ofNullable_ifPresent_can_be_replaced_with_a_null_check: diag(
    1318,
    "Optional_ofNullable_ifPresent_can_be_replaced_with_a_null_check",
    "'Optional.ofNullable(...).ifPresent(...)' can be replaced with a null check.",
    DiagnosticCategory.Warning,
  ),
  Optional_get_0_called_without_an_isPresent_guard: diag(
    1319,
    "Optional_get_0_called_without_an_isPresent_guard",
    "'{0}.get()' is called without a preceding 'isPresent()'/'isEmpty()' check in this method.",
    DiagnosticCategory.Warning,
  ),
  Count_check_0_can_be_replaced_with_1: diag(
    1320,
    "Count_check_0_can_be_replaced_with_1",
    "'{0}' can be replaced with '{1}'.",
    DiagnosticCategory.Warning,
  ),
  Strings_should_be_compared_with_equals_not_0: diag(
    1321,
    "Strings_should_be_compared_with_equals_not_0",
    "Strings should be compared with 'equals()', not '{0}'.",
    DiagnosticCategory.Warning,
  ),
  Boxing_constructor_new_0_is_deprecated: diag(
    1322,
    "Boxing_constructor_new_0_is_deprecated",
    "'new {0}(...)' is deprecated; use '{0}.valueOf(...)' instead.",
    DiagnosticCategory.Warning,
  ),
  IndexOf_check_0_can_be_replaced_with_1: diag(
    1323,
    "IndexOf_check_0_can_be_replaced_with_1",
    "'{0}' can be replaced with '{1}'.",
    DiagnosticCategory.Warning,
  ),
  New_String_0_can_be_replaced_with_1: diag(
    1324,
    "New_String_0_can_be_replaced_with_1",
    "'{0}' can be replaced with '{1}'.",
    DiagnosticCategory.Warning,
  ),
  Equals_empty_0_can_be_replaced_with_1: diag(
    1325,
    "Equals_empty_0_can_be_replaced_with_1",
    "'{0}' can be replaced with '{1}'.",
    DiagnosticCategory.Warning,
  ),
  Suspicious_self_comparison_0: diag(
    1326,
    "Suspicious_self_comparison_0",
    "'{0}' is compared to itself.",
    DiagnosticCategory.Warning,
  ),
  Boxed_types_should_be_compared_with_equals_not_0: diag(
    1327,
    "Boxed_types_should_be_compared_with_equals_not_0",
    "Boxed types should be compared with 'equals()', not '{0}'.",
    DiagnosticCategory.Warning,
  ),
  Empty_catch_block_for_0: diag(
    1328,
    "Empty_catch_block_for_0",
    "The exception '{0}' is caught and silently discarded.",
    DiagnosticCategory.Warning,
  ),
  Optional_of_null_always_throws: diag(
    1329,
    "Optional_of_null_always_throws",
    "'Optional.of(null)' always throws; use 'Optional.ofNullable(null)' or 'Optional.empty()'.",
    DiagnosticCategory.Warning,
  ),
  Redundant_boolean_comparison_0_can_be_replaced_with_1: diag(
    1330,
    "Redundant_boolean_comparison_0_can_be_replaced_with_1",
    "'{0}' can be replaced with '{1}'.",
    DiagnosticCategory.Warning,
  ),
  If_else_returning_booleans_0_can_be_replaced_with_1: diag(
    1331,
    "If_else_returning_booleans_0_can_be_replaced_with_1",
    "'{0}' can be replaced with '{1}'.",
    DiagnosticCategory.Warning,
  ),
  Ternary_with_boolean_literals_0_can_be_replaced_with_1: diag(
    1332,
    "Ternary_with_boolean_literals_0_can_be_replaced_with_1",
    "'{0}' can be replaced with '{1}'.",
    DiagnosticCategory.Warning,
  ),
  Nested_if_can_be_collapsed_to_if_0: diag(
    1333,
    "Nested_if_can_be_collapsed_to_if_0",
    "Nested 'if' statements can be collapsed to 'if ({0})'.",
    DiagnosticCategory.Warning,
  ),
  _0_1_should_not_be_of_type_Optional: diag(
    1334,
    "_0_1_should_not_be_of_type_Optional",
    "{0} '{1}' should not be of type 'Optional'; prefer a nullable type or an overload.",
    DiagnosticCategory.Warning,
  ),
  Indexed_loop_over_0_can_be_a_for_each_loop: diag(
    1335,
    "Indexed_loop_over_0_can_be_a_for_each_loop",
    "This indexed loop over '{0}' can be a for-each loop.",
    DiagnosticCategory.Warning,
  ),
} as const;

/** Replace {0}, {1}, ... placeholders in a message template. */
export function formatMessage(message: DiagnosticMessage, args: readonly string[]): string {
  return message.message.replace(/\{(\d+)\}/g, (whole, indexText: string) => {
    const index = Number(indexText);
    return index < args.length ? args[index]! : whole;
  });
}

export function createDiagnostic(
  start: number,
  length: number,
  message: DiagnosticMessage,
  ...args: string[]
): Diagnostic {
  return {
    pos: start,
    end: start + length,
    code: message.code,
    category: message.category,
    messageText: formatMessage(message, args),
  };
}
