package config

import "testing"

func TestIsValidSpdxExpression(t *testing.T) {
	valid := []string{
		"MIT",
		"Apache-2.0",
		"(MIT OR Apache-2.0)",
		"GPL-2.0-only WITH Classpath-exception-2.0",
		"MIT AND (Apache-2.0 OR BSD-3-Clause)",
		"LGPL-2.1+",
	}
	for _, e := range valid {
		if !IsValidSpdxExpression(e) {
			t.Errorf("expected %q to be valid", e)
		}
	}
	invalid := []string{
		"",
		"The Apache Software License, Version 2.0",
		"MIT OR",
		"(MIT",
		"MIT)",
		"NotARealLicense",
		"MIT WITH NotAnException",
	}
	for _, e := range invalid {
		if IsValidSpdxExpression(e) {
			t.Errorf("expected %q to be invalid", e)
		}
	}
}

func TestSpdxBranchCoverage(t *testing.T) {
	valid := []string{
		"MIT WITH Classpath-exception-2.0 OR Apache-2.0", // an operand continues after a WITH clause
		"(MIT OR (Apache-2.0 AND ISC))",                  // nested groups
		"GPL-2.0-or-later+",                              // or-later suffix
	}
	for _, e := range valid {
		if !IsValidSpdxExpression(e) {
			t.Errorf("expected %q to be valid", e)
		}
	}
	invalid := []string{
		"MIT WITH",              // WITH with no following exception
		"MIT WITH MIT",          // the exception slot holds a license id, not an exception
		"AND MIT",               // an operator where an operand is expected
		"MIT AND OR Apache-2.0", // two operators in a row
		"(MIT))",                // unbalanced: an extra close paren
		"((MIT)",                // unbalanced: an unclosed group
	}
	for _, e := range invalid {
		if IsValidSpdxExpression(e) {
			t.Errorf("expected %q to be invalid", e)
		}
	}
}
