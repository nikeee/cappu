package packages

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A local maven2 repository as `mvn install` leaves it: version directories
// with a POM and a jar each, no maven-metadata.xml.
func TestFileRepositorySource(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pom := func(version string) string {
		return `<project><groupId>org.local</groupId><artifactId>lib</artifactId><version>` + version + `</version>
		  <dependencies><dependency><groupId>org.d</groupId><artifactId>d</artifactId><version>1</version></dependency></dependencies></project>`
	}
	write("org/local/lib/1.0/lib-1.0.pom", pom("1.0"))
	write("org/local/lib/1.0/lib-1.0.jar", "jar-1.0")
	write("org/local/lib/1.10/lib-1.10.pom", pom("1.10"))
	write("org/local/lib/1.2-SNAPSHOT/lib-1.2-SNAPSHOT.pom", pom("1.2-SNAPSHOT"))
	write("org/local/lib/junk/notes.txt", "no pom here: not a version")

	src := NewFileRepositorySource(dir, "~/repo")
	if src.Name() != "~/repo" {
		t.Errorf("name = %q, want the configured entry", src.Name())
	}
	versions, err := src.ListVersions("org.local", "lib")
	if err != nil || !reflect.DeepEqual(versions, []string{"1.0", "1.2-SNAPSHOT", "1.10"}) {
		t.Errorf("versions = %v (err %v), want Maven order oldest first", versions, err)
	}
	if none, err := src.ListVersions("org.local", "missing"); err != nil || len(none) != 0 {
		t.Errorf("unknown artifact versions = %v, %v", none, err)
	}
	meta, err := src.GetMetadata(NewCoordinates("org.local", "lib", "1.0"))
	if err != nil || meta == nil || len(meta.Dependencies) != 1 || meta.Dependencies[0].String() != "org.d:d:1" {
		t.Errorf("metadata = %+v, %v", meta, err)
	}
	if jar, err := src.GetArtifact(NewCoordinates("org.local", "lib", "1.0")); err != nil || string(jar) != "jar-1.0" {
		t.Errorf("jar = %q, %v", jar, err)
	}
	if jar, err := src.GetArtifact(NewCoordinates("org.local", "lib", "1.10")); err != nil || jar != nil {
		t.Errorf("missing jar = %q, %v; want a miss", jar, err)
	}
	if meta, err := src.GetMetadata(NewCoordinates("org.local", "lib", "9")); err != nil || meta != nil {
		t.Errorf("missing pom = %+v, %v; want a miss", meta, err)
	}
}
