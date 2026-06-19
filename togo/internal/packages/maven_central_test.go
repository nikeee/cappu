package packages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Port of src/packages/mavenCentral.test.ts: parent-chain resolution against
// real Maven Central POMs snapshotted under test-fixtures/packages/central-poms
// (same maven2 layout as the live repo), read through an injected fetcher - so
// these run offline. The TS metadata.incomplete signal has no Go equivalent
// (the Go PackageMetadata does not track it), so the incomplete-only assertions
// are skipped; the dependency-resolution assertions (the real behaviour) port.

const centralBase = "https://central.example/maven2"

func centralSource(t *testing.T) *MavenRepositorySource {
	t.Helper()
	fixtures, err := filepath.Abs("../../../test-fixtures/packages/central-poms")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixtures); err != nil {
		t.Skipf("central-poms fixtures missing: %v", err)
	}
	fetchText := func(url string) (string, bool, error) {
		file := filepath.Join(fixtures, strings.TrimPrefix(url, centralBase+"/"))
		data, err := os.ReadFile(file)
		if err != nil {
			return "", false, nil
		}
		return string(data), true, nil
	}
	return NewMavenRepositorySourceWithFetchers(centralBase, "", fetchText, nil)
}

// compileDependencies returns the deps cappu install would follow: non-optional
// compile/runtime, formatted as "group:artifact@version".
func compileDependencies(t *testing.T, src *MavenRepositorySource, group, artifact, version string) []string {
	t.Helper()
	metadata, err := src.GetMetadata(NewCoordinates(group, artifact, version))
	if err != nil {
		t.Fatalf("GetMetadata(%s:%s:%s): %v", group, artifact, version, err)
	}
	if metadata == nil {
		t.Fatalf("GetMetadata(%s:%s:%s) = nil", group, artifact, version)
	}
	var out []string
	for _, d := range metadata.Dependencies {
		if d.Optional {
			continue
		}
		if d.Scope == "" || d.Scope == "compile" || d.Scope == "runtime" {
			out = append(out, string(d.GroupID)+":"+string(d.ArtifactID)+"@"+string(d.Version))
		}
	}
	return out
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestJacksonDatabindParentProperties(t *testing.T) {
	src := centralSource(t)
	got := compileDependencies(t, src, "com.fasterxml.jackson.core", "jackson-databind", "2.18.3")
	want := []string{
		"com.fasterxml.jackson.core:jackson-annotations@2.18.3",
		"com.fasterxml.jackson.core:jackson-core@2.18.3",
	}
	if !eqStrs(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestHttpclient5ParentProperties(t *testing.T) {
	src := centralSource(t)
	got := compileDependencies(t, src, "org.apache.httpcomponents.client5", "httpclient5", "5.4.3")
	want := []string{
		"org.apache.httpcomponents.core5:httpcore5@5.3.4",
		"org.apache.httpcomponents.core5:httpcore5-h2@5.3.4",
		"org.slf4j:slf4j-api@1.7.36",
	}
	if !eqStrs(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestGuavaLiteralVersions(t *testing.T) {
	src := centralSource(t)
	got := compileDependencies(t, src, "com.google.guava", "guava", "33.4.8-jre")
	want := []string{
		"com.google.guava:failureaccess@1.0.3",
		"com.google.guava:listenablefuture@9999.0-empty-to-avoid-conflict-with-guava",
		"org.jspecify:jspecify@1.0.0",
		"com.google.errorprone:error_prone_annotations@2.36.0",
		"com.google.j2objc:j2objc-annotations@3.0.0",
	}
	if !eqStrs(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestGsonParentChain(t *testing.T) {
	src := centralSource(t)
	got := compileDependencies(t, src, "com.google.code.gson", "gson", "2.13.1")
	want := []string{"com.google.errorprone:error_prone_annotations@2.38.0"}
	if !eqStrs(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestCommonsIoTestScopeOnly(t *testing.T) {
	src := centralSource(t)
	metadata, err := src.GetMetadata(NewCoordinates("commons-io", "commons-io", "2.19.0"))
	if err != nil {
		t.Fatal(err)
	}
	if metadata == nil || len(metadata.Dependencies) != 9 {
		t.Fatalf("dependencies = %d, want 9", len(metadata.Dependencies))
	}
	for _, d := range metadata.Dependencies {
		if d.Scope != "test" {
			t.Errorf("dep %s:%s scope = %q, want test", d.GroupID, d.ArtifactID, d.Scope)
		}
	}
	if got := compileDependencies(t, src, "commons-io", "commons-io", "2.19.0"); len(got) != 0 {
		t.Errorf("compile deps = %v, want empty", got)
	}
}

func TestSnapshotArtifactsResolveComplete(t *testing.T) {
	t.Skip("Go PackageMetadata does not track the TS metadata.incomplete signal")
}
