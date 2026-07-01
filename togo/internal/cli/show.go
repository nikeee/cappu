package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/nikeee/cappu/internal/audit"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/lockfile"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// severityStyle maps a severity to its display colour. Port of SEVERITY_STYLE
// in src/cli/show.ts (critical is bold+red, rendered as nested paints).
var severityStyle = map[audit.Severity]string{
	audit.SeverityCritical: "red",
	audit.SeverityHigh:     "red",
	audit.SeverityModerate: "yellow",
	audit.SeverityLow:      "cyan",
	audit.SeverityUnknown:  "dim",
}

const labelWidth = 13

// searchHint is appended to the "bad coordinate" / "not found" errors: both
// mean the user does not have an exact coordinate yet, and `cappu search` is
// how to find one. Port of SEARCH_HINT in src/cli/show.ts.
const searchHint = "search for a package with `cappu search <query>`"

var whitespaceRe = regexp.MustCompile(`\s+`)

// projectShow is how group:artifact relates to the current project.
type projectShow struct {
	configurations []string
	declared       string // "" when not declared
	installed      string // "" when not locked
}

// showData is everything the card (and --json) shows, gathered once. Port of
// ShowData in src/cli/show.ts.
type showData struct {
	groupID, artifactID, version string
	explicitVersion              bool
	latestVersion                string // "" when no versions are published
	versionCount                 int
	newer                        int
	description                  string
	homepage                     string
	scmURL                       string
	spdx                         []string
	rawLicenses                  []string
	dependencies                 []packages.DependencyDeclaration
	project                      projectShow
	vulnerabilities              []audit.Advisory
}

// showError is a message to print and the exit code to use.
type showError struct {
	message string
	code    int
}

// parseCoordinate splits "group:artifact[:version]" into parts, ok=false if malformed.
func parseCoordinate(coord string) (groupID, artifactID, version string, ok bool) {
	parts := strings.Split(coord, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", "", "", false
	}
	for _, p := range parts {
		if p == "" {
			return "", "", "", false
		}
	}
	if len(parts) == 3 {
		version = parts[2]
	}
	return parts[0], parts[1], version, true
}

// listVersionsAcross returns the versions from the first source that has any.
func listVersionsAcross(groupID, artifactID string, srcs []packages.PackageSource) []string {
	for _, s := range srcs {
		versions, err := s.ListVersions(groupID, artifactID)
		if err == nil && len(versions) > 0 {
			return versions
		}
	}
	return nil
}

// metadataAcross returns the metadata from the first source that has it.
func metadataAcross(c packages.Coordinates, srcs []packages.PackageSource) *packages.PackageMetadata {
	for _, s := range srcs {
		if meta, err := s.GetMetadata(c); err == nil && meta != nil {
			return meta
		}
	}
	return nil
}

// gatherProjectContext reports where (if anywhere) this project depends on key.
func gatherProjectContext(cfg *config.Config, key string) projectShow {
	byName := map[string]map[string]string{
		"api":                 cfg.Dependencies.API,
		"implementation":      cfg.Dependencies.Implementation,
		"annotationProcessor": cfg.Dependencies.AnnotationProcessor,
		"testImplementation":  cfg.Dependencies.TestImplementation,
	}
	pc := projectShow{configurations: []string{}}
	for _, name := range config.Configurations {
		if v, ok := byName[name][key]; ok {
			pc.configurations = append(pc.configurations, name)
			if pc.declared == "" {
				pc.declared = v
			}
		}
	}
	if lock := lockfile.Read(cfg); lock != nil {
		all := slices.Concat(lock.Packages, lock.ProcessorPackages, lock.TestPackages)
		for _, p := range all {
			c := p.Coords()
			if string(c.Key()) == key {
				pc.installed = string(c.Version)
				break
			}
		}
	}
	return pc
}

