import { test } from "node:test";
import { expect } from "expect";
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { emitSourceFile } from "./emitter.ts";
import { parseSourceFile } from "./parser.ts";

function hasTool(name: string): boolean {
  try {
    execFileSync(name, ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}
const TOOLS = hasTool("javap") && hasTool("java");

function emitOne(source: string) {
  const sf = parseSourceFile("T.java", source);
  const classes = emitSourceFile(sf);
  expect(classes.length).toBe(1);
  return classes[0]!;
}

test("emits a well-formed class header (magic + Java 21 major version)", () => {
  const { bytes } = emitOne("class Empty {}");
  // 0xCAFEBABE, minor 0, major 65
  expect([...bytes.slice(0, 8)]).toEqual([0xca, 0xfe, 0xba, 0xbe, 0x00, 0x00, 0x00, 0x41]);
});

test(
  "javap reads the emitted class and sees the default constructor",
  { skip: TOOLS ? false : "no JDK toolchain" },
  () => {
    const { name, bytes } = emitOne("class Empty {}");
    const dir = mkdtempSync(join(tmpdir(), "javalsp-emit-"));
    writeFileSync(join(dir, `${name}.class`), bytes);
    const out = execFileSync("javap", ["-p", join(dir, `${name}.class`)], { encoding: "utf8" });
    expect(out).toContain("class Empty");
    expect(out).toContain("Empty();"); // the synthesized constructor
  },
);

test(
  "the JVM verifier accepts the emitted class (loads, then only main is missing)",
  { skip: TOOLS ? false : "no JDK toolchain" },
  () => {
    const { name, bytes } = emitOne("public class Hi {}");
    const dir = mkdtempSync(join(tmpdir(), "javalsp-emit-"));
    writeFileSync(join(dir, `${name}.class`), bytes);
    let stderr = "";
    try {
      execFileSync("java", ["-cp", dir, name], { encoding: "utf8", stdio: "pipe" });
    } catch (e) {
      stderr = String((e as { stderr?: string }).stderr ?? "");
    }
    // A ClassFormatError/VerifyError would mean malformed bytecode. "Main method
    // not found" means the class loaded and verified cleanly.
    expect(stderr).toContain("Main method not found");
    expect(stderr).not.toMatch(/ClassFormatError|VerifyError|Incompatible/);
  },
);
