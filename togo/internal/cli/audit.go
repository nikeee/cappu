package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nikeee/cappu/internal/audit"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// RunAudit handles `cappu audit`: scan the resolved dependencies (transitive
// included) for known vulnerabilities (OSV.dev), grouped by severity and
// coloured like npm, printing the dependency path that pulls each one in. No
// fixing. Exits non-zero when anything is found. Port of src/cli/audit.ts.
func RunAudit(cfg *config.Config, noCache, jsonOut bool) int {
	var fetch audit.FetchJSON
	if !noCache {
		fetch = audit.CachedFetchJSON(nil)
	}
	source := audit.NewOsvSource(fetch)

	// Resolve the whole graph (not just the locked list): the requestedBy edges
	// are what let us show why a transitive package is here.
	showProgress := ColorEnabled(isTTY(os.Stderr), os.Getenv("NO_COLOR"))
	resolving := 0
	roots := append(sources.CompileRoots(cfg), sources.ProcessorRoots(cfg)...)
	roots = append(roots, sources.TestRoots(cfg)...)
	resolution, err := packages.ResolveTransitive(roots, sources.Configured(cfg), func(packages.Coordinates) {
		if showProgress {
			resolving++
			fmt.Fprintf(os.Stderr, "\r\x1b[2Kresolving dependency graph (%d)...", resolving)
		}
	})
	if resolving > 0 {
		fmt.Fprint(os.Stderr, "\r\x1b[2K")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
		return 1
	}

	byKey := map[packages.PackageKey]packages.ResolvedPackage{}
	coordinates := make([]packages.Coordinates, 0, len(resolution.Packages))
	for _, p := range resolution.Packages {
		byKey[p.Coordinates.Key()] = p
		coordinates = append(coordinates, p.Coordinates)
	}
	WarnUnmappedLicenses(resolution.Packages)

	report, err := audit.AuditPackages(coordinates, source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: audit failed: %s\n", err)
		emitAnnotation("error", fmt.Sprintf("audit failed: %s", err), AnnotationLocation{})
		return 2
	}

	if jsonOut {
		return auditJSON(report, byKey)
	}
	return auditText(report, byKey)
}

// dependencyPath is the chain of coordinates from a declared root down to
// target, following each resolved package's requestedBy edge. Returns
// [root, ..., target]; cycle-guarded.
func dependencyPath(byKey map[packages.PackageKey]packages.ResolvedPackage, target packages.Coordinates) []packages.Coordinates {
	var path []packages.Coordinates
	seen := map[packages.PackageKey]struct{}{}
	current := target
	hasCurrent := true
	for hasCurrent {
		key := current.Key()
		if _, ok := seen[key]; ok {
			break
		}
		seen[key] = struct{}{}
		path = append([]packages.Coordinates{current}, path...)
		p, ok := byKey[key]
		// a root has a zero requestedBy; stop there
		if !ok || (p.RequestedBy == packages.Coordinates{}) {
			break
		}
		current = p.RequestedBy
	}
	return path
}

func pathStrings(byKey map[packages.PackageKey]packages.ResolvedPackage, target packages.Coordinates) []string {
	path := dependencyPath(byKey, target)
	out := make([]string, len(path))
	for i, c := range path {
		out[i] = string(c.String())
	}
	return out
}

