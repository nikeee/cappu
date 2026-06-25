// Port of google-java-format core/.../java/javadoc/JavadocFormatter.java.
//
// Entry point: lex a classic `/** ... */` comment, render it through the writer,
// then collapse to a one-liner when it fits. The Markdown `///` path is deferred.

import { lex, LexException } from "./lexer.ts";
import { type Token } from "./token.ts";
import { JavadocWriter } from "./writer.ts";

export const MAX_LINE_LENGTH = 100;

/**
 * Format a classic javadoc comment (starts with `/**`, ends with `*​/`). On any
 * lex failure the input is returned unchanged. `blockIndent` is the column the
 * comment starts at.
 */
export function formatJavadoc(input: string, blockIndent: number): string {
  if (!input.startsWith("/**")) return input; // Markdown `///` deferred
  let tokens: Token[];
  try {
    tokens = lex(input);
  } catch (e) {
    if (e instanceof LexException) return input;
    throw e;
  }
  const result = render(tokens, blockIndent);
  return makeSingleLineIfPossible(blockIndent, result);
}

function render(tokens: Token[], blockIndent: number): string {
  const out = new JavadocWriter(blockIndent, true);
  for (const token of tokens) {
    switch (token.kind) {
      case "beginJavadoc":
        out.writeBeginJavadoc();
        break;
      case "endJavadoc":
        out.writeEndJavadoc();
        return out.toString();
      case "footerJavadocTagStart":
        out.writeFooterJavadocTagStart(token);
        break;
      case "snippetBegin":
        out.writeSnippetBegin(token);
        break;
      case "snippetEnd":
        out.writeSnippetEnd(token);
        break;
      case "listOpen":
        out.writeListOpen(token);
        break;
      case "listClose":
        out.writeListClose(token);
        break;
      case "listItemOpen":
        out.writeListItemOpen(token);
        break;
      case "headerOpen":
        out.writeHeaderOpen(token);
        break;
      case "headerClose":
        out.writeHeaderClose(token);
        break;
      case "paragraphOpen":
        out.writeParagraphOpen(standardize(token, STANDARD_P));
        break;
      case "blockquoteOpen":
      case "blockquoteClose":
        out.writeBlockquoteOpenOrClose(token);
        break;
      case "preOpen":
        out.writePreOpen(token);
        break;
      case "preClose":
        out.writePreClose(token);
        break;
      case "codeOpen":
        out.writeCodeOpen(token);
        break;
      case "codeClose":
        out.writeCodeClose(token);
        break;
      case "tableOpen":
        out.writeTableOpen(token);
        break;
      case "tableClose":
        out.writeTableClose(token);
        break;
      case "moeBeginStrip":
        out.requestMoeBeginStripComment(token);
        break;
      case "moeEndStrip":
        out.writeMoeEndStripComment(token);
        break;
      case "htmlComment":
        out.writeHtmlComment(token);
        break;
      case "br":
        out.writeBr(standardize(token, STANDARD_BR));
        break;
      case "whitespace":
        out.requestWhitespace();
        break;
      case "forcedNewline":
        out.writeLineBreakNoAutoIndent();
        break;
      case "markdownHardLineBreak":
        out.writeMarkdownHardLineBreak();
        break;
      case "literal":
        out.writeLiteral(token);
        break;
      case "markdownFencedCodeBlock":
        out.writeMarkdownFencedCodeBlock(token);
        break;
      case "markdownTable":
        out.writeMarkdownTable(token);
        break;
      // Ignorable: closing tags handled by their open counterparts, optional breaks.
      case "listItemClose":
      case "paragraphClose":
      case "optionalLineBreak":
      case "markdownCodeSpanStart":
      case "markdownCodeSpanEnd":
        break;
    }
  }
  throw new Error("javadoc render: missing endJavadoc");
}

const STANDARD_BR: Token = { kind: "br", value: "<br>" };
const STANDARD_P: Token = { kind: "paragraphOpen", value: "<p>" };
const SIMPLE_TAG = /^<\w+\s*\/?\s*>/i;

function standardize(token: Token, standard: Token): Token {
  return SIMPLE_TAG.test(token.value) ? standard : token;
}

const ONE_CONTENT_LINE = /^ *\/\*\*\n *\* (.*)\n *\*\/$/;

function makeSingleLineIfPossible(blockIndent: number, input: string): string {
  const m = input.match(ONE_CONTENT_LINE);
  if (m) {
    const line = m[1];
    if (line === "") return "/** */";
    if (oneLineJavadoc(line, blockIndent)) return `/** ${line} */`;
  }
  return input;
}

function oneLineJavadoc(line: string, blockIndent: number): boolean {
  const oneLinerContentLength = MAX_LINE_LENGTH - "/**  */".length - blockIndent;
  if (line.length > oneLinerContentLength) return false;
  // A javadoc that is only a tag uses multiple lines (except /** @hide */).
  if (line.startsWith("@") && line !== "@hide") return false;
  return true;
}
