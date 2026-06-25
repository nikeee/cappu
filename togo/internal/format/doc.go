// Port of src/format/doc.ts.
//
// A port of google-java-format's line-breaking engine (the
// com.google.googlejavaformat.Doc / Indent algorithm), not vanilla
// Wadler/Leijen. A Level breaks its direct Breaks by FillMode (UNIFIED = all
// together, INDEPENDENT = fill, FORCED = always), propagates a "must break" flag
// across a broken Level, and carries the continuation indent on the Level/Break.
//
// The AST -> Doc lowering lives in printer.go; this file is purely the IR and
// the breaking algorithm. A leaf is a literal-text token (gjf's Input/Tok
// machinery is dropped - comments are emitted as plain text by the printer).

// Package format is a self-contained, google-java-format-compatible Java source
// formatter (nikeee/cappu#24). Port of src/format.
package format

import "strings"

const maxWidth = 1000

// FillMode controls how a Level breaks its direct Breaks when it does not fit.
type FillMode int

const (
	fillUnified     FillMode = iota // all break together
	fillIndependent                 // break independently, to fill
	fillForced                      // always break
)

// BreakTag records whether a particular Break was taken, for a conditional Indent.
type BreakTag struct{ broken bool }

func (t *BreakTag) recordBroken(b bool) { t.broken = b }
func (t *BreakTag) wasBreakTaken() bool { return t.broken }

// --- indent ----------------------------------------------------------------

// Indent carries a continuation amount in columns at google scale (one indent
// level = 2, a continuation = 4); evalIndent multiplies by the style multiplier
// (1 google, 2 aosp). gjf bakes the multiplier at construction; deferring it
// lets the printer build a Doc once and print it in either style. The cond/then/
// else fields support a future conditional indent (gjf's Indent.If).
type Indent struct {
	isConst bool
	n       int
	cond    *BreakTag
	thenI   *Indent
	elseI   *Indent
}

func indentConst(n int) Indent { return Indent{isConst: true, n: n} }

// ZERO is the zero indent.
var ZERO = indentConst(0)

func evalIndent(i Indent, mult int) int {
	if i.isConst {
		return i.n * mult
	}
	if i.cond.wasBreakTaken() {
		return evalIndent(*i.thenI, mult)
	}
	return evalIndent(*i.elseI, mult)
}

// --- doc nodes -------------------------------------------------------------

// Doc is a node in the layout IR.
type Doc interface {
	width() int
	flat() string
}

type token struct {
	text string
	w    int
}

func (t *token) width() int {
	if t.w < 0 {
		if strings.Contains(t.text, "\n") {
			t.w = maxWidth
		} else {
			t.w = len(t.text)
		}
	}
	return t.w
}
func (t *token) flat() string { return t.text }

// reflowDoc is a leaf whose text is rewritten at write time given the column it
// lands at - the generic hook the printer uses for comment reflow.
type reflowDoc struct{ raw string }

func (r *reflowDoc) width() int {
	if strings.Contains(r.raw, "\n") {
		return maxWidth
	}
	return len(r.raw)
}
func (r *reflowDoc) flat() string { return r.raw }

func reflow(raw string) Doc { return &reflowDoc{raw: raw} }

type concatDoc struct {
	parts []Doc
	w     int
	f     *string
}

func (c *concatDoc) width() int {
	if c.w < 0 {
		c.w = sumWidth(c.parts)
	}
	return c.w
}
func (c *concatDoc) flat() string {
	if c.f == nil {
		s := flatAll(c.parts)
		c.f = &s
	}
	return *c.f
}

// brkDoc carries only immutable description. Its per-occurrence decision lives
// in the controlling Level's parallel arrays, so a shared singleton
// (line/softline/hardline) reused many times never clobbers itself.
type brkDoc struct {
	fillMode FillMode
	flatText string
	plusIndt Indent
	optTag   *BreakTag
}

func (b *brkDoc) width() int {
	if b.fillMode == fillForced {
		return maxWidth
	}
	return len(b.flatText)
}
func (b *brkDoc) flat() string { return b.flatText }

type levelDoc struct {
	plusIndt Indent
	docs     []Doc
	w        int
	f        *string
	// filled in by computeBreaks, read by write.
	oneLine   bool
	splits    [][]Doc
	breaks    []*brkDoc
	broken    []bool
	newIndent []int
}

