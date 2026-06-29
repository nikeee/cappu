package cli

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/audit"
	"github.com/nikeee/cappu/internal/packages"
)

func TestBuildAuditSarif(t *testing.T) {
	root := packages.NewCoordinates("org.apache.logging.log4j", "log4j-core", "2.14.1")
	byKey := map[packages.PackageKey]packages.ResolvedPackage{
		root.Key(): {Coordinates: root}, // zero RequestedBy => a declared root
	}
	report := audit.AuditReport{
		Scanned: 1,
		Vulnerable: []audit.PackageAdvisories{{
			Coordinates: root,
			Advisories: []audit.Advisory{
				{ID: "GHSA-jfh8-c2jp-5v3q", Aliases: []string{"CVE-2021-44228"}, Summary: "Log4Shell", Severity: audit.SeverityCritical, FixedVersions: []string{"2.15.0"}, URL: "https://example/ghsa"},
				{ID: "GHSA-minor", Summary: "minor issue", Severity: audit.SeverityLow},
			},
		}},
	}

	log := buildAuditSarif(report, byKey, "9.9.9")

	if log.Version != "2.1.0" || log.Schema == "" {
		t.Fatalf("expected SARIF 2.1.0 with schema, got %+v", log)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(log.Runs))
	}
	run := log.Runs[0]
	if run.Tool.Driver.Name != "cappu" || run.Tool.Driver.Version != "9.9.9" {
		t.Errorf("driver = %+v", run.Tool.Driver)
	}
	if len(run.Tool.Driver.Rules) != 2 {
		t.Fatalf("rules = %d, want 2 (one per distinct advisory)", len(run.Tool.Driver.Rules))
	}
	scores := map[string]string{}
	for _, r := range run.Tool.Driver.Rules {
		scores[r.ID] = r.Properties.SecuritySeverity
	}
	if scores["GHSA-jfh8-c2jp-5v3q"] != "9.0" || scores["GHSA-minor"] != "1.0" {
		t.Errorf("security-severity scores = %v", scores)
	}

	if len(run.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(run.Results))
	}
	var crit *sarifResult
	for i := range run.Results {
		if run.Results[i].RuleID == "GHSA-jfh8-c2jp-5v3q" {
			crit = &run.Results[i]
		}
	}
	if crit == nil {
		t.Fatal("missing critical result")
	}
	if crit.Level != "error" {
		t.Errorf("critical level = %q, want error", crit.Level)
	}
	if crit.Properties.Severity != "critical" {
		t.Errorf("severity property = %q", crit.Properties.Severity)
	}
	if len(crit.Locations) != 1 || crit.Locations[0].PhysicalLocation.ArtifactLocation.URI != "cappu.json" {
		t.Errorf("location = %+v", crit.Locations)
	}
	if n := len(crit.Properties.Path); n == 0 || crit.Properties.Path[n-1] != crit.Properties.Coordinate {
		t.Errorf("path should end at the vulnerable coordinate: %v", crit.Properties.Path)
	}
	for _, want := range []string{"CVE-2021-44228", "log4j-core:2.14.1", "Fixed in: 2.15.0."} {
		if !strings.Contains(crit.Message.Text, want) {
			t.Errorf("message %q missing %q", crit.Message.Text, want)
		}
	}
}