// buildShowData gathers the full detail of one package, or a showError to print.
// Port of buildShowData in src/cli/show.ts.
func buildShowData(coord string, cfg *config.Config, srcs []packages.PackageSource, auditSource audit.AuditSource) (*showData, *showError) {
	groupID, artifactID, wantVersion, ok := parseCoordinate(coord)
	if !ok {
		return nil, &showError{
			message: "show needs group:artifact[:version], e.g. `cappu show com.google.code.gson:gson`; " + searchHint,
			code:    2,
		}
	}

	versions := listVersionsAcross(groupID, artifactID, srcs)
	latest := ""
	if len(versions) > 0 {
		latest = versions[len(versions)-1]
	}
	version := wantVersion
	if version == "" {
		version = latest
	}
	if version == "" {
		return nil, &showError{message: fmt.Sprintf("package not found: %s:%s; %s", groupID, artifactID, searchHint), code: 1}
	}
	c := packages.NewCoordinates(groupID, artifactID, version)

	meta := metadataAcross(c, srcs)
	if meta == nil && len(versions) == 0 {
		return nil, &showError{message: fmt.Sprintf("package not found: %s:%s:%s; %s", groupID, artifactID, version, searchHint), code: 1}
	}

	// OSV scan of just this version; a network failure must not sink the card.
	var vulnerabilities []audit.Advisory
	if report, err := audit.AuditPackages([]packages.Coordinates{c}, auditSource); err == nil && len(report.Vulnerable) > 0 {
		vulnerabilities = slices.Clone(report.Vulnerable[0].Advisories)
		// OSV returns advisories in database order; show them worst severity first.
		slices.SortStableFunc(vulnerabilities, func(a, b audit.Advisory) int {
			return slices.Index(audit.SeverityOrder, a.Severity) - slices.Index(audit.SeverityOrder, b.Severity)
		})
	}

	newer := 0
	if idx := slices.Index(versions, version); idx >= 0 {
		newer = len(versions) - 1 - idx
	}

	data := &showData{
		groupID:         groupID,
		artifactID:      artifactID,
		version:         version,
		explicitVersion: wantVersion != "",
		latestVersion:   latest,
		versionCount:    len(versions),
		newer:           newer,
		project:         gatherProjectContext(cfg, groupID+":"+artifactID),
		vulnerabilities: vulnerabilities,
	}
	if meta != nil {
		// POM descriptions are often multi-line and indented; collapse to one line.
		data.description = strings.TrimSpace(whitespaceRe.ReplaceAllString(meta.Description, " "))
		data.homepage = meta.Homepage
		data.scmURL = meta.ScmURL
		seen := map[string]bool{}
		for _, l := range meta.Licenses {
			data.rawLicenses = append(data.rawLicenses, l.Name)
			if id, ok := packages.NormalizeLicense(l.Name, l.URL); ok && !seen[string(id)] {
				seen[string(id)] = true
				data.spdx = append(data.spdx, string(id))
			}
		}
		data.dependencies = slices.Clone(meta.Dependencies)
		slices.SortFunc(data.dependencies, func(a, b packages.DependencyDeclaration) int {
			return strings.Compare(
				string(a.GroupID)+":"+string(a.ArtifactID),
				string(b.GroupID)+":"+string(b.ArtifactID),
			)
		})
	}
	return data, nil
}

// --- JSON output (one stable shape for --json) -------------------------------

type showDepJSON struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
	Scope      string `json:"scope,omitempty"`
	Optional   bool   `json:"optional,omitempty"`
}

type showAdvisoryJSON struct {
	ID            string   `json:"id"`
	Aliases       []string `json:"aliases"`
	Severity      string   `json:"severity"`
	Summary       string   `json:"summary"`
	FixedVersions []string `json:"fixedVersions"`
	URL           string   `json:"url"`
}

type showProjectJSON struct {
	Configurations []string `json:"configurations"`
	Declared       *string  `json:"declared"`
	Installed      *string  `json:"installed"`
}

