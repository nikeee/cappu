// Conservative structural validator for java.util.regex patterns. Returns a
// human-readable reason when the pattern is DEFINITELY malformed (a guaranteed
// PatternSyntaxException at runtime), or undefined otherwise. It deliberately
// only catches unambiguous breakage - unbalanced groups/classes and a trailing
// backslash - so a literal that merely looks unusual is never flagged. Mirrored
// by togo/internal/compiler/regexvalidate.go.

/** Reason string when `re` is provably malformed, else undefined. */
export function validateRegex(re: string): string | undefined {
  let paren = 0;
  let cls = 0; // character-class nesting depth ([a-z&&[^b]] nests in Java)
  let i = 0;
  while (i < re.length) {
    const ch = re[i];
    if (ch === "\\") {
      if (i + 1 >= re.length) return "trailing backslash";
      if (re[i + 1] === "Q") {
        // \Q...\E is a literal region; skip it whole.
        const end = re.indexOf("\\E", i + 2);
        i = end === -1 ? re.length : end + 2;
        continue;
      }
      i += 2; // an escaped metacharacter
      continue;
    }
    if (cls > 0) {
      if (ch === "[") cls++;
      else if (ch === "]") cls--;
      i++;
      continue;
    }
    if (ch === "[") cls++;
    else if (ch === "(") paren++;
    else if (ch === ")") {
      if (paren === 0) return "unmatched ')'";
      paren--;
    }
    // a bare ']' outside a class is a literal in Java, not an error
    i++;
  }
  if (cls > 0) return "unclosed character class '['";
  if (paren > 0) return "unclosed group '('";
  return undefined;
}
