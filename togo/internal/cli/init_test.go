package cli

import (
	"strings"
	"testing"
)

func TestSanitizeID(t *testing.T) {
	cases := map[string]string{
		"my-app":      "my-app",
		"My App!":     "My-App",
		"--weird--":   "weird",
		"com.example": "com.example",
		"":            "app",
		"@@@":         "app",
	}
	for in, want := range cases {
		if got := sanitizeID(in); got != want {
			t.Errorf("sanitizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderInitConfig(t *testing.T) {
	got := renderInitConfig(initAnswers{GroupID: "com.example", ArtifactID: "lib", Version: "2.0.0", Output: "jar"})
	// key order and content
	for _, want := range []string{
		`"$schema": "./cappu.schema.json"`,
		`"groupId": "com.example"`,
		`"artifactId": "lib"`,
		`"version": "2.0.0"`,
		`"output": "jar"`,
		`"annotationProcessor": {}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered config missing %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "{\n  \"$schema\"") {
		t.Errorf("$schema should be first:\n%s", got)
	}
}
