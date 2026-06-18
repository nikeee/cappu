package audit

import (
	"sort"

	"github.com/nikeee/cappu/internal/packages"
)

// worstSeverity is the highest-ranked severity among advisories.
func worstSeverity(advisories []Advisory) Severity {
	worst := SeverityUnknown
	for _, a := range advisories {
		if severityRank(a.Severity) < severityRank(worst) {
			worst = a.Severity
		}
	}
	return worst
}

// AuditPackages scans coordinates (deduped) for known vulnerabilities. Port of
// auditPackages.
func AuditPackages(coordinates []packages.Coordinates, source AuditSource) (AuditReport, error) {
	// dedupe by exact coordinate (a package can appear in several lock sets),
	// preserving first-seen order
	seen := map[packages.CoordinateString]struct{}{}
	var distinct []packages.Coordinates
	for _, c := range coordinates {
		key := c.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		distinct = append(distinct, c)
	}

	found, err := source.FindVulnerabilities(distinct)
	if err != nil {
		return AuditReport{}, err
	}

	report := AuditReport{Scanned: len(distinct)}
	for _, c := range distinct {
		advisories := found[c.String()]
		if len(advisories) == 0 {
			continue
		}
		for _, a := range advisories {
			report.Counts.inc(a.Severity)
		}
		report.Vulnerable = append(report.Vulnerable, PackageAdvisories{Coordinates: c, Advisories: advisories})
	}

	// worst severity first, then by coordinate for stable output
	sort.SliceStable(report.Vulnerable, func(i, j int) bool {
		byWorst := severityRank(worstSeverity(report.Vulnerable[i].Advisories)) -
			severityRank(worstSeverity(report.Vulnerable[j].Advisories))
		if byWorst != 0 {
			return byWorst < 0
		}
		return report.Vulnerable[i].Coordinates.String() < report.Vulnerable[j].Coordinates.String()
	})
	return report, nil
}
