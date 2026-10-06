package config

import _ "embed"

// schemaJSON is the JSON Schema for cappu.json, checked in and embedded: it is
// what `cappu config-schema` prints and what `cappu init --with-schema`
// writes. It is edited by hand alongside the Config struct, and
// TestJSONSchemaCoversEveryConfigField fails when the two drift.
//
//go:embed cappu.schema.json
var schemaJSON string

// JSONSchema is the JSON Schema for cappu.json.
func JSONSchema() (string, error) {
	return schemaJSON, nil
}
