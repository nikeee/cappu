// Package cli holds one file per cappu subcommand plus shared CLI styling.
// Ports of src/cli/.
package cli

import (
	"os"
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
// opted out via NO_COLOR (set and non-empty). Port of colorEnabled in color.ts;
// the env lookup is split out as a parameter so it stays testable.
func ColorEnabled(isTTY bool, noColor string) bool {
	return isTTY && noColor == ""
}

// painter returns a colour function for f: text unchanged when colour is off.
func painter(f *os.File) func(format, text string) string {
	on := ColorEnabled(isTTY(f), os.Getenv("NO_COLOR"))
	return func(format, text string) string {
		code, ok := styleCodes[format]
		if !on || !ok {
			return text
		}
		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
