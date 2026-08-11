// Reordering the import block moves lines past each other, so comment handling
// is a correctness requirement: nothing may vanish, nothing may end up next to
// an import it did not document, and the result must still parse.
//
// google-java-format's rule (ImportOrderer.Import: "the import itself and
// following comments") is that an own-line comment between two imports belongs
// to the PRECEDING import and travels with it; comments above the first import
// head the whole block. These tests pin that for the default order and for a
// configured `importOrder`, where blocks move imports much further.

import { test } from "node:test";

import { expect } from "expect";

import { parseSourceFile } from "../compiler/parser.ts";
import { collectComments } from "./comments.ts";
import { formatSource } from "./index.ts";

const src = (...lines: string[]): string => `${lines.join("\n")}\n`;

/** Every comment in a file, in source order, so none can go missing unseen. */
const comments = (text: string): string[] => collectComments(text).map(c => c.text);

/**
 * Format, then assert the output still parses and carries exactly the comments
 * the input did - the invariant behind "a comment never vanishes".
 */
function formatPreservingComments(source: string, importOrder?: string[]): string {
  const out = formatSource(source, { style: "google", importOrder });
  expect(parseSourceFile("T.java", out).parseDiagnostics).toEqual([]);
  expect(comments(out).toSorted()).toEqual(comments(source).toSorted());
  return out;
}

test("a trailing comment follows its import into another block", () => {
  const out = formatPreservingComments(
    src(
      "package p;",
      "",
      "import java.io.File; // needed for the temp dir",
      "import org.junit.Test; // NOPMD",
      "",
      "class T {",
      "  File f;",
      "  Test t;",
      "}",
    ),
    ["org.*", "", "java.*"],
  );
  expect(out).toContain("import org.junit.Test; // NOPMD\n\nimport java.io.File; // needed");
});

test("an own-line comment follows the import it sits under", () => {
  // gjf hangs it off the PRECEDING import, so it moves with org.junit.Test.
  const out = formatPreservingComments(
    src(
      "package p;",
      "",
      "import org.junit.Test;",
      "// only used by the parameterized cases",
      "import java.io.File;",
      "",
      "class T {",
      "  File f;",
      "  Test t;",
      "}",
    ),
    ["java.*", "", "org.*"],
  );
  expect(out).toContain(
    "import java.io.File;\n\nimport org.junit.Test;\n// only used by the parameterized cases",
  );
});

test("comments above the first import head the block and stay on top", () => {
  const out = formatPreservingComments(
    src(
      "package p;",
      "",
      "// Source: http://example.com/snippet",
      "// License: Open Source",
      "",
      "import org.junit.Test;",
      "import java.io.File;",
      "",
      "class T {",
      "  File f;",
      "  Test t;",
      "}",
    ),
    ["java.*", "", "org.*"],
  );
  expect(out).toContain(
    "// Source: http://example.com/snippet\n// License: Open Source\n\nimport java.io.File;",
  );
});

test("a duplicate import hands its comments to the survivor", () => {
  const out = formatPreservingComments(
    src(
      "package p;",
      "",
      "import java.io.File;",
      "import java.io.File; // the duplicate's comment",
      "// and its follower",
      "import org.junit.Test;",
      "",
      "class T {",
      "  File f;",
      "  Test t;",
      "}",
    ),
  );
  // One import line left, both comments still attached to it.
  expect(out.match(/import java\.io\.File;/g)).toHaveLength(1);
  expect(out).toContain("// the duplicate's comment");
  expect(out).toContain("// and its follower");
});

test("block and javadoc-shaped comments between imports survive a regroup", () => {
  const out = formatPreservingComments(
    src(
      "package p;",
      "",
      "import org.junit.Test;",
      "/* a block comment */",
      "/** javadoc-shaped, but not documenting anything */",
      "import java.io.File;",
      "",
      "class T {",
      "  File f;",
      "  Test t;",
      "}",
    ),
    ["java.*", "", "org.*"],
  );
  expect(out).toContain("/* a block comment */");
  expect(out).toContain("/** javadoc-shaped, but not documenting anything */");
});

test("a comment after the last import belongs to what follows, not to the block", () => {
  const out = formatPreservingComments(
    src(
      "package p;",
      "",
      "import java.io.File;",
      "",
      "/** The class javadoc. */",
      "class T {",
      "  File f;",
      "}",
    ),
    ["*"],
  );
  expect(out).toContain("/** The class javadoc. */\nclass T {");
});

test("comments survive when every import changes block", () => {
  const out = formatPreservingComments(
    src(
      "package p;",
      "",
      "import static org.junit.Assert.assertTrue; // static one",
      "import android.view.View; // android",
      "// follows the android import",
      "import java.io.File; // java",
      "import com.acme.Widget; // third party",
      "",
      "class T {",
      "  File f;",
      "  View v;",
      "  Widget w;",
      "}",
    ),
    ["java.*", "", "com.*", "", "android.*"],
  );
  // Each comment is still on, or directly under, the import it came with.
  expect(out).toContain("import java.io.File; // java");
  expect(out).toContain("import com.acme.Widget; // third party");
  expect(out).toContain("import android.view.View; // android\n// follows the android import");
  expect(out).toContain("import static org.junit.Assert.assertTrue; // static one");
});
