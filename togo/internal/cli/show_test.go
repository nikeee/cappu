package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/audit"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
)

// plain is a no-op paint so the card asserts on content, not ANSI codes.
func plain(_ string, text string) string { return text }

// showAuditStub returns canned advisories keyed by "group:artifact:version".
type showAuditStub struct {
	byCoord map[packages.CoordinateString][]audit.Advisory
}

func (showAuditStub) Name() string { return "stub" }
func (s showAuditStub) FindVulnerabilities(coords []packages.Coordinates) (map[packages.CoordinateString][]audit.Advisory, error) {
	out := map[packages.CoordinateString][]audit.Advisory{}
	for _, c := range coords {
		if adv, ok := s.byCoord[c.String()]; ok {
			out[c.String()] = adv
		}
	}
	return out, nil
}

func showSource() *packages.InMemoryPackageSource {
	pkg := func(g, a, v string, mut func(*packages.PackageMetadata)) packages.PackageMetadata {
		m := packages.PackageMetadata{Coordinates: packages.NewCoordinates(g, a, v)}
		if mut != nil {
			mut(&m)
		}
		return m
	}
	return packages.NewInMemoryPackageSource("test", []packages.PackageMetadata{
		pkg("com.google.code.gson", "gson", "2.11.0", nil),
		pkg("com.google.code.gson", "gson", "2.13.0", nil),
		pkg("com.google.code.gson", "gson", "2.13.1", func(m *packages.PackageMetadata) {
			m.Description = "A library to convert Java Objects into JSON and back"
			m.Homepage = "https://github.com/google/gson"
			m.ScmURL = "https://github.com/google/gson.git"
			m.Licenses = []packages.License{{Name: "Apache-2.0", URL: "https://www.apache.org/licenses/LICENSE-2.0.txt"}}
			m.Dependencies = []packages.DependencyDeclaration{
				{Coordinates: packages.NewCoordinates("com.google.errorprone", "error_prone_annotations", "2.27.0")},
			}
		}),
	})
}

func showConfig(t *testing.T, cappuJSON string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if cappuJSON == "" {
		cappuJSON = "{}"
	}
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"), []byte(cappuJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func mustShow(t *testing.T, coord string, cfg *config.Config, a audit.AuditSource) *showData {
	t.Helper()
	if a == nil {
		a = showAuditStub{}
	}
	data, showErr := buildShowData(coord, cfg, []packages.PackageSource{showSource()}, a)
	if showErr != nil {
		t.Fatalf("unexpected error: %s", showErr.message)
	}
	return data
}

func TestShowDefaultsToLatest(t *testing.T) {
	data := mustShow(t, "com.google.code.gson:gson", showConfig(t, ""), nil)
	if data.version != "2.13.1" || data.latestVersion != "2.13.1" || data.versionCount != 3 {
		t.Fatalf("version=%q latest=%q count=%d", data.version, data.latestVersion, data.versionCount)
	}
	if data.explicitVersion {
		t.Error("explicitVersion = true, want false")
	}
	card := renderShowCard(data, plain)
	for _, want := range []string{
		"com.google.code.gson:gson 2.13.1  latest",
		"convert Java Objects into JSON",
		"License      Apache-2.0",
		"Homepage     https://github.com/google/gson",
		"Repository   https://github.com/google/gson.git",
		"not a direct dependency",
		"Dependencies (1)",
		"com.google.errorprone:error_prone_annotations:2.27.0",
		"no known vulnerabilities",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card missing %q\n%s", want, card)
		}
	}
}

func TestShowOlderPinnedFlagsNewer(t *testing.T) {
	data := mustShow(t, "com.google.code.gson:gson:2.11.0", showConfig(t, ""), nil)
	if !data.explicitVersion || data.newer != 2 {
		t.Fatalf("explicit=%v newer=%d", data.explicitVersion, data.newer)
	}
	if card := renderShowCard(data, plain); !strings.Contains(card, "2.11.0  2 newer available") {
		t.Errorf("card missing newer hint\n%s", card)
	}
}

