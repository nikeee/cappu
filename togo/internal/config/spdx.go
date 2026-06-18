package config

import "strings"

// SPDX validation for the cappu.json "license" field: an npm-style SPDX license
// expression. Only SPDX is accepted, so a free-text name is rejected. Ids are
// checked against a curated set of the identifiers Java projects use in
// practice. Port of src/spdx.ts.

var licenseIDs = map[string]struct{}{
	"0BSD": {}, "AGPL-3.0-only": {}, "AGPL-3.0-or-later": {}, "Apache-1.1": {},
	"Apache-2.0": {}, "Artistic-2.0": {}, "BSD-2-Clause": {}, "BSD-3-Clause": {},
	"BSD-4-Clause": {}, "BSL-1.0": {}, "CC0-1.0": {}, "CC-BY-4.0": {},
	"CC-BY-SA-4.0": {}, "CDDL-1.0": {}, "CDDL-1.1": {}, "EPL-1.0": {}, "EPL-2.0": {},
	"EUPL-1.2": {}, "GPL-2.0-only": {}, "GPL-2.0-or-later": {}, "GPL-3.0-only": {},
	"GPL-3.0-or-later": {}, "ISC": {}, "LGPL-2.1-only": {}, "LGPL-2.1-or-later": {},
	"LGPL-3.0-only": {}, "LGPL-3.0-or-later": {}, "MIT": {}, "MIT-0": {},
	"MPL-1.1": {}, "MPL-2.0": {}, "Unlicense": {}, "WTFPL": {}, "Zlib": {},
	// deprecated but still commonly written short forms
	"AGPL-3.0": {}, "GPL-2.0": {}, "GPL-3.0": {}, "LGPL-2.1": {}, "LGPL-3.0": {},
}

var exceptionIDs = map[string]struct{}{
	"Classpath-exception-2.0": {}, "GPL-3.0-linking-exception": {},
	"LLVM-exception": {}, "OpenJDK-assembly-exception-1.0": {},
}

// IsValidSpdxExpression reports whether expression is a valid SPDX license
// expression: ids from the known set, combined with AND / OR / parentheses, an
// optional `+` (or-later), and `<id> WITH <exception>`.
func IsValidSpdxExpression(expression string) bool {
	spaced := strings.ReplaceAll(expression, "(", " ( ")
	spaced = strings.ReplaceAll(spaced, ")", " ) ")
	tokens := strings.Fields(spaced)
	if len(tokens) == 0 {
		return false
	}

	expectOperand := true // a license id or "(" comes next
	depth := 0
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch token {
		case "(":
			if !expectOperand {
				return false
			}
			depth++
		case ")":
			if expectOperand || depth == 0 {
				return false
			}
			depth--
		case "AND", "OR":
			if expectOperand {
				return false
			}
			expectOperand = true
		default:
			if !expectOperand {
				return false
			}
			id := strings.TrimSuffix(token, "+")
			if _, ok := licenseIDs[id]; !ok {
				return false
			}
			if i+1 < len(tokens) && tokens[i+1] == "WITH" {
				if i+2 >= len(tokens) {
					return false
				}
				if _, ok := exceptionIDs[tokens[i+2]]; !ok {
					return false
				}
				i += 2
			}
			expectOperand = false
		}
	}
	return !expectOperand && depth == 0
}
