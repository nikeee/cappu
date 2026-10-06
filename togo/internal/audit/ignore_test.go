package audit

import (
	"slices"
	"testing"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
)

func TestApplyIgnores(t *testing.T) {
	report := AuditReport{
		Scanned: 2,
		Vulnerable: []PackageAdvisories{
			{
				Coordinates: packages.NewCoordinates("org.apache.logging.log4j", "log4j-core", "2.14.1"),
				Advisories: []Advisory{
					{ID: "GHSA-jfh8-c2jp-5v3q", Aliases: []string{"CVE-2021-44228"}, Severity: SeverityCritical},
					{ID: "GHSA-other", Severity: SeverityLow},
				},
			},
			{
				Coordinates: packages.NewCoordinates("com.example", "lib", "1.0"),
				Advisories:  []Advisory{{ID: "GHSA-by-id", Severity: SeverityHigh}},
			},
		},
	}
	ignores := []config.AuditIgnore{
		{ID: "CVE-2021-44228", Reason: "via alias"},
		{ID: "ghsa-by-id", Reason: "via primary id"},
		{ID: "GHSA-jfh8-c2jp-5v3q", Reason: "same advisory again"},
		{ID: "CVE-2000-0001", Reason: "fixed long ago"},
	}

	result := ApplyIgnores(report, ignores)

	if reason, ok := result.Ignored(Advisory{ID: "GHSA-jfh8-c2jp-5v3q"}); !ok || reason != "via alias" {
		t.Errorf("alias match = %q, %v; want the first matching entry's reason", reason, ok)
	}
	if reason, ok := result.Ignored(Advisory{ID: "GHSA-by-id"}); !ok || reason != "via primary id" {
		t.Errorf("case-insensitive primary id match = %q, %v", reason, ok)
	}
	if _, ok := result.Ignored(Advisory{ID: "GHSA-other"}); ok {
		t.Error("GHSA-other is not listed and must not be ignored")
	}
	// Both entries naming the same advisory count as used; only the one with
	// no finding is stale.
	if want := []config.AuditIgnore{ignores[3]}; !slices.Equal(result.Stale, want) {
		t.Errorf("stale = %v, want %v", result.Stale, want)
	}
}

func TestApplyIgnoresEmptyReportMakesAllStale(t *testing.T) {
	ignores := []config.AuditIgnore{{ID: "CVE-2021-44228", Reason: "r"}}
	result := ApplyIgnores(AuditReport{}, ignores)
	if !slices.Equal(result.Stale, ignores) {
		t.Errorf("stale = %v, want %v", result.Stale, ignores)
	}
}
