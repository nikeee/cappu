// Port of src/compiler/regexValidate.ts
//
// Conservative structural validator for java.util.regex patterns. Returns a
// reason ("", false) when the pattern is DEFINITELY malformed (a guaranteed
// PatternSyntaxException), or ("", false)->ok=false otherwise. It only catches
// unambiguous breakage - unbalanced groups/classes and a trailing backslash.

package compiler

import "strings"

// ValidateRegex returns (reason, true) when re is provably malformed, else
// ("", false).
func ValidateRegex(re string) (string, bool) {
	paren := 0
	cls := 0 // character-class nesting depth ([a-z&&[^b]] nests in Java)
	i := 0
	for i < len(re) {
		ch := re[i]
		if ch == '\\' {
			if i+1 >= len(re) {
				return "trailing backslash", true
			}
			if re[i+1] == 'Q' {
				// \Q...\E is a literal region; skip it whole.
				end := strings.Index(re[i+2:], "\\E")
				if end == -1 {
					i = len(re)
				} else {
					i = i + 2 + end + 2
				}
				continue
			}
			i += 2 // an escaped metacharacter
			continue
		}
		if cls > 0 {
			switch ch {
			case '[':
				cls++
			case ']':
				cls--
			}
			i++
			continue
		}
		switch ch {
		case '[':
			cls++
		case '(':
			paren++
		case ')':
			if paren == 0 {
				return "unmatched ')'", true
			}
			paren--
		}
		// a bare ']' outside a class is a literal in Java, not an error
		i++
	}
	if cls > 0 {
		return "unclosed character class '['", true
	}
	if paren > 0 {
		return "unclosed group '('", true
	}
	return "", false
}
