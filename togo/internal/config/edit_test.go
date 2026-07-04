package config

import (
	"strings"
	"testing"
)

// Byte-exact mirror of src/cli/jsoncEdit.test.ts - the two builds must
// produce identical files for the same edit.

const multi = `{
  // the project version
  "version": "1.2.3",
  "dependencies": {
    // app deps
    "implementation": {
      "org.slf4j:slf4j-api": "2.0.0"
    }
  }
}
`

func set(t *testing.T, text, configuration, key, version string) string {
	t.Helper()
	out, err := SetDependency([]byte(text), configuration, key, version)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestSetStringFieldReplacesOnlyTheValue(t *testing.T) {
	out, err := SetStringField([]byte(multi), "version", "1.2.4")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(multi, `"1.2.3"`, `"1.2.4"`, 1)
	if string(out) != want {
		t.Errorf("SetStringField = %s, want %s", out, want)
	}
}

func TestOverwriteDependencyKeepsFormatting(t *testing.T) {
	got := set(t, multi, "implementation", "org.slf4j:slf4j-api", "2.1.0")
	want := strings.Replace(multi, `"2.0.0"`, `"2.1.0"`, 1)
	if got != want {
		t.Errorf("overwrite = %s, want %s", got, want)
	}
}

func TestInsertDependencyAppendsInFileIndentation(t *testing.T) {
	got := set(t, multi, "implementation", "com.google.code.gson:gson", "2.14.0")
	want := strings.Replace(multi,
		`"org.slf4j:slf4j-api": "2.0.0"`,
		"\"org.slf4j:slf4j-api\": \"2.0.0\",\n      \"com.google.code.gson:gson\": \"2.14.0\"", 1)
	if got != want {
		t.Errorf("insert = %s, want %s", got, want)
	}
}

func TestInsertCreatesMissingSections(t *testing.T) {
	got := set(t, multi, "testImplementation", "org.j:junit", "5.0")
	want := strings.Replace(multi,
		"    \"implementation\": {\n      \"org.slf4j:slf4j-api\": \"2.0.0\"\n    }",
		"    \"implementation\": {\n      \"org.slf4j:slf4j-api\": \"2.0.0\"\n    },\n    \"testImplementation\": {\n      \"org.j:junit\": \"5.0\"\n    }", 1)
	if got != want {
		t.Errorf("new section = %s, want %s", got, want)
	}
}

func TestInsertIntoEmptyMultilineObjectGrowsIt(t *testing.T) {
	text := "{\n  \"dependencies\": {}\n}\n"
	got := set(t, text, "implementation", "a:b", "1.0")
	want := "{\n  \"dependencies\": {\n    \"implementation\": {\n      \"a:b\": \"1.0\"\n    }\n  }\n}\n"
	if got != want {
		t.Errorf("empty object = %s, want %s", got, want)
	}
}

func TestCompactFilesStayCompact(t *testing.T) {
	text := `{"dependencies":{"implementation":{"org.x:y":"1.0"}}}`
	got := set(t, text, "implementation", "a:b", "2.0")
	want := `{"dependencies":{"implementation":{"org.x:y":"1.0","a:b":"2.0"}}}`
	if got != want {
		t.Errorf("compact = %s, want %s", got, want)
	}
}

func TestTrailingCommaRespectedOnInsert(t *testing.T) {
	text := "{\n  \"dependencies\": {\n    \"implementation\": {\n      \"org.x:y\": \"1.0\",\n    }\n  }\n}\n"
	got := set(t, text, "implementation", "a:b", "2.0")
	want := "{\n  \"dependencies\": {\n    \"implementation\": {\n      \"org.x:y\": \"1.0\",\n      \"a:b\": \"2.0\"\n    }\n  }\n}\n"
	if got != want {
		t.Errorf("trailing comma = %s, want %s", got, want)
	}
}

func TestFourSpaceIndentationPreserved(t *testing.T) {
	text := "{\n    \"dependencies\": {\n        \"implementation\": {\n            \"org.x:y\": \"1.0\"\n        }\n    }\n}\n"
	got := set(t, text, "implementation", "a:b", "2.0")
	if !strings.Contains(got, "            \"org.x:y\": \"1.0\",\n            \"a:b\": \"2.0\"") {
		t.Errorf("4-space indent not preserved:\n%s", got)
	}
}

func TestRemoveMiddleMember(t *testing.T) {
	text := "{\n  \"dependencies\": {\n    \"implementation\": {\n      \"a:b\": \"1.0\",\n      \"c:d\": \"2.0\",\n      \"e:f\": \"3.0\"\n    }\n  }\n}\n"
	out, removed, err := RemoveDependency([]byte(text), "implementation", "c:d")
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	want := strings.Replace(text, "      \"c:d\": \"2.0\",\n", "", 1)
	if string(out) != want {
		t.Errorf("remove middle = %s, want %s", out, want)
	}
}

func TestRemoveLastMemberSwallowsPrecedingComma(t *testing.T) {
	text := "{\n  \"dependencies\": {\n    \"implementation\": {\n      \"a:b\": \"1.0\",\n      \"c:d\": \"2.0\"\n    }\n  }\n}\n"
	out, removed, err := RemoveDependency([]byte(text), "implementation", "c:d")
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	want := strings.Replace(text, ",\n      \"c:d\": \"2.0\"", "", 1)
	if string(out) != want {
		t.Errorf("remove last = %s, want %s", out, want)
	}
}

func TestRemoveOnlyMemberLeavesObject(t *testing.T) {
	text := "{\n  \"dependencies\": {\n    \"implementation\": {\n      \"a:b\": \"1.0\"\n    }\n  }\n}\n"
	out, removed, err := RemoveDependency([]byte(text), "implementation", "a:b")
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	want := "{\n  \"dependencies\": {\n    \"implementation\": {\n    }\n  }\n}\n"
	if string(out) != want {
		t.Errorf("remove only = %s, want %s", out, want)
	}
}

func TestRemoveAbsentIsNoOp(t *testing.T) {
	text := `{"dependencies":{"implementation":{"org.x:y":"1.0"}}}`
	out, removed, err := RemoveDependency([]byte(text), "implementation", "org.absent:z")
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Error("expected removed=false for an absent key")
	}
	if string(out) != text {
		t.Errorf("text changed on no-op: %s", out)
	}
	if _, removed, _ := RemoveDependency([]byte(text), "testImplementation", "org.x:y"); removed {
		t.Error("expected removed=false for a missing configuration")
	}
}

