// Port of src/format/doc.ts.
//
// A small Wadler/Leijen-style document IR and printer - the same model
// google-java-format, prettier, ruff and biome use: build a tree of layout
// intentions, then print it at a target column width, breaking a group onto
// multiple lines only when it does not fit flat.
//
// The AST -> Doc lowering lives in printer.go; this file is purely the IR and
// the printing algorithm and knows nothing about Java.

// Package format is a self-contained, google-java-format-compatible Java source
// formatter (nikeee/cappu#24). Port of src/format.
package format

import "strings"

type docKind int

const (
	docText docKind = iota
	docConcat
	docLine
	docGroup
	docIndent
)

// Doc is a node in the layout IR. A string-typed Doc in the TypeScript original
// is represented here by a docText node (see text).
type Doc struct {
	kind  docKind
	text  string // docText
	parts []Doc  // docConcat
	soft  bool   // docLine
	hard  bool   // docLine
	child *Doc   // docGroup, docIndent
}

// text is a literal Doc (the TS original uses bare strings).
func text(s string) Doc { return Doc{kind: docText, text: s} }

// line is a break that is a space when flat and a newline when its group breaks.
var line = Doc{kind: docLine, soft: false, hard: false}

// softline is a break that is nothing when flat and a newline when its group breaks.
var softline = Doc{kind: docLine, soft: true, hard: false}

// hardline is always a newline; it forces every enclosing group to break.
var hardline = Doc{kind: docLine, soft: false, hard: true}

func concat(parts ...Doc) Doc { return Doc{kind: docConcat, parts: parts} }

// join joins parts with sep between each.
func join(sep Doc, parts []Doc) Doc {
	out := make([]Doc, 0, len(parts)*2)
	for i, p := range parts {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, p)
	}
	return concat(out...)
}

// group lays the doc out flat if it fits the remaining width, else breaks its lines.
func group(doc Doc) Doc {
	d := doc
	return Doc{kind: docGroup, child: &d}
}

// indent increases the indent of any newline produced inside doc by one unit.
func indent(doc Doc) Doc {
	d := doc
	return Doc{kind: docIndent, child: &d}
}

// --- printing --------------------------------------------------------------

type mode int

const (
	modeFlat mode = iota
	modeBreak
)

type cmd struct {
	indent int
	mode   mode
	doc    Doc
}

type printOptions struct {
	width      int // hard wrap column (google-java-format: 100)
	indentUnit int // spaces per indent level (google: 2, aosp: 4)
}

// fits reports whether the command (and everything queued after it on the
// current line) fits in remaining columns laid out flat. A hardline never fits,
// which is what breaks a group containing one onto multiple lines.
func fits(remaining int, next cmd, rest []cmd, indentUnit int) bool {
	if remaining < 0 {
		return false
	}
	cmds := []cmd{next}
	restIdx := len(rest) - 1
	for remaining >= 0 {
		if len(cmds) == 0 {
			if restIdx < 0 {
				return true
			}
			cmds = append(cmds, rest[restIdx])
			restIdx--
			continue
		}
		// biome/prettier order: process the most recently pushed command (a stack).
		c := cmds[len(cmds)-1]
		cmds = cmds[:len(cmds)-1]
		doc := c.doc
		switch doc.kind {
		case docText:
			remaining -= len(doc.text)
		case docConcat:
			for i := len(doc.parts) - 1; i >= 0; i-- {
				cmds = append(cmds, cmd{indent: c.indent, mode: c.mode, doc: doc.parts[i]})
			}
		case docIndent:
			cmds = append(cmds, cmd{indent: c.indent + indentUnit, mode: c.mode, doc: *doc.child})
		case docGroup:
			// Inside fits-check a group is measured in its parent's mode.
			cmds = append(cmds, cmd{indent: c.indent, mode: c.mode, doc: *doc.child})
		case docLine:
			// A line break that will actually break (the surrounding content is in
			// Break mode, or a later hardline in the trailing rest) ends the current
			// line, so everything measured so far fits. Only a hardline *inside* the
			// group being measured flat means the group cannot stay flat.
			if c.mode == modeBreak {
				return true
			}
			if doc.hard {
				return false
			}
			if !doc.soft {
				remaining-- // a flat soft line is nothing, a flat line is a space
			}
		}
	}
	return false
}

func printDoc(doc Doc, options printOptions) string {
	width, indentUnit := options.width, options.indentUnit
	var out []string
	pos := 0 // current column
	cmds := []cmd{{indent: 0, mode: modeBreak, doc: doc}}

	for len(cmds) > 0 {
		c := cmds[len(cmds)-1]
		cmds = cmds[:len(cmds)-1]
		d := c.doc
		switch d.kind {
		case docText:
			out = append(out, d.text)
			pos += len(d.text)
		case docConcat:
			for i := len(d.parts) - 1; i >= 0; i-- {
				cmds = append(cmds, cmd{indent: c.indent, mode: c.mode, doc: d.parts[i]})
			}
		case docIndent:
			cmds = append(cmds, cmd{indent: c.indent + indentUnit, mode: c.mode, doc: *d.child})
		case docGroup:
			flat := cmd{indent: c.indent, mode: modeFlat, doc: *d.child}
			if fits(width-pos, flat, cmds, indentUnit) {
				cmds = append(cmds, flat)
			} else {
				cmds = append(cmds, cmd{indent: c.indent, mode: modeBreak, doc: *d.child})
			}
		case docLine:
			if c.mode == modeFlat && !d.hard {
				if !d.soft {
					out = append(out, " ")
					pos++
				}
			} else {
				// Trim trailing spaces on the line we are ending (g-j-f never leaves them).
				trimTrailingSpace(&out)
				out = append(out, "\n"+strings.Repeat(" ", c.indent))
				pos = c.indent
			}
		}
	}
	trimTrailingSpace(&out)
	return strings.Join(out, "")
}

func trimTrailingSpace(out *[]string) {
	// Walk back over pushed chunks that are all spaces; trim the first non-space.
	o := *out
	for i := len(o) - 1; i >= 0; i-- {
		s := o[i]
		if len(s) == 0 {
			continue
		}
		if isAllSpaces(s) {
			o[i] = ""
			continue
		}
		trimmed := strings.TrimRight(s, " ")
		if trimmed != s {
			o[i] = trimmed
		}
		break
	}
}

func isAllSpaces(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' {
			return false
		}
	}
	return len(s) > 0
}
