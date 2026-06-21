import assert from "node:assert/strict";
import { test } from "node:test";

import { type MethodLines, resolveLine } from "./lineMapping.ts";

const entry = (lineCodeIndex: number, lineNumber: number) => ({
  lineCodeIndex: BigInt(lineCodeIndex),
  lineNumber,
});

test("exact line match binds to the lowest code index for that line", () => {
  const methods: MethodLines[] = [
    { methodId: 1n, lines: [entry(0, 3), entry(5, 4), entry(12, 6)] },
  ];
  assert.deepEqual(resolveLine(methods, 4), { methodId: 1n, index: 5n, line: 4 });
});

test("a line with no entry adjusts to the next executable line", () => {
  // line 5 is blank/comment: there is no entry, so bind to line 6.
  const methods: MethodLines[] = [
    { methodId: 1n, lines: [entry(0, 3), entry(5, 4), entry(12, 6)] },
  ];
  assert.deepEqual(resolveLine(methods, 5), { methodId: 1n, index: 12n, line: 6 });
});

test("the match can come from any method of the class (e.g. a lambda body)", () => {
  const methods: MethodLines[] = [
    { methodId: 1n, lines: [entry(0, 3), entry(8, 10)] },
    { methodId: 2n, lines: [entry(0, 6), entry(4, 7)] }, // synthetic lambda method
  ];
  assert.deepEqual(resolveLine(methods, 7), { methodId: 2n, index: 4n, line: 7 });
});

test("a line past the end of every method is unresolvable", () => {
  const methods: MethodLines[] = [{ methodId: 1n, lines: [entry(0, 3), entry(5, 4)] }];
  assert.equal(resolveLine(methods, 99), null);
});

test("when two methods report the same next line, the lower code index wins", () => {
  const methods: MethodLines[] = [
    { methodId: 1n, lines: [entry(20, 8)] },
    { methodId: 2n, lines: [entry(4, 8)] },
  ];
  assert.deepEqual(resolveLine(methods, 8), { methodId: 2n, index: 4n, line: 8 });
});
