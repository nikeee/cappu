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
	"licenses": true, "tree": true, "publish": true, "verify": true, "compile": true, "test": true,
	"format": true,
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

// downloadBar is the shared CLI download progress bar (jars + JDK archives): a
// {bar} {value}/{total} {label} line redrawn in place on stderr. newDownloadBar
// returns nil when the stream is not a colour-capable TTY, so piped output stays
// plain; the methods are nil-safe. Port of downloadBar in style.ts (the
// cli-progress SingleBar), reimplemented with a plain \r redraw to avoid a
// progress-bar dependency.
type downloadBar struct {
	stream *os.File
	paint  func(format, text string) string
	total  int
	unit   string // "" for a plain item count, e.g. "MiB" otherwise
}

const downloadBarWidth = 40

func newDownloadBar(stream *os.File, unit string) *downloadBar {
	if !ColorEnabled(isTTY(stream), os.Getenv) {
		return nil
	}
	return &downloadBar{stream: stream, paint: painter(stream), unit: unit}
}

func (b *downloadBar) start(total int) {
	if b == nil {
		return
	}
	b.total = total
}

func (b *downloadBar) update(value int, label string) {
	if b == nil {
		return
	}
	frac := 0.0
	if b.total > 0 {
		frac = float64(value) / float64(b.total)
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * downloadBarWidth)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", downloadBarWidth-filled)
	count := fmt.Sprintf("%d/%d", value, b.total)
	if b.unit != "" {
		count += " " + b.unit
	}
	fmt.Fprintf(b.stream, "\r\x1b[2K%s %s %s", b.paint("cyan", bar), b.paint("bold", count), b.paint("dim", label))
}

// stop clears the bar line (cli-progress clearOnComplete).
func (b *downloadBar) stop() {
	if b == nil {
		return
	}
	fmt.Fprint(b.stream, "\r\x1b[2K")
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
