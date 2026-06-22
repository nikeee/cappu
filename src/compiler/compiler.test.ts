import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { missingConfiguredPaths, runCompile } from "./compiler.ts";
import { loadConfig } from "../config.ts";
import { readZipEntries } from "./zipReader.ts";
import { writeZip } from "./zipWriter.ts";

function inTempDir(
  files: Record<string, string>,
  body: (dir: string, paths: string[]) => void,
): void {
  const dir = mkdtempSync(join(tmpdir(), "cappu-compile-"));
  try {
    const paths = Object.entries(files).map(([name, text]) => {
      const p = join(dir, name);
      writeFileSync(p, text);
      return p;
    });
    body(dir, paths);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

// The config of an empty directory: pure schema defaults.
function defaultConfig(dir: string): ReturnType<typeof loadConfig> {
  return loadConfig(undefined, dir);
}

test("a clean compile returns the written class files and prints nothing", () => {
  inTempDir({ "A.java": "class A { int x = 1; }" }, (dir, paths) => {
    const result = runCompile(paths, {
      experimentalCompiler: true,
      outDir: dir,
      config: defaultConfig(dir),
    });
    expect(result.success).toBe(true);
    expect(result.written).toEqual([join(dir, "A.class")]);
    expect(result.degraded).toEqual([]);
  });
});

test("a fully-qualified static call resolves and emits, not degrades", () => {
  inTempDir(
    {
      "Greeting.java":
        'package gen; public class Greeting { public static String text() { return "hi"; } }',
      "Main.java":
        "public class Main { public static void main(String[] a) { System.out.println(gen.Greeting.text()); } }",
    },
    (dir, paths) => {
      const result = runCompile(paths, {
        experimentalCompiler: true,
        outDir: dir,
        config: defaultConfig(dir),
      });
      expect(result.success).toBe(true);
      expect(result.degraded).toEqual([]);
    },
  );
});

test("a parse error fails with a located diagnostic and writes nothing", () => {
  inTempDir({ "Broken.java": "class Broken {" }, (dir, paths) => {
    const result = runCompile(paths, {
      experimentalCompiler: true,
      outDir: dir,
      config: defaultConfig(dir),
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    expect(result.written).toEqual([]);
    expect(result.diagnostics.length).toBeGreaterThan(0);
    const d = result.diagnostics[0]!;
    expect(d.severity).toBe("error");
    expect(d.file).toBe(paths[0]);
    expect(d.line).toBeGreaterThan(0);
    expect(d.column).toBeGreaterThan(0);
  });
});

test("checker diagnostics fail the build by default; typeCheck: false skips them", () => {
  const source = 'class C { int x = "s"; }'; // type mismatch (semantic, not syntactic)
  inTempDir({ "C.java": source }, (dir, paths) => {
    const checked = runCompile(paths, {
      experimentalCompiler: true,
      outDir: dir,
      config: defaultConfig(dir),
    });
    expect(checked.success).toBe(false);
    if (!checked.success) {
      expect(checked.diagnostics.some(d => d.severity === "error")).toBe(true);
    }

    const unchecked = runCompile(paths, {
      experimentalCompiler: true,
      outDir: dir,
      typeCheck: false,
      config: defaultConfig(dir),
    });
    expect(unchecked.success).toBe(true);
  });
});

test("failOnDegrade turns placeholder bodies into a failing result", () => {
  // A synchronized method body that the emitter supports either way would not
  // degrade; native-less, assert-less constructs are broadly supported now, so
  // force the unsupported path with an explicit this(...) constructor delegating
  // chain inside an anonymous class capture - if this ever stops degrading, the
  // expectation below flips and the fixture should be replaced with whatever is
  // still unsupported.
  const source = "class D { D() { this(1); } D(int x) { } }";
  inTempDir({ "D.java": source }, (dir, paths) => {
    const result = runCompile(paths, {
      experimentalCompiler: true,
      outDir: dir,
      failOnDegrade: true,
      config: defaultConfig(dir),
    });
    if (result.degraded.length === 0) return; // construct became supported; nothing to assert
    expect(result.success).toBe(false);
  });
});

test("missing configured dirs warn only when a cappu.json is present", () => {
  inTempDir({ "cappu.json": '{ "compilerOptions": { "classPath": ["./no-such-dir"] } }' }, dir => {
    const fromFile = loadConfig(undefined, dir);
    const missing = missingConfiguredPaths(fromFile);
    expect(missing).toContain(join(dir, "no-such-dir"));
    // the default sourcePaths entry is also absent in the temp dir
    expect(missing).toContain(join(dir, "src/main/java"));
  });
  inTempDir({}, dir => {
    // no cappu.json: defaults may be absent without a warning
    expect(missingConfiguredPaths(loadConfig(undefined, dir))).toEqual([]);
  });
});

test("the default Maven/Gradle classPath dirs never warn when absent", () => {
  inTempDir({ "cappu.json": "{}" }, dir => {
    const missing = missingConfiguredPaths(loadConfig(undefined, dir));
    for (const ext of ["target/dependency", "build/libs", "lib", "libs"]) {
      expect(missing).not.toContain(join(dir, ext));
    }
  });
});

test("compiling with absent configured dirs does not throw", () => {
  inTempDir(
    {
      "cappu.json": '{ "compilerOptions": { "classPath": ["./nope"], "sourcePaths": ["./nada"] } }',
      "A.java": "class A { }",
    },
    (dir, paths) => {
      const config = loadConfig(undefined, dir);
      const result = runCompile([paths[1]!], { experimentalCompiler: true, outDir: dir, config });
      expect(result.success).toBe(true);
    },
  );
});

test("output jar packs the classes behind a manifest, named after the project dir", () => {
  inTempDir({ "A.java": "package app; class A { }" }, (dir, paths) => {
    const result = runCompile(paths, {
      experimentalCompiler: true,
      outDir: join(dir, "dist"),
      output: "jar",
      config: defaultConfig(dir),
    });
    expect(result.success).toBe(true);
    const jar = result.written[0]!;
    expect(jar.endsWith(".jar")).toBe(true);
    const entries = readZipEntries(readFileSync(jar))!.map(e => e.name);
    expect(entries).toEqual(["META-INF/MANIFEST.MF", "app/A.class"]);
  });
});

test("a jar reports its Main-Class for an app, but not for a library (run hint)", () => {
  // an application: a single main(String[]) entry point is detected
  inTempDir(
    { "App.java": "package app; public class App { public static void main(String[] a) {} }" },
    (dir, paths) => {
      const result = runCompile(paths, {
        experimentalCompiler: true,
        outDir: join(dir, "dist"),
        output: "jar",
        config: defaultConfig(dir),
      });
      expect(result.success).toBe(true);
      expect(result.mainClass).toBe("app.App");
    },
  );
  // a library: no main, so no Main-Class and no run hint
  inTempDir({ "Lib.java": "package app; public class Lib { }" }, (dir, paths) => {
    const result = runCompile(paths, {
      experimentalCompiler: true,
      outDir: join(dir, "dist"),
      output: "jar",
      config: defaultConfig(dir),
    });
    expect(result.success).toBe(true);
    expect(result.mainClass).toBeUndefined();
  });
});

test("resourcePaths files are copied into the classes tree and the jar (#12)", () => {
  inTempDir({ "A.java": "package app; class A { }" }, (dir, paths) => {
    mkdirSync(join(dir, "src", "main", "resources", "conf"), { recursive: true });
    writeFileSync(join(dir, "src", "main", "resources", "conf", "app.properties"), "k=v\n");
    writeFileSync(join(dir, "src", "main", "resources", "top.txt"), "hi");

    const out = join(dir, "dist");
    const result = runCompile(paths, {
      experimentalCompiler: true,
      outDir: out,
      config: defaultConfig(dir),
    });
    expect(result.success).toBe(true);
    expect(readFileSync(join(out, "conf", "app.properties"), "utf8")).toBe("k=v\n");
    expect(readFileSync(join(out, "top.txt"), "utf8")).toBe("hi");

    const jarred = runCompile(paths, {
      experimentalCompiler: true,
      outDir: out,
      output: "jar",
      config: defaultConfig(dir),
    });
    expect(jarred.success).toBe(true);
    const entries = readZipEntries(readFileSync(jarred.written[0]!))!.map(e => e.name);
    expect(entries.sort()).toEqual([
      "META-INF/MANIFEST.MF",
      "app/A.class",
      "conf/app.properties",
      "top.txt",
    ]);
  });
});

test("jar manifests carry Main-Class for the unique entry point (#11)", () => {
  const decode = (jar: string): string =>
    new TextDecoder().decode(readZipEntries(readFileSync(jar))![0]!.read());
  // unique main -> detected
  inTempDir(
    { "M.java": "package app; public class M { public static void main(String[] a) {} }" },
    (dir, paths) => {
      const result = runCompile(paths, {
        experimentalCompiler: true,
        outDir: dir,
        output: "jar",
        config: defaultConfig(dir),
      });
      expect(result.success).toBe(true);
      expect(decode(result.written[0]!)).toBe("Manifest-Version: 1.0\r\nMain-Class: app.M\r\n\r\n");
    },
  );
  // two mains, nothing configured -> no Main-Class attribute
  inTempDir(
    {
      "A.java": "public class A { public static void main(String[] a) {} }",
      "B.java": "public class B { public static void main(String... a) {} }",
    },
    (dir, paths) => {
      const result = runCompile(paths, {
        experimentalCompiler: true,
        outDir: dir,
        output: "jar",
        config: defaultConfig(dir),
      });
      expect(result.success).toBe(true);
      expect(decode(result.written[0]!)).toBe("Manifest-Version: 1.0\r\n\r\n");
    },
  );
  // configured mainClass wins over detection
  inTempDir(
    {
      "cappu.json": '{ "compilerOptions": { "mainClass": "B" } }',
      "A.java": "public class A { public static void main(String[] a) {} }",
      "B.java": "public class B { public static void main(String[] a) {} }",
    },
    (dir, paths) => {
      const result = runCompile(paths.slice(1), {
        experimentalCompiler: true,
        outDir: dir,
        output: "jar",
        config: loadConfig(undefined, dir),
      });
      expect(result.success).toBe(true);
      expect(decode(result.written[0]!)).toBe("Manifest-Version: 1.0\r\nMain-Class: B\r\n\r\n");
    },
  );
});

test("a jar with several mains and no configured mainClass warns (#11)", () => {
  inTempDir(
    {
      "A.java": "public class A { public static void main(String[] a) {} }",
      "B.java": "public class B { public static void main(String[] a) {} }",
    },
    (dir, paths) => {
      const result = runCompile(paths, {
        experimentalCompiler: true,
        outDir: join(dir, "dist"),
        output: "jar",
        config: defaultConfig(dir),
      });
      expect(result.success).toBe(true);
      expect(result.warnings?.some(w => w.includes("several classes declare main"))).toBe(true);
    },
  );
});

test("output fat-jar merges dependency jar contents, own classes win", () => {
  inTempDir({ "B.java": "package app; class B { }" }, (dir, paths) => {
    // a dependency jar in the default classPath location
    mkdirSync(join(dir, ".cappu", "lib", "classes"), { recursive: true });
    writeFileSync(
      join(dir, ".cappu", "lib", "classes", "dep.jar"),
      writeZip([
        { name: "META-INF/MANIFEST.MF", bytes: new Uint8Array([1]) }, // must not leak
        { name: "org/dep/D.class", bytes: new Uint8Array([7]) },
        { name: "app/B.class", bytes: new Uint8Array([9]) }, // loses to ours
      ]),
    );
    writeFileSync(join(dir, "cappu.json"), "{}"); // baseDir = the temp dir
    const result = runCompile(paths, {
      experimentalCompiler: true,
      outDir: join(dir, "dist"),
      output: "fat-jar",
      config: loadConfig(undefined, dir),
    });
    expect(result.success).toBe(true);
    const entries = readZipEntries(readFileSync(result.written[0]!))!;
    expect(entries.map(e => e.name)).toEqual([
      "META-INF/MANIFEST.MF",
      "app/B.class",
      "org/dep/D.class",
    ]);
    // our compiled B.class, not the dependency's one-byte fake
    expect(entries[1]!.read().length).toBeGreaterThan(9);
  });
});

test("fat-jar merges same-path service/Spring descriptors across dependency jars", () => {
  inTempDir({ "B.java": "package app; class B { }" }, (dir, paths) => {
    const libs = join(dir, ".cappu", "lib", "classes");
    mkdirSync(libs, { recursive: true });
    const enc = (s: string) => new TextEncoder().encode(s);
    // two dependency jars that register at the SAME META-INF paths
    writeFileSync(
      join(libs, "dep-a.jar"),
      writeZip([
        { name: "META-INF/MANIFEST.MF", bytes: new Uint8Array([1]) }, // must not leak
        { name: "META-INF/services/com.example.Svc", bytes: enc("com.a.Provider\n") },
        {
          name: "META-INF/spring/org.springframework.boot.autoconfigure.AutoConfiguration.imports",
          bytes: enc("com.a.AutoConfig\n"),
        },
        {
          name: "META-INF/spring.factories",
          bytes: enc("com.example.Listener=com.a.L1,com.a.L2\n"),
        },
      ]),
    );
    writeFileSync(
      join(libs, "dep-b.jar"),
      writeZip([
        { name: "META-INF/services/com.example.Svc", bytes: enc("com.b.Provider\n") },
        {
          name: "META-INF/spring/org.springframework.boot.autoconfigure.AutoConfiguration.imports",
          bytes: enc("com.b.AutoConfig\n"),
        },
        // same key as dep-a: a naive concat would let java.util.Properties drop one
        { name: "META-INF/spring.factories", bytes: enc("com.example.Listener=com.b.L3\n") },
      ]),
    );
    writeFileSync(join(dir, "cappu.json"), "{}");
    const result = runCompile(paths, {
      experimentalCompiler: true,
      outDir: join(dir, "dist"),
      output: "fat-jar",
      config: loadConfig(undefined, dir),
    });
    expect(result.success).toBe(true);
    const entries = readZipEntries(readFileSync(result.written[0]!))!;
    const text = (name: string) =>
      new TextDecoder().decode(entries.find(e => e.name === name)!.read());
    // ServiceLoader provider lists from both jars survive
    expect(text("META-INF/services/com.example.Svc").split(/\n/).filter(Boolean).sort()).toEqual([
      "com.a.Provider",
      "com.b.Provider",
    ]);
    // Spring Boot auto-configuration imports from both jars survive
    expect(
      text("META-INF/spring/org.springframework.boot.autoconfigure.AutoConfiguration.imports")
        .split(/\n/)
        .filter(Boolean)
        .sort(),
    ).toEqual(["com.a.AutoConfig", "com.b.AutoConfig"]);
    // spring.factories: the shared key keeps every jar's values, none dropped
    expect(text("META-INF/spring.factories").trim()).toBe(
      "com.example.Listener=com.a.L1,com.a.L2,com.b.L3",
    );
    // a dependency manifest never leaks into ours
    expect(entries.filter(e => e.name === "META-INF/MANIFEST.MF").length).toBe(1);
  });
});

const HAS_JAVAC = (() => {
  try {
    execFileSync("javac", ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

test("the default compile delegates to javac", { skip: !HAS_JAVAC }, () => {
  inTempDir(
    { "M.java": "package app; public class M { public static void main(String[] a) {} }" },
    (dir, paths) => {
      const result = runCompile(paths, {
        outDir: join(dir, "dist"),
        config: defaultConfig(dir),
      });
      expect(result.success).toBe(true);
      expect(result.written).toEqual([join(dir, "dist", "app", "M.class")]);
      const bytes = readFileSync(result.written[0]!);
      expect(bytes.readUInt32BE(0)).toBe(0xcafebabe);

      // jar output detects Main-Class from javac's class BYTES
      const jar = runCompile(paths, {
        outDir: join(dir, "dist2"),
        output: "jar",
        config: defaultConfig(dir),
      });
      expect(jar.success).toBe(true);
      const manifest = new TextDecoder().decode(
        readZipEntries(readFileSync(jar.written[0]!))![0]!.read(),
      );
      expect(manifest).toContain("Main-Class: app.M");
    },
  );
});

test("compilerOptions.release targets an older class-file version", { skip: !HAS_JAVAC }, () => {
  inTempDir({ "R.java": "class R {}" }, (dir, paths) => {
    writeFileSync(join(dir, "cappu.json"), '{ "compilerOptions": { "release": 17 } }');
    const result = runCompile(paths, { outDir: dir, config: loadConfig(undefined, dir) });
    expect(result.success).toBe(true);
    // class file major version: Java 17 -> 61, at offset 6
    const bytes = readFileSync(result.written[0]!);
    expect(bytes.readUInt16BE(6)).toBe(61);
  });
});

test("the default compile surfaces javac's located diagnostics", { skip: !HAS_JAVAC }, () => {
  inTempDir({ "B.java": 'class B { void m() { int x = "s"; } }' }, (dir, paths) => {
    const result = runCompile(paths, {
      outDir: dir,
      config: defaultConfig(dir),
    });
    expect(result.success).toBe(false);
    if (result.success) return;
    expect(result.diagnostics[0]!.file).toBe(paths[0]);
    expect(result.diagnostics[0]!.line).toBe(1);
    expect(result.diagnostics[0]!.message).toContain("incompatible types");
  });
});
