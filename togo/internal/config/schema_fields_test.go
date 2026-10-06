package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The schema is a checked-in asset the binary embeds and `cappu init
// --with-schema` writes, so nothing regenerates it when a config field is
// added: this is the guard that was the zod schema's job. Every field of the
// config carries a property in the schema, and every property is a field.
func TestJSONSchemaCoversEveryConfigField(t *testing.T) {
	text, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(text), &schema); err != nil {
		t.Fatal(err)
	}
	inSchema := map[string]bool{}
	collectProperties(schema, inSchema)
	inConfig := map[string]bool{}
	collectFields(reflect.TypeOf(Config{}), inConfig)
	for name := range inConfig {
		if !inSchema[name] {
			t.Errorf("the schema has no property for the %q field", name)
		}
	}
	for name := range inSchema {
		if !inConfig[name] {
			t.Errorf("the config has no field for the %q property", name)
		}
	}
}

// collectProperties walks a JSON Schema and records every property name.
func collectProperties(node any, out map[string]bool) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "properties" {
				if properties, ok := child.(map[string]any); ok {
					for name := range properties {
						out[name] = true
					}
				}
			}
			collectProperties(child, out)
		}
	case []any:
		for _, child := range value {
			collectProperties(child, out)
		}
	}
}

// collectFields records the JSON name of every field of a config struct, and
// of the structs it holds.
func collectFields(at reflect.Type, out map[string]bool) {
	for at.Kind() == reflect.Pointer || at.Kind() == reflect.Slice || at.Kind() == reflect.Map {
		at = at.Elem()
	}
	if at.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < at.NumField(); i++ {
		field := at.Field(i)
		tag, ok := field.Tag.Lookup("json")
		if !ok {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
		collectFields(field.Type, out)
	}
}
