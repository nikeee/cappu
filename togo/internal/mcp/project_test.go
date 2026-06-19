package mcp

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/audit"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
)

// Port of src/services/mcpProject.test.ts.

// configWith builds a config whose implementation deps are the given
// "group:artifact:version" specs.
func configWith(specs ...string) *config.Config {
	impl := map[string]string{}
	for _, spec := range specs {
		g, a, v := splitCoord3(spec)
		impl[g+":"+a] = v
	}
	return &config.Config{Dependencies: config.Dependencies{Implementation: impl}}
}

func meta(spec string, mutate func(*packages.PackageMetadata)) packages.PackageMetadata {
	g, a, v := splitCoord3(spec)
	m := packages.PackageMetadata{Coordinates: packages.NewCoordinates(g, a, v)}
	if mutate != nil {
		mutate(&m)
	}
	return m
}

func dep(spec string) packages.DependencyDeclaration {
	g, a, v := splitCoord3(spec)
	return packages.DependencyDeclaration{Coordinates: packages.NewCoordinates(g, a, v)}
}

// fakeAuditSource returns advisories for the coordinates named in the map.
type fakeAuditSource struct {
	advisories map[string][]audit.Advisory
}

func (f fakeAuditSource) Name() string { return "fake" }

func (f fakeAuditSource) FindVulnerabilities(coords []packages.Coordinates) (map[packages.CoordinateString][]audit.Advisory, error) {
	out := map[packages.CoordinateString][]audit.Advisory{}
	for _, c := range coords {
		if a, ok := f.advisories[string(c.String())]; ok {
			out[c.String()] = a
		}
	}
	return out, nil
}

func projectToolsFor(specs []string, pkgs []packages.PackageMetadata, deps ProjectToolDeps) *ProjectTools {
	if deps.Sources == nil {
		deps.Sources = []packages.PackageSource{packages.NewInMemoryPackageSource("test", pkgs)}
	}
	return NewProjectTools(configWith(specs...), deps)
}

func TestProjectSearchPackages(t *testing.T) {
	tools := projectToolsFor(nil, []packages.PackageMetadata{meta("org.a:alpha:1", nil), meta("org.b:beta:1", nil)}, ProjectToolDeps{})
	matches, err := tools.SearchPackages("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0] != "org.a:alpha:1" {
		t.Errorf("matches = %v", matches)
	}
}

func TestProjectLicenses(t *testing.T) {
	tools := projectToolsFor([]string{"org.a:a:1"}, []packages.PackageMetadata{
		meta("org.a:a:1", func(m *packages.PackageMetadata) {
			m.Dependencies = []packages.DependencyDeclaration{dep("org.b:b:1")}
			m.Licenses = []packages.License{{Name: "Apache-2.0", URL: "https://apache.org/licenses/LICENSE-2.0"}}
			m.LicenseNormalized = []packages.SpdxID{"Apache-2.0"}
		}),
		meta("org.b:b:1", func(m *packages.PackageMetadata) {
			m.Licenses = []packages.License{{Name: "MIT"}}
			m.LicenseNormalized = []packages.SpdxID{"MIT"}
		}),
	}, ProjectToolDeps{})
	rows, err := tools.Licenses()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Coordinate != "org.a:a:1" || rows[1].Coordinate != "org.b:b:1" {
		t.Fatalf("rows = %+v", rows)
	}
	if len(rows[0].SPDX) != 1 || rows[0].SPDX[0] != "Apache-2.0" {
		t.Errorf("spdx = %v", rows[0].SPDX)
	}
	if rows[0].Licenses[0].URL != "https://apache.org/licenses/LICENSE-2.0" {
		t.Errorf("url = %q", rows[0].Licenses[0].URL)
	}
	if rows[1].Licenses[0].URL != "" {
		t.Errorf("MIT url should be empty, got %q", rows[1].Licenses[0].URL)
	}
}

