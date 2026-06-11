// Emit-robustness tests over real-world Java projects checked out as git
// submodules under test-fixtures/emitter/corpus/. Every submodule directory is
// auto-discovered, so adding a project is just
// `git submodule add <url> test-fixtures/emitter/corpus/<name>` - no change here.
// For every source file the emitter must produce class bytes without throwing;
// anything it cannot compile degrades to a verifiable placeholder, never a
// crash. Skipped when no submodule is checked out, so CI without them still
// passes:
//
//   git submodule update --init

import { execFileSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { emitSourceFile } from "./emitter.ts";
import { type Disasm, disasmFiles } from "./javapNormalize.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createProgram } from "./program.ts";
import { pathToUri } from "../workspace.ts";

const here = dirname(fileURLToPath(import.meta.url));
const corpusRoot = join(here, "..", "..", "test-fixtures", "emitter", "corpus");
const baselineDir = join(here, "..", "..", "test-fixtures", "emitter", "corpus-baselines");
const shouldUpdate = process.env.UPDATE_BASELINES === "1";

function hasTool(name: string): boolean {
  try {
    execFileSync(name, ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}
const HAS_JAVA = hasTool("java") && hasTool("javap");
const HAS_JAVAC = hasTool("javac");

function findJavaFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    if (entry === ".git") continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) out.push(...findJavaFiles(full));
    else if (entry.endsWith(".java")) out.push(full);
  }
  return out;
}

// Each checked-out submodule (a directory under test-fixtures/emitter/corpus/ holding .java files).
const projects: { name: string; files: string[] }[] = existsSync(corpusRoot)
  ? readdirSync(corpusRoot)
      .map(name => join(corpusRoot, name))
      .filter(p => statSync(p).isDirectory())
      .map(p => ({ name: p.split("/").pop()!, files: findJavaFiles(p) }))
      .filter(p => p.files.length > 0)
  : [];

if (projects.length === 0) {
  test("corpus submodules emit without crashing", { skip: "no submodule checked out" }, () => {});
}

for (const project of projects) {
  test(`corpus: ${project.name} emits without crashing`, () => {
    // One program over all of the project's sources (+ the stub) so its own
    // cross-file types resolve; JDK types outside the stub degrade gracefully.
    const program = createProgram();
    loadJdkStub(program);
    const uris = project.files.map(f => {
      const uri = pathToUri(f);
      program.addProjectFile(uri, readFileSync(f, "utf8"));
      return uri;
    });
    const checker = createChecker(program);

    let emittedClasses = 0;
    const failures: string[] = [];
    for (const uri of uris) {
      try {
        emittedClasses += emitSourceFile(program.getSourceFile(uri)!, program, checker).length;
      } catch (e) {
        failures.push(`${uri.split("/").pop()}: ${(e as Error).message || (e as Error).stack}`);
      }
    }
    expect(failures).toEqual([]); // emission must never throw
    expect(emittedClasses).toBeGreaterThan(0);
  });
}

// --- Bytecode-equivalence tier --------------------------------------------
// Beyond "no crash", verify our emitted bytecode against javac for the corpus.
// javac cannot build these projects (external deps), and we degrade methods that
// use unstubbed types, so the baseline records only the (class, method) pairs we
// CURRENTLY match javac on (normalized javap -c), regenerated with
// UPDATE_BASELINES. The test is then a regression guard over that verified set,
// which grows as our codegen improves.

type ClassCode = [string, string[]][]; // [methodSignature, instructionLines]
const sameCode = (a: string[], b: string[]): boolean =>
  a.length === b.length && a.every((x, i) => x === b[i]);

function classFilesIn(dir: string): string[] {
  const out: string[] = [];
  for (const e of readdirSync(dir)) {
    const f = join(dir, e);
    if (statSync(f).isDirectory()) out.push(...classFilesIn(f));
    else if (e.endsWith(".class")) out.push(f);
  }
  return out;
}

// Emit every class of a project (one program, like the no-crash test), keyed by
// the dotted class name javap prints (our internal name has '/').
function emitProjectBytes(project: { files: string[] }): Map<string, Uint8Array> {
  const program = createProgram();
  loadJdkStub(program);
  const uris = project.files.map(f => {
    const uri = pathToUri(f);
    program.addProjectFile(uri, readFileSync(f, "utf8"));
    return uri;
  });
  const checker = createChecker(program);
  const out = new Map<string, Uint8Array>();
  for (const uri of uris) {
    try {
      for (const c of emitSourceFile(program.getSourceFile(uri)!, program, checker)) {
        out.set(c.name.replaceAll("/", "."), c.bytes);
      }
    } catch {
      // the no-crash test already asserts emission does not throw
    }
  }
  return out;
}

