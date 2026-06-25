// Port of src/format/javadoc/lexer.ts (which ports gjf's JavadocLexer).
//
// Lexes a classic `/** ... */` javadoc comment. The Markdown `///` path is
// deferred; classic is always assumed here.

package javadoc

import (
	"fmt"
	"regexp"
	"strings"
)

type lexError struct{ msg string }

func (e *lexError) Error() string { return e.msg }

type ctx int

const (
	ctxHTMLPre ctx = iota
	ctxHTMLCode
	ctxMarkdownCode
	ctxTable
	ctxSnippet
	ctxBrace
	ctxInlineTag
)

var (
	nonUnixLineEnding = regexp.MustCompile(`\r\n?`)
	reClassicNewline  = regexp.MustCompile(`^[ \t]*\n[ \t]*[*]?[ \t]?`)
	reFooterTag       = regexp.MustCompile(`^@(?:param\s+<\w+>|[a-z]\w*)`)
	reMoeBegin        = regexp.MustCompile(`^<!--\s*MOE:begin_intracomment_strip\s*-->`)
	reMoeEnd          = regexp.MustCompile(`^<!--\s*MOE:end_intracomment_strip\s*-->`)
	reHTMLComment     = regexp.MustCompile(`(?s)^<!--.*?-->`)
	reSnippetTagOpen  = regexp.MustCompile(`^[{]@snippet\b`)
	reInlineTagOpen   = regexp.MustCompile(`^[{]@\w*`)
	reClassicLiteral  = regexp.MustCompile(`(?s)^.[^ \t\n@<{}*]*`)

	rePreOpen         = openTagRe("pre")
	rePreClose        = closeTagRe("pre")
	reCodeOpen        = openTagRe("code")
	reCodeClose       = closeTagRe("code")
	reTableOpen       = openTagRe("table")
	reTableClose      = closeTagRe("table")
	reListOpen        = openTagRe("ul|ol|dl")
	reListClose       = closeTagRe("ul|ol|dl")
	reListItemOpen    = openTagRe("li|dt|dd")
	reListItemClose   = closeTagRe("li|dt|dd")
	reHeaderOpen      = openTagRe("h[1-6]")
	reHeaderClose     = closeTagRe("h[1-6]")
	reParagraphOpen   = openTagRe("p")
	reParagraphClose  = closeTagRe("p")
	reBlockquoteOpen  = openTagRe("blockquote")
	reBlockquoteClose = closeTagRe("blockquote")
	reBr              = openTagRe("br")
)

func openTagRe(name string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?i)^<(?:%s)\b[^>]*>`, name))
}
func closeTagRe(name string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?i)^</(?:%s)\b[^>]*>`, name))
}

var (
	tagContexts        = []ctx{ctxSnippet, ctxInlineTag}
	braceContexts      = []ctx{ctxSnippet, ctxInlineTag, ctxBrace}
	preserveFormatting = []ctx{ctxHTMLPre, ctxTable, ctxHTMLCode, ctxSnippet}
)

// lex lexes a `/** ... */` comment (including delimiters) into tokens.
func lex(input string) ([]Token, error) {
	input = nonUnixLineEnding.ReplaceAllString(input, "\n")
	if !strings.HasPrefix(input, "/**") || !strings.HasSuffix(input, "*/") || len(input) <= 4 {
		return nil, &lexError{"not a /** */ comment: " + input}
	}
	body := input[3 : len(input)-2]
	l := &javadocLexer{input: newCharStream(body)}
	return l.generateTokens()
}

type javadocLexer struct {
	input               *charStream
	contextStack        nestingStack[ctx]
	somethingSinceNewln bool
}

