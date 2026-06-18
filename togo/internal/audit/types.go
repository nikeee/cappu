// Package audit queries a CVE source (OSV by default) for the resolved Maven
// dependencies and builds a print-free report the CLI renders. Port of
// src/audit/.
package audit

import "github.com/nikeee/cappu/internal/packages"

// AdvisoryID is a vulnerability id (GHSA/OSV/etc.), distinct from a CVE alias.
type AdvisoryID string

// Severity buckets, npm-aligned (GHSA's MODERATE maps to "moderate").
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityModerate Severity = "moderate"
	SeverityLow      Severity = "low"
	SeverityUnknown  Severity = "unknown"
)

// SeverityOrder is worst-first, for grouping and "highest severity" comparisons.
var SeverityOrder = []Severity{SeverityCritical, SeverityHigh, SeverityModerate, SeverityLow, SeverityUnknown}

func severityRank(s Severity) int {
	for i, x := range SeverityOrder {
		if x == s {
			return i
		}
	}
	return len(SeverityOrder)
}

// Advisory is one known vulnerability affecting a package version.
type Advisory struct {
	ID       AdvisoryID `json:"id"`
	Aliases  []string   `json:"aliases"`
	Summary  string     `json:"summary"`
	Severity Severity   `json:"severity"`
	// FixedVersions are versions the advisory records as fixed (audit never fixes).
	FixedVersions []string `json:"fixedVersions"`
	URL           string   `json:"url"`
}

// PackageAdvisories are the advisories affecting one resolved package.
type PackageAdvisories struct {
	Coordinates packages.Coordinates
	Advisories  []Advisory
}

// Counts holds vulnerability counts per severity (ordered for stable JSON).
type Counts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Moderate int `json:"moderate"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

func (c *Counts) inc(s Severity) {
	switch s {
	case SeverityCritical:
		c.Critical++
	case SeverityHigh:
		c.High++
	case SeverityModerate:
		c.Moderate++
	case SeverityLow:
		c.Low++
	default:
		c.Unknown++
	}
}

// Get returns the count for a severity.
func (c Counts) Get(s Severity) int {
	switch s {
	case SeverityCritical:
		return c.Critical
	case SeverityHigh:
		return c.High
	case SeverityModerate:
		return c.Moderate
	case SeverityLow:
		return c.Low
	default:
		return c.Unknown
	}
}

// Total is the sum across all severities.
func (c Counts) Total() int {
	return c.Critical + c.High + c.Moderate + c.Low + c.Unknown
}

// AuditReport is the print-free result the CLI renders.
type AuditReport struct {
	// Scanned is how many distinct package versions were scanned.
	Scanned int
	// Vulnerable holds only the vulnerable packages, worst severity first.
	Vulnerable []PackageAdvisories
	// Counts holds vulnerability counts per severity.
	Counts Counts
}

// AuditSource is a source of vulnerability data (OSV by default; pluggable).
type AuditSource interface {
	Name() string
	// FindVulnerabilities returns the advisories for each coordinate, keyed by
	// "group:artifact:version".
	FindVulnerabilities(coordinates []packages.Coordinates) (map[packages.CoordinateString][]Advisory, error)
}
