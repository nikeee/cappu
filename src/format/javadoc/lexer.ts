// Port of google-java-format core/.../java/javadoc/JavadocLexer.java.
//
// Lexes a classic `/** ... */` javadoc comment into a token stream for the
// writer. The Markdown `///` path (MarkdownPositions) is deferred (phase 5):
// markdownTokensAt() returns nothing and classicJavadoc is always true here, so
// the markdown-only branches are inert.

import { CharStream } from "./char-stream.ts";
import { NestingStack } from "./nesting-stack.ts";
import { factory, type Token, tok } from "./token.ts";

export class LexException extends Error {}

const enum Ctx {
  HtmlPre,
  HtmlCode,
  MarkdownCode,
  Table,
  Snippet,
  Brace,
  InlineTag,
}

// Sticky/anchored patterns (the `y` flag matches only at the cursor).
const NON_UNIX_LINE_ENDING = /\r\n?/g;
const CLASSIC_NEWLINE = /[ \t]*\n[ \t]*[*]?[ \t]?/y;
const FOOTER_TAG = /@(?:param\s+<\w+>|[a-z]\w*)/y;
const MOE_BEGIN = /<!--\s*MOE:begin_intracomment_strip\s*-->/y;
const MOE_END = /<!--\s*MOE:end_intracomment_strip\s*-->/y;
const HTML_COMMENT = /<!--[\s\S]*?-->/y;
const SNIPPET_TAG_OPEN = /[{]@snippet\b/y;
const INLINE_TAG_OPEN = /[{]@\w*/y;
const CLASSIC_LITERAL = /[\s\S][^ \t\n@<{}*]*/y;

const openTag = (name: string): RegExp => new RegExp(`<(?:${name})\\b[^>]*>`, "iy");
const closeTag = (name: string): RegExp => new RegExp(`</(?:${name})\\b[^>]*>`, "iy");

const PRE_OPEN = openTag("pre");
const PRE_CLOSE = closeTag("pre");
const CODE_OPEN = openTag("code");
const CODE_CLOSE = closeTag("code");
const TABLE_OPEN = openTag("table");
const TABLE_CLOSE = closeTag("table");
const LIST_OPEN = openTag("ul|ol|dl");
const LIST_CLOSE = closeTag("ul|ol|dl");
const LIST_ITEM_OPEN = openTag("li|dt|dd");
const LIST_ITEM_CLOSE = closeTag("li|dt|dd");
const HEADER_OPEN = openTag("h[1-6]");
const HEADER_CLOSE = closeTag("h[1-6]");
const PARAGRAPH_OPEN = openTag("p");
const PARAGRAPH_CLOSE = closeTag("p");
const BLOCKQUOTE_OPEN = openTag("blockquote");
const BLOCKQUOTE_CLOSE = closeTag("blockquote");
const BR = openTag("br");

const TAG_CONTEXTS = [Ctx.Snippet, Ctx.InlineTag];
const BRACE_CONTEXTS = [Ctx.Snippet, Ctx.InlineTag, Ctx.Brace];
const PRESERVE_FORMATTING = [Ctx.HtmlPre, Ctx.Table, Ctx.HtmlCode, Ctx.Snippet];

/** Lex a `/** ... *​/` comment (including the delimiters) into tokens. */
export function lex(input: string): Token[] {
  input = input.replace(NON_UNIX_LINE_ENDING, "\n");
  // stripJavadocBeginAndEnd
  if (!input.startsWith("/**") || !input.endsWith("*/") || input.length <= 4) {
    throw new LexException(`not a /** */ comment: ${input}`);
  }
  const body = input.slice(3, input.length - 2);
  return new JavadocLexer(new CharStream(body)).generateTokens();
}

class JavadocLexer {
  private readonly contextStack = new NestingStack<Ctx>();
  private somethingSinceNewline = false;

  constructor(private readonly input: CharStream) {}

  generateTokens(): Token[] {
    const tokens: Token[] = [tok("beginJavadoc", "/**")];
    while (!this.input.isExhausted()) {
      tokens.push(this.readToken());
    }
    this.checkMatchingTags();
    tokens.push(tok("endJavadoc", "*/"));

    let result = joinAdjacentLiteralsAndAdjacentWhitespace(tokens);
    result = inferParagraphTags(result);
    result = optionalizeSpacesAfterLinks(result);
    result = deindentPreCodeBlocks(result);
    return result;
  }

  private readToken(): Token {
    const make = this.consumeToken();
    return make(this.input.readAndResetRecorded());
  }

  private consumeToken(): (value: string) => Token {
    const preserve = this.preserveExistingFormatting();

    if (this.input.tryConsumeRegex(CLASSIC_NEWLINE)) {
      this.somethingSinceNewline = false;
      return factory(preserve ? "forcedNewline" : "whitespace");
    }
    if (this.input.tryConsume(" ") || this.input.tryConsume("\t")) {
      // Literal in a preserved context prevents breaking a <pre> line.
      return factory(preserve ? "literal" : "whitespace");
    }

    if (!this.somethingSinceNewline && this.input.tryConsumeRegex(FOOTER_TAG)) {
      this.checkMatchingTags();
      this.somethingSinceNewline = true;
      return factory("footerJavadocTagStart");
    }
    this.somethingSinceNewline = true;

    if (this.input.tryConsumeRegex(SNIPPET_TAG_OPEN)) {
      if (this.contextStack.containsAny(BRACE_CONTEXTS)) {
        this.contextStack.push(Ctx.Brace);
        return factory("literal");
      }
      this.contextStack.push(Ctx.Snippet);
      return factory("snippetBegin");
    }
    if (this.input.tryConsumeRegex(INLINE_TAG_OPEN)) {
      this.contextStack.push(Ctx.InlineTag);
      return factory("literal");
    }
    if (this.input.tryConsume("{")) {
      if (this.contextStack.containsAny(BRACE_CONTEXTS)) this.contextStack.push(Ctx.Brace);
      return factory("literal");
    }
    if (this.input.tryConsume("}")) {
      const popped = this.contextStack.popIfIn(BRACE_CONTEXTS);
      return factory(popped === Ctx.Snippet ? "snippetEnd" : "literal");
    }

    // Inside an inline tag, no HTML interpretation.
    if (this.contextStack.containsAny(TAG_CONTEXTS)) {
      this.mustConsume(CLASSIC_LITERAL);
      return factory("literal");
    }

    if (this.input.tryConsumeRegex(PRE_OPEN)) {
      this.contextStack.push(Ctx.HtmlPre);
      return factory(preserve ? "literal" : "preOpen");
    }
    if (this.input.tryConsumeRegex(PRE_CLOSE)) {
      this.contextStack.popUntil(Ctx.HtmlPre);
      return factory(this.preserveExistingFormatting() ? "literal" : "preClose");
    }
    if (this.input.tryConsumeRegex(CODE_OPEN)) {
      this.contextStack.push(Ctx.HtmlCode);
      return factory(preserve ? "literal" : "codeOpen");
    }
    if (this.input.tryConsumeRegex(CODE_CLOSE)) {
      this.contextStack.popUntil(Ctx.HtmlCode);
      return factory(this.preserveExistingFormatting() ? "literal" : "codeClose");
    }
    if (this.input.tryConsumeRegex(TABLE_OPEN)) {
      this.contextStack.push(Ctx.Table);
      return factory(preserve ? "literal" : "tableOpen");
    }
    if (this.input.tryConsumeRegex(TABLE_CLOSE)) {
      this.contextStack.popUntil(Ctx.Table);
      return factory(this.preserveExistingFormatting() ? "literal" : "tableClose");
    }

    if (preserve) {
      this.mustConsume(CLASSIC_LITERAL);
      return factory("literal");
    }

    if (this.input.tryConsumeRegex(PARAGRAPH_OPEN)) return factory("paragraphOpen");
    if (this.input.tryConsumeRegex(PARAGRAPH_CLOSE)) return factory("paragraphClose");
    if (this.input.tryConsumeRegex(LIST_OPEN)) return factory("listOpen");
    if (this.input.tryConsumeRegex(LIST_CLOSE)) return factory("listClose");
    if (this.input.tryConsumeRegex(LIST_ITEM_OPEN)) return factory("listItemOpen");
    if (this.input.tryConsumeRegex(LIST_ITEM_CLOSE)) return factory("listItemClose");
    if (this.input.tryConsumeRegex(BLOCKQUOTE_OPEN)) return factory("blockquoteOpen");
    if (this.input.tryConsumeRegex(BLOCKQUOTE_CLOSE)) return factory("blockquoteClose");
    if (this.input.tryConsumeRegex(HEADER_OPEN)) return factory("headerOpen");
    if (this.input.tryConsumeRegex(HEADER_CLOSE)) return factory("headerClose");
    if (this.input.tryConsumeRegex(BR)) return factory("br");
    if (this.input.tryConsumeRegex(MOE_BEGIN)) return factory("moeBeginStrip");
    if (this.input.tryConsumeRegex(MOE_END)) return factory("moeEndStrip");
    if (this.input.tryConsumeRegex(HTML_COMMENT)) return factory("htmlComment");
    if (this.input.tryConsumeRegex(CLASSIC_LITERAL)) return factory("literal");
    throw new Error("javadoc lexer: no token matched");
  }

  private mustConsume(p: RegExp): void {
    if (!this.input.tryConsumeRegex(p)) throw new Error("javadoc lexer: expected literal");
  }

  private preserveExistingFormatting(): boolean {
    return this.contextStack.containsAny(PRESERVE_FORMATTING);
  }

  private checkMatchingTags(): void {
    if (!this.contextStack.isEmpty()) throw new LexException("unbalanced javadoc tags");
  }
}

function hasMultipleNewlines(s: string): boolean {
  return (s.match(/\n/g)?.length ?? 0) > 1;
}

// Join adjacent literals (and adjacent whitespace), with the special case that a
// run of literals followed by whitespace + an `@`-literal is joined too (so a
// line break is not inserted before it, which would turn it into a tag).
function joinAdjacentLiteralsAndAdjacentWhitespace(input: Token[]): Token[] {
  const output: Token[] = [];
  let accumulated = "";
  let i = 0;
  const peek = (): Token | undefined => input[i];
  while (i < input.length) {
    if (peek()!.kind === "literal") {
      accumulated += input[i++].value;
      continue;
    }
    if (accumulated === "") {
      output.push(input[i++]);
      continue;
    }
    let seenWhitespace = "";
    while (peek()?.kind === "whitespace") seenWhitespace += input[i++].value;
    const p = peek();
    if (p && p.kind === "literal" && p.value.startsWith("@")) {
      accumulated += " ";
      accumulated += input[i++].value;
      continue;
    }
    output.push(tok("literal", accumulated));
    accumulated = "";
    if (seenWhitespace !== "") output.push(tok("whitespace", seenWhitespace));
    // leave the current token for the next iteration
  }
  return output;
}

// Insert a <p> between literals separated by a blank line. Must run after joining.
function inferParagraphTags(input: Token[]): Token[] {
  const output: Token[] = [];
  let i = 0;
  const peek = (): Token | undefined => input[i];
  while (i < input.length) {
    if (peek()!.kind === "literal") {
      output.push(input[i++]);
      if (peek()?.kind === "whitespace" && hasMultipleNewlines(peek()!.value)) {
        output.push(input[i++]);
        if (peek()?.kind === "literal") output.push(tok("paragraphOpen", "<p>"));
      }
    } else {
      output.push(input[i++]);
    }
  }
  return output;
}

// Replace whitespace after an `href=...>` literal with an optional line break.
function optionalizeSpacesAfterLinks(input: Token[]): Token[] {
  const output: Token[] = [];
  let i = 0;
  const peek = (): Token | undefined => input[i];
  while (i < input.length) {
    if (peek()!.kind === "literal" && /^href=[^>]*>$/.test(peek()!.value)) {
      output.push(input[i++]);
      if (peek()?.kind === "whitespace") output.push(tok("optionalLineBreak", input[i++].value));
    } else {
      output.push(input[i++]);
    }
  }
  return output;
}

// Adjust indentation inside `<pre>{@code` blocks: trim leading/trailing blank
// lines, de-indent to the least-indented line, move a trailing `}` to its own line.
function deindentPreCodeBlocks(input: Token[]): Token[] {
  const output: Token[] = [];
  let i = 0;
  const peek = (): Token | undefined => input[i];
  while (i < input.length) {
    if (peek()!.kind !== "preOpen") {
      output.push(input[i++]);
      continue;
    }
    output.push(input[i++]);
    const initialNewlines: Token[] = [];
    while (i < input.length && peek()!.kind === "forcedNewline") initialNewlines.push(input[i++]);
    const p = peek();
    if (!p || p.kind !== "literal" || !/^[ \t]*[{]@code$/.test(p.value)) {
      output.push(...initialNewlines);
      if (i < input.length) output.push(input[i++]);
      continue;
    }
    i = deindentPreCodeBlock(output, input, i);
  }
  return output;
}

function deindentPreCodeBlock(output: Token[], input: Token[], i: number): number {
  output.push(tok("literal", input[i++].value.trim()));
  const saved: Token[] = [];
  while (i < input.length && input[i].kind !== "preClose") saved.push(input[i++]);
  while (saved.length > 0 && saved[0].kind === "forcedNewline") saved.shift();
  while (saved.length > 0 && saved[saved.length - 1].kind === "forcedNewline") saved.pop();
  if (saved.length === 0) return i;

  // move a trailing `}` to its own line
  const last = saved[saved.length - 1];
  let trailingBrace = false;
  if (last.kind === "literal" && last.value.endsWith("}")) {
    saved.pop();
    if (last.value.length > 1) {
      saved.push(tok("literal", last.value.slice(0, -1)));
      saved.push(tok("forcedNewline", ""));
    }
    trailingBrace = true;
  }

  let trim = -1;
  for (const t of saved) {
    if (t.kind === "literal") {
      const idx = t.value.search(/[^ ]/);
      if (idx !== -1 && (trim === -1 || idx < trim)) trim = idx;
    }
  }

  output.push(tok("forcedNewline", "\n"));
  for (const t of saved) {
    if (t.kind === "literal") {
      output.push(
        tok("literal", trim > 0 && t.value.length > trim ? t.value.slice(trim) : t.value),
      );
    } else {
      output.push(t);
    }
  }
  output.push(trailingBrace ? tok("literal", "}") : tok("forcedNewline", "\n"));
  return i;
}
