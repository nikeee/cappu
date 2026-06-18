package publish

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

// cfgFrom builds a config straight from a literal (defaults applied via Load).
func cfgFrom(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestGeneratePom(t *testing.T) {
	pom, err := GeneratePom(cfgFrom(t, `{
		"groupId":"com.example","artifactId":"my-lib","version":"1.2.0","license":"MIT",
		"dependencies":{
			"api":{"com.google.code.gson:gson":"2.13.1"},
			"implementation":{"com.google.guava:guava":"33.2.1-jre"},
			"testImplementation":{"org.junit.jupiter:junit-jupiter":"5.12.2"},
			"annotationProcessor":{"org.mapstruct:mapstruct-processor":"1.6.3"}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<groupId>com.example</groupId>",
		"<artifactId>my-lib</artifactId>",
		"<version>1.2.0</version>",
		"<packaging>jar</packaging>",
		"<name>MIT</name>",
	} {
		if !strings.Contains(pom, want) {
			t.Errorf("pom missing %q:\n%s", want, pom)
		}
	}
	// api -> compile (no <scope>); implementation -> runtime; test -> test
	if !regexp.MustCompile(`<artifactId>gson</artifactId>\s*<version>2\.13\.1</version>\s*</dependency>`).MatchString(pom) {
		t.Errorf("gson (api) should have no scope:\n%s", pom)
	}
	if !regexp.MustCompile(`<artifactId>guava</artifactId>[\s\S]*?<scope>runtime</scope>`).MatchString(pom) {
		t.Errorf("guava should be runtime-scoped")
	}
	if !regexp.MustCompile(`<artifactId>junit-jupiter</artifactId>[\s\S]*?<scope>test</scope>`).MatchString(pom) {
		t.Errorf("junit should be test-scoped")
	}
	if strings.Contains(pom, "mapstruct-processor") { // annotationProcessor never in the POM
		t.Errorf("annotationProcessor leaked into the POM:\n%s", pom)
	}
}

func TestGeneratePomMinimal(t *testing.T) {
	pom, err := GeneratePom(cfgFrom(t, `{"groupId":"g","artifactId":"a","version":"1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pom, "<licenses>") || strings.Contains(pom, "<dependencies>") {
		t.Errorf("license-less, dependency-less project should omit both blocks:\n%s", pom)
	}
}

func TestMissingCoordinates(t *testing.T) {
	cfg := cfgFrom(t, `{"groupId":"g","artifactId":"a"}`) // no version
	if got := MissingCoordinates(cfg); len(got) != 1 || got[0] != "version" {
		t.Errorf("MissingCoordinates = %v, want [version]", got)
	}
	if _, err := GeneratePom(cfg); err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("GeneratePom should error mentioning version, got %v", err)
	}
}