func TestCommentsSurviveEditsAroundThem(t *testing.T) {
	text := "{\n  \"dependencies\": {\n    \"implementation\": {\n      // keep me\n      \"a:b\": \"1.0\", // and me\n      \"c:d\": \"2.0\"\n    }\n  }\n}\n"
	got := set(t, text, "implementation", "e:f", "3.0")
	for _, want := range []string{"// keep me", "// and me", "\"c:d\": \"2.0\",\n      \"e:f\": \"3.0\""} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	out, removed, err := RemoveDependency([]byte(text), "implementation", "c:d")
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	for _, want := range []string{"// keep me", "// and me"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("comment lost on remove: %q missing in:\n%s", want, out)
		}
	}
}

func TestHasDependency(t *testing.T) {
	if !HasDependency([]byte(multi), "implementation", "org.slf4j:slf4j-api") {
		t.Error("expected existing dependency to be reported")
	}
	if HasDependency([]byte(multi), "api", "org.slf4j:slf4j-api") {
		t.Error("expected missing section to be reported false")
	}
}

func TestNonObjectConfigErrors(t *testing.T) {
	if _, err := SetStringField([]byte("[1,2]"), "version", "1"); err == nil || !strings.Contains(err.Error(), "does not contain an object") {
		t.Errorf("err = %v, want the shared non-object error", err)
	}
}
