// The import block end to end: what `importOrder` does to a real file, as
// opposed to src/format/import-order.test.ts, which tests the grouping in
// isolation. The concerns here are the ones only the printer can get wrong -
// stray blank lines, a block that does not exist, and stability under a second
// pass.

import { test } from "node:test";

import { expect } from "expect";

import { formatSource } from "./index.ts";

const src = (...lines: string[]): string => `${lines.join("\n")}\n`;

const order = ["java.*", "", "*"];

test("a file without imports is unaffected by importOrder", () => {
  const source = src("package p;", "", "class T {}");
  expect(formatSource(source, { style: "google", importOrder: order })).toBe(source);
});

test("a file of only static imports gets one block and no stray blank line", () => {
  const out = formatSource(
    src(
      "package p;",
      "",
      "import static org.junit.Assert.fail;",
      "import static java.util.Arrays.asList;",
      "",
      "class T {}",
    ),
    { style: "google", importOrder: order },
  );
  expect(out).toBe(
    src(
      "package p;",
      "",
      "import static java.util.Arrays.asList;",
      "import static org.junit.Assert.fail;",
      "",
      "class T {}",
    ),
  );
});

// Whatever blank lines the source had inside the import block are discarded:
// the configured groups decide where they go.
test("source blank lines inside the import block are replaced by the configured ones", () => {
  const out = formatSource(
    src(
      "package p;",
      "",
      "import java.io.File;",
      "",
      "",
      "import org.junit.Test;",
      "import com.acme.Widget;",
      "",
      "class T {",
      "  File f;",
      "  Test t;",
      "  Widget w;",
      "}",
    ),
    { style: "google", importOrder: order },
  );
  expect(out).toContain(
    "import java.io.File;\n\nimport com.acme.Widget;\nimport org.junit.Test;\n\nclass T {",
  );
});

// The name of `import java.util.*;` is `java.util`, so it has to match the
// pattern for its own package rather than falling through to a broader one.
test("an on-demand import lands in its own package's group", () => {
  const out = formatSource(
    src(
      "package p;",
      "",
      "import java.util.*;",
      "import java.util.List;",
      "import java.io.File;",
      "",
      "class T {",
      "  File f;",
      "  List<String> l;",
      "}",
    ),
    { style: "google", importOrder: ["java.util.*", "", "java.*"] },
  );
  // gjf sorts the wildcard first within its group.
  expect(out).toContain("import java.util.*;\nimport java.util.List;\n\nimport java.io.File;");
});

test("a file without a package declaration still groups its imports", () => {
  const out = formatSource(
    src(
      "import java.io.File;",
      "import com.acme.Widget;",
      "",
      "class T {",
      "  File f;",
      "  Widget w;",
      "}",
    ),
    { style: "google", importOrder: order },
  );
  expect(out).toBe(
    src(
      "import java.io.File;",
      "",
      "import com.acme.Widget;",
      "",
      "class T {",
      "  File f;",
      "  Widget w;",
      "}",
    ),
  );
});

// Formatting an already-formatted file must not move imports again, in either
// style and with or without a configured order.
test("import grouping is idempotent", () => {
  const source = src(
    "package p;",
    "",
    "import static org.junit.Assert.fail;",
    "import org.junit.Test;",
    "import java.io.File;",
    "import android.view.View;",
    "import com.acme.Widget;",
    "",
    "class T {",
    "  File f;",
    "  Test t;",
    "  View v;",
    "  Widget w;",
    "}",
  );
  for (const options of [
    { style: "google" as const },
    { style: "aosp" as const },
    { style: "google" as const, importOrder: order },
    { style: "aosp" as const, importOrder: ["android.*", "", "com.*", "", "*"] },
  ]) {
    const once = formatSource(source, options);
    expect(formatSource(once, options)).toBe(once);
  }
});

test("aosp style groups android, third party and java without a configured order", () => {
  const out = formatSource(
    src(
      "package p;",
      "",
      "import java.io.File;",
      "import android.view.View;",
      "import com.acme.Widget;",
      "",
      "class T {",
      "  File f;",
      "  View v;",
      "  Widget w;",
      "}",
    ),
    { style: "aosp" },
  );
  expect(out).toContain(
    "import android.view.View;\n\nimport com.acme.Widget;\n\nimport java.io.File;",
  );
});
