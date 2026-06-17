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
