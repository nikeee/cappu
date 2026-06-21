// Package cli holds one file per cappu subcommand plus shared CLI styling.
// Ports of src/cli/.
package cli

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// ANSI SGR codes for the few styles the commands use.
var styleCodes = map[string]string{
	"red":    "31",
	"green":  "32",
	"yellow": "33",
	"dim":    "2",
	"bold":   "1",
	"cyan":   "36",
}

// ColorEnabled reports whether coloured output may render: a TTY that has not
// opted out via NO_COLOR (set and non-empty) and is not being driven by an AI
// agent. Port of colorEnabled in color.ts; the env lookup is a parameter so it
// stays testable.
func ColorEnabled(isTTY bool, env func(string) string) bool {
	return isTTY && env("NO_COLOR") == "" && !AgentEnabled(env)
}

// painter returns a colour function for f: text unchanged when colour is off.
func painter(f *os.File) func(format, text string) string {
	on := ColorEnabled(isTTY(f), os.Getenv)
	return func(format, text string) string {
		code, ok := styleCodes[format]
		if !on || !ok {
			return text
		}
		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
}

// timedCommands are the dependency/build commands whose duration is printed
// when they finish. lsp/mcp run until the client disconnects, so a duration
// there is meaningless. Port of TIMED_COMMANDS in main.ts.
var timedCommands = map[string]bool{
	"install": true, "update": true, "add": true, "audit": true,
	"licenses": true, "publish": true, "verify": true, "compile": true, "test": true,
}

// formatDuration is a short human duration: "850ms" under a second, else "1.2s".
// Port of formatDuration in style.ts.
func formatDuration(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	if ms < 1000 {
		return fmt.Sprintf("%dms", int(math.Round(ms)))
	}
	return fmt.Sprintf("%.1fs", ms/1000)
}

// PrintDurationFooter writes "done in <dur>" (dim, to stderr) for a timed
// command, however it exited. command is kong's selected command path; only the
// first word is matched. Port of the process.on("exit") footer in main.ts.
func PrintDurationFooter(command string, d time.Duration) {
	name := command
	if i := strings.IndexByte(name, ' '); i >= 0 {
		name = name[:i]
	}
	if !timedCommands[name] {
		return
	}
	paint := painter(os.Stderr)
	fmt.Fprint(os.Stderr, paint("dim", "done in "+formatDuration(d)+"\n"))
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
