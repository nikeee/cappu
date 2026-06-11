import { test } from "node:test";

import { expect } from "expect";

import { dependencyLenses, findDependencyEntries } from "./dependencyLens.ts";

const CONFIG = `{
  // Project configuration.
  "$schema": "./cappu.schema.json",
  "compilerOptions": {
    "outDir": "./build",
  },
  "packageSources": ["https://repo.maven.apache.org/maven2"],
  "dependencies": {
    "api": {
      "org.lib:core": "1.0",
    },
    "implementation": {
      // "org.commented:out": "9.9",
      "com.google.code.gson:gson": "2.10.1",
    },
  },
}
`;

test("dependency entries are found by their colon-bearing keys only", () => {
  const entries = findDependencyEntries(CONFIG);
  expect(entries.map(e => `${e.groupId}:${e.artifactId}@${e.version}`)).toEqual([
    "org.lib:core@1.0",
    "com.google.code.gson:gson@2.10.1",
  ]);
  // not matched: $schema/outDir keys (no colon in the key), the packageSources
  // url (an array element, not a key), the commented-out entry
  const gson = entries[1]!;
  expect(gson.line).toBe(13);
  expect(CONFIG.split("\n")[gson.line]!.slice(gson.startCharacter, gson.endCharacter)).toBe(
    '"com.google.code.gson:gson": "2.10.1"',
  );
});

test("a lens appears exactly for entries with a different newest version", async () => {
  const lookup = (groupId: string, artifactId: string): Promise<string | undefined> =>
    Promise.resolve(
      `${groupId}:${artifactId}` === "com.google.code.gson:gson" ? "2.14.0" : undefined,
    );
  const lenses = await dependencyLenses(CONFIG, lookup);
  expect(lenses).toHaveLength(1); // org.lib:core is unknown to the source: no lens
  expect(lenses[0]!.title).toBe("newer version: 2.14.0");
  expect(lenses[0]!.entry.artifactId).toBe("gson");
});

test("an up-to-date entry gets no lens", async () => {
  const lenses = await dependencyLenses(CONFIG, () => Promise.resolve("1.0"));
  expect(lenses.map(l => l.entry.artifactId)).toEqual(["gson"]); // 2.10.1 != 1.0
});