func auditJSON(report audit.AuditReport, byKey map[packages.PackageKey]packages.ResolvedPackage) int {
	type advisoryJSON struct {
		ID            string   `json:"id"`
		Aliases       []string `json:"aliases"`
		Severity      string   `json:"severity"`
		Summary       string   `json:"summary"`
		FixedVersions []string `json:"fixedVersions"`
		URL           string   `json:"url"`
	}
	type packageJSON struct {
		Coordinate string         `json:"coordinate"`
		Path       []string       `json:"path"`
		Advisories []advisoryJSON `json:"advisories"`
	}
	vulnerable := make([]packageJSON, 0, len(report.Vulnerable))
	for _, p := range report.Vulnerable {
		advisories := make([]advisoryJSON, 0, len(p.Advisories))
		for _, a := range p.Advisories {
			advisories = append(advisories, advisoryJSON{
				ID:            string(a.ID),
				Aliases:       orEmpty(a.Aliases),
				Severity:      string(a.Severity),
				Summary:       a.Summary,
				FixedVersions: orEmpty(a.FixedVersions),
				URL:           a.URL,
			})
		}
		vulnerable = append(vulnerable, packageJSON{
			Coordinate: string(p.Coordinates.String()),
			Path:       pathStrings(byKey, p.Coordinates),
			Advisories: advisories,
		})
	}
	out, _ := json.MarshalIndent(struct {
		Scanned    int           `json:"scanned"`
		Counts     audit.Counts  `json:"counts"`
		Vulnerable []packageJSON `json:"vulnerable"`
	}{report.Scanned, report.Counts, vulnerable}, "", "  ")
	fmt.Fprintf(os.Stdout, "%s\n", out)
	if len(report.Vulnerable) > 0 {
		return 1
	}
	return 0
}

func auditText(report audit.AuditReport, byKey map[packages.PackageKey]packages.ResolvedPackage) int {
	paint := painter(os.Stdout)
	sev := func(s audit.Severity, text string) string { return severityPaint(paint, s, text) }

	if len(report.Vulnerable) == 0 {
		fmt.Fprintf(os.Stdout, "%s in %d packages\n", paint("green", "found no known vulnerabilities"), report.Scanned)
		return 0
	}

	printTree := func(target packages.Coordinates) {
		path := dependencyPath(byKey, target)
		for i, c := range path {
			label := string(c.String())
			if i == len(path)-1 {
				label = severityPaint(paint, audit.SeverityCritical, label) // deepest = bold red
			} else {
				label = paint("dim", label)
			}
			fmt.Fprintf(os.Stdout, "    %s%s\n", strings.Repeat("  ", i), label)
		}
	}

	for _, severity := range audit.SeverityOrder {
		type finding struct {
			c packages.Coordinates
			a audit.Advisory
		}
		var inBucket []finding
		for _, p := range report.Vulnerable {
			for _, a := range p.Advisories {
				if a.Severity == severity {
					inBucket = append(inBucket, finding{p.Coordinates, a})
				}
			}
		}
		if len(inBucket) == 0 {
			continue
		}
		fmt.Fprintf(os.Stdout, "\n%s\n", sev(severity, strings.ToUpper(string(severity))))
		for _, f := range inBucket {
			cve := ""
			if len(f.a.Aliases) > 0 {
				cve = " (" + strings.Join(f.a.Aliases, ", ") + ")"
			}
			fixed := ""
			if len(f.a.FixedVersions) > 0 {
				fixed = "  [fixed in: " + strings.Join(f.a.FixedVersions, ", ") + "]"
			}
			fmt.Fprintf(os.Stdout, "  %s  %s%s - %s%s\n", f.c.String(), f.a.ID, cve, f.a.Summary, fixed)
			fmt.Fprintf(os.Stdout, "    %s\n", paint("dim", f.a.URL))
			printTree(f.c)
		}
	}

	total := report.Counts.Total()
	var parts []string
	for _, s := range audit.SeverityOrder {
		if n := report.Counts.Get(s); n > 0 {
			parts = append(parts, sev(s, fmt.Sprintf("%d %s", n, s)))
		}
	}
	noun := "vulnerabilities"
	if total == 1 {
		noun = "vulnerability"
	}
	fmt.Fprintf(os.Stdout, "\n%d %s (%s) across %d of %d packages\n",
		total, noun, strings.Join(parts, ", "), len(report.Vulnerable), report.Scanned)
	return 1
}

// severityPaint colours text in npm's palette for a severity (critical is
// bold+red, nested so both apply).
func severityPaint(paint func(format, text string) string, s audit.Severity, text string) string {
	switch s {
	case audit.SeverityCritical:
		return paint("red", paint("bold", text))
	case audit.SeverityHigh:
		return paint("red", text)
	case audit.SeverityModerate:
		return paint("yellow", text)
	case audit.SeverityLow:
		return paint("cyan", text)
	default:
		return paint("dim", text)
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
