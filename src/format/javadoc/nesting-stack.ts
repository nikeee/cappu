// Port of google-java-format core/.../java/javadoc/NestingStack.java.
//
// A generic nesting stack (used by the lexer for HTML/code/table contexts) and
// an Int variant (used by the writer for list/footer indentation levels).

export class NestingStack<E> {
  private readonly stack: E[] = [];

  push(value: E): void {
    this.stack.push(value);
  }

  /** If the top is in `values`, pop and return it; else return undefined. */
  popIfIn(values: readonly E[]): E | undefined {
    if (this.stack.length === 0 || !values.includes(this.peek()!)) return undefined;
    return this.stack.pop();
  }

  /** If the stack contains `value`, pop it and everything above it; else nothing. */
  popUntil(value: E): void {
    if (!this.stack.includes(value)) return;
    let popped: E;
    do {
      popped = this.stack.pop()!;
    } while (popped !== value);
  }

  contains(value: E): boolean {
    return this.stack.includes(value);
  }

  containsAny(values: readonly E[]): boolean {
    return this.stack.some(e => values.includes(e));
  }

  isEmpty(): boolean {
    return this.stack.length === 0;
  }

  private peek(): E | undefined {
    return this.stack[this.stack.length - 1];
  }
}

/** Integer nesting stack tracking a running total (list/footer indent levels). */
export class IntNestingStack {
  private readonly stack: number[] = [];
  private _total = 0;

  total(): number {
    return this._total;
  }

  push(value = 1): void {
    this.stack.push(value);
    this._total += value;
  }

  popIfNotEmpty(): void {
    if (this.stack.length > 0) this._total -= this.stack.pop()!;
  }

  isEmpty(): boolean {
    return this.stack.length === 0;
  }

  reset(): void {
    this.stack.length = 0;
    this._total = 0;
  }
}