func TestProjectAudit(t *testing.T) {
	src := fakeAuditSource{advisories: map[string][]audit.Advisory{
		"org.b:bad:1": {{ID: "CVE-1", Aliases: []string{}, Summary: "CVE-1", Severity: audit.SeverityHigh, FixedVersions: []string{}, URL: "https://osv.dev/vulnerability/CVE-1"}},
	}}
	tools := projectToolsFor([]string{"org.a:a:1"}, []packages.PackageMetadata{
		meta("org.a:a:1", func(m *packages.PackageMetadata) {
			m.Dependencies = []packages.DependencyDeclaration{dep("org.b:bad:1")}
		}),
		meta("org.b:bad:1", nil),
	}, ProjectToolDeps{AuditSource: src})
	report, err := tools.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 2 || report.Counts.High != 1 || len(report.Vulnerable) != 1 {
		t.Fatalf("report = %+v", report)
	}
	v := report.Vulnerable[0]
	if v.Coordinate != "org.b:bad:1" || strings.Join(v.Path, ",") != "org.a:a:1,org.b:bad:1" || v.Advisories[0].ID != "CVE-1" {
		t.Errorf("vulnerable = %+v", v)
	}
}

func TestProjectAuditClean(t *testing.T) {
	tools := projectToolsFor([]string{"org.a:a:1"}, []packages.PackageMetadata{meta("org.a:a:1", nil)}, ProjectToolDeps{AuditSource: fakeAuditSource{advisories: map[string][]audit.Advisory{}}})
	report, err := tools.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Vulnerable) != 0 {
		t.Errorf("vulnerable = %+v, want empty", report.Vulnerable)
	}
}

func TestProjectOutdated(t *testing.T) {
	tools := projectToolsFor([]string{"org.a:a:1.0"}, []packages.PackageMetadata{meta("org.a:a:1.0", nil), meta("org.a:a:1.1", nil)}, ProjectToolDeps{})
	outdated, err := tools.Outdated()
	if err != nil {
		t.Fatal(err)
	}
	if len(outdated) != 1 || outdated[0] != (McpOutdatedDependency{Configuration: "implementation", Coordinate: "org.a:a", From: "1.0", To: "1.1"}) {
		t.Errorf("outdated = %+v", outdated)
	}
}

func TestProjectLatestVersion(t *testing.T) {
	tools := projectToolsFor(nil, []packages.PackageMetadata{meta("org.a:a:1.0", nil), meta("org.a:a:1.1", nil)}, ProjectToolDeps{})
	got, err := tools.LatestVersion("org.a:a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Coordinate != "org.a:a" || got.Latest != "1.1" {
		t.Errorf("latest = %+v", got)
	}
}

func TestProjectDependencyTree(t *testing.T) {
	tools := projectToolsFor([]string{"org.a:a:1"}, []packages.PackageMetadata{
		meta("org.a:a:1", func(m *packages.PackageMetadata) { m.Dependencies = []packages.DependencyDeclaration{dep("org.b:b:1")} }),
		meta("org.b:b:1", nil),
	}, ProjectToolDeps{})
	res, err := tools.DependencyTree("")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Packages) != 2 || res.Packages[0].Coordinate != "org.a:a:1" || res.Packages[1].Coordinate != "org.b:b:1" || res.Packages[1].Depth != 1 {
		t.Errorf("packages = %+v", res.Packages)
	}
}

func TestProjectDependencyTreeWithCoord(t *testing.T) {
	tools := projectToolsFor([]string{"org.a:a:1"}, []packages.PackageMetadata{
		meta("org.a:a:1", func(m *packages.PackageMetadata) { m.Dependencies = []packages.DependencyDeclaration{dep("org.b:b:1")} }),
		meta("org.b:b:1", nil),
	}, ProjectToolDeps{})
	res, err := tools.DependencyTree("org.b:b:1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Path, ",") != "org.a:a:1,org.b:b:1" {
		t.Errorf("path = %v", res.Path)
	}
}
