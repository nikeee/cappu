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
