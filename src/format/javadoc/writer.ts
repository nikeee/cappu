// Port of google-java-format core/.../java/javadoc/JavadocWriter.java.
//
// Stateful renderer: accepts "requests" and "writes" from the formatter and
// produces wrapped javadoc. It tracks list/footer nesting and remaining columns
// so it can re-wrap prose. We only support classic `/** */` javadoc (the Markdown
// `///` methods are kept for fidelity but are not exercised yet).

import { MAX_LINE_LENGTH } from "./formatter.ts";
import { IntNestingStack } from "./nesting-stack.ts";
import { isStartOfLine, type Token } from "./token.ts";

const enum WS {
  None,
  Whitespace,
  Newline,
  BlankLine,
}

const BACKSLASH_LITERAL: Token = { kind: "literal", value: "\\" };

export class JavadocWriter {
  private out = "";
  private continuingListItemOfInnermostList = false;
  private continuingFooterTag = false;
  private readonly continuingListItemStack = new IntNestingStack();
  private readonly continuingListStack = new IntNestingStack();
  private readonly postWriteModifiedContinuingListStack = new IntNestingStack();
  private remainingOnLine = 0;
  private atStartOfLine = false;
  private requestedWhitespace = WS.None;
  private wroteAnythingSignificant = false;

  constructor(
    private readonly blockIndent: number,
    private readonly classicJavadoc = true,
  ) {}

  requestWhitespace(): void {
    this.request(WS.Whitespace);
  }

  private request(ws: WS): void {
    if (ws > this.requestedWhitespace) this.requestedWhitespace = ws;
  }

  private requestBlankLine(): void {
    this.request(WS.BlankLine);
  }

  private requestNewline(): void {
    this.request(WS.Newline);
  }

  writeBeginJavadoc(): void {
    this.out += "/**";
    this.writeNewline();
  }

  writeEndJavadoc(): void {
    this.out += "\n";
    this.appendSpaces(this.blockIndent + 1);
    this.out += "*/";
  }

  writeFooterJavadocTagStart(token: Token): void {
    this.continuingListItemOfInnermostList = false;
    this.continuingListItemStack.reset();
    this.continuingListStack.reset();
    this.postWriteModifiedContinuingListStack.reset();
    if (!this.wroteAnythingSignificant) {
      // Javadoc consists solely of tags (OK for @Override).
    } else if (!this.continuingFooterTag) {
      this.requestBlankLine();
    } else {
      this.continuingFooterTag = false;
      this.requestNewline();
    }
    this.writeToken(token);
    this.continuingFooterTag = true;
  }

  writeSnippetBegin(token: Token): void {
    this.requestBlankLine();
    this.writeToken(token);
  }

  writeSnippetEnd(token: Token): void {
    this.writeToken(token);
    this.requestBlankLine();
  }

  writeListOpen(token: Token): void {
    this.requestBlankLine();
    this.writeToken(token);
    this.continuingListItemOfInnermostList = false;
    this.continuingListStack.push(token.value === "" ? 0 : 2);
    this.postWriteModifiedContinuingListStack.push();
    this.requestNewline();
  }

  writeListClose(token: Token): void {
    this.requestNewline();
    this.continuingListItemStack.popIfNotEmpty();
    this.continuingListStack.popIfNotEmpty();
    this.writeToken(token);
    this.postWriteModifiedContinuingListStack.popIfNotEmpty();
    this.requestBlankLine();
  }

  writeListItemOpen(token: Token): void {
    this.requestNewline();
    if (this.continuingListItemOfInnermostList) {
      this.continuingListItemOfInnermostList = false;
      this.continuingListItemStack.popIfNotEmpty();
    }
    this.writeToken(token);
    this.continuingListItemOfInnermostList = true;
    this.continuingListItemStack.push(token.value.length);
  }

  writeHeaderOpen(token: Token): void {
    if (this.wroteAnythingSignificant) this.requestBlankLine();
    this.writeToken(token);
  }

  writeHeaderClose(token: Token): void {
    this.writeToken(token);
    this.requestBlankLine();
  }

  writeParagraphOpen(token: Token): void {
    if (!this.wroteAnythingSignificant) return; // ignore a leading <p>
    this.requestBlankLine();
    this.writeToken(token);
  }

