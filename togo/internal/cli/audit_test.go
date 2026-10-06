package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/audit"
	"github.com/nikeee/cappu/internal/config"
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

	log := buildAuditSarif(report, audit.IgnoreResult{}, byKey, "9.9.9")

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

// auditFixture is one vulnerable root with a critical (CVE-aliased) and a low
// advisory.
func auditFixture() (audit.AuditReport, map[packages.PackageKey]packages.ResolvedPackage) {
	root := packages.NewCoordinates("org.apache.logging.log4j", "log4j-core", "2.14.1")
	byKey := map[packages.PackageKey]packages.ResolvedPackage{root.Key(): {Coordinates: root}}
	report := audit.AuditReport{
		Scanned: 1,
		Vulnerable: []audit.PackageAdvisories{{
			Coordinates: root,
			Advisories: []audit.Advisory{
				{ID: "GHSA-jfh8-c2jp-5v3q", Aliases: []string{"CVE-2021-44228"}, Summary: "Log4Shell", Severity: audit.SeverityCritical},
				{ID: "GHSA-minor", Summary: "minor issue", Severity: audit.SeverityLow},
			},
		}},
		Counts: audit.Counts{Critical: 1, Low: 1},
	}
	return report, byKey
}

// runRender renders the report through renderAudit into temp files (no TTY, so
// no colour) and returns the exit code, stdout and stderr.
func runRender(t *testing.T, report audit.AuditReport, byKey map[packages.PackageKey]packages.ResolvedPackage,
	ignores []config.AuditIgnore, format string, allow bool) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	stdout, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stderr.Close() }()
	cfg := &config.Config{ConfigPath: "/project/cappu.json", AuditOptions: config.AuditOptions{Ignore: ignores}}
	code := renderAudit(stdout, stderr, cfg, report, byKey, format, allow)
	out, _ := os.ReadFile(stdout.Name())
	errOut, _ := os.ReadFile(stderr.Name())
	return code, string(out), string(errOut)
}

// Go build only: ignored findings are still printed, marked with their reason,
// and only unignored findings or stale ignore entries fail the run.
func TestRenderAuditTextIgnores(t *testing.T) {
	report, byKey := auditFixture()

	code, out, _ := runRender(t, report, byKey, nil, "text", false)
	if code != 1 || strings.Contains(out, "ignored") {
		t.Errorf("no ignores: code = %d, want 1 and no ignored marker:\n%s", code, out)
	}

	code, out, _ = runRender(t, report, byKey, []config.AuditIgnore{{ID: "CVE-2021-44228", Reason: "JNDI disabled"}}, "text", false)
	if code != 1 {
		t.Errorf("partial: code = %d, want 1 (GHSA-minor is not ignored)", code)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "GHSA-jfh8-c2jp-5v3q") && !strings.HasSuffix(line, "  (ignored: JNDI disabled)"):
			t.Errorf("ignored finding not marked: %q", line)
		case strings.Contains(line, "GHSA-minor") && strings.Contains(line, "ignored"):
			t.Errorf("unignored finding marked: %q", line)
		}
	}
	if !strings.Contains(out, "2 vulnerabilities (1 critical, 1 low) across 1 of 1 packages, 1 ignored\n") {
		t.Errorf("partial summary missing:\n%s", out)
	}

	all := []config.AuditIgnore{{ID: "GHSA-jfh8-c2jp-5v3q", Reason: "a"}, {ID: "ghsa-minor", Reason: "b"}}
	code, out, errOut := runRender(t, report, byKey, all, "text", false)
	if code != 0 || errOut != "" {
		t.Errorf("all ignored: code = %d, stderr = %q; want 0 and nothing", code, errOut)
	}
	if !strings.Contains(out, ", 2 ignored\n") || !strings.Contains(out, "(ignored: b)") {
		t.Errorf("all ignored output:\n%s", out)
	}
}

// A stale ignore entry fails the run (even with nothing found) unless
// --allow-stale-ignores turns it into a warning; it never touches stdout.
func TestRenderAuditStaleIgnores(t *testing.T) {
	stale := []config.AuditIgnore{{ID: "CVE-2000-0001", Reason: "r"}}
	for _, format := range []string{"text", "sarif"} {
		code, out, errOut := runRender(t, audit.AuditReport{Scanned: 3}, nil, stale, format, false)
		if code != 1 {
			t.Errorf("%s: code = %d, want 1", format, code)
		}
		want := "error: auditOptions.ignore entry \"CVE-2000-0001\" matches no finding; remove it from /project/cappu.json (or pass --allow-stale-ignores)\n"
		if !strings.Contains(errOut, want) {
			t.Errorf("%s: stderr = %q, want %q", format, errOut, want)
		}
		if strings.Contains(out, "CVE-2000-0001") {
			t.Errorf("%s: stale entry leaked into stdout:\n%s", format, out)
		}

		code, _, errOut = runRender(t, audit.AuditReport{Scanned: 3}, nil, stale, format, true)
		if code != 0 || !strings.Contains(errOut, "warning: auditOptions.ignore entry \"CVE-2000-0001\" matches no finding\n") {
			t.Errorf("%s allowed: code = %d, stderr = %q; want 0 and a warning", format, code, errOut)
		}
	}

	// a stale entry also fails a run whose findings are all ignored
	report, byKey := auditFixture()
	ignores := []config.AuditIgnore{
		{ID: "GHSA-jfh8-c2jp-5v3q", Reason: "a"},
		{ID: "GHSA-minor", Reason: "b"},
		{ID: "CVE-2000-0001", Reason: "c"},
	}
	if code, _, _ := runRender(t, report, byKey, ignores, "text", false); code != 1 {
		t.Errorf("all ignored + stale: code = %d, want 1", code)
	}
}

// Go build only: ignored findings keep their SARIF result with a suppression,
// unignored ones have no suppressions key, and the exit code follows the
// unignored findings.
func TestRenderAuditSarifIgnores(t *testing.T) {
	report, byKey := auditFixture()
	code, out, _ := runRender(t, report, byKey, []config.AuditIgnore{{ID: "CVE-2021-44228", Reason: "JNDI disabled"}}, "sarif", false)
	if code != 1 {
		t.Errorf("partial: code = %d, want 1", code)
	}
	var log sarifLog
	if err := json.Unmarshal([]byte(out), &log); err != nil {
		t.Fatal(err)
	}
	results := log.Runs[0].Results
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, r := range results {
		var want []sarifSuppression
		if r.RuleID == "GHSA-jfh8-c2jp-5v3q" {
			want = []sarifSuppression{{Kind: "external", Justification: "JNDI disabled"}}
		}
		if !slices.Equal(r.Suppressions, want) {
			t.Errorf("%s suppressions = %v, want %v", r.RuleID, r.Suppressions, want)
		}
	}
	if n := strings.Count(out, `"suppressions"`); n != 1 {
		t.Errorf(`"suppressions" appears %d times, want 1 (omitted when not ignored)`, n)
	}

	all := []config.AuditIgnore{{ID: "GHSA-jfh8-c2jp-5v3q", Reason: "a"}, {ID: "GHSA-minor", Reason: "b"}}
	if code, _, _ := runRender(t, report, byKey, all, "sarif", false); code != 0 {
		t.Errorf("all ignored: code = %d, want 0", code)
	}
}
