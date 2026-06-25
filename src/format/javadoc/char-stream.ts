// Port of google-java-format core/.../java/javadoc/CharStream.java.
//
// String reader for the lexer: tryConsume* match at the current position, then
// readAndResetRecorded() returns and consumes the match. Regexes must be anchored
// (we use the sticky flag `y` to match only at the current position).

export class CharStream {
  private position = 0;
  private tokenEnd = -1; // negative => no pending token

  constructor(private readonly input: string) {}

  tryConsume(expected: string): boolean {
    if (!this.input.startsWith(expected, this.position)) return false;
    this.tokenEnd = this.position + expected.length;
    return true;
  }

  /** `pattern` must be a sticky (`y`) RegExp so it matches only at `position`. */
  tryConsumeRegex(pattern: RegExp): boolean {
    pattern.lastIndex = this.position;
    const m = pattern.exec(this.input);
    if (!m || m.index !== this.position) return false;
    this.tokenEnd = this.position + m[0].length;
    return true;
  }

  readAndResetRecorded(): string {
    const result = this.input.slice(this.position, this.tokenEnd);
    this.position = this.tokenEnd;
    this.tokenEnd = -1;
    return result;
  }

  isExhausted(): boolean {
    return this.position === this.input.length;
  }

  pos(): number {
    return this.position;
  }
}
