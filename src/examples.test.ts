// End-to-end over the committed example projects: install from Maven Central
// (lockfiles pin the versions), compile (annotation processors included), run
// the fat jar and compare stdout exactly. Needs a JDK and network access;
// skipped without javac like the other JDK-gated suites.

import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { once } from "node:events";
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

const here = import.meta.dirname;
const examplesDir = join(here, "..", "examples");
const tsx = join(here, "..", "node_modules", ".bin", "tsx");
const cli = join(here, "cli", "main.ts");

const HAS_JAVAC = (() => {
  try {
    execFileSync("javac", ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

// `java` from the same JDK as the `javac` that compiled (a PATH skew between
// the two would otherwise fail on class file versions).
function javaBin(): string {
  try {
    const javac = realpathSync(execFileSync("which", ["javac"], { encoding: "utf8" }).trim());
    return join(dirname(javac), "java");
  } catch {
    return "java";
  }
}

// CI's "experimental" matrix leg sets this to cover cappu's own compiler
// against real Maven Central dependencies.
const EXPERIMENTAL = process.env.CAPPU_EXAMPLES_EXPERIMENTAL === "1";

function runExample(name: string, command: string[] = ["compile"]): string {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  // the fat jar is named after the project directory: keep the example's name
  const work = join(root, name);
  try {
    // only the committed files; lib/dist/.cappu from local runs stay behind
    for (const entry of ["cappu.json", "cappu-lock.json", "src", ".gitignore"]) {
      cpSync(join(examplesDir, name, entry), join(work, entry), { recursive: true });
    }
    const env = { ...process.env, CAPPU_PACKAGE_STORE: store };
    execFileSync(tsx, [cli, "install"], { cwd: work, env, stdio: ["ignore", "ignore", "pipe"] });
    // The experimental compiler is enabled via cappu.json (no CLI flag); tolerate
    // degraded bodies so best-effort emission doesn't fail the build.
    if (EXPERIMENTAL && command[0] === "compile") {
      const cfgPath = join(work, "cappu.json");
      const cfg = JSON.parse(readFileSync(cfgPath, "utf8")) as {
        compilerOptions?: Record<string, unknown>;
      };
      cfg.compilerOptions = {
        ...cfg.compilerOptions,
        experimentalCompiler: { enabled: true, failOnDegrade: false },
      };
      writeFileSync(cfgPath, JSON.stringify(cfg, null, 2));
    }
    const output = execFileSync(tsx, [cli, ...command], {
      cwd: work,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    if (command[0] !== "compile") return output;
    return execFileSync(javaBin(), ["-jar", join(work, "dist", `${name}.jar`)], {
      encoding: "utf8",
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
}

test("examples/gson-app builds and runs", { skip: !HAS_JAVAC }, () => {
  expect(runExample("gson-app")).toBe('{"x":1,"y":2}\n');
});

// In experimental mode MapStruct's generated code would run through the
// best-effort emitter; degraded bodies must not flake CI, so this example is
// javac-mode only.
test(
  "examples/mapstruct-app builds and runs (annotation processor)",
  { skip: !HAS_JAVAC || EXPERIMENTAL },
  () => {
    expect(runExample("mapstruct-app")).toBe("Wartburg 353 / 50 hp\n");
  },
);

// Like mapstruct, the Immutables processor generates code that the experimental
// emitter would run through best-effort, so this example is javac-mode only.
test(
  "examples/immutables-app builds and runs (annotation processor)",
  { skip: !HAS_JAVAC || EXPERIMENTAL },
  () => {
    expect(runExample("immutables-app")).toBe("Ant has 6 legs\n");
  },
);

test("examples/junit-app runs its tests with cappu test", { skip: !HAS_JAVAC }, () => {
  const output = runExample("junit-app", ["test"]);
  expect(output).toContain("2 tests successful");
  expect(output).toContain("0 tests failed");
});

// audit needs network (Maven resolve + OSV), not a JDK; gated on HAS_JAVAC
// only so it runs in the same networked legs as the other example e2e tests
// and skips on the hermetic no-JDK leg. The findings exit non-zero, which
// execFileSync surfaces as a throw whose stdout we read.
test("examples/audit-app reports its vulnerable dependency", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  const work = join(root, "audit-app");
  try {
    cpSync(join(examplesDir, "audit-app", "cappu.json"), join(work, "cappu.json"));
    let stdout: string;
    let code = 0;
    try {
      stdout = execFileSync(tsx, [cli, "audit"], {
        cwd: work,
        env: { ...process.env, CAPPU_PACKAGE_STORE: store },
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (e) {
      stdout = (e as { stdout?: string }).stdout ?? "";
      code = (e as { status?: number }).status ?? 1;
    }
    expect(code).toBe(1); // findings -> non-zero exit
    // Log4Shell is a permanent advisory; OSV will always return it
    expect(stdout).toContain("CVE-2021-44228");
    expect(stdout).toContain("org.apache.logging.log4j:log4j-core:2.14.1");

    // --no-cache ignores the now-warm caches and still finds the same advisory
    let freshOut = "";
    let freshCode = 0;
    try {
      freshOut = execFileSync(tsx, [cli, "audit", "--no-cache"], {
        cwd: work,
        env: { ...process.env, CAPPU_PACKAGE_STORE: store },
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (e) {
      freshOut = (e as { stdout?: string }).stdout ?? "";
      freshCode = (e as { status?: number }).status ?? 1;
    }
    expect(freshCode).toBe(1);
    expect(freshOut).toContain("CVE-2021-44228");

    // --json emits machine-readable findings (still exit 1)
    let jsonOut = "";
    let jsonCode = 0;
    try {
      jsonOut = execFileSync(tsx, [cli, "audit", "--json"], {
        cwd: work,
        env: { ...process.env, CAPPU_PACKAGE_STORE: store },
        encoding: "utf8",
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (e) {
      jsonOut = (e as { stdout?: string }).stdout ?? "";
      jsonCode = (e as { status?: number }).status ?? 1;
    }
    expect(jsonCode).toBe(1);
    const report = JSON.parse(jsonOut) as {
      vulnerable: { coordinate: string; path: string[]; advisories: { aliases: string[] }[] }[];
    };
    const log4j = report.vulnerable.find(v =>
      v.coordinate.startsWith("org.apache.logging.log4j:log4j-core:"),
    );
    expect(log4j).toBeDefined();
    expect(log4j!.advisories.flatMap(a => a.aliases)).toContain("CVE-2021-44228");
    expect(log4j!.path.at(-1)).toBe(log4j!.coordinate); // path ends at the vulnerable pkg
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
});

// licenses resolves the graph (no JDK) and prints each dependency's license;
// gson declares Apache-2.0, which maps cleanly to an SPDX id. Networked-leg
// gated on HAS_JAVAC like the other example e2e tests.
test("examples/gson-app reports dependency licenses (human + --json)", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  const work = join(root, "gson-app");
  try {
    cpSync(join(examplesDir, "gson-app", "cappu.json"), join(work, "cappu.json"));
    const env = { ...process.env, CAPPU_PACKAGE_STORE: store };
    const human = execFileSync(tsx, [cli, "licenses"], {
      cwd: work,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    expect(human).toContain("com.google.code.gson:gson:2.13.1");
    expect(human).toContain("Apache-2.0");

    const json = execFileSync(tsx, [cli, "licenses", "--json"], {
      cwd: work,
      env,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });
    const rows = JSON.parse(json) as { coordinate: string; spdx: string[] }[];
    const gson = rows.find(r => r.coordinate === "com.google.code.gson:gson:2.13.1");
    expect(gson?.spdx).toContain("Apache-2.0");
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
});

// A throwaway project pinned to an old gson; `cappu update` should move it to
// a newer stable version, rewrite cappu.json (comment kept) and write a lock.
// Network-only (no JDK); gated on HAS_JAVAC like the other example e2e tests.
test("cappu update bumps an outdated dependency end to end", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  const work = join(root, "update-proj");
  try {
    mkdirSync(work, { recursive: true });
    writeFileSync(
      join(work, "cappu.json"),
      '{\n  "dependencies": {\n    "implementation": {\n' +
        "      // pinned old on purpose\n" +
        '      "com.google.code.gson:gson": "2.8.9"\n' +
        "    }\n  }\n}\n",
    );
    execFileSync(tsx, [cli, "update"], {
      cwd: work,
      env: { ...process.env, CAPPU_PACKAGE_STORE: store },
      stdio: ["ignore", "pipe", "pipe"],
    });
    const after = readFileSync(join(work, "cappu.json"), "utf8");
    expect(after).not.toContain("2.8.9"); // bumped away from the old pin
    expect(after).toContain("com.google.code.gson:gson");
    expect(after).toContain("// pinned old on purpose"); // comment preserved
    expect(existsSync(join(work, "cappu-lock.json"))).toBe(true); // lock refreshed
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
});

// src/main/resources is bundled into the build output and read at runtime; the
// emitter may degrade the resource-reading main, so the compile/run check is
// javac-only (EXPERIMENTAL skips it). cappu test always uses javac.
test(
  "examples/resources-app bundles main resources into the jar",
  {
    skip: !HAS_JAVAC || EXPERIMENTAL,
  },
  () => {
    expect(runExample("resources-app")).toBe("hello from main resources\n");
  },
);

test(
  "examples/resources-app reads main and test resources under cappu test",
  {
    skip: !HAS_JAVAC,
  },
  () => {
    const output = runExample("resources-app", ["test"]);
    expect(output).toContain("2 tests successful");
    expect(output).toContain("0 tests failed");
  },
);

// --artifact steers the output jar's name (predictable name for Docker builds).
// No dependencies, so javac-only (no network).
test("cappu compile --artifact steers the output jar name", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const work = join(root, "p");
  try {
    mkdirSync(join(work, "src", "main", "java", "x"), { recursive: true });
    writeFileSync(
      join(work, "cappu.json"),
      '{ "compilerOptions": { "mainClass": "x.M", "quiet": true } }',
    );
    writeFileSync(
      join(work, "src", "main", "java", "x", "M.java"),
      "package x; public class M { public static void main(String[] a) {} }",
    );
    execFileSync(tsx, [cli, "compile", "-o", "jar", "--artifact", "app"], {
      cwd: work,
      env: { ...process.env, CAPPU_PACKAGE_STORE: mkdtempSync(join(tmpdir(), "cappu-store-")) },
      stdio: ["ignore", "pipe", "pipe"],
    });
    expect(existsSync(join(work, "dist", "app.jar"))).toBe(true);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

// A minimal Spring Boot app: cappu resolves the whole starter tree and compiles
// it, then it runs from a single fat jar. A flat fat jar would normally break
// Spring Boot (auto-config descriptors live at the same META-INF path in many
// jars); cappu's `fat-jar` merges those same-path descriptors so one jar boots
// like the classpath of separate jars would. Networked + JDK-gated.
test(
  "examples/spring-boot-app boots Spring Boot from a single fat jar",
  {
    skip: !HAS_JAVAC,
  },
  () => {
    const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
    const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
    const work = join(root, "spring-boot-app");
    try {
      for (const entry of ["cappu.json", "cappu-lock.json", "src", ".gitignore"]) {
        cpSync(join(examplesDir, "spring-boot-app", entry), join(work, entry), { recursive: true });
      }
      const env = { ...process.env, CAPPU_PACKAGE_STORE: store };
      execFileSync(tsx, [cli, "install"], { cwd: work, env, stdio: ["ignore", "ignore", "pipe"] });
      // "output": "fat-jar" is set in the example's cappu.json
      execFileSync(tsx, [cli, "compile"], { cwd: work, env, stdio: ["ignore", "ignore", "pipe"] });
      const output = execFileSync(
        javaBin(),
        ["-jar", join(work, "dist", "spring-boot-app-1.0.0.jar")],
        { encoding: "utf8" },
      );
      expect(output).toContain("Spring Boot"); // the startup banner
      expect(output).toContain("Started App"); // the context booted
    } finally {
      rmSync(root, { recursive: true, force: true });
      rmSync(store, { recursive: true, force: true });
    }
  },
);

// A tiny DAP client: speaks the Content-Length-framed protocol to a spawned
// `cappu dap` over its stdio, correlating responses by request_seq and letting
// the test await named events.
function dapFrame(msg: unknown): string {
  const body = JSON.stringify(msg);
  return `Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`;
}

class DapDriver {
  private buf: Buffer = Buffer.alloc(0);
  private seq = 1;
  private readonly responses = new Map<number, (m: any) => void>();
  private readonly events: any[] = [];
  private readonly eventWaiters: { event: string; resolve: (m: any) => void }[] = [];
  /** All `output` event text, in order, so tests can assert program stdout. */
  outputText = "";

  constructor(private readonly child: ChildProcess) {
    child.stdout!.on("data", (c: Buffer) => this.onData(c));
  }

  private onData(chunk: Buffer): void {
    this.buf = Buffer.concat([this.buf, chunk]);
    for (;;) {
      const sep = this.buf.indexOf("\r\n\r\n");
      if (sep < 0) break;
      const len = Number(/Content-Length:\s*(\d+)/i.exec(this.buf.toString("ascii", 0, sep))![1]);
      if (this.buf.length < sep + 4 + len) break;
      const msg = JSON.parse(this.buf.toString("utf8", sep + 4, sep + 4 + len));
      this.buf = this.buf.subarray(sep + 4 + len);
      if (msg.type === "response") {
        this.responses.get(msg.request_seq)?.(msg);
        this.responses.delete(msg.request_seq);
      } else if (msg.type === "event") {
        if (msg.event === "output") this.outputText += msg.body.output;
        const i = this.eventWaiters.findIndex(w => w.event === msg.event);
        if (i >= 0) this.eventWaiters.splice(i, 1)[0].resolve(msg);
        else this.events.push(msg);
      }
    }
  }

  request(command: string, args?: unknown): Promise<any> {
    const seq = this.seq++;
    return new Promise(resolve => {
      this.responses.set(seq, resolve);
      this.child.stdin!.write(dapFrame({ seq, type: "request", command, arguments: args }));
    });
  }

  waitEvent(event: string): Promise<any> {
    const i = this.events.findIndex(e => e.event === event);
    if (i >= 0) return Promise.resolve(this.events.splice(i, 1)[0]);
    return new Promise(resolve => this.eventWaiters.push({ event, resolve }));
  }
}

function withTimeout<T>(p: Promise<T>, ms: number, label: string): Promise<T> {
  let timer: NodeJS.Timeout;
  const guard = new Promise<T>((_, reject) => {
    timer = setTimeout(() => reject(new Error(`timeout: ${label}`)), ms);
  });
  return Promise.race([p, guard]).finally(() => clearTimeout(timer));
}

// End-to-end debugger: spawn `cappu dap` and drive a full session over DAP -
// threads, a breakpoint hit on each loop iteration with the expected local
// values, stepping, then continue-to-completion with program output and
// termination. No network (debug-app has no dependencies), so this runs on the
// hermetic JDK leg too.
test(
  "examples/debug-app debugs over the Debug Adapter Protocol",
  { skip: !HAS_JAVAC },
  async () => {
    const root = mkdtempSync(join(tmpdir(), "cappu-dap-"));
    const work = join(root, "debug-app");
    for (const entry of ["cappu.json", "src", ".gitignore"]) {
      cpSync(join(examplesDir, "debug-app", entry), join(work, entry), { recursive: true });
    }
    const appJava = join(work, "src", "main", "java", "example", "App.java");
    const child = spawn(tsx, [cli, "dap"], {
      cwd: work,
      env: { ...process.env },
      stdio: ["pipe", "pipe", "pipe"],
    });
    const dap = new DapDriver(child);
    const t = (label: string, p: Promise<any>) => withTimeout(p, 60_000, label);

    // Read the Locals of the top frame of `threadId` as a name->value map.
    async function locals(threadId: number): Promise<Record<string, string>> {
      const stack = await t("stackTrace", dap.request("stackTrace", { threadId }));
      const top = stack.body.stackFrames[0];
      expect(top.line).toBe(8);
      expect(top.name).toBe("example.App.main"); // class.method
      const scopes = await t("scopes", dap.request("scopes", { frameId: top.id }));
      const ref = scopes.body.scopes[0].variablesReference;
      const vars = await t("variables", dap.request("variables", { variablesReference: ref }));
      return Object.fromEntries(vars.body.variables.map((v: any) => [v.name, v.value]));
    }

    try {
      expect(
        (await t("initialize", dap.request("initialize", { adapterID: "cappu" }))).success,
      ).toBe(true);
      await t("initialized", dap.waitEvent("initialized"));
      expect((await t("launch", dap.request("launch", {}))).success).toBe(true);
      await t(
        "setBreakpoints",
        dap.request("setBreakpoints", {
          source: { path: appJava },
          breakpoints: [{ line: 8 }], // `sum += squared;`
        }),
      );
      await t("configurationDone", dap.request("configurationDone"));

      // Breakpoint on line 8 (`sum += squared;`) is hit once per loop iteration,
      // BEFORE the add runs: (i, squared, sum) = (1,1,0), (2,4,1), (3,9,5).
      const expected = [
        { i: "1", squared: "1", sum: "0" },
        { i: "2", squared: "4", sum: "1" },
        { i: "3", squared: "9", sum: "5" },
      ];
      let threadId = -1;
      for (let hit = 0; hit < expected.length; hit++) {
        const stopped = await t(`stopped#${hit}`, dap.waitEvent("stopped"));
        expect(stopped.body.reason).toBe("breakpoint");
        threadId = stopped.body.threadId;

        if (hit === 0) {
          // The running program has a thread named "main".
          const threads = await t("threads", dap.request("threads", {}));
          expect(threads.body.threads.some((th: any) => th.name === "main")).toBe(true);
        }

        const byName = await locals(threadId);
        expect(byName.i).toBe(expected[hit].i);
        expect(byName.squared).toBe(expected[hit].squared);
        expect(byName.sum).toBe(expected[hit].sum);
        // The main(String[] args) parameter is in scope; an object local renders
        // as its type (the trailing @<id> is not stable, so only check the type).
        if (hit === 0) expect(byName.args).toMatch(/^java\.lang\.String\[\]@/);

        if (hit < expected.length - 1) {
          await t(`continue#${hit}`, dap.request("continue", { threadId }));
        }
      }

      // From the last hit, step over one line and confirm the step pipeline
      // produces another stop, then run to completion.
      await t("next", dap.request("next", { threadId }));
      const stepped = await t("stepped", dap.waitEvent("stopped"));
      expect(stepped.body.reason).toBe("step");

      await t("continue-final", dap.request("continue", { threadId }));
      await t("terminated", dap.waitEvent("terminated"));
      // sum = 1 + 4 + 9 = 14, printed by the program before it exits.
      expect(dap.outputText).toContain("sum=14");

      await dap.request("disconnect");
      child.stdin!.end();
      await t("exit", once(child, "exit"));
    } finally {
      child.kill();
      rmSync(root, { recursive: true, force: true });
    }
  },
);

// stopOnEntry + vmArgs: launch with no breakpoints; the debugger stops on the
// first line of main, then runs to completion. The -D vm arg just proves the
// JVM accepts caller JVM flags (the program still runs).
test("examples/debug-app stops on entry and accepts vm args", { skip: !HAS_JAVAC }, async () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-dap-"));
  const work = join(root, "debug-app");
  for (const entry of ["cappu.json", "src", ".gitignore"]) {
    cpSync(join(examplesDir, "debug-app", entry), join(work, entry), { recursive: true });
  }
  const child = spawn(tsx, [cli, "dap"], { cwd: work, env: { ...process.env }, stdio: "pipe" });
  const dap = new DapDriver(child);
  const t = (label: string, p: Promise<any>) => withTimeout(p, 60_000, label);
  try {
    await t("initialize", dap.request("initialize", { adapterID: "cappu" }));
    await t("initialized", dap.waitEvent("initialized"));
    expect(
      (await t("launch", dap.request("launch", { stopOnEntry: true, vmArgs: ["-Dcappu.dap=on"] })))
        .success,
    ).toBe(true);
    await t("configurationDone", dap.request("configurationDone"));

    const stopped = await t("entry", dap.waitEvent("stopped"));
    expect(stopped.body.reason).toBe("entry");
    const threadId = stopped.body.threadId;
    const stack = await t("stackTrace", dap.request("stackTrace", { threadId }));
    expect(stack.body.stackFrames[0].name).toBe("example.App.main");
    expect(stack.body.stackFrames[0].line).toBe(5); // `int sum = 0;` - main's first line

    await t("continue", dap.request("continue", { threadId }));
    await t("terminated", dap.waitEvent("terminated"));
    expect(dap.outputText).toContain("sum=14");

    await dap.request("disconnect");
    child.stdin!.end();
    await t("exit", once(child, "exit"));
  } finally {
    child.kill();
    rmSync(root, { recursive: true, force: true });
  }
});

// With full coordinates, `cappu compile -o jar` produces the publishable pair:
// <artifactId>-<version>.jar plus its generated POM. Javac-gated like the rest.
test("cappu compile -o jar emits a publishable jar and POM", { skip: !HAS_JAVAC }, () => {
  const root = mkdtempSync(join(tmpdir(), "cappu-example-"));
  const store = mkdtempSync(join(tmpdir(), "cappu-example-store-"));
  const work = join(root, "pub-proj");
  try {
    mkdirSync(join(work, "src", "main", "java", "com", "example"), { recursive: true });
    writeFileSync(
      join(work, "cappu.json"),
      JSON.stringify({
        groupId: "com.example",
        artifactId: "demo-lib",
        version: "1.0.0",
        license: "MIT",
        dependencies: { implementation: { "com.google.code.gson:gson": "2.13.1" } },
      }),
    );
    writeFileSync(
      join(work, "src", "main", "java", "com", "example", "Hello.java"),
      "package com.example; public class Hello {}",
    );
    execFileSync(tsx, [cli, "compile", "-o", "jar"], {
      cwd: work,
      env: { ...process.env, CAPPU_PACKAGE_STORE: store },
      stdio: ["ignore", "pipe", "pipe"],
    });
    expect(existsSync(join(work, "dist", "demo-lib-1.0.0.jar"))).toBe(true);
    const pom = readFileSync(join(work, "dist", "demo-lib-1.0.0.pom"), "utf8");
    expect(pom).toContain("<artifactId>demo-lib</artifactId>");
    expect(pom).toContain("<version>1.0.0</version>");
    expect(pom).toMatch(/<artifactId>gson<\/artifactId>[\s\S]*?<scope>runtime<\/scope>/);
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(store, { recursive: true, force: true });
  }
});
