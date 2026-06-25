// Port of src/format/javadoc/writer.ts (gjf's JavadocWriter).
//
// Stateful renderer: accepts requests/writes and produces wrapped javadoc.
// Classic `/** */` only; Markdown methods kept for fidelity but unexercised.

package javadoc

import "strings"

type wsKind int

const (
	wsNone wsKind = iota
	wsWhitespace
	wsNewline
	wsBlankLine
)

var backslashLiteral = Token{kind: literal, value: "\\"}

type javadocWriter struct {
	blockIndent int

	out                                 strings.Builder
	continuingListItemOfInnermostList   bool
	continuingFooterTag                 bool
	continuingListItemStack             intNestingStack
	continuingListStack                 intNestingStack
	postWriteModifiedContinuingListStck intNestingStack
	remainingOnLine                     int
	atStartOfLine                       bool
	requestedWhitespace                 wsKind
	wroteAnythingSignificant            bool
}

func newWriter(blockIndent int) *javadocWriter {
	return &javadocWriter{blockIndent: blockIndent}
}

func (w *javadocWriter) requestWhitespace() { w.request(wsWhitespace) }

func (w *javadocWriter) request(ws wsKind) {
	if ws > w.requestedWhitespace {
		w.requestedWhitespace = ws
	}
}

func (w *javadocWriter) requestBlankLine() { w.request(wsBlankLine) }
func (w *javadocWriter) requestNewline()   { w.request(wsNewline) }

func (w *javadocWriter) writeBeginJavadoc() {
	w.out.WriteString("/**")
	w.writeNewline(true)
}

func (w *javadocWriter) writeEndJavadoc() {
	w.out.WriteString("\n")
	w.appendSpaces(w.blockIndent + 1)
	w.out.WriteString("*/")
}

func (w *javadocWriter) writeFooterJavadocTagStart(t Token) {
	w.continuingListItemOfInnermostList = false
	w.continuingListItemStack.reset()
	w.continuingListStack.reset()
	w.postWriteModifiedContinuingListStck.reset()
	switch {
	case !w.wroteAnythingSignificant:
		// Javadoc consists solely of tags (OK for @Override).
	case !w.continuingFooterTag:
		w.requestBlankLine()
	default:
		w.continuingFooterTag = false
		w.requestNewline()
	}
	w.writeToken(t)
	w.continuingFooterTag = true
}

func (w *javadocWriter) writeSnippetBegin(t Token) {
	w.requestBlankLine()
	w.writeToken(t)
}

func (w *javadocWriter) writeSnippetEnd(t Token) {
	w.writeToken(t)
	w.requestBlankLine()
}

func (w *javadocWriter) writeListOpen(t Token) {
	w.requestBlankLine()
	w.writeToken(t)
	w.continuingListItemOfInnermostList = false
	indent := 0
	if t.value != "" {
		indent = 2
	}
	w.continuingListStack.push(indent)
	w.postWriteModifiedContinuingListStck.push(1)
	w.requestNewline()
}

func (w *javadocWriter) writeListClose(t Token) {
	w.requestNewline()
	w.continuingListItemStack.popIfNotEmpty()
	w.continuingListStack.popIfNotEmpty()
	w.writeToken(t)
	w.postWriteModifiedContinuingListStck.popIfNotEmpty()
	w.requestBlankLine()
}

func (w *javadocWriter) writeListItemOpen(t Token) {
	w.requestNewline()
	if w.continuingListItemOfInnermostList {
		w.continuingListItemOfInnermostList = false
		w.continuingListItemStack.popIfNotEmpty()
	}
	w.writeToken(t)
	w.continuingListItemOfInnermostList = true
	w.continuingListItemStack.push(len(t.value))
}

func (w *javadocWriter) writeHeaderOpen(t Token) {
	if w.wroteAnythingSignificant {
		w.requestBlankLine()
	}
	w.writeToken(t)
}

func (w *javadocWriter) writeHeaderClose(t Token) {
	w.writeToken(t)
	w.requestBlankLine()
}

func (w *javadocWriter) writeParagraphOpen(t Token) {
	if !w.wroteAnythingSignificant {
		return
	}
	w.requestBlankLine()
	w.writeToken(t)
}

func (w *javadocWriter) writeBlockquoteOpenOrClose(t Token) {
	w.requestBlankLine()
	w.writeToken(t)
	w.requestBlankLine()
}