func (l *levelDoc) width() int {
	if l.w < 0 {
		l.w = sumWidth(l.docs)
	}
	return l.w
}
func (l *levelDoc) flat() string {
	if l.f == nil {
		s := flatAll(l.docs)
		l.f = &s
	}
	return *l.f
}

func sumWidth(docs []Doc) int {
	w := 0
	for _, d := range docs {
		w += d.width()
		if w >= maxWidth {
			return maxWidth
		}
	}
	return w
}

func flatAll(docs []Doc) string {
	var b strings.Builder
	for _, d := range docs {
		b.WriteString(d.flat())
	}
	return b.String()
}

// --- constructors ----------------------------------------------------------

func text(s string) Doc { return &token{text: s, w: -1} }

func concat(parts ...Doc) Doc { return &concatDoc{parts: parts, w: -1} }

// join joins parts with sep between each.
func join(sep Doc, parts []Doc) Doc {
	out := make([]Doc, 0, len(parts)*2)
	for i, p := range parts {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, p)
	}
	return &concatDoc{parts: out, w: -1}
}

// level is a breakable group whose breaks take an extra plusIndent.
func level(plusIndent Indent, docs []Doc) Doc {
	return &levelDoc{plusIndt: plusIndent, docs: docs, w: -1}
}

// brk is a break: flat text when not broken, a newline + plusIndent when broken.
func brk(fillMode FillMode, flatText string, plusIndent Indent, optTag *BreakTag) Doc {
	return &brkDoc{fillMode: fillMode, flatText: flatText, plusIndt: plusIndent, optTag: optTag}
}

// --- compatibility layer (the original Wadler-ish API, mapped onto gjf) -----

// group lays flat if it fits, else breaks this group's UNIFIED lines.
func group(doc Doc) Doc { return &levelDoc{plusIndt: ZERO, docs: []Doc{doc}, w: -1} }

// indent adds one indent level (2 columns at google scale) to any break inside doc.
func indent(doc Doc) Doc { return &levelDoc{plusIndt: indentConst(2), docs: []Doc{doc}, w: -1} }

// line/softline/hardline are shared singletons; safe to reuse because a Break's
// per-occurrence decision lives in the controlling Level, not on the Break.
var (
	// line is a space when flat and a newline when its group breaks.
	line Doc = &brkDoc{fillMode: fillUnified, flatText: " ", plusIndt: ZERO}
	// hardline is always a newline; it forces every enclosing group to break.
	hardline Doc = &brkDoc{fillMode: fillForced, flatText: "", plusIndt: ZERO}
)

// --- breaking algorithm ----------------------------------------------------

type state struct {
	lastIndent int
	indent     int
	column     int
	mustBreak  bool
}

type printOptions struct {
	width      int // hard wrap column (google-java-format: 100)
	indentMult int // indent multiplier: 1 google (2-space), 2 aosp (4-space)
	// commentRewriter rewrites a reflow leaf's text given the column it lands at.
	commentRewriter func(raw string, column int) string
}

// splitByBreaks splits a Level's docs into Break-separated groups. Concats are
// transparent and flattened in place (so breaks they contain are controlled by
// this Level); Levels are opaque.
func splitByBreaks(docs []Doc) ([][]Doc, []*brkDoc) {
	splits := [][]Doc{{}}
	var breaks []*brkDoc
	var walk func(ds []Doc)
	walk = func(ds []Doc) {
		for _, d := range ds {
			switch n := d.(type) {
			case *brkDoc:
				breaks = append(breaks, n)
				splits = append(splits, []Doc{})
			case *concatDoc:
				walk(n.parts)
			default:
				splits[len(splits)-1] = append(splits[len(splits)-1], d)
			}
		}
	}
	walk(docs)
	return splits, breaks
}

func computeBreaks(doc Doc, maxW, mult int, st state) state {
	switch n := doc.(type) {
	case *token:
		st.column += n.width()
		return st
	case *concatDoc:
		for _, d := range n.parts {
			st = computeBreaks(d, maxW, mult, st)
		}
		return st
	case *levelDoc:
		return computeLevel(n, maxW, mult, st)
	case *reflowDoc:
		st.column += n.width()
		return st
	default:
		// A *brkDoc here would be a bug (not a direct child of a Level).
		panic("unexpected Break outside a Level")
	}
}

