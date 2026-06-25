import { test } from "node:test";
import { expect } from "expect";

import { concat, group, hardline, indent, join, line, printDoc, softline } from "./doc.ts";

const print = (doc: Parameters<typeof printDoc>[0], width = 100, indentMultiplier = 1) =>
  printDoc(doc, { width, indentMultiplier });

test("flat group stays on one line when it fits", () => {
  const doc = group(concat(["f(", join(concat([",", line]), ["a", "b", "c"]), ")"]));
  expect(print(doc)).toBe("f(a, b, c)");
});

test("group breaks when it does not fit", () => {
  const doc = group(
    concat([
      "f(",
      indent(concat([softline, join(concat([",", line]), ["aa", "bb"])])),
      softline,
      ")",
    ]),
  );
  expect(print(doc, 6)).toBe("f(\n  aa,\n  bb\n)");
});

test("hardline always breaks and forces the enclosing group", () => {
  const doc = group(concat(["{", indent(concat([hardline, "stmt;"])), hardline, "}"]));
  expect(print(doc)).toBe("{\n  stmt;\n}");
});

test("indent multiplier controls indent width", () => {
  const doc = group(concat(["{", indent(concat([hardline, "x;"])), hardline, "}"]));
  expect(print(doc, 100, 2)).toBe("{\n    x;\n}");
});

test("trailing spaces on broken lines are trimmed", () => {
  const doc = concat(["a", " ", hardline, "b"]);
  expect(print(doc)).toBe("a\nb");
});
