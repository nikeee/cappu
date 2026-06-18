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
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}
