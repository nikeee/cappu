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