// Disassemble just the named classes from a bytes map (one javap invocation),
// so large projects do not javap hundreds of irrelevant classes.
function disasmSelected(bytes: Map<string, Uint8Array>, wanted: string[]): Map<string, Disasm> {
  const dir = mkdtempSync(join(tmpdir(), "corpus-ours-"));
  const paths: string[] = [];
  let i = 0;
  for (const cn of wanted) {
    const b = bytes.get(cn);
    if (b) {
      const p = join(dir, `c${i++}.class`); // javap reads the class name from the bytes
      writeFileSync(p, b);
      paths.push(p);
    }
  }
  return paths.length > 0 ? disasmFiles(paths) : new Map();
}

// A file is eligible for a javac reference only if all its imports are JDK
// (java./javax.); otherwise javac cannot compile it without external deps.
function importsOnlyJdk(text: string): boolean {
  const imports = text.match(/^\s*import\s+(?:static\s+)?[\w.]+\s*;/gm) ?? [];
  return imports.every(i => /import\s+(?:static\s+)?(?:java|javax)\./.test(i));
}
// The source root of a file (its path with the package directories stripped),
// for javac -sourcepath so same-project references resolve.
function sourceRoot(file: string, text: string): string {
  const pkg = text.match(/^\s*package\s+([\w.]+)\s*;/m)?.[1];
  const dir = dirname(file);
  if (!pkg) return dir;
  const suffix = `/${pkg.split(".").join("/")}`;
  return dir.endsWith(suffix) ? dir.slice(0, -suffix.length) : dir;
}

// Regenerate a project's baseline: javac each JDK-only file, and keep the
// per-method instruction streams where our emitted code already matches javac.
function generateBaseline(project: { files: string[] }): Map<string, ClassCode> {
  const bytes = emitProjectBytes(project);
  const roots = new Set<string>();
  const eligible: string[] = [];
  for (const f of project.files) {
    const text = readFileSync(f, "utf8");
    if (importsOnlyJdk(text)) {
      eligible.push(f);
      roots.add(sourceRoot(f, text));
    }
  }
  const sourcepath = [...roots].join(delimiter);
  // Collect javac's disassembly for every class it can produce from the JDK-only
  // files (the class + any same-project deps pulled in via -sourcepath).
  const javacByClass = new Map<string, Disasm>();
  for (const f of eligible) {
    const dir = mkdtempSync(join(tmpdir(), "corpus-ref-"));
    try {
      execFileSync("javac", ["--release", "21", "-d", dir, "-sourcepath", sourcepath, f], {
        stdio: "ignore",
      });
    } catch {
      continue; // needs deps beyond the JDK
    }
    const produced = classFilesIn(dir);
    if (produced.length === 0) continue;
    for (const [cn, jc] of disasmFiles(produced))
      if (!javacByClass.has(cn)) javacByClass.set(cn, jc);
  }
  // Keep the per-method instruction streams where our emitted code matches javac.
  const ours = disasmSelected(
    bytes,
    [...javacByClass.keys()].filter(cn => bytes.has(cn)),
  );
  const ref = new Map<string, ClassCode>();
  for (const [cn, jc] of javacByClass) {
    const ourCode = new Map(ours.get(cn)?.code ?? []);
    const kept = jc.code.filter(([sig, instrs]) => {
      const o = ourCode.get(sig);
      return o !== undefined && sameCode(o, instrs);
    });
    if (kept.length > 0) ref.set(cn, kept);
  }
  return ref;
}

for (const project of projects) {
  const file = join(baselineDir, `${project.name}.json`);
  const canGenerate = shouldUpdate && HAS_JAVAC && HAS_JAVA;
  if (!existsSync(file) && !canGenerate) continue; // nothing to compare, cannot generate
  test(
    `corpus bytecode matches javac: ${project.name}`,
    { skip: HAS_JAVA ? false : "no JDK" },
    () => {
      let ref: Map<string, ClassCode>;
      if (canGenerate) {
        ref = generateBaseline(project);
        mkdirSync(baselineDir, { recursive: true });
        writeFileSync(file, `${JSON.stringify(Object.fromEntries(ref), null, 2)}\n`);
      } else {
        ref = new Map(
          Object.entries(JSON.parse(readFileSync(file, "utf8")) as Record<string, ClassCode>),
        );
      }
      if (ref.size === 0) return;
      const ours = disasmSelected(emitProjectBytes(project), [...ref.keys()]);
      let matched = 0;
      const divergences: string[] = [];
      for (const [cn, code] of ref) {
        const ourCode = new Map(ours.get(cn)?.code ?? []);
        for (const [sig, instrs] of code) {
          const o = ourCode.get(sig);
          if (o === undefined) divergences.push(`${cn} ${sig}: not emitted`);
          else if (sameCode(o, instrs)) matched++;
          else divergences.push(`${cn} ${sig}: ours=[${o.join(" ")}] javac=[${instrs.join(" ")}]`);
        }
      }
      expect(divergences).toEqual([]); // a baselined method must still match javac
      expect(matched).toBeGreaterThan(0);
    },
  );
}
