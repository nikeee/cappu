// Package javacdiag turns javac stderr into located diagnostics. A leaf package
// (no compiler/compile imports) so both the compile driver and the
// annotation-processing runner can use it without an import cycle. Port of
// src/compiler/javacDiagnostics.ts.
package javacdiag

import (
	"regexp"
	"strconv"
	"strings"
)

// CompileDiagnostic is a source diagnostic located for display (1-based line/column).
type CompileDiagnostic struct {
	Severity string // "error" | "warning"
	File     string
	Line     int
	Column   int
	Code     int
	Message  string
}

var (
	locatedRe    = regexp.MustCompile(`^(.+?):(\d+): (error|warning): (.*)$`)
	leadingSpace = regexp.MustCompile(`^\s`)
	summaryRe    = regexp.MustCompile(`^\d+ (error|warning)s?$`)
)

// ParseJavacDiagnostics parses javac's stderr: located `file:line: error|warning:
// msg` lines map 1:1; indented continuations and the `N errors` summary are
// dropped. If nothing located parses but something was printed, that collapses
// into one unlocated error.
func ParseJavacDiagnostics(stderr string) []CompileDiagnostic {
	var diagnostics []CompileDiagnostic
	var leftovers []string
	for _, line := range strings.Split(stderr, "\n") {
		if m := locatedRe.FindStringSubmatch(line); m != nil {
			sev := "error"
			if m[3] == "warning" {
				sev = "warning"
			}
			ln, _ := strconv.Atoi(m[2])
			diagnostics = append(diagnostics, CompileDiagnostic{Severity: sev, File: m[1], Line: ln, Message: m[4]})
		} else if strings.TrimSpace(line) != "" && !leadingSpace.MatchString(line) && !summaryRe.MatchString(line) {
			leftovers = append(leftovers, line)
		}
	}
	if len(diagnostics) == 0 && len(leftovers) > 0 {
		diagnostics = append(diagnostics, CompileDiagnostic{Severity: "error", Message: strings.Join(leftovers, " ")})
	}
	return diagnostics
}
