// Package javadoc is a faithful port of google-java-format's javadoc reflow
// engine (src/format/javadoc): lex a classic `/** ... */` comment, render it
// column-aware through the writer, then collapse to a one-liner when it fits.
// This file ports JavadocFormatter.java.
package javadoc

import (
	"regexp"
	"strings"
)

const maxLineLength = 100

// FormatJavadoc formats a classic javadoc comment (starts `/**`, ends `*​/`).
// On any lex failure the input is returned unchanged. blockIndent is the column
// the comment starts at.
func FormatJavadoc(input string, blockIndent int) string {
	if !strings.HasPrefix(input, "/**") {
		return input // Markdown `///` deferred
	}
	tokens, err := lex(input)
	if err != nil {
		return input
	}
	result := render(tokens, blockIndent)
	return makeSingleLineIfPossible(blockIndent, result)
}

func render(tokens []Token, blockIndent int) string {
	out := newWriter(blockIndent)
	for _, t := range tokens {
		switch t.kind {
		case beginJavadoc:
			out.writeBeginJavadoc()
		case endJavadoc:
			out.writeEndJavadoc()
			return out.String()
		case footerJavadocTagStart:
			out.writeFooterJavadocTagStart(t)
		case snippetBegin:
			out.writeSnippetBegin(t)
		case snippetEnd:
			out.writeSnippetEnd(t)
		case listOpen:
			out.writeListOpen(t)
		case listClose:
			out.writeListClose(t)
		case listItemOpen:
			out.writeListItemOpen(t)
		case headerOpen:
			out.writeHeaderOpen(t)
		case headerClose:
			out.writeHeaderClose(t)
		case paragraphOpen:
			out.writeParagraphOpen(standardize(t, standardP))
		case blockquoteOpen, blockquoteClose:
			out.writeBlockquoteOpenOrClose(t)
		case preOpen:
			out.writePreOpen(t)
		case preClose:
			out.writePreClose(t)
		case codeOpen:
			out.writeCodeOpen(t)
		case codeClose:
			out.writeCodeClose(t)
		case tableOpen:
			out.writeTableOpen(t)
		case tableClose:
			out.writeTableClose(t)
		case moeBeginStrip:
			out.requestMoeBeginStripComment(t)
		case moeEndStrip:
			out.writeMoeEndStripComment(t)
		case htmlComment:
			out.writeHTMLComment(t)
		case br:
			out.writeBr(standardize(t, standardBr))
		case whitespace:
			out.requestWhitespace()
		case forcedNewline:
			out.writeLineBreakNoAutoIndent()
		case markdownHardLineBreak:
			out.writeMarkdownHardLineBreak()
		case literal:
			out.writeLiteral(t)
		case markdownFencedCodeBlock:
			out.writeMarkdownFencedCodeBlock(t)
		case markdownTable:
			out.writeMarkdownTable(t)
		case listItemClose, paragraphClose, optionalLineBreak,
			markdownCodeSpanStart, markdownCodeSpanEnd:
			// ignorable
		}
	}
	panic("javadoc render: missing endJavadoc")
}

var (
	standardBr = Token{kind: br, value: "<br>"}
	standardP  = Token{kind: paragraphOpen, value: "<p>"}
	simpleTag  = regexp.MustCompile(`(?i)^<\w+\s*/?\s*>`)
)

func standardize(t, std Token) Token {
	if simpleTag.MatchString(t.value) {
		return std
	}
	return t
}

var oneContentLine = regexp.MustCompile(`^ *\/\*\*\n *\* (.*)\n *\*\/$`)

func makeSingleLineIfPossible(blockIndent int, input string) string {
	m := oneContentLine.FindStringSubmatch(input)
	if m != nil {
		line := m[1]
		if line == "" {
			return "/** */"
		}
		if oneLineJavadoc(line, blockIndent) {
			return "/** " + line + " */"
		}
	}
	return input
}

func oneLineJavadoc(line string, blockIndent int) bool {
	oneLinerContentLength := maxLineLength - len("/**  */") - blockIndent
	if len(line) > oneLinerContentLength {
		return false
	}
	if strings.HasPrefix(line, "@") && line != "@hide" {
		return false
	}
	return true
}
