package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nikeee/cappu/internal/audit"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/meta"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// RunAudit handles `cappu audit`: scan the resolved dependencies (transitive
// included) for known vulnerabilities (OSV.dev), grouped by severity and
// coloured like npm, printing the dependency path that pulls each one in. No
// fixing. Exits non-zero when an unignored finding or a stale ignore entry is
// found. Port of src/cli/audit.ts.
//
// Go build only: findings covered by auditOptions.ignore are still printed,
// marked ignored, and do not fail the run. An ignore entry that matches no
// finding fails it, unless allowStaleIgnores downgrades that to a warning.
func RunAudit(cfg *config.Config, noCache, allowStaleIgnores bool, formatFlag *string) int {
	format := "text"
	if AgentEnabled(os.Getenv) {
		format = "sarif"
	}
	if formatFlag != nil {
		format = *formatFlag
	}
	if format != "text" && format != "sarif" {
		fmt.Fprintf(os.Stderr, "cappu: unknown --format '%s' (expected: text, sarif)\n", format)
		return 2
	}

	var fetch audit.FetchJSON
	if !noCache {
		fetch = audit.CachedFetchJSON(nil)
	}
	source := audit.NewOsvSource(fetch)

	// Resolve the whole graph (not just the locked list): the requestedBy edges
	// are what let us show why a transitive package is here.
	showProgress := ColorEnabled(isTTY(os.Stderr), os.Getenv)
	resolving := 0
	roots := append(sources.CompileRoots(cfg), sources.ProcessorRoots(cfg)...)
	roots = append(roots, sources.TestRoots(cfg)...)
	srcs := sources.Configured(cfg)
	if noCache {
		srcs = sources.ConfiguredUncached(cfg)
	}
	resolution, err := packages.ResolveTransitive(roots, srcs, func(packages.Coordinates) {
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

	// Nothing resolved means there were no declared dependencies (no cappu.json,
	// or empty dependency configurations) - warn so a clean report here is not
	// mistaken for "scanned and found nothing".
	if len(coordinates) == 0 {
		warn := painter(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s no dependencies to scan (no cappu.json or empty dependencies)\n", warn("yellow", "warning:"))
	}

	report, err := audit.AuditPackages(coordinates, source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: audit failed: %s\n", err)
		emitAnnotation("error", fmt.Sprintf("audit failed: %s", err), AnnotationLocation{})
		return 2
	}

	return renderAudit(os.Stdout, os.Stderr, cfg, report, byKey, format, allowStaleIgnores)
}

// renderAudit prints a finished scan in the given format and returns the exit
// code: 1 for an unignored finding or a stale ignore entry, else 0.
func renderAudit(stdout, stderr *os.File, cfg *config.Config, report audit.AuditReport,
	byKey map[packages.PackageKey]packages.ResolvedPackage, format string, allowStaleIgnores bool) int {
	ignores := audit.ApplyIgnores(report, cfg.AuditOptions.Ignore)
	var code int
	if format == "sarif" {
		code = auditSarif(stdout, report, ignores, byKey)
	} else {
		code = auditText(stdout, report, ignores, byKey)
	}
	if reportStaleIgnores(stderr, ignores.Stale, cfg.ConfigPath, allowStaleIgnores) {
		code = 1
	}
	return code
}

// reportStaleIgnores prints the ignore entries that matched no finding to
// stderr (stdout may be SARIF), plus a CI annotation each: as errors, or as
// warnings when allowed. Reports whether they fail the run.
func reportStaleIgnores(stderr *os.File, stale []config.AuditIgnore, configPath string, allow bool) bool {
	paint := painter(stderr)
	for _, ignore := range stale {
		msg := fmt.Sprintf("auditOptions.ignore entry %q matches no finding", ignore.ID)
		if allow {
			fmt.Fprintf(stderr, "%s %s\n", paint("yellow", "warning:"), msg)
			emitAnnotation("warning", msg, AnnotationLocation{})
		} else {
			fmt.Fprintf(stderr, "%s %s; remove it from %s (or pass --allow-stale-ignores)\n",
				paint("red", "error:"), msg, configPath)
			emitAnnotation("error", msg, AnnotationLocation{})
		}
	}
	return len(stale) > 0 && !allow
}

// unignoredFindings counts the (package, advisory) findings not covered by an
// ignore entry; any of them fails the run.
func unignoredFindings(report audit.AuditReport, ignores audit.IgnoreResult) int {
	n := 0
	for _, p := range report.Vulnerable {
		for _, a := range p.Advisories {
			if _, ok := ignores.Ignored(a); !ok {
				n++
			}
		}
	}
	return n
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

// SARIF 2.1.0 log structs (GitHub code-scanning ingestible). Only the fields we
// populate are modelled. Port of src/cli/audit.ts buildAuditSarif.
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}
type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}
type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}
type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Version        string      `json:"version"`
	Rules          []sarifRule `json:"rules"`
}
type sarifText struct {
	Text string `json:"text"`
}
type sarifRule struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	ShortDescription sarifText      `json:"shortDescription"`
	HelpURI          string         `json:"helpUri"`
	Properties       sarifRuleProps `json:"properties"`
}
type sarifRuleProps struct {
	Tags             []string `json:"tags"`
	SecuritySeverity string   `json:"security-severity,omitempty"`
}
type sarifResult struct {
	RuleID       string             `json:"ruleId"`
	Level        string             `json:"level"`
	Message      sarifText          `json:"message"`
	Locations    []sarifLocation    `json:"locations"`
	Suppressions []sarifSuppression `json:"suppressions,omitempty"`
	Properties   sarifResultProps   `json:"properties"`
}