func computeLevel(lvl *levelDoc, maxW, mult int, st state) state {
	w := lvl.width()
	if st.column+w <= maxW {
		lvl.oneLine = true
		st.column += w
		return st
	}
	lvl.oneLine = false
	startIndent := st.indent + evalIndent(lvl.plusIndt, mult)
	s := state{lastIndent: startIndent, indent: startIndent, column: st.column, mustBreak: false}
	splits, breaks := splitByBreaks(lvl.docs)
	lvl.splits = splits
	lvl.breaks = breaks
	lvl.broken = make([]bool, len(breaks))
	lvl.newIndent = make([]int, len(breaks))

	s = breakAndSplit(lvl, -1, maxW, mult, s, nil, splits[0])
	for i := 0; i < len(breaks); i++ {
		s = breakAndSplit(lvl, i, maxW, mult, s, breaks[i], splits[i+1])
	}
	st.column = s.column
	return st
}

// breakAndSplit lays out one Break-separated group; when optBreak is non-nil its
// decision is recorded into lvl.broken[i]/lvl.newIndent[i].
func breakAndSplit(lvl *levelDoc, i, maxW, mult int, st state, optBreak *brkDoc, split []Doc) state {
	breakWidth := 0
	if optBreak != nil {
		breakWidth = optBreak.width()
	}
	splitWidth := sumWidth(split)
	shouldBreak := (optBreak != nil && optBreak.fillMode == fillUnified) ||
		st.mustBreak ||
		st.column+breakWidth+splitWidth > maxW

	s := st
	if optBreak != nil {
		if optBreak.optTag != nil {
			optBreak.optTag.recordBroken(shouldBreak)
		}
		if shouldBreak {
			ni := s.lastIndent + evalIndent(optBreak.plusIndt, mult)
			if ni < 0 {
				ni = 0
			}
			lvl.broken[i] = true
			lvl.newIndent[i] = ni
			s.column = ni
		} else {
			lvl.broken[i] = false
			lvl.newIndent[i] = -1
			s.column += len(optBreak.flatText)
		}
	}
	enoughRoom := s.column+splitWidth <= maxW
	s.mustBreak = false
	s = computeSplit(maxW, mult, split, s)
	if !enoughRoom {
		s.mustBreak = true
	}
	return s
}

func computeSplit(maxW, mult int, docs []Doc, st state) state {
	for _, d := range docs {
		st = computeBreaks(d, maxW, mult, st)
	}
	return st
}

// --- output ----------------------------------------------------------------

// writer holds output chunks, the running column (for the reflow hook), and the
// optional comment rewriter.
type writer struct {
	out     []string
	col     int
	rewrite func(raw string, column int) string
}

func (w *writer) push(s string) {
	w.out = append(w.out, s)
	if nl := strings.LastIndexByte(s, '\n'); nl >= 0 {
		w.col = len(s) - nl - 1
	} else {
		w.col += len(s)
	}
}

func writeDoc(doc Doc, w *writer) {
	switch n := doc.(type) {
	case *token:
		w.push(n.text)
	case *reflowDoc:
		if w.rewrite != nil {
			w.push(w.rewrite(n.raw, w.col))
		} else {
			w.push(n.raw)
		}
	case *concatDoc:
		for _, d := range n.parts {
			writeDoc(d, w)
		}
	case *levelDoc:
		if n.oneLine {
			w.push(n.flat())
			return
		}
		for _, d := range n.splits[0] {
			writeDoc(d, w)
		}
		for i := 0; i < len(n.breaks); i++ {
			if n.broken[i] {
				trimTrailingSpace(&w.out)
				w.push("\n" + strings.Repeat(" ", n.newIndent[i]))
			} else {
				w.push(n.breaks[i].flatText)
			}
			for _, d := range n.splits[i+1] {
				writeDoc(d, w)
			}
		}
	default:
		panic("unexpected Break in writeDoc")
	}
}

func printDoc(doc Doc, options printOptions) string {
	// Wrap in a root Level so top-level breaks have a controlling Level.
	root, ok := doc.(*levelDoc)
	if !ok {
		root = &levelDoc{plusIndt: ZERO, docs: []Doc{doc}, w: -1}
	}
	computeLevel(root, options.width, options.indentMult, state{})
	w := &writer{rewrite: options.commentRewriter}
	writeDoc(root, w)
	trimTrailingSpace(&w.out)
	return strings.Join(w.out, "")
}

func trimTrailingSpace(out *[]string) {
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
