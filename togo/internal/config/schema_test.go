package config

import (
	"strings"
	"testing"
)

// Every config field has a default or is optional, so the generated schema must
// mark nothing required - else editors flag a sparse cappu.json. The Node build's
// zod schema (io: "input") has zero required properties too (nikeee/cappu#29).
func TestJSONSchemaHasNoRequired(t *testing.T) {
	s, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, `"required"`) {
		t.Fatalf("schema must not mark any property required:\n%s", s)
	}
}
