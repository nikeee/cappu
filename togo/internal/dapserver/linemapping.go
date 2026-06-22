package dapserver

// Map a source line to a JDWP code location across a class's methods. A
// breakpoint binds to the lowest code index reporting the line; if the exact
// line has no entry, it binds to the next executable line. Port of
// src/services/dap/lineMapping.ts.

import "github.com/nikeee/cappu/internal/jdwp"

type MethodLines struct {
	MethodID uint64
	Lines    []jdwp.LineTableEntry
}

type ResolvedLocation struct {
	MethodID uint64
	Index    uint64
	Line     int32
}

// ResolveLine picks the code location for requestedLine across all methods.
// Prefers an exact line match (lowest code index), else the next executable
// line at or after the request. ok is false when nothing at or after exists.
func ResolveLine(methods []MethodLines, requestedLine int32) (best ResolvedLocation, ok bool) {
	for _, m := range methods {
		for _, e := range m.Lines {
			if e.LineNumber < requestedLine {
				continue
			}
			cand := ResolvedLocation{MethodID: m.MethodID, Index: e.LineCodeIndex, Line: e.LineNumber}
			switch {
			case !ok || cand.Line < best.Line:
				best, ok = cand, true
			case cand.Line == best.Line && cand.Index < best.Index:
				best = cand
			}
		}
	}
	return best, ok
}
