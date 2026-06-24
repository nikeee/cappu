package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/install"
)

// FormatOutdated renders the outdated rows as an aligned table, or "" when
// nothing is outdated. Port of formatOutdated.
func FormatOutdated(rows []install.OutdatedDependency) string {
	if len(rows) == 0 {
		return ""
	}
	header := []string{"dependency", "current", "wanted", "latest", "configuration"}
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		wanted := r.Wanted
		if wanted == "" {
			wanted = r.Current
		}
		latest := r.Latest
		if latest == "" {
			latest = wanted
		}
		cells = append(cells, []string{r.Key, r.Current, wanted, latest, r.Configuration})
	}
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len(h)
	}
	for _, row := range cells {
		for i, c := range row {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	line := func(cols []string) string {
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = c + strings.Repeat(" ", widths[i]-len(c))
		}
		return strings.TrimRight(strings.Join(parts, "  "), " ")
	}
	var b strings.Builder
	b.WriteString(line(header) + "\n")
	for _, row := range cells {
		b.WriteString(line(row) + "\n")
	}
	return b.String()
}

// RunOutdated handles `cappu outdated`: report every declared dependency that
// has a newer published version (current/wanted/latest). Read-only - it never
// edits cappu.json or the lock. Port of src/cli/outdated.ts.
func RunOutdated(cfg *config.Config) int {
	if !cfg.FromFile {
		fmt.Fprintln(os.Stderr, "cappu: no cappu.json found - run `cappu init` first")
		emitAnnotation("error", "no cappu.json found - run `cappu init` first", AnnotationLocation{})
		return 1
	}
	rows, err := install.PlanOutdated(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cappu: outdated failed: %s\n", err)
		emitAnnotation("error", fmt.Sprintf("outdated failed: %s", err), AnnotationLocation{})
		return 2
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stdout, "all dependencies are up to date")
		return 0
	}
	fmt.Fprint(os.Stdout, FormatOutdated(rows))
	return 0
}
