package mcp

// MCP project tools: read-only package-management queries (audit, licenses,
// search, outdated, latestVersion, dependencyTree) over the project's resolved
// dependency graph. Unlike the semantic tools in tools.go these resolve
// dependencies from the configured sources (not the Java program) and return the
// same structured findings as the `cappu audit` / `licenses` / `search` CLI
// commands. Sources are injectable so the transport passes real network sources while
// tests pass in-memory ones. Port of src/services/mcpProject.ts.

import (
	"cmp"
	"slices"
	"strings"

	"github.com/nikeee/cappu/internal/audit"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// McpAdvisory is one CVE advisory.
type McpAdvisory struct {
	ID            string         `json:"id"`
	Aliases       []string       `json:"aliases"`
	Severity      audit.Severity `json:"severity"`
	Summary       string         `json:"summary"`
	FixedVersions []string       `json:"fixedVersions"`
	URL           string         `json:"url"`
}

// McpVulnerablePackage is a vulnerable package plus why it is in the tree.
type McpVulnerablePackage struct {
	Coordinate string        `json:"coordinate"`
	Path       []string      `json:"path"`
	Advisories []McpAdvisory `json:"advisories"`
}

// McpAuditReport is the audit tool result.
type McpAuditReport struct {
	Scanned    int                    `json:"scanned"`
	Counts     audit.Counts           `json:"counts"`
	Vulnerable []McpVulnerablePackage `json:"vulnerable"`
}

// McpLicenseEntry is a declared license.
type McpLicenseEntry struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// McpLicenseRow is one package's licenses.
type McpLicenseRow struct {
	Coordinate string            `json:"coordinate"`
	Licenses   []McpLicenseEntry `json:"licenses"`
	SPDX       []string          `json:"spdx"`
}

// McpOutdatedDependency is one declared dependency with a newer version.
type McpOutdatedDependency struct {
	Configuration string `json:"configuration"`
	Coordinate    string `json:"coordinate"`
	From          string `json:"from"`
	To            string `json:"to"`
}

// McpTreeNode is one node of the resolved dependency graph.
type McpTreeNode struct {
	Coordinate  string `json:"coordinate"`
	Depth       int    `json:"depth"`
	RequestedBy string `json:"requestedBy,omitempty"`
}

// ProjectToolDeps injects the package sources and CVE source (defaults: the
// configured sources and OSV over a disk-caching fetcher).
type ProjectToolDeps struct {
	Sources     []packages.PackageSource
	AuditSource audit.AuditSource
}

// ProjectTools is the package-management MCP tool surface.
type ProjectTools struct {
	config      *config.Config
	sources     []packages.PackageSource
	auditSource audit.AuditSource
}

// NewProjectTools builds the project tools over a config and injectable deps.
func NewProjectTools(cfg *config.Config, deps ProjectToolDeps) *ProjectTools {
	srcs := deps.Sources
	if srcs == nil {
		srcs = sources.Configured(cfg)
	}
	auditSource := deps.AuditSource
	if auditSource == nil {
		auditSource = audit.NewOsvSource(audit.CachedFetchJSON(nil))
	}
	return &ProjectTools{config: cfg, sources: srcs, auditSource: auditSource}
}

// resolveAll resolves the whole graph (compile + processor + test, transitive).
func (t *ProjectTools) resolveAll() (packages.Resolution, error) {
	roots := append(append(sources.CompileRoots(t.config), sources.ProcessorRoots(t.config)...), sources.TestRoots(t.config)...)
	return packages.ResolveTransitive(roots, t.sources, nil)
}

// dependencyPath is the chain [root, ..., target] following requestedBy edges.
func dependencyPath(byKey map[packages.PackageKey]packages.ResolvedPackage, target packages.Coordinates) []packages.Coordinates {
	var path []packages.Coordinates
	seen := map[packages.PackageKey]bool{}
	current := target
	has := true
	for has {
		key := current.Key()
		if seen[key] {
			break
		}
		seen[key] = true
		path = append([]packages.Coordinates{current}, path...)
		p, ok := byKey[key]
		if !ok || (p.RequestedBy == packages.Coordinates{}) {
			break
		}
		current = p.RequestedBy
	}
	return path
}

// Audit reports vulnerable packages with severity counts and dependency paths.
func (t *ProjectTools) Audit() (McpAuditReport, error) {
	resolution, err := t.resolveAll()
	if err != nil {
		return McpAuditReport{}, err
	}
	byKey := map[packages.PackageKey]packages.ResolvedPackage{}
	coords := make([]packages.Coordinates, 0, len(resolution.Packages))
	for _, p := range resolution.Packages {
		byKey[p.Coordinates.Key()] = p
		coords = append(coords, p.Coordinates)
	}
	report, err := audit.AuditPackages(coords, t.auditSource)
	if err != nil {
		return McpAuditReport{}, err
	}
	vulnerable := []McpVulnerablePackage{}
	for _, p := range report.Vulnerable {
		var pathStrs []string
		for _, c := range dependencyPath(byKey, p.Coordinates) {
			pathStrs = append(pathStrs, string(c.String()))
		}
		var advisories []McpAdvisory
		for _, a := range p.Advisories {
			advisories = append(advisories, McpAdvisory{
				ID:            string(a.ID),
				Aliases:       append([]string{}, a.Aliases...),
				Severity:      a.Severity,
				Summary:       a.Summary,
				FixedVersions: append([]string{}, a.FixedVersions...),
				URL:           a.URL,
			})
		}
		vulnerable = append(vulnerable, McpVulnerablePackage{Coordinate: string(p.Coordinates.String()), Path: pathStrs, Advisories: advisories})
	}
	return McpAuditReport{Scanned: report.Scanned, Counts: report.Counts, Vulnerable: vulnerable}, nil
}

// Licenses lists resolved packages with their declared licenses and SPDX ids, sorted.
func (t *ProjectTools) Licenses() ([]McpLicenseRow, error) {
	resolution, err := t.resolveAll()
	if err != nil {
		return nil, err
	}
	rows := []McpLicenseRow{}
	for _, p := range resolution.Packages {
		licenses := []McpLicenseEntry{}
		for _, l := range p.Metadata.Licenses {
			licenses = append(licenses, McpLicenseEntry{Name: l.Name, URL: l.URL})
		}
		spdx := []string{}
		for _, s := range p.Metadata.LicenseNormalized {
			spdx = append(spdx, string(s))
		}
		rows = append(rows, McpLicenseRow{Coordinate: string(p.Coordinates.String()), Licenses: licenses, SPDX: spdx})
	}
	slices.SortStableFunc(rows, func(a, b McpLicenseRow) int { return cmp.Compare(a.Coordinate, b.Coordinate) })
	return rows, nil
}

// SearchPackages returns the matching coordinate strings for a query.
func (t *ProjectTools) SearchPackages(query string) ([]string, error) {
	hits, err := packages.SearchPackages(query, t.sources)
	if err != nil {
		return nil, err
	}
	matches := []string{}
	for _, c := range hits {
		matches = append(matches, string(c.String()))
	}
	return matches, nil
}

// Outdated previews `cappu update`: the newest conflict-free stable bump per dep.
func (t *ProjectTools) Outdated() ([]McpOutdatedDependency, error) {
	bumps, err := install.PlanUpdates(t.config, t.sources)
	if err != nil {
		return nil, err
	}
	out := []McpOutdatedDependency{}
	for _, b := range bumps {
		out = append(out, McpOutdatedDependency{Configuration: b.Configuration, Coordinate: b.Key, From: b.From, To: b.To})
	}
	return out, nil
}

// LatestResult is the latestVersion tool result.
type LatestResult struct {
	Coordinate string `json:"coordinate"`
	Latest     string `json:"latest,omitempty"`
}

// LatestVersion returns the newest published version of a "group:artifact".
func (t *ProjectTools) LatestVersion(coord string) (LatestResult, error) {
	groupID, artifactID := splitCoord2(coord)
	version, err := packages.LatestVersion(groupID, artifactID, t.sources)
	if err != nil {
		return LatestResult{}, err
	}
	return LatestResult{Coordinate: groupID + ":" + artifactID, Latest: version}, nil
}

// DependencyTreeResult is the dependencyTree tool result (a graph, or - with a
// coord - the path that introduces it).
type DependencyTreeResult struct {
	Packages []McpTreeNode `json:"packages,omitempty"`
	Path     []string      `json:"path,omitempty"`
}

// DependencyTree returns the whole resolved graph, or the path to a coordinate.
func (t *ProjectTools) DependencyTree(coord string) (DependencyTreeResult, error) {
	resolution, err := t.resolveAll()
	if err != nil {
		return DependencyTreeResult{}, err
	}
	if coord != "" {
		byKey := map[packages.PackageKey]packages.ResolvedPackage{}
		for _, p := range resolution.Packages {
			byKey[p.Coordinates.Key()] = p
		}
		g, a, v := splitCoord3(coord)
		target := packages.NewCoordinates(g, a, v)
		path := []string{}
		for _, c := range dependencyPath(byKey, target) {
			path = append(path, string(c.String()))
		}
		return DependencyTreeResult{Path: path}, nil
	}
	nodes := []McpTreeNode{}
	for _, p := range resolution.Packages {
		node := McpTreeNode{Coordinate: string(p.Coordinates.String()), Depth: p.Depth}
		if (p.RequestedBy != packages.Coordinates{}) {
			node.RequestedBy = string(p.RequestedBy.String())
		}
		nodes = append(nodes, node)
	}
	return DependencyTreeResult{Packages: nodes}, nil
}

func splitCoord2(coord string) (group, artifact string) {
	parts := strings.SplitN(coord, ":", 2)
	group = parts[0]
	if len(parts) > 1 {
		artifact = parts[1]
	}
	return
}

func splitCoord3(coord string) (group, artifact, version string) {
	parts := strings.SplitN(coord, ":", 3)
	group = parts[0]
	if len(parts) > 1 {
		artifact = parts[1]
	}
	if len(parts) > 2 {
		version = parts[2]
	}
	return
}