func (w *javadocWriter) writePreOpen(t Token) {
	w.requestBlankLine()
	w.writeToken(t)
}

func (w *javadocWriter) writePreClose(t Token) {
	w.writeToken(t)
	w.requestBlankLine()
}

func (w *javadocWriter) writeCodeOpen(t Token)  { w.writeToken(t) }
func (w *javadocWriter) writeCodeClose(t Token) { w.writeToken(t) }

func (w *javadocWriter) writeTableOpen(t Token) {
	w.requestBlankLine()
	w.writeToken(t)
}

func (w *javadocWriter) writeTableClose(t Token) {
	w.writeToken(t)
	w.requestBlankLine()
}

func (w *javadocWriter) writeHTMLComment(t Token) {
	w.requestNewline()
	w.writeToken(t)
	w.requestNewline()
}

func (w *javadocWriter) writeBr(t Token) {
	w.writeToken(t)
	w.requestNewline()
}

func (w *javadocWriter) writeMoeEndStripComment(t Token) {
	w.writeNewline(false)
	w.writeToken(t)
	w.requestNewline()
}

func (w *javadocWriter) requestMoeBeginStripComment(_ Token) { w.requestNewline() }

func (w *javadocWriter) writeLineBreakNoAutoIndent() { w.writeNewline(false) }

func (w *javadocWriter) writeMarkdownHardLineBreak() {
	w.writeLiteral(backslashLiteral)
	w.writeNewline(true)
}

func (w *javadocWriter) writeLiteral(t Token) { w.writeToken(t) }

func (w *javadocWriter) writeMarkdownFencedCodeBlock(t Token) {
	w.flushWhitespace()
	w.out.WriteString(t.value)
	w.requestBlankLine()
}

func (w *javadocWriter) writeMarkdownTable(t Token) {
	w.flushWhitespace()
	lines := strings.Split(t.value, "\n")
	w.out.WriteString(lines[0])
	for _, line := range lines[1:] {
		w.writeNewline(false)
		w.out.WriteString(line)
	}
	w.requestBlankLine()
}

func (w *javadocWriter) String() string { return w.out.String() }

func (w *javadocWriter) flushWhitespace() {
	if w.requestedWhitespace == wsBlankLine &&
		(!w.postWriteModifiedContinuingListStck.isEmpty() || w.continuingFooterTag) {
		w.requestedWhitespace = wsNewline
	}
	switch w.requestedWhitespace {
	case wsBlankLine:
		w.writeBlankLine()
		w.requestedWhitespace = wsNone
	case wsNewline:
		w.writeNewline(true)
		w.requestedWhitespace = wsNone
	}
}

func (w *javadocWriter) writeToken(t Token) {
	if t.value == "" {
		return
	}
	w.flushWhitespace()
	needWhitespace := w.requestedWhitespace == wsWhitespace

	extra := 0
	if needWhitespace {
		extra = 1
	}
	if !w.atStartOfLine && len(t.value)+extra > w.remainingOnLine {
		w.writeNewline(true)
	}
	if !w.atStartOfLine && needWhitespace {
		w.out.WriteString(" ")
		w.remainingOnLine--
	}

	w.out.WriteString(t.value)
	if !isStartOfLine(t.kind) {
		w.atStartOfLine = false
	}
	w.remainingOnLine -= len(t.value)
	w.requestedWhitespace = wsNone
	w.wroteAnythingSignificant = true
}

func (w *javadocWriter) writeNewlineStart() {
	w.out.WriteString("\n")
	w.appendSpaces(w.blockIndent + 1)
	w.out.WriteString("*")
}

func (w *javadocWriter) writeBlankLine() {
	w.writeNewlineStart()
	w.writeNewline(true)
}

func (w *javadocWriter) writeNewline(autoIndent bool) {
	w.writeNewlineStart()
	w.appendSpaces(1)
	w.remainingOnLine = maxLineLength - w.blockIndent - 3
	if autoIndent {
		w.appendSpaces(w.innerIndent())
		w.remainingOnLine -= w.innerIndent()
	}
	w.atStartOfLine = true
}

func (w *javadocWriter) innerIndent() int {
	n := w.continuingListItemStack.total + w.continuingListStack.total
	if w.continuingFooterTag {
		n += 4
	}
	return n
}

func (w *javadocWriter) appendSpaces(count int) { w.out.WriteString(strings.Repeat(" ", count)) }
