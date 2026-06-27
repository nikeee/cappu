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
	"runtime"

	"golang.org/x/sync/errgroup"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/format"
)

// fileOutcome is the result of reading+formatting one file, produced in the
// parallel phase and consumed (in target order) in the serial phase.
type fileOutcome struct {
	rel       string
	readErr   string // file could not be read
	fmtErr    string // unexpected (non-unsupported) format error
	skipped   bool   // unsupported syntax: left untouched
	changed   bool   // differs from its formatted form
	formatted string // the formatted text (write mode)
}

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
	opts := format.FormatOptions{Style: style}

	// Parallel phase: read + format every file with bounded concurrency. The
	// formatter is independent per file, so this scales with the CPU count.
	// Results are written by index to keep output deterministic in target order
	// (FormatSource never mutates shared state; writes happen in the serial phase
	// below). Pattern mirrors internal/install's errgroup fan-out.
	outcomes := make([]fileOutcome, len(targets))
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	for i, file := range targets {
		g.Go(func() error {
			rel, err := filepath.Rel(cwd, file)
			if err != nil {
				rel = file
			}
			o := fileOutcome{rel: rel}
			source, err := os.ReadFile(file)
			if err != nil {
				o.readErr = err.Error()
				outcomes[i] = o
				return nil
			}
			formatted, ferr := format.FormatSource(string(source), opts, file)
			if ferr != nil {
				if errors.Is(ferr, format.ErrUnsupportedSyntax) {
					o.skipped = true
				} else {
					o.fmtErr = ferr.Error()
				}
				outcomes[i] = o
				return nil
			}
			if formatted != string(source) {
				o.changed = true
				o.formatted = formatted
			}
			outcomes[i] = o
			return nil
		})
	}
	_ = g.Wait()

	// Serial phase: emit output and apply writes in target order (deterministic).
	var unformatted []string
	var changed []string
	skipped := 0
	for i, o := range outcomes {
		if o.readErr != "" {
			fmt.Fprintf(os.Stderr, "cappu: cannot read %s: %s\n", o.rel, o.readErr)
			return 2
		}
		if o.fmtErr != "" {
			fmt.Fprintf(os.Stderr, "cappu: %s\n", o.fmtErr)
			return 2
		}
		if o.skipped {
			skipped++
			fmt.Fprint(os.Stderr, paint("dim", fmt.Sprintf("skipped %s (unsupported syntax)\n", o.rel)))
			continue
		}
		if !o.changed {
			continue
		}
		if write {
			if err := os.WriteFile(targets[i], []byte(o.formatted), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "cappu: cannot write %s: %s\n", o.rel, err)
				return 2
			}
			changed = append(changed, o.rel)
			// List each rewritten file on stdout (machine-readable, like the check
			// mode lists the unformatted ones); the count summary goes to stderr.
			fmt.Fprintf(os.Stdout, "%s\n", o.rel)
		} else {
			unformatted = append(unformatted, o.rel)
			fmt.Fprintf(os.Stdout, "%s\n", o.rel)
			emitAnnotation("error", "not formatted (run `cappu format --write`)", AnnotationLocation{File: o.rel})
		}
	}

	if write {
		extra := ""
		if skipped > 0 {
			extra = fmt.Sprintf(", %s", paint("yellow", fmt.Sprintf("skipped %d", skipped)))
		}
		fmt.Fprintf(os.Stderr, "cappu: formatted %s file(s)%s\n", paint("green", fmt.Sprintf("%d of %d", len(changed), len(targets))), extra)
		return 0
	}

	if len(unformatted) > 0 {
		fmt.Fprintf(os.Stderr, "cappu: %s file(s) not formatted; run %s\n", paint("red", fmt.Sprintf("%d of %d", len(unformatted), len(targets))), paint("bold", "`cappu format --write`"))
		return 1
	}
	extra := ""
	if skipped > 0 {
		extra = fmt.Sprintf(" (%s)", paint("yellow", fmt.Sprintf("skipped %d", skipped)))
	}
	fmt.Fprintf(os.Stderr, "cappu: %s%s\n", paint("green", fmt.Sprintf("all %d file(s) formatted", len(targets))), extra)
	return 0
}
