// `cappu init`: scaffold a project - write cappu.json (asking for the project
// coordinates and build output, npm-init style), create the default directories
// and a .gitignore; --with-schema also writes the JSON schema the $schema entry
// points at. -y/--yes skips the questions and takes the defaults. Runs before
// loadConfig - bootstrapping must not depend on an existing, possibly broken
// config.

import { mkdirSync, writeFileSync } from "node:fs";
import { basename, resolve } from "node:path";

import { input, select } from "@inquirer/prompts";

import { type OutputKind } from "../compiler/compiler.ts";
import {
  configJsonSchema,
  DEFAULT_CLASS_PATH,
  DEFAULT_CONFIG_NAME,
  DEFAULT_RESOURCE_PATH,
  DEFAULT_SOURCE_PATH,
  DEFAULT_TEST_CLASS_PATH,
  DEFAULT_TEST_RESOURCE_PATH,
  DEFAULT_TEST_SOURCE_PATH,
  MAVEN_ID,
  SCHEMA_FILE_NAME,
  SEMVER,
} from "../config.ts";

const GITIGNORE_TEMPLATE = `# installed dependencies, provisioned JDKs, generated sources, local state
/.cappu/

# build output of \`cappu compile\`
/dist/
`;

interface InitAnswers {
  groupId: string;
  artifactId: string;
  version: string;
  output: OutputKind;
}

// A directory name reduced to a valid Maven artifactId (a sensible default).
function sanitizeId(name: string): string {
  const cleaned = name.replace(/[^A-Za-z0-9_.-]+/g, "-").replace(/^-+|-+$/g, "");
  return cleaned === "" ? "app" : cleaned;
}

function defaults(projectDir: string): InitAnswers {
  return {
    groupId: "com.example",
    artifactId: sanitizeId(basename(projectDir)),
    version: "1.0.0",
    output: "fat-jar",
  };
}

// The prompts, defaulting to `base`. Output is labelled by intent.
async function ask(base: InitAnswers): Promise<InitAnswers> {
  const groupId = await input({
    message: "groupId",
    default: base.groupId,
    validate: v => MAVEN_ID.test(v) || "letters, digits, '.', '_' or '-' only",
  });
  const artifactId = await input({
    message: "artifactId",
    default: base.artifactId,
    validate: v => MAVEN_ID.test(v) || "letters, digits, '.', '_' or '-' only",
  });
  const version = await input({
    message: "version",
    default: base.version,
    validate: v => SEMVER.test(v) || "must be semver, e.g. 1.0.0",
  });
  const output = await select<OutputKind>({
    message: "build output",
    default: "fat-jar",
    choices: [
      { name: "application (fat-jar)", value: "fat-jar" },
      { name: "library (jar)", value: "jar" },
      { name: "classes", value: "classes" },
    ],
  });
  return { groupId, artifactId, version, output };
}

/** The cappu.json contents for the chosen answers. */
export function renderInitConfig(answers: InitAnswers): string {
  const config = {
    $schema: `./${SCHEMA_FILE_NAME}`,
    groupId: answers.groupId,
    artifactId: answers.artifactId,
    version: answers.version,
    compilerOptions: { output: answers.output },
    dependencies: {
      api: {},
      implementation: {},
      annotationProcessor: {},
      testImplementation: {},
    },
  };
  return `${JSON.stringify(config, null, 2)}\n`;
}

export async function runInit(
  configPath: string | undefined,
  withSchema: boolean,
  options: { yes?: boolean } = {},
): Promise<never> {
  const target = resolve(configPath ?? DEFAULT_CONFIG_NAME);
  const base = defaults(resolve(target, ".."));
  const answers = options.yes ? base : await ask(base);

  try {
    // wx: create only if absent - atomic, no exists/write race
    writeFileSync(target, renderInitConfig(answers), { flag: "wx" });
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code !== "EEXIST") throw e;
    process.stderr.write(`cappu: ${target} already exists, not overwriting\n`);
    process.exit(1);
  }
  // The standard project layout (nikeee/cappu#3, #12): dependency and source
  // directories plus their resource/test counterparts, so a fresh project
  // compiles warning-free and the layout is visible from the start.
  for (const dir of [
    DEFAULT_CLASS_PATH,
    DEFAULT_TEST_CLASS_PATH,
    DEFAULT_SOURCE_PATH,
    DEFAULT_RESOURCE_PATH,
    DEFAULT_TEST_SOURCE_PATH,
    DEFAULT_TEST_RESOURCE_PATH,
  ]) {
    mkdirSync(resolve(target, "..", dir), { recursive: true });
  }
  // A .gitignore covering what cappu generates; an existing one is left alone
  // but flagged, so the user knows cappu's ignores (/.cappu/, /dist/) were not
  // added and downloaded deps / build output could otherwise get committed.
  try {
    writeFileSync(resolve(target, "..", ".gitignore"), GITIGNORE_TEMPLATE, { flag: "wx" });
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code !== "EEXIST") throw e;
    process.stderr.write(
      ".gitignore already exists, left unchanged - add /.cappu/ and /dist/ if missing\n",
    );
  }
  process.stdout.write(`${target}\n`);
  if (withSchema) {
    // The schema the template's $schema entry points at; regenerated freely
    // (it is derived from the zod schema, not user-edited).
    const schemaTarget = resolve(target, "..", SCHEMA_FILE_NAME);
    writeFileSync(schemaTarget, configJsonSchema());
    process.stdout.write(`${schemaTarget}\n`);
  }
  process.exit(0);
}
