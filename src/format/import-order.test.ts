import { test } from "node:test";

import { expect } from "expect";

import { type ImportLike, orderImports } from "./import-order.ts";

const imp = (name: string, isStatic = false): ImportLike => ({ name, isStatic });

/** Blocks as name lists, so an expectation reads like the printed output. */
const names = (blocks: ImportLike[][]): string[][] => blocks.map(b => b.map(i => i.name));

test("google style is one lexicographic block", () => {
  const out = orderImports([imp("org.junit.Test"), imp("java.io.File")], { style: "google" });
  expect(names(out)).toEqual([["java.io.File", "org.junit.Test"]]);
});

test("static imports are their own first block", () => {
  const out = orderImports(
    [imp("java.io.File"), imp("org.junit.Assert.assertTrue", true), imp("a.B.c", true)],
    { style: "google" },
  );
  expect(names(out)).toEqual([["a.B.c", "org.junit.Assert.assertTrue"], ["java.io.File"]]);
});

test("a pattern groups by prefix and an empty entry breaks the block", () => {
  const out = orderImports(
    [imp("java.io.File"), imp("org.junit.Test"), imp("javax.inject.Inject")],
    { style: "google", importOrder: ["org.*", "", "java.*", "javax.*"] },
  );
  expect(names(out)).toEqual([["org.junit.Test"], ["java.io.File", "javax.inject.Inject"]]);
});

test("the longest matching prefix wins, whatever the list order", () => {
  const out = orderImports([imp("com.acme.Widget"), imp("com.other.Thing")], {
    style: "google",
    importOrder: ["com.*", "", "com.acme.*"],
  });
  // Under a first-match rule the second block could never fill.
  expect(names(out)).toEqual([["com.other.Thing"], ["com.acme.Widget"]]);
});

test("equally specific patterns fall back to list order", () => {
  const out = orderImports([imp("com.acme.Widget")], {
    style: "google",
    importOrder: ["com.acme.*", "", "com.acme.*"],
  });
  expect(names(out)).toEqual([["com.acme.Widget"]]);
});

test("`*` is the catch-all wherever it sits", () => {
  const out = orderImports([imp("zzz.Other"), imp("java.io.File"), imp("com.acme.Widget")], {
    style: "google",
    importOrder: ["*", "", "java.*"],
  });
  expect(names(out)).toEqual([["com.acme.Widget", "zzz.Other"], ["java.io.File"]]);
});

test("imports matching nothing form a last block of their own", () => {
  const out = orderImports([imp("java.io.File"), imp("com.acme.Widget")], {
    style: "google",
    importOrder: ["java.*"],
  });
  expect(names(out)).toEqual([["java.io.File"], ["com.acme.Widget"]]);
});

test("a prefix does not match a longer top-level name", () => {
  // `com.` must not swallow `common.`.
  const out = orderImports([imp("common.Thing"), imp("com.acme.Widget")], {
    style: "google",
    importOrder: ["com.*"],
  });
  expect(names(out)).toEqual([["com.acme.Widget"], ["common.Thing"]]);
});

test("an empty block is dropped rather than printed as a stray blank line", () => {
  const out = orderImports([imp("java.io.File")], {
    style: "google",
    importOrder: ["org.*", "", "java.*"],
  });
  expect(names(out)).toEqual([["java.io.File"]]);
});

// gjf's AOSP order: android, then third party, then java/javax, with a blank
// line wherever the top-level package changes.
test("aosp style groups android, third party and java", () => {
  const out = orderImports(
    [
      imp("java.io.File"),
      imp("org.junit.Test"),
      imp("android.view.View"),
      imp("javax.inject.Inject"),
      imp("com.acme.Widget"),
    ],
    { style: "aosp" },
  );
  expect(names(out)).toEqual([
    ["android.view.View"],
    ["com.acme.Widget"],
    ["org.junit.Test"],
    ["java.io.File"],
    ["javax.inject.Inject"],
  ]);
});

test("aosp splits an android import from a non-android one sharing its top level", () => {
  const out = orderImports([imp("com.android.Foo"), imp("com.acme.Widget")], { style: "aosp" });
  expect(names(out)).toEqual([["com.android.Foo"], ["com.acme.Widget"]]);
});

test("aosp keeps one top-level package in one block", () => {
  const out = orderImports([imp("org.junit.Test"), imp("org.mockito.Mockito")], { style: "aosp" });
  expect(names(out)).toEqual([["org.junit.Test", "org.mockito.Mockito"]]);
});

test("importOrder overrides the style's built-in order", () => {
  const out = orderImports([imp("android.view.View"), imp("java.io.File")], {
    style: "aosp",
    importOrder: ["java.*", "", "*"],
  });
  expect(names(out)).toEqual([["java.io.File"], ["android.view.View"]]);
});