func (l *javadocLexer) generateTokens() ([]Token, error) {
	tokens := []Token{tok(beginJavadoc, "/**")}
	for !l.input.isExhausted() {
		t, err := l.readToken()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	if !l.contextStack.isEmpty() {
		return nil, &lexError{"unbalanced javadoc tags"}
	}
	tokens = append(tokens, tok(endJavadoc, "*/"))

	result := joinAdjacentLiteralsAndAdjacentWhitespace(tokens)
	result = inferParagraphTags(result)
	result = optionalizeSpacesAfterLinks(result)
	result = deindentPreCodeBlocks(result)
	return result, nil
}

func (l *javadocLexer) readToken() (Token, error) {
	kind, err := l.consumeToken()
	if err != nil {
		return Token{}, err
	}
	return tok(kind, l.input.readAndResetRecorded()), nil
}

func (l *javadocLexer) consumeToken() (Kind, error) {
	preserve := l.preserveExistingFormatting()

	if l.input.tryConsumeRegex(reClassicNewline) {
		l.somethingSinceNewln = false
		if preserve {
			return forcedNewline, nil
		}
		return whitespace, nil
	}
	if l.input.tryConsume(" ") || l.input.tryConsume("\t") {
		if preserve {
			return literal, nil
		}
		return whitespace, nil
	}

	if !l.somethingSinceNewln && l.input.tryConsumeRegex(reFooterTag) {
		if !l.contextStack.isEmpty() {
			return 0, &lexError{"unbalanced javadoc tags"}
		}
		l.somethingSinceNewln = true
		return footerJavadocTagStart, nil
	}
	l.somethingSinceNewln = true

	if l.input.tryConsumeRegex(reSnippetTagOpen) {
		if l.contextStack.containsAny(braceContexts) {
			l.contextStack.push(ctxBrace)
			return literal, nil
		}
		l.contextStack.push(ctxSnippet)
		return snippetBegin, nil
	}
	if l.input.tryConsumeRegex(reInlineTagOpen) {
		l.contextStack.push(ctxInlineTag)
		return literal, nil
	}
	if l.input.tryConsume("{") {
		if l.contextStack.containsAny(braceContexts) {
			l.contextStack.push(ctxBrace)
		}
		return literal, nil
	}
	if l.input.tryConsume("}") {
		if popped, ok := l.contextStack.popIfIn(braceContexts); ok && popped == ctxSnippet {
			return snippetEnd, nil
		}
		return literal, nil
	}

	if l.contextStack.containsAny(tagContexts) {
		l.mustConsume(reClassicLiteral)
		return literal, nil
	}

	if l.input.tryConsumeRegex(rePreOpen) {
		l.contextStack.push(ctxHTMLPre)
		return preserveOr(preserve, preOpen), nil
	}
	if l.input.tryConsumeRegex(rePreClose) {
		l.contextStack.popUntil(ctxHTMLPre)
		return preserveOr(l.preserveExistingFormatting(), preClose), nil
	}
	if l.input.tryConsumeRegex(reCodeOpen) {
		l.contextStack.push(ctxHTMLCode)
		return preserveOr(preserve, codeOpen), nil
	}
	if l.input.tryConsumeRegex(reCodeClose) {
		l.contextStack.popUntil(ctxHTMLCode)
		return preserveOr(l.preserveExistingFormatting(), codeClose), nil
	}
	if l.input.tryConsumeRegex(reTableOpen) {
		l.contextStack.push(ctxTable)
		return preserveOr(preserve, tableOpen), nil
	}
	if l.input.tryConsumeRegex(reTableClose) {
		l.contextStack.popUntil(ctxTable)
		return preserveOr(l.preserveExistingFormatting(), tableClose), nil
	}

	if preserve {
		l.mustConsume(reClassicLiteral)
		return literal, nil
	}

	switch {
	case l.input.tryConsumeRegex(reParagraphOpen):
		return paragraphOpen, nil
	case l.input.tryConsumeRegex(reParagraphClose):
		return paragraphClose, nil
	case l.input.tryConsumeRegex(reListOpen):
		return listOpen, nil
	case l.input.tryConsumeRegex(reListClose):
		return listClose, nil
	case l.input.tryConsumeRegex(reListItemOpen):
		return listItemOpen, nil
	case l.input.tryConsumeRegex(reListItemClose):
		return listItemClose, nil
	case l.input.tryConsumeRegex(reBlockquoteOpen):
		return blockquoteOpen, nil
	case l.input.tryConsumeRegex(reBlockquoteClose):
		return blockquoteClose, nil
	case l.input.tryConsumeRegex(reHeaderOpen):
		return headerOpen, nil
	case l.input.tryConsumeRegex(reHeaderClose):
		return headerClose, nil
	case l.input.tryConsumeRegex(reBr):
		return br, nil
	case l.input.tryConsumeRegex(reMoeBegin):
		return moeBeginStrip, nil
	case l.input.tryConsumeRegex(reMoeEnd):
		return moeEndStrip, nil
	case l.input.tryConsumeRegex(reHTMLComment):
		return htmlComment, nil
	case l.input.tryConsumeRegex(reClassicLiteral):
		return literal, nil
	}
	return 0, &lexError{"javadoc lexer: no token matched"}
}

func preserveOr(preserve bool, k Kind) Kind {
	if preserve {
		return literal
	}
	return k
}

func (l *javadocLexer) mustConsume(p *regexp.Regexp) {
	if !l.input.tryConsumeRegex(p) {
		panic("javadoc lexer: expected literal")
	}
}

func (l *javadocLexer) preserveExistingFormatting() bool {
	return l.contextStack.containsAny(preserveFormatting)
}

func hasMultipleNewlines(s string) bool { return strings.Count(s, "\n") > 1 }

func joinAdjacentLiteralsAndAdjacentWhitespace(input []Token) []Token {
	var output []Token
	var accumulated strings.Builder
	i := 0
	for i < len(input) {
		if input[i].kind == literal {
			accumulated.WriteString(input[i].value)
			i++
			continue
		}
		if accumulated.Len() == 0 {
			output = append(output, input[i])
			i++
			continue
		}
		var seenWhitespace strings.Builder
		for i < len(input) && input[i].kind == whitespace {
			seenWhitespace.WriteString(input[i].value)
			i++
		}
		if i < len(input) && input[i].kind == literal && strings.HasPrefix(input[i].value, "@") {
			accumulated.WriteString(" ")
			accumulated.WriteString(input[i].value)
			i++
			continue
		}
		output = append(output, tok(literal, accumulated.String()))
		accumulated.Reset()
		if seenWhitespace.Len() > 0 {
			output = append(output, tok(whitespace, seenWhitespace.String()))
		}
	}
	return output
}

func inferParagraphTags(input []Token) []Token {
	var output []Token
	i := 0
	for i < len(input) {
		if input[i].kind == literal {
			output = append(output, input[i])
			i++
			if i < len(input) && input[i].kind == whitespace && hasMultipleNewlines(input[i].value) {
				output = append(output, input[i])
				i++
				if i < len(input) && input[i].kind == literal {
					output = append(output, tok(paragraphOpen, "<p>"))
				}
			}
		} else {
			output = append(output, input[i])
			i++
		}
	}
	return output
}

var reHref = regexp.MustCompile(`^href=[^>]*>$`)

func optionalizeSpacesAfterLinks(input []Token) []Token {
	var output []Token
	i := 0
	for i < len(input) {
		if input[i].kind == literal && reHref.MatchString(input[i].value) {
			output = append(output, input[i])
			i++
			if i < len(input) && input[i].kind == whitespace {
				output = append(output, tok(optionalLineBreak, input[i].value))
				i++
			}
		} else {
			output = append(output, input[i])
			i++
		}
	}
	return output
}

var reCodeFence = regexp.MustCompile(`^[ \t]*[{]@code$`)

func deindentPreCodeBlocks(input []Token) []Token {
	var output []Token
	i := 0
	for i < len(input) {
		if input[i].kind != preOpen {
			output = append(output, input[i])
			i++
			continue
		}
		output = append(output, input[i])
		i++
		var initialNewlines []Token
		for i < len(input) && input[i].kind == forcedNewline {
			initialNewlines = append(initialNewlines, input[i])
			i++
		}
		if i >= len(input) || input[i].kind != literal || !reCodeFence.MatchString(input[i].value) {
			output = append(output, initialNewlines...)
			if i < len(input) {
				output = append(output, input[i])
				i++
			}
			continue
		}
		output, i = deindentPreCodeBlock(output, input, i)
	}
	return output
}

func deindentPreCodeBlock(output []Token, input []Token, i int) ([]Token, int) {
	output = append(output, tok(literal, strings.TrimSpace(input[i].value)))
	i++
	var saved []Token
	for i < len(input) && input[i].kind != preClose {
		saved = append(saved, input[i])
		i++
	}
	for len(saved) > 0 && saved[0].kind == forcedNewline {
		saved = saved[1:]
	}
	for len(saved) > 0 && saved[len(saved)-1].kind == forcedNewline {
		saved = saved[:len(saved)-1]
	}
	if len(saved) == 0 {
		return output, i
	}

	last := saved[len(saved)-1]
	trailingBrace := false
	if last.kind == literal && strings.HasSuffix(last.value, "}") {
		saved = saved[:len(saved)-1]
		if len(last.value) > 1 {
			saved = append(saved, tok(literal, last.value[:len(last.value)-1]))
			saved = append(saved, tok(forcedNewline, ""))
		}
		trailingBrace = true
	}

	trim := -1
	for _, t := range saved {
		if t.kind == literal {
			idx := strings.IndexFunc(t.value, func(r rune) bool { return r != ' ' })
			if idx != -1 && (trim == -1 || idx < trim) {
				trim = idx
			}
		}
	}

	output = append(output, tok(forcedNewline, "\n"))
	for _, t := range saved {
		if t.kind == literal {
			if trim > 0 && len(t.value) > trim {
				output = append(output, tok(literal, t.value[trim:]))
			} else {
				output = append(output, t)
			}
		} else {
			output = append(output, t)
		}
	}
	if trailingBrace {
		output = append(output, tok(literal, "}"))
	} else {
		output = append(output, tok(forcedNewline, "\n"))
	}
	return output, i
}
