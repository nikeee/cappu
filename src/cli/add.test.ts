import { test } from "node:test";

import { expect } from "expect";
import { parse } from "comment-json";

import { addDependencyToJsonc, parseAddCoordinate, resolveConfiguration } from "./add.ts";

test("configuration names and short aliases resolve to the canonical form", () => {
  expect(resolveConfiguration("implementation")).toBe("implementation");
  expect(resolveConfiguration("i")).toBe("implementation");
  expect(resolveConfiguration("a")).toBe("api");
  expect(resolveConfiguration("ap")).toBe("annotationProcessor");
  expect(resolveConfiguration("ti")).toBe("testImplementation");
  expect(resolveConfiguration("nope")).toBeUndefined();
  expect(resolveConfiguration(undefined)).toBeUndefined();
});

test("coordinates parse as Gradle-style group:artifact[:version]", () => {
  // a line copied straight from a build.gradle
  expect(parseAddCoordinate("com.google.code.gson:gson:2.14.0")).toEqual({
    key: "com.google.code.gson:gson",
    version: "2.14.0",
  });
  expect(parseAddCoordinate("org.example:thing")).toEqual({
    key: "org.example:thing",
    version: undefined,
  });
  // not that shape: bare name, empty segment, dangling version, classifier (4 parts)
  expect(parseAddCoordinate("gson")).toBeUndefined();
  expect(parseAddCoordinate(":gson:1")).toBeUndefined();
  expect(parseAddCoordinate("a:b:")).toBeUndefined();
  expect(parseAddCoordinate("a:b:c:d")).toBeUndefined();
});

test("a dependency can be added to any configuration section", () => {
  const out = addDependencyToJsonc(
    "{}\n",
    "testImplementation",
    "org.junit.jupiter:junit-jupiter",
    "5.12.2",
  );
  expect(
    (parse(out) as unknown as { dependencies: { testImplementation: Record<string, string> } })
      .dependencies.testImplementation,
  ).toEqual({ "org.junit.jupiter:junit-jupiter": "5.12.2" });
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
  const parsed = parse(out) as unknown as {
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
    (parse(out) as unknown as { dependencies: { api: Record<string, string> } }).dependencies.api,
  ).toEqual({ "org.x:y": "3" });
});

test("adding an existing key overwrites its version", () => {
  const once = addDependencyToJsonc("{}\n", "implementation", "org.x:y", "1");
  const twice = addDependencyToJsonc(once, "implementation", "org.x:y", "2");
  expect(
    (parse(twice) as unknown as { dependencies: { implementation: Record<string, string> } })
      .dependencies.implementation,
  ).toEqual({ "org.x:y": "2" });
});
