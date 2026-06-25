import { test } from "node:test";

import { expect } from "expect";

import { selectMainClass } from "./run.ts";

test("selectMainClass: configured mainClass always wins", () => {
  expect(selectMainClass(["com.app.A", "com.app.B"], "com.app.Chosen")).toEqual({
    mainClass: "com.app.Chosen",
  });
  // even when nothing was detected
  expect(selectMainClass([], "com.app.Chosen")).toEqual({ mainClass: "com.app.Chosen" });
});

test("selectMainClass: a single detected entry point is used", () => {
  expect(selectMainClass(["com.app.Main"], undefined)).toEqual({ mainClass: "com.app.Main" });
});

test("selectMainClass: none detected is an error", () => {
  const r = selectMainClass([], undefined);
  expect(r).toEqual({ error: expect.stringMatching(/no class declares a main/) });
});

test("selectMainClass: ambiguous detection lists the candidates", () => {
  const r = selectMainClass(["com.app.A", "com.app.B"], undefined);
  expect(r).toEqual({ error: expect.stringMatching(/com\.app\.A, com\.app\.B/) });
});
