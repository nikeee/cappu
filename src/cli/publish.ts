// `cappu publish`: build the project jar, generate its POM, and upload both
// (with checksums) to a Maven registry. Coordinates and a registry url are
// required; credentials come from the environment.

import { readFileSync } from "node:fs";

import { runCompile } from "../compiler/compiler.ts";
import { artifactBaseName, type CappuConfig } from "../config.ts";
import {
  generatePom,
  missingCoordinates,
  type PublishFile,
  publishArtifacts,
  resolvePublishAuth,
} from "../publish/index.ts";
import { findSourceJavaFiles } from "../workspace.ts";
import { renderDiagnostics } from "./renderDiagnostics.ts";
import { painter } from "./style.ts";

export async function runPublish(
  config: CappuConfig,
  options: { repo?: string } = {},
): Promise<never> {
  const err = painter(process.stderr);
  const out = painter(process.stdout);

  const missing = missingCoordinates(config);
  if (missing.length > 0) {
    process.stderr.write(
      `${err("red", "error:")} cappu publish needs ${missing.join(", ")} in cappu.json\n`,
    );
    process.exit(2);
  }
  const repo = options.repo ?? config.publishRepository;
  if (!repo) {
    process.stderr.write(
      `${err("red", "error:")} no registry: pass --repo <url> or set publishRepository in cappu.json\n`,
    );
    process.exit(2);
  }
  const auth = resolvePublishAuth();
  if (!auth) {
    process.stderr.write(
      `${err("red", "error:")} no credentials: set CAPPU_PUBLISH_USERNAME + CAPPU_PUBLISH_PASSWORD, or CAPPU_PUBLISH_TOKEN\n`,
    );
    process.exit(2);
  }

  // Build the jar (default javac path) over the configured sources.
  const inputs = findSourceJavaFiles(config);
  if (inputs.length === 0) {
    process.stderr.write(
      `${err("red", "error:")} no sources to compile (configured sourcePaths are empty)\n`,
    );
    process.exit(2);
  }
  const result = runCompile(inputs, { output: "jar", config });
  for (const w of result.warnings ?? []) process.stderr.write(err("yellow", `warning: ${w}\n`));
  if (!result.success) {
    renderDiagnostics(result.diagnostics);
    process.exit(1);
  }
  const jarPath = result.written.find(f => f.endsWith(".jar"))!;

  const base = artifactBaseName(config);
  const coordinates = {
    groupId: config.groupId!,
    artifactId: config.artifactId!,
    version: config.version!,
  };
  const files: PublishFile[] = [
    { filename: `${base}.jar`, bytes: readFileSync(jarPath) },
    { filename: `${base}.pom`, bytes: new TextEncoder().encode(generatePom(config)) },
  ];

  try {
    const uploaded = await publishArtifacts({
      repo,
      coordinates,
      files,
      auth,
      onUpload: url => process.stderr.write(err("dim", `uploading ${url}\n`)),
    });
    const id = `${coordinates.groupId}:${coordinates.artifactId}:${coordinates.version}`;
    process.stdout.write(
      `${out("green", "✓")} published ${out("bold", id)} (${uploaded.length} files) to ${repo}\n`,
    );
    process.exit(0);
  } catch (e) {
    process.stderr.write(`${err("red", "error:")} publish failed: ${(e as Error).message}\n`);
    process.exit(1);
  }
}
