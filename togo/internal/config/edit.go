package config

import (
	"strings"

	"github.com/tidwall/sjson"
)

// SetStringField sets a top-level string field in the JSONC config text,
// preserving comments and formatting. sjson rewrites only the targeted value's
// span and leaves the surrounding bytes (including comments and trailing
// commas) untouched - the Go stand-in for comment-json's round-tripping used by
// `cappu version`/`add`/`update`. The path is a plain top-level key.
func SetStringField(text []byte, key, value string) ([]byte, error) {
	return sjson.SetBytes(text, key, value)
}

// SetDependency inserts or overwrites dependencies.<configuration>.<key> in the
// JSONC config text, preserving comments. The dependency key ("group:artifact")
// contains dots, which are sjson's path separator, so they are escaped. Port of
// addDependencyToJsonc / applyBumpsToJsonc.
func SetDependency(text []byte, configuration, key, version string) ([]byte, error) {
	path := "dependencies." + configuration + "." + escapePathKey(key)
	return sjson.SetBytes(text, path, version)
}

// escapePathKey escapes the characters sjson treats specially in a path so a
// literal map key (e.g. "com.google.code.gson:gson") is used verbatim.
func escapePathKey(key string) string {
	r := strings.NewReplacer(`\`, `\\`, `.`, `\.`, `*`, `\*`, `?`, `\?`)
	return r.Replace(key)
}
