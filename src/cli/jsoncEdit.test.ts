import assert from "node:assert/strict";
import { test } from "node:test";

import { hasJsoncKey, removeJsoncKey, setJsoncValue } from "./jsoncEdit.ts";

// These byte-exact cases are mirrored in togo/internal/config/edit_test.go;
// the two builds must produce identical files for the same edit.

const multi = `{
  // the project version
  "version": "1.2.3",
  "dependencies": {
    // app deps
    "implementation": {
      "org.slf4j:slf4j-api": "2.0.0"
    }
  }
}
`;

test("replacing an existing value touches only the value bytes", () => {
  const got = setJsoncValue(multi, ["version"], "1.2.4");
  assert.equal(got, multi.replace('"1.2.3"', '"1.2.4"'));
});

test("overwriting a dependency version keeps comments and formatting", () => {
  const got = setJsoncValue(
    multi,
    ["dependencies", "implementation", "org.slf4j:slf4j-api"],
    "2.1.0",
  );
  assert.equal(got, multi.replace('"2.0.0"', '"2.1.0"'));
});

test("inserting a dependency appends a member in the file's indentation", () => {
  const got = setJsoncValue(
    multi,
    ["dependencies", "implementation", "com.google.code.gson:gson"],
    "2.14.0",
  );
  assert.equal(
    got,
    multi.replace(
      '"org.slf4j:slf4j-api": "2.0.0"',
      '"org.slf4j:slf4j-api": "2.0.0",\n      "com.google.code.gson:gson": "2.14.0"',
    ),
  );
});

test("inserting creates missing sections with nested indentation", () => {
  const got = setJsoncValue(multi, ["dependencies", "testImplementation", "org.j:junit"], "5.0");
  assert.equal(
    got,
    multi.replace(
      '    "implementation": {\n      "org.slf4j:slf4j-api": "2.0.0"\n    }',
      '    "implementation": {\n      "org.slf4j:slf4j-api": "2.0.0"\n    },\n    "testImplementation": {\n      "org.j:junit": "5.0"\n    }',
    ),
  );
});

test("inserting into an empty multiline-file object grows it", () => {
  const text = `{
  "dependencies": {}
}
`;
  const got = setJsoncValue(text, ["dependencies", "implementation", "a:b"], "1.0");
  assert.equal(
    got,
    `{
  "dependencies": {
    "implementation": {
      "a:b": "1.0"
    }
  }
}
`,
  );
});

test("compact files stay compact", () => {
  const text = `{"dependencies":{"implementation":{"org.x:y":"1.0"}}}`;
  const got = setJsoncValue(text, ["dependencies", "implementation", "a:b"], "2.0");
  assert.equal(got, `{"dependencies":{"implementation":{"org.x:y":"1.0","a:b":"2.0"}}}`);
});

test("a trailing comma is respected when inserting", () => {
  const text = `{
  "implementation": {
    "org.x:y": "1.0",
  }
}
`;
  const got = setJsoncValue(text, ["implementation", "a:b"], "2.0");
  assert.equal(
    got,
    `{
  "implementation": {
    "org.x:y": "1.0",
    "a:b": "2.0"
  }
}
`,
  );
});

test("4-space indentation is preserved", () => {
  const text = `{
    "dependencies": {
        "implementation": {
            "org.x:y": "1.0"
        }
    }
}
`;
  const got = setJsoncValue(text, ["dependencies", "implementation", "a:b"], "2.0");
  assert.ok(got.includes(`            "org.x:y": "1.0",\n            "a:b": "2.0"`), got);
});

test("removing a middle member swallows its separator", () => {
  const text = `{
  "implementation": {
    "a:b": "1.0",
    "c:d": "2.0",
    "e:f": "3.0"
  }
}
`;
  const { text: got, removed } = removeJsoncKey(text, ["implementation", "c:d"]);
  assert.equal(removed, true);
  assert.equal(got, text.replace('"c:d": "2.0",\n    ', ""));
});

test("removing the last member swallows the preceding comma", () => {
  const text = `{
  "implementation": {
    "a:b": "1.0",
    "c:d": "2.0"
  }
}
`;
  const { text: got, removed } = removeJsoncKey(text, ["implementation", "c:d"]);
  assert.equal(removed, true);
  assert.equal(got, text.replace(',\n    "c:d": "2.0"', ""));
});

test("removing the only member leaves the (whitespace-only) object", () => {
  const text = `{
  "implementation": {
    "a:b": "1.0"
  }
}
`;
  const { text: got, removed } = removeJsoncKey(text, ["implementation", "a:b"]);
  assert.equal(removed, true);
  assert.equal(got, `{\n  "implementation": {\n  }\n}\n`);
});

test("removing an absent key or section is a no-op", () => {
  const text = `{"implementation":{"a:b":"1.0"}}`;
  assert.deepEqual(removeJsoncKey(text, ["implementation", "x:y"]), { text, removed: false });
  assert.deepEqual(removeJsoncKey(text, ["testImplementation", "a:b"]), { text, removed: false });
});

test("comments between members survive edits around them", () => {
  const text = `{
  "implementation": {
    // keep me
    "a:b": "1.0", // and me
    "c:d": "2.0"
  }
}
`;
  const got = setJsoncValue(text, ["implementation", "e:f"], "3.0");
  assert.ok(got.includes("// keep me"), got);
  assert.ok(got.includes("// and me"), got);
  assert.ok(got.includes(`"c:d": "2.0",\n    "e:f": "3.0"`), got);
});

test("hasJsoncKey reports existence", () => {
  assert.equal(hasJsoncKey(multi, ["dependencies", "implementation", "org.slf4j:slf4j-api"]), true);
  assert.equal(hasJsoncKey(multi, ["dependencies", "api", "org.slf4j:slf4j-api"]), false);
});

test("a non-object config file throws the shared error", () => {
  assert.throws(
    () => setJsoncValue("[1,2]", ["version"], "1"),
    /the config file does not contain an object/,
  );
});
