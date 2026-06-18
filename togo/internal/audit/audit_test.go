package audit

import (
	"testing"

	"github.com/nikeee/cappu/internal/packages"
)

// stubSource returns canned advisories keyed by coordinate.
type stubSource struct {
	byCoord map[packages.CoordinateString][]Advisory
}

func (s stubSource) Name() string { return "stub" }

func (s stubSource) FindVulnerabilities(coords []packages.Coordinates) (map[packages.CoordinateString][]Advisory, error) {
	return s.byCoord, nil
}

func TestAuditPackagesReportSortedWorstFirst(t *testing.T) {
	src := stubSource{byCoord: map[packages.CoordinateString][]Advisory{
		"org.low:low:1":   {{ID: "L", Severity: SeverityLow}},
		"org.crit:crit:1": {{ID: "C", Severity: SeverityCritical}, {ID: "M", Severity: SeverityModerate}},
	}}
	coords := []packages.Coordinates{
		coord("org.low:low:1"),
		coord("org.clean:clean:1"), // no advisories
		coord("org.crit:crit:1"),
		coord("org.crit:crit:1"), // duplicate, deduped
	}
	report, err := AuditPackages(coords, src)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 3 { // duplicate deduped, clean still counts as scanned
		t.Errorf("scanned = %d, want 3", report.Scanned)
	}
	if len(report.Vulnerable) != 2 {
		t.Fatalf("vulnerable = %d, want 2", len(report.Vulnerable))
	}
	// worst-first: crit (has a critical) before low
	if report.Vulnerable[0].Coordinates.String() != "org.crit:crit:1" {
		t.Errorf("first vulnerable = %s, want org.crit:crit:1", report.Vulnerable[0].Coordinates.String())
	}
	if report.Counts.Critical != 1 || report.Counts.Moderate != 1 || report.Counts.Low != 1 {
		t.Errorf("counts = %+v", report.Counts)
	}
	if report.Counts.Total() != 3 {
		t.Errorf("total = %d, want 3", report.Counts.Total())
	}
}

func TestAuditPackagesNoneVulnerable(t *testing.T) {
	report, err := AuditPackages([]packages.Coordinates{coord("org.a:a:1")}, stubSource{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 1 || len(report.Vulnerable) != 0 {
		t.Errorf("report = %+v", report)
	}
}
