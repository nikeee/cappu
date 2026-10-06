package audit

import (
	"strings"

	"github.com/nikeee/cappu/internal/config"
)

// IgnoreResult is how a report's findings relate to auditOptions.ignore.
type IgnoreResult struct {
	// Reasons maps each ignored advisory to the reason of the entry covering it.
	Reasons map[AdvisoryID]string
	// Stale are the entries that matched no finding, in config order.
	Stale []config.AuditIgnore
}

// Ignored reports the reason a finding is ignored, if it is.
func (r IgnoreResult) Ignored(a Advisory) (string, bool) {
	reason, ok := r.Reasons[a.ID]
	return reason, ok
}

// ApplyIgnores matches the ignore entries against the report: an entry covers
// an advisory when its id equals the advisory's primary id or one of its CVE
// aliases, ignoring case. Go build only (no TS counterpart yet).
func ApplyIgnores(report AuditReport, ignores []config.AuditIgnore) IgnoreResult {
	result := IgnoreResult{Reasons: map[AdvisoryID]string{}}
	used := make([]bool, len(ignores))
	for _, p := range report.Vulnerable {
		for _, a := range p.Advisories {
			for i, ignore := range ignores {
				if !matches(a, ignore.ID) {
					continue
				}
				used[i] = true
				if _, ok := result.Reasons[a.ID]; !ok {
					result.Reasons[a.ID] = ignore.Reason
				}
			}
		}
	}
	for i, ignore := range ignores {
		if !used[i] {
			result.Stale = append(result.Stale, ignore)
		}
	}
	return result
}

func matches(a Advisory, id string) bool {
	if strings.EqualFold(string(a.ID), id) {
		return true
	}
	for _, alias := range a.Aliases {
		if strings.EqualFold(alias, id) {
			return true
		}
	}
	return false
}
