package config

import (
	"strings"
	"testing"
)

// The sjson edit path must change only the targeted value and leave comments
// and formatting intact - the property `cappu version`/`add`/`update` rely on
// (the Go stand-in for comment-json round-tripping).
func TestSetStringFieldPreservesComments(t *testing.T) {
	text := []byte(`{
  // the project version
  "version": "1.2.3",
  "groupId": "com.example"
}`)
	updated, err := SetStringField(text, "version", "1.2.4")
	if err != nil {
		t.Fatal(err)
	}
	got := string(updated)
	if !strings.Contains(got, `"version": "1.2.4"`) {
		t.Errorf("version not updated:\n%s", got)
	}
	if !strings.Contains(got, "// the project version") {
		t.Errorf("comment was dropped:\n%s", got)
	}
	if !strings.Contains(got, `"groupId": "com.example"`) {
		t.Errorf("sibling field was disturbed:\n%s", got)
	}
}

func TestSetDependencyPreservesComments(t *testing.T) {
	text := []byte(`{
  "dependencies": {
    // app deps
    "implementation": {
      "org.slf4j:slf4j-api": "2.0.0"
    }
  }
}`)
	// The dotted key must land verbatim, not as a nested object.
	updated, err := SetDependency(text, "implementation", "com.google.code.gson:gson", "2.14.0")
	if err != nil {
		t.Fatal(err)
	}
	got := string(updated)
	// sjson inserts new keys compactly (no space after the colon); the key must
	// still land verbatim - the dots are escaped, not treated as nesting.
	if !strings.Contains(got, `"com.google.code.gson:gson":"2.14.0"`) {
		t.Errorf("dependency not set verbatim:\n%s", got)
	}
	if !strings.Contains(got, "// app deps") {
		t.Errorf("comment was dropped:\n%s", got)
	}
	if !strings.Contains(got, `"org.slf4j:slf4j-api": "2.0.0"`) {
		t.Errorf("sibling dependency disturbed:\n%s", got)
	}
}