type showJSON struct {
	GroupID         string             `json:"groupId"`
	ArtifactID      string             `json:"artifactId"`
	Version         string             `json:"version"`
	LatestVersion   *string            `json:"latestVersion"`
	VersionCount    int                `json:"versionCount"`
	Description     *string            `json:"description"`
	Homepage        *string            `json:"homepage"`
	ScmURL          *string            `json:"scmUrl"`
	License         []string           `json:"license"`
	Dependencies    []showDepJSON      `json:"dependencies"`
	Project         showProjectJSON    `json:"project"`
	Vulnerabilities []showAdvisoryJSON `json:"vulnerabilities"`
}

// nilIfEmpty returns a *string that is nil (JSON null) for the empty string.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func showToJSON(d *showData) showJSON {
	license := d.spdx
	if len(license) == 0 {
		license = d.rawLicenses
	}
	if license == nil {
		license = []string{}
	}
	deps := make([]showDepJSON, 0, len(d.dependencies))
	for _, dep := range d.dependencies {
		deps = append(deps, showDepJSON{
			GroupID:    string(dep.GroupID),
			ArtifactID: string(dep.ArtifactID),
			Version:    string(dep.Version),
			Scope:      string(dep.Scope),
			Optional:   dep.Optional,
		})
	}
	vulns := make([]showAdvisoryJSON, 0, len(d.vulnerabilities))
	for _, a := range d.vulnerabilities {
		aliases := a.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		fixed := a.FixedVersions
		if fixed == nil {
			fixed = []string{}
		}
		vulns = append(vulns, showAdvisoryJSON{
			ID:            string(a.ID),
			Aliases:       aliases,
			Severity:      string(a.Severity),
			Summary:       a.Summary,
			FixedVersions: fixed,
			URL:           a.URL,
		})
	}
	return showJSON{
		GroupID:       d.groupID,
		ArtifactID:    d.artifactID,
		Version:       d.version,
		LatestVersion: nilIfEmpty(d.latestVersion),
		VersionCount:  d.versionCount,
		Description:   nilIfEmpty(d.description),
		Homepage:      nilIfEmpty(d.homepage),
		ScmURL:        nilIfEmpty(d.scmURL),
		License:       license,
		Dependencies:  deps,
		Project: showProjectJSON{
			Configurations: d.project.configurations,
			Declared:       nilIfEmpty(d.project.declared),
			Installed:      nilIfEmpty(d.project.installed),
		},
		Vulnerabilities: vulns,
	}
}

// --- text card ---------------------------------------------------------------

