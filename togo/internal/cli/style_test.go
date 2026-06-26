package cli

import (
	"testing"
	"time"
)

// Port of formatDuration in style.ts: "<1s" rounds to whole ms, else one decimal s.
func TestFormatDuration(t *testing.T) {
	cases := []struct {
		ms   float64
		want string
	}{
		{0, "0ms"},
		{499.4, "499ms"},
		{850, "850ms"},
		{999, "999ms"},
		{1000, "1.0s"},
		{1234, "1.2s"},
		{12340, "12.3s"},
	}
	for _, c := range cases {
		got := formatDuration(time.Duration(c.ms * float64(time.Millisecond)))
		if got != c.want {
			t.Errorf("formatDuration(%vms) = %q, want %q", c.ms, got, c.want)
		}
	}
}

// The timed-command set must match main.ts's TIMED_COMMANDS (lsp/mcp excluded).
func TestTimedCommands(t *testing.T) {
	for _, name := range []string{"install", "update", "add", "audit", "licenses", "publish", "verify", "compile", "check", "test"} {
		if !timedCommands[name] {
			t.Errorf("%q should be a timed command", name)
		}
	}
	for _, name := range []string{"lsp", "mcp", "init", "version", "search", "cache", "rage", "self-upgrade", "config-schema"} {
		if timedCommands[name] {
			t.Errorf("%q should not be a timed command", name)
		}
	}
}

// Port of src/cli/color.test.ts.
func TestColorEnabled(t *testing.T) {
	cases := []struct {
		isTTY   bool
		noColor string
		want    bool
	}{
		{true, "", true},
		{false, "", false},
		// NO_COLOR (https://no-color.org): set and non-empty disables colour...
		{true, "1", false},
		{true, "anything", false},
		// ...but an empty value does not count as set
		{true, "", true},
	}
	for _, c := range cases {
		env := func(name string) string {
			if name == "NO_COLOR" {
				return c.noColor
			}
			return ""
		}
		if got := ColorEnabled(c.isTTY, env); got != c.want {
			t.Errorf("ColorEnabled(%v, %q) = %v, want %v", c.isTTY, c.noColor, got, c.want)
		}
	}
}
