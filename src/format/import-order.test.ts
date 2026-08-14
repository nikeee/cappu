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

// --- degenerate inputs -------------------------------------------------------

test("no imports produce no blocks at all", () => {
  expect(orderImports([], { style: "google" })).toEqual([]);
  expect(orderImports([], { style: "aosp" })).toEqual([]);
  expect(orderImports([], { style: "google", importOrder: ["java.*", "", "*"] })).toEqual([]);
});

test("a file of only static imports is one block", () => {
  const out = orderImports([imp("b.C.d", true), imp("a.B.c", true)], {
    style: "google",
    importOrder: ["java.*", "", "*"],
  });
  expect(names(out)).toEqual([["a.B.c", "b.C.d"]]);
});

test("an empty importOrder puts everything in the unmatched block", () => {
  const out = orderImports([imp("org.junit.Test"), imp("java.io.File")], {
    style: "google",
    importOrder: [],
  });
  expect(names(out)).toEqual([["java.io.File", "org.junit.Test"]]);
});

test("an importOrder of only blank-line entries still groups everything once", () => {
  const out = orderImports([imp("org.junit.Test"), imp("java.io.File")], {
    style: "google",
    importOrder: ["", ""],
  });
  expect(names(out)).toEqual([["java.io.File", "org.junit.Test"]]);
});

// A blank-line entry with no group on one side of it must not print as a stray
// blank line: the empty block is dropped, wherever it sits.
test("leading, trailing and doubled blank-line entries collapse", () => {
  const imports = [imp("java.io.File"), imp("org.junit.Test")];
  expect(
    names(orderImports(imports, { style: "google", importOrder: ["", "java.*", "", "org.*"] })),
  ).toEqual([["java.io.File"], ["org.junit.Test"]]);
  expect(
    names(orderImports(imports, { style: "google", importOrder: ["java.*", "", "org.*", ""] })),
  ).toEqual([["java.io.File"], ["org.junit.Test"]]);
  expect(
    names(orderImports(imports, { style: "google", importOrder: ["java.*", "", "", "org.*"] })),
  ).toEqual([["java.io.File"], ["org.junit.Test"]]);
});

test("a group whose pattern matches nothing is dropped", () => {
  const out = orderImports([imp("java.io.File")], {
    style: "google",
    importOrder: ["org.*", "", "java.*", "", "com.*"],
  });
  expect(names(out)).toEqual([["java.io.File"]]);
});

// --- what a prefix selects ---------------------------------------------------

// `import java.util.*;` has the name `java.util`, so a pattern must select its
// own package as well as everything under it.
test("an on-demand import matches the pattern for its own package", () => {
  const out = orderImports([imp("java.util"), imp("java.util.List"), imp("java.io.File")], {
    style: "google",
    importOrder: ["java.util.*", "", "java.*"],
  });
  expect(names(out)).toEqual([["java.util", "java.util.List"], ["java.io.File"]]);
});

test("prefix matching is case-sensitive", () => {
  const out = orderImports([imp("Java.io.File"), imp("java.io.File")], {
    style: "google",
    importOrder: ["java.*"],
  });
  expect(names(out)).toEqual([["java.io.File"], ["Java.io.File"]]);
});

test("the deepest of several nested prefixes wins", () => {
  const out = orderImports(
    [imp("com.acme.internal.Impl"), imp("com.acme.Widget"), imp("com.other.Thing")],
    { style: "google", importOrder: ["com.*", "", "com.acme.*", "", "com.acme.internal.*"] },
  );
  expect(names(out)).toEqual([
    ["com.other.Thing"],
    ["com.acme.Widget"],
    ["com.acme.internal.Impl"],
  ]);
});

test("a static import is never routed by a pattern", () => {
  const out = orderImports([imp("java.io.File"), imp("java.util.Arrays.asList", true)], {
    style: "google",
    importOrder: ["java.*"],
  });
  expect(names(out)).toEqual([["java.util.Arrays.asList"], ["java.io.File"]]);
});

// The result depends on the imports, not on the order they arrive in - the
// printer hands them over in source order, the code action in its own.
test("the input order does not affect the result", () => {
  const imports = [
    imp("org.junit.Test"),
    imp("java.io.File"),
    imp("com.acme.Widget"),
    imp("a.B.c", true),
  ];
  const options = { style: "google", importOrder: ["com.*", "", "java.*", "", "*"] } as const;
  const expected = names(orderImports(imports, options));
  expect(names(orderImports([...imports].reverse(), options))).toEqual(expected);
  expect(names(orderImports([imports[2], imports[0], imports[3], imports[1]], options))).toEqual(
    expected,
  );
});

// --- aosp built-in -----------------------------------------------------------

test("aosp counts androidx, dalvik and libcore as android", () => {
  const out = orderImports(
    [imp("libcore.io.Streams"), imp("androidx.core.App"), imp("dalvik.system.VMStack")],
    { style: "aosp" },
  );
  // Each is its own top-level package, so each gets a block, android first.
  expect(names(out)).toEqual([
    ["androidx.core.App"],
    ["dalvik.system.VMStack"],
    ["libcore.io.Streams"],
  ]);
});

test("aosp keeps static imports first, before the android block", () => {
  const out = orderImports([imp("android.view.View"), imp("org.junit.Assert.fail", true)], {
    style: "aosp",
  });
  expect(names(out)).toEqual([["org.junit.Assert.fail"], ["android.view.View"]]);
});