// renderShowCard renders the coloured detail card (paint is a no-op when colour
// is disabled). Port of renderShowCard in src/cli/show.ts.
func renderShowCard(d *showData, paint func(format, text string) string) string {
	var lines []string
	row := func(label, value string) {
		lines = append(lines, "  "+paint("dim", fmt.Sprintf("%-*s", labelWidth, label))+value)
	}

	// Header: coordinates + version, with a freshness hint from the version list.
	hint := ""
	switch {
	case !d.explicitVersion || d.version == d.latestVersion:
		hint = paint("green", "latest")
	case d.newer > 0:
		hint = paint("yellow", fmt.Sprintf("%d newer available", d.newer))
	}
	header := paint("cyan", paint("bold", d.groupID+":"+d.artifactID)) + " " + paint("bold", d.version)
	if hint != "" {
		header += "  " + hint
	}
	lines = append(lines, header)
	if d.description != "" {
		lines = append(lines, paint("dim", "  "+d.description))
	}
	lines = append(lines, "")

	switch {
	case len(d.spdx) > 0:
		row("License", paint("cyan", strings.Join(d.spdx, ", ")))
	case len(d.rawLicenses) > 0:
		row("License", paint("yellow", strings.Join(d.rawLicenses, ", ")+" (no SPDX id)"))
	default:
		row("License", paint("dim", "none declared"))
	}
	if d.homepage != "" {
		row("Homepage", paint("blue", d.homepage))
	}
	if d.scmURL != "" {
		row("Repository", paint("blue", d.scmURL))
	}
	if d.latestVersion != "" {
		row("Versions", d.latestVersion+" "+paint("dim", "(latest)")+paint("dim", fmt.Sprintf(", %d published", d.versionCount)))
	}
	row("In project", formatProject(d.project, paint))

	lines = append(lines, "", "  "+paint("bold", fmt.Sprintf("Dependencies (%d)", len(d.dependencies))))
	if len(d.dependencies) == 0 {
		lines = append(lines, "    "+paint("dim", "none"))
	} else {
		for _, dep := range d.dependencies {
			lines = append(lines, "    "+formatDependency(dep, paint))
		}
	}

	lines = append(lines, "", "  "+paint("bold", "Vulnerabilities"))
	if len(d.vulnerabilities) == 0 {
		lines = append(lines, "    "+paint("green", "no known vulnerabilities"))
	} else {
		for _, a := range d.vulnerabilities {
			cve := ""
			if len(a.Aliases) > 0 {
				cve = " (" + strings.Join(a.Aliases, ", ") + ")"
			}
			fixed := ""
			if len(a.FixedVersions) > 0 {
				fixed = paint("dim", "  [fixed in: "+strings.Join(a.FixedVersions, ", ")+"]")
			}
			sev := paint(severityStyle[a.Severity], strings.ToUpper(string(a.Severity)))
			if a.Severity == audit.SeverityCritical {
				sev = paint("bold", sev) // critical is bold+red
			}
			lines = append(lines, "    "+sev+"  "+string(a.ID)+cve+" - "+a.Summary+fixed)
			lines = append(lines, "      "+paint("dim", a.URL))
		}
		lines = append(lines, "    "+paint("dim", "run `cappu audit` to scan the whole dependency tree"))
	}

	return strings.Join(lines, "\n") + "\n"
}

func formatProject(pc projectShow, paint func(format, text string) string) string {
	if len(pc.configurations) == 0 {
		return paint("dim", "not a direct dependency")
	}
	where := strings.Join(pc.configurations, ", ")
	var parts []string
	if pc.declared != "" {
		parts = append(parts, "declared "+pc.declared)
	}
	if pc.installed != "" {
		parts = append(parts, "installed "+pc.installed)
	}
	if len(parts) > 0 {
		where += paint("dim", " ("+strings.Join(parts, ", ")+")")
	}
	return where
}

func formatDependency(d packages.DependencyDeclaration, paint func(format, text string) string) string {
	coord := string(d.GroupID) + ":" + string(d.ArtifactID) + ":" + string(d.Version)
	var tags []string
	if d.Scope != "" && d.Scope != "compile" {
		tags = append(tags, string(d.Scope))
	}
	if d.Optional {
		tags = append(tags, "optional")
	}
	if len(tags) > 0 {
		coord += paint("dim", "  "+strings.Join(tags, ", "))
	}
	return coord
}

// RunShow handles `cappu show <coord>`: a single-package detail card. With
// jsonOut, the same data is emitted machine-readable. Port of src/cli/show.ts.
func RunShow(coord string, cfg *config.Config, jsonOut bool) int {
	auditSource := audit.NewOsvSource(audit.CachedFetchJSON(nil))
	data, showErr := buildShowData(coord, cfg, sources.Configured(cfg), auditSource)
	if showErr != nil {
		fmt.Fprintf(os.Stderr, "cappu: %s\n", showErr.message)
		return showErr.code
	}

	if jsonOut {
		b, _ := json.MarshalIndent(showToJSON(data), "", "  ")
		fmt.Fprintf(os.Stdout, "%s\n", b)
	} else {
		fmt.Fprint(os.Stdout, renderShowCard(data, painter(os.Stdout)))
	}
	if len(data.vulnerabilities) > 0 {
		return 1
	}
	return 0
}
