// Port of google-java-format core/.../java/javadoc/Token.java.
//
// Javadoc token taxonomy. The lexer (lexer.ts) produces a stream of these and
// the writer (writer.ts) renders them. "kind" replaces gjf's sealed record
// hierarchy; `value` is the token text. MarkdownFencedCodeBlock carries extra
// fields (start/end/literal); other kinds use only `value`.

export type TokenKind =
  | "beginJavadoc"
  | "endJavadoc"
  | "footerJavadocTagStart"
  | "snippetBegin"
  | "snippetEnd"
  | "listOpen"
  | "listClose"
  | "listItemOpen"
  | "listItemClose"
  | "headerOpen"
  | "headerClose"
  | "paragraphOpen"
  | "paragraphClose"
  | "blockquoteOpen"
  | "blockquoteClose"
  | "preOpen"
  | "preClose"
  | "codeOpen"
  | "codeClose"
  | "tableOpen"
  | "tableClose"
  | "moeBeginStrip"
  | "moeEndStrip"
  | "htmlComment"
  | "br"
  | "markdownCodeSpanStart"
  | "markdownCodeSpanEnd"
  | "markdownFencedCodeBlock"
  | "markdownTable"
  | "whitespace"
  | "forcedNewline"
  | "markdownHardLineBreak"
  | "optionalLineBreak"
  | "literal";

export interface Token {
  kind: TokenKind;
  value: string;
}

export function tok(kind: TokenKind, value: string): Token {
  return { kind, value };
}

/** A factory `(value) => Token` of a fixed kind, mirroring gjf's `Type::new`. */
export function factory(kind: TokenKind): (value: string) => Token {
  return value => ({ kind, value });
}

/**
 * Tokens always pinned to the following token (no break or space after them):
 * `<p>`, `<li>`, headers. Mirrors gjf's `StartOfLineToken` marker interface.
 */
export function isStartOfLine(kind: TokenKind): boolean {
  return kind === "listItemOpen" || kind === "headerOpen" || kind === "paragraphOpen";
}
