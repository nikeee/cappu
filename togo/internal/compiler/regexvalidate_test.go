// Port of src/compiler/regexValidate.test.ts
package compiler

import (
	"strings"
	"testing"
)

func TestRegexValid(t *testing.T) {
	for _, re := range []string{"a.c", "[a-z]+", "(foo|bar)*", `\d{3}`, `a\(b`, "[a-z&&[^bc]]", "]", "[()]"} {
		if reason, bad := ValidateRegex(re); bad {
			t.Errorf("ValidateRegex(%q) = %q, want ok", re, reason)
		}
	}
}

func TestRegexUnbalanced(t *testing.T) {
	cases := map[string]string{
		"(foo": "unclosed group",
		"foo)": "unmatched",
		"[abc": "unclosed character class",
		`abc\`: "trailing backslash",
	}
	for re, want := range cases {
		reason, bad := ValidateRegex(re)
		if !bad || !strings.Contains(reason, want) {
			t.Errorf("ValidateRegex(%q) = (%q,%v), want contains %q", re, reason, bad, want)
		}
	}
}

func TestRegexQuoteRegion(t *testing.T) {
	if _, bad := ValidateRegex(`\Q(unbalanced\E`); bad {
		t.Error(`\Q...\E region should be skipped`)
	}
}
