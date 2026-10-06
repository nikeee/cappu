import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import { BASH_COMPLETION, ZSH_COMPLETION } from "./completion.ts";

const here = import.meta.dirname;
const tsx = join(here, "..", "..", "node_modules", ".bin", "tsx");
const cli = join(here, "main.ts");

// The Go build embeds copies so `cappu completion` prints byte-identical
// scripts on both builds. Regenerate with `node --run completion:write`.
test("the checked-in Go completion scripts match the TS ones", () => {
  const dir = join(here, "..", "..", "togo", "internal", "cli", "completion");
  assert.equal(readFileSync(join(dir, "cappu.bash"), "utf8"), BASH_COMPLETION);
  assert.equal(readFileSync(join(dir, "cappu.zsh"), "utf8"), ZSH_COMPLETION);
});

test("cappu completion prints the script, or exits 2 on a missing/unknown shell", () => {
  const run = (...args: string[]) =>
    spawnSync(tsx, [cli, "completion", ...args], { encoding: "utf8" });

  const bash = run("bash");
  assert.equal(bash.status, 0);
  assert.equal(bash.stdout, BASH_COMPLETION);

  const zsh = run("zsh");
  assert.equal(zsh.status, 0);
  assert.equal(zsh.stdout, ZSH_COMPLETION);

  const missing = run();
  assert.equal(missing.status, 2);
  assert.equal(missing.stderr, "cappu: completion needs a shell: bash or zsh\n");

  const unknown = run("fish");
  assert.equal(unknown.status, 2);
  assert.equal(unknown.stderr, "cappu: unknown shell 'fish' (expected: bash, zsh)\n");
});

// Drive the bash function like readline would and check the candidates.
test(
  "the bash script completes commands, flags and fixed values",
  { skip: process.platform === "win32" },
  () => {
    const complete = (line: string): string[] => {
      const script = `${BASH_COMPLETION}
COMP_WORDS=(${line}); COMP_CWORD=$((\${#COMP_WORDS[@]} - 1))
[[ "${line}" == *" " ]] && { COMP_WORDS+=(""); COMP_CWORD=$((COMP_CWORD + 1)); }
_cappu; printf '%s\\n' "\${COMPREPLY[@]}"`;
      const r = spawnSync("bash", ["-c", script], { encoding: "utf8" });
      assert.equal(r.status, 0, r.stderr);
      return r.stdout.split("\n").filter(Boolean);
    };
    assert.deepEqual(complete("cappu ins"), ["install"]);
    assert.deepEqual(complete("cappu add "), [
      "api",
      "implementation",
      "annotationProcessor",
      "testImplementation",
    ]);
    assert.deepEqual(complete("cappu add api "), []);
    assert.deepEqual(complete("cappu compile -o "), ["classes", "jar", "fat-jar"]);
    assert.deepEqual(complete("cappu audit --f"), ["--format"]);
    assert.deepEqual(complete("cappu -c x.json cache "), ["clean", "verify"]);
    assert.deepEqual(complete("cappu completion z"), ["zsh"]);
  },
);
