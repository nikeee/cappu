package config

import _ "embed"

// schemaJSON is the zod-generated JSON Schema for cappu.json, checked in so
// both builds print byte-identical output (`cappu config-schema`, and the
// cappu.schema.json that `cappu init --with-schema` writes). Regenerate with
// `node --run schema:write` in the repo root after changing src/config.ts;
// src/cli/configSchema.test.ts fails when this file drifts from the zod schema.
//
//go:embed cappu.schema.json
var schemaJSON string

// JSONSchema is the JSON Schema for cappu.json.
func JSONSchema() (string, error) {
	return schemaJSON, nil
}
