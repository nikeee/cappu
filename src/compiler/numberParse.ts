// Helpers for validating a literal string passed to Integer.parseInt and its
// siblings (Long/Short/Byte parse*/valueOf). Only definite failures are
// reported: a radix outside [2, 36], or a string with a digit that is not valid
// in the radix - both guaranteed NumberFormatExceptions. Overflow (a value too
// large for the target type) is intentionally not checked, to stay false-positive
// free. Mirrored by togo/internal/compiler/numberparse.go.

export const MIN_RADIX = 2;
export const MAX_RADIX = 36;

/** The value of a Java digit character, or -1 (mirrors Character.digit). */
function digitValue(ch: string): number {
  const c = ch.charCodeAt(0);
  if (c >= 48 && c <= 57) return c - 48; // 0-9
  if (c >= 65 && c <= 90) return c - 65 + 10; // A-Z
  if (c >= 97 && c <= 122) return c - 97 + 10; // a-z
  return -1;
}

/** Whether `s` parses as an integer in `radix` (Java Integer.parseInt rules). */
export function isParseableInteger(s: string, radix: number): boolean {
  let body = s;
  if (s.startsWith("+") || s.startsWith("-")) body = s.slice(1);
  if (body.length === 0) return false; // "", "+", "-"
  for (const ch of body) {
    const d = digitValue(ch);
    if (d < 0 || d >= radix) return false;
  }
  return true;
}
