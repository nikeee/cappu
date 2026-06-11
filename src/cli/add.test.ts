import { test } from "node:test";

import { expect } from "expect";
import { parse } from "comment-json";

import { addDependencyToJsonc, parseAddCoordinate } from "./add.ts";

test("coordinates parse as group:artifact with an optional @version", () => {
  expect(parseAddCoordinate("com.google.code.gson:gson@2.14.0")).toEqual({
    key: "com.google.code.gson:gson",
    version: "2.14.0",
  });
  expect(parseAddCoordinate("org.example:thing")).toEqual({
    key: "org.example:thing",
    version: undefined,
  });
  // not that shape: missing artifact, empty segment, dangling @, extra segment
  expect(parseAddCoordinate("gson")).toBeUndefined();
  expect(parseAddCoordinate(":gson@1")).toBeUndefined();
  expect(parseAddCoordinate("a:b@")).toBeUndefined();
  expect(parseAddCoordinate("a:b:c@1")).toBeUndefined();
});

test("adding a dependency preserves the JSONC comments around it", () => {
  const text = `{
  // keep me
  "compilerOptions": {
    "outDir": "./out", // and me
  },
  "dependencies": {
    "implementation": {
      "org.kept:kept": "1.0",
    },
  },
}
`;
  const out = addDependencyToJsonc(text, "implementation", "com.google.code.gson:gson", "2.14.0");
  expect(out).toContain("// keep me");
  expect(out).toContain("// and me");
  const parsed = parse(out) as {
    dependencies: { implementation: Record<string, string> };
    compilerOptions: { outDir: string };
  };
  expect(parsed.compilerOptions.outDir).toBe("./out");
  expect(parsed.dependencies.implementation).toEqual({
    "org.kept:kept": "1.0",
    "com.google.code.gson:gson": "2.14.0",
  });
});

test("a missing dependencies section (or configuration) is created", () => {
  const out = addDependencyToJsonc("{}\n", "api", "org.x:y", "3");
  expect(
    (parse(out) as { dependencies: { api: Record<string, string> } }).dependencies.api,
  ).toEqual({ "org.x:y": "3" });
});

test("adding an existing key overwrites its version", () => {
  const once = addDependencyToJsonc("{}\n", "implementation", "org.x:y", "1");
  const twice = addDependencyToJsonc(once, "implementation", "org.x:y", "2");
  expect(
    (parse(twice) as { dependencies: { implementation: Record<string, string> } }).dependencies
      .implementation,
  ).toEqual({ "org.x:y": "2" });
});