  writeBlockquoteOpenOrClose(token: Token): void {
    this.requestBlankLine();
    this.writeToken(token);
    this.requestBlankLine();
  }

  writePreOpen(token: Token): void {
    this.requestBlankLine();
    this.writeToken(token);
  }

  writePreClose(token: Token): void {
    this.writeToken(token);
    this.requestBlankLine();
  }

  writeCodeOpen(token: Token): void {
    this.writeToken(token);
  }

  writeCodeClose(token: Token): void {
    this.writeToken(token);
  }

  writeTableOpen(token: Token): void {
    this.requestBlankLine();
    this.writeToken(token);
  }

  writeTableClose(token: Token): void {
    this.writeToken(token);
    this.requestBlankLine();
  }

  writeHtmlComment(token: Token): void {
    this.requestNewline();
    this.writeToken(token);
    this.requestNewline();
  }

  writeBr(token: Token): void {
    this.writeToken(token);
    this.requestNewline();
  }

  writeMoeEndStripComment(token: Token): void {
    this.writeLineBreakNoAutoIndent();
    this.writeToken(token);
    this.requestNewline();
  }

  requestMoeBeginStripComment(_token: Token): void {
    // MOE strip comments are not used; request handled minimally.
    this.requestNewline();
  }

  writeLineBreakNoAutoIndent(): void {
    this.writeNewline(false);
  }

  writeMarkdownHardLineBreak(): void {
    this.writeLiteral(BACKSLASH_LITERAL);
    this.writeNewline();
  }

  writeLiteral(token: Token): void {
    this.writeToken(token);
  }

  // Markdown-only; inert for classic javadoc but kept for fidelity.
  writeMarkdownFencedCodeBlock(token: Token): void {
    this.flushWhitespace();
    this.out += token.value;
    this.requestBlankLine();
  }

  writeMarkdownTable(token: Token): void {
    this.flushWhitespace();
    const lines = token.value.split("\n");
    this.out += lines[0];
    for (const line of lines.slice(1)) {
      this.writeNewline(false);
      this.out += line;
    }
    this.requestBlankLine();
  }

  toString(): string {
    return this.out;
  }

  private flushWhitespace(): void {
    if (
      this.classicJavadoc &&
      this.requestedWhitespace === WS.BlankLine &&
      (!this.postWriteModifiedContinuingListStack.isEmpty() || this.continuingFooterTag)
    ) {
      // No blank lines inside lists or footer tags.
      this.requestedWhitespace = WS.Newline;
    }
    if (this.requestedWhitespace === WS.BlankLine) {
      this.writeBlankLine();
      this.requestedWhitespace = WS.None;
    } else if (this.requestedWhitespace === WS.Newline) {
      this.writeNewline();
      this.requestedWhitespace = WS.None;
    }
  }

  private writeToken(token: Token): void {
    if (token.value === "") return;
    this.flushWhitespace();
    const needWhitespace = this.requestedWhitespace === WS.Whitespace;

    if (
      !this.atStartOfLine &&
      token.value.length + (needWhitespace ? 1 : 0) > this.remainingOnLine
    ) {
      this.writeNewline();
    }
    if (!this.atStartOfLine && needWhitespace) {
      this.out += " ";
      this.remainingOnLine--;
    }

    this.out += token.value;
    if (!isStartOfLine(token.kind)) this.atStartOfLine = false;
    this.remainingOnLine -= token.value.length;
    this.requestedWhitespace = WS.None;
    this.wroteAnythingSignificant = true;
  }

  private writeNewlineStart(): void {
    this.out += "\n";
    this.appendSpaces(this.blockIndent + 1);
    this.out += "*";
  }

  private writeBlankLine(): void {
    this.writeNewlineStart();
    this.writeNewline();
  }

  private writeNewline(autoIndent = true): void {
    this.writeNewlineStart();
    this.appendSpaces(1);
    this.remainingOnLine = MAX_LINE_LENGTH - this.blockIndent - 3;
    if (autoIndent) {
      this.appendSpaces(this.innerIndent());
      this.remainingOnLine -= this.innerIndent();
    }
    this.atStartOfLine = true;
  }

  private innerIndent(): number {
    let n = this.continuingListItemStack.total() + this.continuingListStack.total();
    if (this.continuingFooterTag) n += 4;
    return n;
  }

  private appendSpaces(count: number): void {
    this.out += " ".repeat(count);
  }
}
