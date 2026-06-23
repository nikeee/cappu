// End-to-end: publish to a throwaway Maven registry, then install back from it.
// Spins up Reposilite (a lightweight Maven repository) in a container via
// testcontainers, so it exercises the real `cappu publish` -> resolve ->
// `cappu install` loop against an actual repo. Gated on Docker AND javac (publish
// builds the jar with javac); slow (container startup), so it is skipped when
// either is missing.

import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { expect } from "expect";
import { GenericContainer, type StartedTestContainer, Wait } from "testcontainers";

const here = import.meta.dirname;
const tsx = join(here, "..", "..", "node_modules", ".bin", "tsx");
const cli = join(here, "..", "cli", "main.ts");

const HAS_JAVAC = (() => {
  try {
    execFileSync("javac", ["-version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

const HAS_DOCKER = (() => {
  try {
    execFileSync("docker", ["info"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
})();

test(
  "cappu publish round-trips through a real Maven registry (publish then install)",
  { skip: !HAS_DOCKER || !HAS_JAVAC, timeout: 300_000 },
  async () => {
    let container: StartedTestContainer | undefined;
    using root = TempDir.create("cappu-publish-e2e-");
    try {
      // The image entrypoint forwards $REPOSILITE_OPTS (not CMD args); --token
      // mints a temporary all-permissions token, and releases is public-read.
      container = await new GenericContainer("dzikoysk/reposilite:3.5.22")
        .withExposedPorts(8080)
        .withEnvironment({ REPOSILITE_OPTS: "--token deployer:secret" })
        .withWaitStrategy(Wait.forHttp("/", 8080).forStatusCode(200))
        .withStartupTimeout(180_000)
        .start();
      const repo = `http://${container.getHost()}:${container.getMappedPort(8080)}/releases`;

      // 1. Publish a tiny library to the registry.
      const pub = join(root.path, "lib-proj");
      mkdirSync(join(pub, "src", "main", "java", "com", "example"), { recursive: true });
      writeFileSync(
        join(pub, "cappu.json"),
        JSON.stringify({
          groupId: "com.example",
          artifactId: "demo-lib",
          version: "1.0.0",
          license: "MIT",
        }),
      );
      writeFileSync(
        join(pub, "src", "main", "java", "com", "example", "Hello.java"),
        'package com.example; public class Hello { public static String hi() { return "hi"; } }',
      );
      execFileSync(tsx, [cli, "publish", "--repo", repo], {
        cwd: pub,
        env: {
          ...process.env,
          CAPPU_PACKAGE_STORE: join(root.path, "store-pub"),
          CAPPU_PUBLISH_USERNAME: "deployer",
          CAPPU_PUBLISH_PASSWORD: "secret",
        },
        stdio: ["ignore", "pipe", "pipe"],
      });

      // 2. A consumer resolves and installs it from that same registry.
      const app = join(root.path, "app-proj");
      mkdirSync(app, { recursive: true });
      writeFileSync(
        join(app, "cappu.json"),
        JSON.stringify({
          packageSources: [repo],
          dependencies: { implementation: { "com.example:demo-lib": "1.0.0" } },
        }),
      );
      execFileSync(tsx, [cli, "install"], {
        cwd: app,
        env: { ...process.env, CAPPU_PACKAGE_STORE: join(root.path, "store-app") },
        stdio: ["ignore", "pipe", "pipe"],
      });

      const installed = join(app, ".cappu", "lib", "classes", "demo-lib-1.0.0.jar");
      expect(readFileSync(installed).length).toBeGreaterThan(0);
      const lock = readFileSync(join(app, "cappu-lock.json"), "utf8");
      expect(lock).toContain("com.example");
      expect(lock).toContain("demo-lib");
    } finally {
      await container?.stop();
      rmSync(root.path, { recursive: true, force: true });
    }
  },
);
