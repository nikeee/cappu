package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Argument-validation parity with the TS build (src/cli/main.ts, add.ts,
// remove.ts, version.ts): configuration aliases must reach the handler (kong
// must not pre-reject them) and bad/missing args must exit 2 with the
// handler's message, not kong's exit 1.
func TestCliArgValidationParity(t *testing.T) {
	bin := cappu(t)
	cases := []struct {
		name     string
		args     []string
		exitCode int
		stderr   string
	}{
		{"add alias reaches handler", []string{"add", "i"}, 2, "usage: cappu add"},
		{"add no args", []string{"add"}, 2, "usage: cappu add"},
		{"add bad configuration", []string{"add", "bogus", "com.example:x"}, 2, "usage: cappu add"},
		{"remove alias reaches handler", []string{"remove", "ti"}, 2, "usage: cappu remove"},
		{"version missing release", []string{"version"}, 2, "cappu: version needs one of: major, minor, patch"},
		{"version bad release", []string{"version", "bogus"}, 2, "cappu: version needs one of: major, minor, patch"},
		{"audit rejects --json", []string{"audit", "--json"}, 2, "cappu: `audit` uses --format (text|sarif), not --json"},
		{"audit bad format", []string{"audit", "--format", "yaml"}, 2, "cappu: unknown --format 'yaml' (expected: text, sarif)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Dir = t.TempDir() // no cappu.json
			cmd.Env = childEnv()
			out, err := cmd.CombinedOutput()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			if code != tc.exitCode {
				t.Errorf("exit code = %d, want %d\n%s", code, tc.exitCode, out)
			}
			if !strings.Contains(string(out), tc.stderr) {
				t.Errorf("output missing %q:\n%s", tc.stderr, out)
			}
		})
	}
}
