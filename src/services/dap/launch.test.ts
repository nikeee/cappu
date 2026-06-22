import assert from "node:assert/strict";
import { test } from "node:test";

import { debuggeeJavaArgs, jdwpAgentArg } from "./launch.ts";

test("debuggeeJavaArgs orders agent, vm args, classpath, main class, program args", () => {
  const args = debuggeeJavaArgs("/cp", "example.App", {
    vmArgs: ["-Xmx64m", "-Dk=v"],
    programArgs: ["a", "b"],
  });
  assert.deepEqual(args, [
    jdwpAgentArg(),
    "-Xmx64m",
    "-Dk=v",
    "-cp",
    "/cp",
    "example.App",
    "a",
    "b",
  ]);
});

test("debuggeeJavaArgs omits empty vm/program args but keeps -cp and main class", () => {
  assert.deepEqual(debuggeeJavaArgs("/cp", "M"), [jdwpAgentArg(), "-cp", "/cp", "M"]);
});

test("vm args precede the main class so the JVM (not the program) receives them", () => {
  const args = debuggeeJavaArgs("/cp", "M", { vmArgs: ["-ea"], programArgs: ["-ea"] });
  // Both literally "-ea": the first (JVM flag) is before "M", the program one after.
  assert.ok(args.indexOf("-ea") < args.indexOf("M"));
  assert.ok(args.lastIndexOf("-ea") > args.indexOf("M"));
});
