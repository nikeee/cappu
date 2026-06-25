// Port of src/cli/format.ts.
//
// `cappu format`: a google-java-format-compatible formatter (nikeee/cappu#24).
// By default it only CHECKS formatting - it lists the files that are not
// formatted and exits non-zero, changing nothing. With --write it rewrites
// those files in place. Files it cannot format without losing information (a
// syntax error, or a comment in an unsupported position) are skipped with a
// warning and never rewritten.

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/format"
)

// RunFormat handles `cappu format`. write rewrites differing files; otherwise it
// only checks. It returns the process exit code (0 ok, 1 unformatted, 2 usage).
func RunFormat(files []string, write bool, cfg *config.Config) int {
	style := cfg.FormatterOptions.Style

	// Explicit file arguments win; otherwise format the whole project.
	var targets []string
	if len(files) > 0 {
		cwd, _ := os.Getwd()
		for _, f := range files {
			if filepath.IsAbs(f) {
				targets = append(targets, f)
			} else {
				targets = append(targets, filepath.Join(cwd, f))
			}
		}
	} else {
		targets = build.FormattableFiles(cfg)
	}

	if len(targets) == 0 {
		fmt.Fprint(os.Stderr, "cappu: no .java files to format\n")
		return 0
	}

	paint := painter(os.Stderr)
	cwd, _ := os.Getwd()
	var unformatted []string
	var changed []string
	skipped := 0

	for _, file := range targets {
		rel, err := filepath.Rel(cwd, file)
		if err != nil {
			rel = file
		}
		source, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cappu: cannot read %s: %s\n", rel, err)
			return 2
		}

		formatted, err := format.FormatSource(string(source), format.FormatOptions{Style: style}, file)
		if err != nil {
			if errors.Is(err, format.ErrUnsupportedSyntax) {
				skipped++
				fmt.Fprint(os.Stderr, paint("dim", fmt.Sprintf("skipped %s (unsupported syntax)\n", rel)))
				continue
			}
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 2
		}

		if formatted == string(source) {
			continue
		}

		if write {
			if err := os.WriteFile(file, []byte(formatted), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "cappu: cannot write %s: %s\n", rel, err)
				return 2
			}
			changed = append(changed, rel)
			// List each rewritten file on stdout (machine-readable, like the check
			// mode lists the unformatted ones); the count summary goes to stderr.
			fmt.Fprintf(os.Stdout, "%s\n", rel)
		} else {
			unformatted = append(unformatted, rel)
			fmt.Fprintf(os.Stdout, "%s\n", rel)
			emitAnnotation("error", "not formatted (run `cappu format --write`)", AnnotationLocation{File: rel})
		}
	}

	if write {
		extra := ""
		if skipped > 0 {
			extra = fmt.Sprintf(", skipped %d", skipped)
		}
		fmt.Fprintf(os.Stderr, "cappu: formatted %d of %d file(s)%s\n", len(changed), len(targets), extra)
		return 0
	}

	if len(unformatted) > 0 {
		fmt.Fprintf(os.Stderr, "cappu: %d of %d file(s) not formatted; run `cappu format --write`\n", len(unformatted), len(targets))
		return 1
	}
	extra := ""
	if skipped > 0 {
		extra = fmt.Sprintf(" (skipped %d)", skipped)
	}
	fmt.Fprintf(os.Stderr, "cappu: all %d file(s) formatted%s\n", len(targets), extra)
	return 0
}
