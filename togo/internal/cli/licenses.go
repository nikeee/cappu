package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

// WarnUnmappedLicenses prints a warning for every declared license with no SPDX
// mapping. Shared by the resolving commands (licenses, and later install/audit).
// Port of warnUnmappedLicenses in src/cli/licenses.ts.
func WarnUnmappedLicenses(pkgs []packages.ResolvedPackage) {
	paint := painter(os.Stderr)
	for _, pkg := range pkgs {
		for _, license := range pkg.Metadata.Licenses {
			if _, ok := packages.NormalizeLicense(license.Name, license.URL); ok {
				continue
			}
			name, _ := json.Marshal(license.Name)
			fmt.Fprintf(os.Stderr, "%s %s: license %s has no SPDX mapping\n",
				paint("yellow", "warning:"), pkg.Coordinates.String(), name)
		}
	}
}

// licenseEntry is a raw POM license plus its best-effort SPDX id (null when the
// name/url maps to nothing).
type licenseEntry struct {
	Name string  `json:"name"`
	URL  string  `json:"url,omitempty"`
	Spdx *string `json:"spdx"`
}

type licenseRow struct {
	Coordinate string         `json:"coordinate"`
	Licenses   []licenseEntry `json:"licenses"`
}

// newLicenseEntry builds a licenseEntry, filling spdx with the best-effort SPDX
// id (nil when name/url maps to nothing).
func newLicenseEntry(name, url string) licenseEntry {
	e := licenseEntry{Name: name, URL: url}
	if id, ok := packages.NormalizeLicense(name, url); ok {
		s := string(id)
		e.Spdx = &s
	}
	return e
}

func licenseEntries(ls []packages.License) []licenseEntry {
	out := make([]licenseEntry, len(ls))
	for i, l := range ls {
		out[i] = newLicenseEntry(l.Name, l.URL)
	}
	return out
}

// RunLicenses handles `cappu licenses`: resolve the full dependency graph
// (compile + processor + test, transitive) and print each package with the
// license it ships under - the best-effort SPDX id when one maps, otherwise the
// raw POM name. --json emits the same data machine-readable. Port of
// src/cli/licenses.ts.
func RunLicenses(cfg *config.Config, jsonOut bool) int {
	roots := append(sources.CompileRoots(cfg), sources.ProcessorRoots(cfg)...)
	roots = append(roots, sources.TestRoots(cfg)...)

	resolving := 0
	showProgress := ColorEnabled(isTTY(os.Stderr), os.Getenv)
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

	rows := make([]licenseRow, 0, len(resolution.Packages))
	for _, p := range resolution.Packages {
		rows = append(rows, licenseRow{
			Coordinate: string(p.Coordinates.String()),
			Licenses:   licenseEntries(p.Metadata.Licenses),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Coordinate < rows[j].Coordinate })

	if jsonOut {
		// The project's own license appears in the human output; include it here
		// too (as a leading row) so --json carries the same information.
		out := rows
		if cfg.License != "" {
			out = append([]licenseRow{{
				Coordinate: "this project",
				Licenses:   []licenseEntry{newLicenseEntry(cfg.License, "")},
			}}, rows...)
		}
		buf, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintf(os.Stdout, "%s\n", buf)
		return 0
	}

	out := painter(os.Stdout)
	if cfg.License != "" {
		fmt.Fprintf(os.Stdout, "%s %s\n", out("dim", "this project:"), out("bold", out("cyan", cfg.License)))
	}
	width := 0
	for _, r := range rows {
		if len(r.Coordinate) > width {
			width = len(r.Coordinate)
		}
	}
	for _, r := range rows {
		var spdx []string
		seen := map[string]bool{}
		for _, l := range r.Licenses {
			if l.Spdx != nil && !seen[*l.Spdx] {
				seen[*l.Spdx] = true
				spdx = append(spdx, *l.Spdx)
			}
		}
		var label string
		switch {
		case len(spdx) > 0:
			label = out("cyan", strings.Join(spdx, ", "))
		case len(r.Licenses) > 0:
			raw := make([]string, len(r.Licenses))
			for i, l := range r.Licenses {
				raw[i] = l.Name
			}
			label = out("yellow", strings.Join(raw, ", ")+" (no SPDX id)")
		default:
			label = out("dim", "no license declared")
		}
		fmt.Fprintf(os.Stdout, "%-*s  %s\n", width, r.Coordinate, label)
	}
	return 0
}
