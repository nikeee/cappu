package config

import "github.com/tidwall/sjson"

// SetStringField sets a top-level string field in the JSONC config text,
// preserving comments and formatting. sjson rewrites only the targeted value's
// span and leaves the surrounding bytes (including comments and trailing
// commas) untouched - the Go stand-in for comment-json's round-tripping used by
// `cappu version`/`add`/`update`. The path is a plain top-level key.
func SetStringField(text []byte, key, value string) ([]byte, error) {
	return sjson.SetBytes(text, key, value)
}
