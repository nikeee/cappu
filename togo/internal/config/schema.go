package config

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// JSONSchema is the JSON Schema for cappu.json that `cappu init --with-schema`
// writes (the $schema entry points editors at it). It is reflected from the
// Config struct - a best-effort editor aid, not a byte-for-byte match of the
// Node build's zod-generated schema (which also carries enums, regex patterns
// and descriptions). Regenerated freely; not user-edited.
func JSONSchema() (string, error) {
	reflector := &jsonschema.Reflector{DoNotReference: true}
	schema := reflector.Reflect(&Config{})
	// Every config field has a default (or is genuinely optional), so nothing is
	// required. The Node build's zod schema (io: "input") marks zero properties
	// required for the same reason; the reflector would otherwise require every
	// non-omitempty field and editors would flag valid configs (nikeee/cappu#29).
	clearRequired(schema)
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

// clearRequired strips "required" from a schema and every nested subschema.
func clearRequired(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	s.Required = nil
	if s.Properties != nil {
		for pair := s.Properties.Oldest(); pair != nil; pair = pair.Next() {
			clearRequired(pair.Value)
		}
	}
	clearRequired(s.Items)
	clearRequired(s.AdditionalProperties)
	for _, sub := range s.PrefixItems {
		clearRequired(sub)
	}
	for _, sub := range s.PatternProperties {
		clearRequired(sub)
	}
	for _, sub := range s.Definitions {
		clearRequired(sub)
	}
}
