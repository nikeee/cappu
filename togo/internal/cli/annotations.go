package cli

// Port of src/cli/annotations.ts. GitHub Actions / Forgejo / Gitea
// workflow-command annotations: when cappu runs inside a runner that parses
// GitHub-style workflow commands, errors and warnings are echoed as
// `::error file=...,line=...::message` so they surface as inline annotations,
// in addition to the normal stderr output.
// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// AnnotationLocation is the optional file/line/col of an annotation. A zero
// field is treated as absent (line/col 0 is never a real 1-based position).
type AnnotationLocation struct {
	File   string
	Line   int
	Column int
}

// AnnotationsEnabled reports whether the current runner understands
// GitHub-style workflow commands. Forgejo/Gitea Actions also set
// GITHUB_ACTIONS=true for compatibility, but we check their own vars too so the
// intent is explicit. Bare CI=true is not a trigger: there is no generic-CI
// annotation format, and emitting GitHub syntax in a non-GitHub runner would
// just be noise. The env lookup is a parameter so it stays testable.
func AnnotationsEnabled(env func(string) string) bool {
	return env("GITHUB_ACTIONS") == "true" ||
		env("FORGEJO_ACTIONS") == "true" ||
		env("GITEA_ACTIONS") == "true"
}

// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands#example-setting-an-error-message
var dataEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
var propEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")

// FormatAnnotation builds one workflow-command line (no trailing newline).
func FormatAnnotation(severity, message string, loc AnnotationLocation) string {
	var props []string
	if loc.File != "" {
		props = append(props, "file="+propEscaper.Replace(loc.File))
	}
	if loc.Line != 0 {
		props = append(props, "line="+propEscaper.Replace(strconv.Itoa(loc.Line)))
	}
	if loc.Column != 0 {
		props = append(props, "col="+propEscaper.Replace(strconv.Itoa(loc.Column)))
	}
	head := "::" + severity
	if len(props) > 0 {
		head += " " + strings.Join(props, ",")
	}
	return head + "::" + dataEscaper.Replace(message)
}

// emitAnnotation echoes an annotation to stderr when running under a supporting
// CI runner.
func emitAnnotation(severity, message string, loc AnnotationLocation) {
	if AnnotationsEnabled(os.Getenv) {
		fmt.Fprintln(os.Stderr, FormatAnnotation(severity, message, loc))
	}
}

// EmitErrorAnnotation is the exported, location-less error variant for callers
// outside package cli (the config-load error path in package main).
func EmitErrorAnnotation(message string) {
	emitAnnotation("error", message, AnnotationLocation{})
}