func TestShowProjectContext(t *testing.T) {
	cfg := showConfig(t, `{"dependencies":{"implementation":{"com.google.code.gson:gson":"2.13.1"}}}`)
	data := mustShow(t, "com.google.code.gson:gson", cfg, nil)
	if len(data.project.configurations) != 1 || data.project.configurations[0] != "implementation" {
		t.Fatalf("configurations = %v", data.project.configurations)
	}
	if data.project.declared != "2.13.1" {
		t.Errorf("declared = %q", data.project.declared)
	}
	if card := renderShowCard(data, plain); !strings.Contains(card, "In project   implementation (declared 2.13.1)") {
		t.Errorf("card missing project row\n%s", card)
	}
}

func TestShowVulnerabilities(t *testing.T) {
	stub := showAuditStub{byCoord: map[packages.CoordinateString][]audit.Advisory{
		"com.google.code.gson:gson:2.13.0": {{
			ID:            "GHSA-xxxx-yyyy-zzzz",
			Aliases:       []string{"CVE-2022-0001"},
			Summary:       "Example deserialization issue",
			Severity:      audit.SeverityHigh,
			FixedVersions: []string{"2.13.1"},
			URL:           "https://osv.dev/vulnerability/GHSA-xxxx-yyyy-zzzz",
		}},
	}}
	data := mustShow(t, "com.google.code.gson:gson:2.13.0", showConfig(t, ""), stub)
	if len(data.vulnerabilities) != 1 {
		t.Fatalf("vulnerabilities = %d", len(data.vulnerabilities))
	}
	card := renderShowCard(data, plain)
	if !strings.Contains(card, "HIGH  GHSA-xxxx-yyyy-zzzz (CVE-2022-0001) - Example deserialization issue") {
		t.Errorf("card missing advisory line\n%s", card)
	}
	if !strings.Contains(card, "[fixed in: 2.13.1]") {
		t.Errorf("card missing fixed hint\n%s", card)
	}
}

func TestShowJSON(t *testing.T) {
	cfg := showConfig(t, `{"dependencies":{"implementation":{"com.google.code.gson:gson":"2.13.1"}}}`)
	data := mustShow(t, "com.google.code.gson:gson", cfg, nil)
	j := showToJSON(data)
	if j.Version != "2.13.1" || j.LatestVersion == nil || *j.LatestVersion != "2.13.1" {
		t.Fatalf("version=%q latest=%v", j.Version, j.LatestVersion)
	}
	if len(j.License) != 1 || j.License[0] != "Apache-2.0" {
		t.Errorf("license = %v", j.License)
	}
	if len(j.Dependencies) != 1 || j.Dependencies[0].ArtifactID != "error_prone_annotations" {
		t.Errorf("dependencies = %+v", j.Dependencies)
	}
	if len(j.Project.Configurations) != 1 || j.Project.Declared == nil || *j.Project.Declared != "2.13.1" {
		t.Errorf("project = %+v", j.Project)
	}
	if j.Project.Installed != nil {
		t.Errorf("installed = %v, want nil", j.Project.Installed)
	}
	if len(j.Vulnerabilities) != 0 {
		t.Errorf("vulnerabilities = %v", j.Vulnerabilities)
	}
}

func TestShowErrors(t *testing.T) {
	cfg := showConfig(t, "")
	if _, e := buildShowData("not-a-coord", cfg, []packages.PackageSource{showSource()}, showAuditStub{}); e == nil || e.code != 2 {
		t.Fatalf("malformed coord error = %v", e)
	}
	if _, e := buildShowData("org.x:nope", cfg, []packages.PackageSource{showSource()}, showAuditStub{}); e == nil || e.code != 1 {
		t.Fatalf("unknown package error = %v", e)
	}
}
