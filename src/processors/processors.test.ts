import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";

import { runCompile } from "../compiler/compiler.ts";
import { writeZip } from "../compiler/zipWriter.ts";
import { loadConfig } from "../config.ts";
import {
  discoverProcessors,
  generatedSourcesDir,
  procOnlyArgs,
  processorJars,
  runAnnotationProcessing,
} from "./processors.ts";

const HAS_JAVAC = (() => {
  try {
    execFileSync("javac", ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

const encode = (text: string): Uint8Array => new TextEncoder().encode(text);

function jarWithServices(dir: string, name: string, services: string): string {
  const path = join(dir, name);
  writeFileSync(
    path,
    writeZip([
      { name: "META-INF/services/javax.annotation.processing.Processor", bytes: encode(services) },
    ]),
  );
  return path;
}

test("processor classes are discovered from META-INF/services", () => {
  const dir = mkdtempSync(join(tmpdir(), "cappu-proc-"));
  try {
    const a = jarWithServices(
      dir,
      "a.jar",
      "# comment\ncom.example.AProcessor\n\ncom.example.BProcessor # trailing\n",
    );
    const plain = join(dir, "plain.jar");
    writeFileSync(plain, writeZip([{ name: "com/example/X.class", bytes: encode("x") }]));
    const corrupt = join(dir, "corrupt.jar");
    writeFileSync(corrupt, encode("not a zip"));

    expect(discoverProcessors([a, plain, corrupt])).toEqual([
      "com.example.AProcessor",
      "com.example.BProcessor",
    ]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("processor jars come from lib/processors, sorted; absence means none", () => {
  const project = mkdtempSync(join(tmpdir(), "cappu-proc-"));
  try {
    const config = loadConfig(undefined, project);
    expect(processorJars(config)).toEqual([]);
    mkdirSync(join(project, ".cappu", "lib", "processors"), { recursive: true });
    writeFileSync(join(project, ".cappu", "lib", "processors", "b.jar"), encode("b"));
    writeFileSync(join(project, ".cappu", "lib", "processors", "a.jar"), encode("a"));
    writeFileSync(join(project, ".cappu", "lib", "processors", "notes.txt"), encode("x"));
    expect(processorJars(config)).toEqual([
      join(project, ".cappu", "lib", "processors", "a.jar"),
      join(project, ".cappu", "lib", "processors", "b.jar"),
    ]);
  } finally {
    rmSync(project, { recursive: true, force: true });
  }
});

test("proc-only argument building", () => {
  const project = mkdtempSync(join(tmpdir(), "cappu-proc-"));
  try {
    mkdirSync(join(project, ".cappu", "lib", "classes"), { recursive: true });
    const config = loadConfig(undefined, project);
    const args = procOnlyArgs(config, ["/p/A.java"], ["/p/proc.jar", "/p/extra.jar"], {
      sources: "/out/sources",
      classes: "/out/classes",
    });
    expect(args).toEqual([
      "-proc:only",
      "-processorpath",
      `/p/proc.jar${delimiter}/p/extra.jar`,
      "-s",
      "/out/sources",
      "-d",
      "/out/classes",
      "-encoding",
      "UTF-8",
      "-cp",
      join(project, ".cappu", "lib", "classes"),
      // no -sourcepath: ./src/main/java does not exist in this project
      "/p/A.java",
    ]);
  } finally {
    rmSync(project, { recursive: true, force: true });
  }
});

test("without processor jars nothing runs at all", () => {
  const project = mkdtempSync(join(tmpdir(), "cappu-proc-"));
  try {
    const config = loadConfig(undefined, project);
    const result = runAnnotationProcessing(config, ["/p/A.java"], () => {
      throw new Error("exec must not be called");
    });
    expect(result).toEqual({ ran: false, generatedFiles: [], diagnostics: [] });
  } finally {
    rmSync(project, { recursive: true, force: true });
  }
});

test("failure modes map to diagnostics; success keeps located warnings only", () => {
  const project = mkdtempSync(join(tmpdir(), "cappu-proc-"));
  try {
    mkdirSync(join(project, ".cappu", "lib", "processors"), { recursive: true });
    jarWithServices(join(project, ".cappu", "lib", "processors"), "p.jar", "com.example.P\n");
    const config = loadConfig(undefined, project);

    // located error from a failed run
    const failed = runAnnotationProcessing(config, ["/p/A.java"], () => ({
      status: 1,
      stderr: "/p/A.java:3: error: cannot find symbol\n  symbol: class Missing\n1 error\n",
    }));
    expect(failed.diagnostics).toEqual([
      { severity: "error", file: "/p/A.java", line: 3, message: "cannot find symbol" },
    ]);

    // an uncaught processor exception has no located line: collapses to one error
    const threw = runAnnotationProcessing(config, ["/p/A.java"], () => ({
      status: 3,
      stderr:
        "error: An annotation processor threw an uncaught exception.\n\tat com.example.P.process(P.java:10)\n",
    }));
    expect(threw.diagnostics).toHaveLength(1);
    expect(threw.diagnostics[0]!.severity).toBe("error");

    // ENOENT-style spawn failure
    const missing = runAnnotationProcessing(config, ["/p/A.java"], () => ({
      status: null,
      stderr: "",
      error: new Error("spawnSync javac ENOENT"),
    }));
    expect(missing.diagnostics[0]!.message).toContain("needs javac");
    expect(missing.diagnostics[0]!.message).toContain('configure "jdk"');

    // success: Note: lines do not become errors; located warnings survive
    const ok = runAnnotationProcessing(config, ["/p/A.java"], () => ({
      status: 0,
      stderr: "Note: com.example.P did things\n/p/A.java:1: warning: something odd\n",
    }));
    expect(ok.ran).toBe(true);
    expect(ok.diagnostics).toEqual([
      { severity: "warning", file: "/p/A.java", line: 1, message: "something odd" },
    ]);
    expect(existsSync(generatedSourcesDir(config))).toBe(true);
  } finally {
    rmSync(project, { recursive: true, force: true });
  }
});

// --- end to end (needs a JDK): a real processor generates a real class ---------

const PROCESSOR_SOURCE = `
import java.io.Writer;
import java.util.Set;
import javax.annotation.processing.*;
import javax.lang.model.SourceVersion;
import javax.lang.model.element.TypeElement;

@SupportedAnnotationTypes("*")
@SupportedSourceVersion(SourceVersion.RELEASE_21)
public class GenProcessor extends AbstractProcessor {
  private boolean done;
  @Override
  public boolean process(Set<? extends TypeElement> annotations, RoundEnvironment env) {
    if (done) return false;
    done = true;
    try (Writer w = processingEnv.getFiler().createSourceFile("gen.Greeting").openWriter()) {
      w.write("package gen; public class Greeting { public static String text() { return \\"generated!\\"; } }");
    } catch (Exception e) {
      throw new RuntimeException(e);
    }
    return false;
  }
}
`;

function buildProcessorJar(into: string): void {
  const work = mkdtempSync(join(tmpdir(), "cappu-procbuild-"));
  try {
    writeFileSync(join(work, "GenProcessor.java"), PROCESSOR_SOURCE);
    execFileSync("javac", ["-proc:none", "-d", work, join(work, "GenProcessor.java")], {
      stdio: "ignore",
    });
    writeFileSync(
      into,
      writeZip([
        {
          name: "META-INF/services/javax.annotation.processing.Processor",
          bytes: encode("GenProcessor\n"),
        },
        { name: "GenProcessor.class", bytes: readFileSync(join(work, "GenProcessor.class")) },
      ]),
    );
  } finally {
    rmSync(work, { recursive: true, force: true });
  }
}

test("a real processor generates a source both compile modes pick up", { skip: !HAS_JAVAC }, () => {
  const project = mkdtempSync(join(tmpdir(), "cappu-proce2e-"));
  try {
    mkdirSync(join(project, ".cappu", "lib", "processors"), { recursive: true });
    mkdirSync(join(project, "src", "main", "java"), { recursive: true });
    buildProcessorJar(join(project, ".cappu", "lib", "processors", "gen.jar"));
    const main = join(project, "src", "main", "java", "Main.java");
    writeFileSync(
      main,
      "public class Main { public static void main(String[] a) { System.out.println(gen.Greeting.text()); } }",
    );
    writeFileSync(join(project, "cappu.json"), "{}");
    const config = loadConfig(undefined, project);

    // default mode: one javac invocation runs the processor and compiles
    const byJavac = runCompile([main], { outDir: join(project, "dist"), config });
    expect(byJavac.success).toBe(true);
    expect(byJavac.written.some(f => f.endsWith(join("gen", "Greeting.class")))).toBe(true);
    expect(existsSync(join(generatedSourcesDir(config), "gen", "Greeting.java"))).toBe(true);

    // experimental mode: -proc:only generates, our compiler compiles both
    rmSync(join(project, "dist"), { recursive: true, force: true });
    const byOurs = runCompile([main], {
      experimentalCompiler: true,
      outDir: join(project, "dist"),
      config,
    });
    expect(byOurs.success).toBe(true);
    expect(byOurs.written.some(f => f.endsWith(join("gen", "Greeting.class")))).toBe(true);
  } finally {
    rmSync(project, { recursive: true, force: true });
  }
});