// sarifSuppression marks a result ignored via auditOptions.ignore; "external"
// because the decision lives in cappu.json, not in the scanned source.
type sarifSuppression struct {
	Kind          string `json:"kind"`
	Justification string `json:"justification"`
}
type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}
type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
}
type sarifArtifact struct {
	URI string `json:"uri"`
}
type sarifResultProps struct {
	Coordinate string   `json:"coordinate"`
	Severity   string   `json:"severity"`
	Path       []string `json:"path"`
}

// sarifSeverity maps an npm severity bucket to a SARIF level and a representative
// GitHub "security-severity" score (we have buckets, not CVSS; "" for unknown).
func sarifSeverity(s audit.Severity) (level, score string) {
	switch s {
	case audit.SeverityCritical:
		return "error", "9.0"
	case audit.SeverityHigh:
		return "error", "7.0"
	case audit.SeverityModerate:
		return "warning", "4.0"
	case audit.SeverityLow:
		return "note", "1.0"
	default:
		return "note", ""
	}
}

// buildAuditSarif builds the SARIF log: one rule per distinct advisory, one
// result per (package, advisory). Results point at cappu.json (the file that
// declares the dependency, directly or transitively); the dependency path is
// kept in result.properties for traceability. An ignored finding keeps its
// result, with a suppression carrying the ignore entry's reason.
func buildAuditSarif(report audit.AuditReport, ignores audit.IgnoreResult, byKey map[packages.PackageKey]packages.ResolvedPackage, version string) sarifLog {
	seen := map[string]struct{}{}
	rules := []sarifRule{}
	results := []sarifResult{}
	for _, p := range report.Vulnerable {
		coordinate := string(p.Coordinates.String())
		path := pathStrings(byKey, p.Coordinates)
		for _, a := range p.Advisories {
			level, score := sarifSeverity(a.Severity)
			id := string(a.ID)
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				short := a.Summary
				if short == "" {
					short = id
				}
				rules = append(rules, sarifRule{
					ID:               id,
					Name:             id,
					ShortDescription: sarifText{Text: short},
					HelpURI:          a.URL,
					Properties:       sarifRuleProps{Tags: []string{"security"}, SecuritySeverity: score},
				})
			}
			cve := ""
			if len(a.Aliases) > 0 {
				cve = " (" + strings.Join(a.Aliases, ", ") + ")"
			}
			fixed := ""
			if len(a.FixedVersions) > 0 {
				fixed = " Fixed in: " + strings.Join(a.FixedVersions, ", ") + "."
			}
			var suppressions []sarifSuppression
			if reason, ok := ignores.Ignored(a); ok {
				suppressions = []sarifSuppression{{Kind: "external", Justification: reason}}
			}
			results = append(results, sarifResult{
				RuleID:       id,
				Suppressions: suppressions,
				Level:        level,
				Message:      sarifText{Text: fmt.Sprintf("%s is affected by %s%s: %s.%s", coordinate, id, cve, a.Summary, fixed)},
				Locations:    []sarifLocation{{PhysicalLocation: sarifPhysical{ArtifactLocation: sarifArtifact{URI: "cappu.json"}}}},
				Properties: sarifResultProps{
					Coordinate: coordinate,
					Severity:   string(a.Severity),
					Path:       path,
				},
			})
		}
	}
	return sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool:    sarifTool{Driver: sarifDriver{Name: "cappu", InformationURI: "https://github.com/nikeee/cappu", Version: version, Rules: rules}},
			Results: results,
		}},
	}
}

func auditSarif(stdout *os.File, report audit.AuditReport, ignores audit.IgnoreResult, byKey map[packages.PackageKey]packages.ResolvedPackage) int {
	out, _ := json.MarshalIndent(buildAuditSarif(report, ignores, byKey, meta.Version), "", "  ")
	fmt.Fprintf(stdout, "%s\n", out)
	if unignoredFindings(report, ignores) > 0 {
		return 1
	}
	return 0
}

func auditText(stdout *os.File, report audit.AuditReport, ignores audit.IgnoreResult, byKey map[packages.PackageKey]packages.ResolvedPackage) int {
	paint := painter(stdout)
	sev := func(s audit.Severity, text string) string { return severityPaint(paint, s, text) }

	if len(report.Vulnerable) == 0 {
		fmt.Fprintf(stdout, "%s in %d packages\n", paint("green", "found no known vulnerabilities"), report.Scanned)
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
			fmt.Fprintf(stdout, "    %s%s\n", strings.Repeat("  ", i), label)
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
		fmt.Fprintf(stdout, "\n%s\n", sev(severity, strings.ToUpper(string(severity))))
		for _, f := range inBucket {
			cve := ""
			if len(f.a.Aliases) > 0 {
				cve = " (" + strings.Join(f.a.Aliases, ", ") + ")"
			}
			fixed := ""
			if len(f.a.FixedVersions) > 0 {
				fixed = "  [fixed in: " + strings.Join(f.a.FixedVersions, ", ") + "]"
			}
			ignored := ""
			if reason, ok := ignores.Ignored(f.a); ok {
				ignored = "  " + paint("yellow", "(ignored: "+reason+")")
			}
			fmt.Fprintf(stdout, "  %s  %s%s - %s%s%s\n", f.c.String(), f.a.ID, cve, f.a.Summary, fixed, ignored)
			fmt.Fprintf(stdout, "    %s\n", paint("dim", f.a.URL))
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
	unignored := unignoredFindings(report, ignores)
	ignoredNote := ""
	if n := total - unignored; n > 0 {
		ignoredNote = fmt.Sprintf(", %d ignored", n)
	}
	fmt.Fprintf(stdout, "\n%d %s (%s) across %d of %d packages%s\n",
		total, noun, strings.Join(parts, ", "), len(report.Vulnerable), report.Scanned, ignoredNote)
	if unignored > 0 {
		return 1
	}
	return 0
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
