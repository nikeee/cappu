---
name: setup
description: "Use when creating a new Java project with cappu, reading or editing cappu.json (any field: JDK pinning, source and resource paths, output kind, main class, annotation processors, nullness), installing or upgrading the cappu binary, or when you land in a repo that has a cappu.json and need to know its layout. Not for migrating from Maven or Gradle - see the migrate skill."
---

# Set up and configure a cappu project

cappu is a single-module Java toolchain driven by one JSONC file, `cappu.json`,
found by walking up from the current directory (`-c <file>` points elsewhere).
Every dependency version is pinned literally. There is no parent POM, BOM,
`${property}` resolution, multi-module reactor, dependency exclusion, private
repository auth, Kotlin or WAR support. `cappu config-schema` prints the schema;
`references/cappu-json-fields.md` lists every field with defaults and gotchas.

## Procedure

1. **Have the binary.** `cappu --version`. If missing, download the release
   asset for the platform from https://github.com/nikeee/cappu/releases/latest
   (`cappu-linux-x64`, `cappu-linux-arm64`, `cappu-darwin-arm64`,
   `cappu-darwin-x64`, `cappu-win-x64.exe`, `cappu-win-arm64.exe`), `chmod +x`,
   put it on PATH. Later `cappu self-upgrade` pulls the newest release. There is
   no npm package. `cappu rage` prints version and environment for bug reports.
2. **Scaffold.** `cappu init -y` writes `cappu.json` (groupId `com.example`,
   artifactId = directory name, version `1.0.0`, `output: "fat-jar"`, four empty
   dependency maps), a `.gitignore` for `/.cappu/` and `/dist/`, and `src/`.
   `--with-schema` also writes `cappu.schema.json` plus a `$schema` pointer for
   editor completion. Without `-y` it prompts interactively.
3. **Keep the config minimal.** The standard layout (`src/main/java`,
   `src/main/resources`, `src/test/java`) needs no path settings.
   ```jsonc
   {
     "groupId": "com.example",
     "artifactId": "shop",
     "version": "1.0.0",               // strict semver; "1.0" is rejected, "1.0.0-SNAPSHOT" is fine
     "compilerOptions": {
       "release": 21,                  // integer, passed as javac --release
       "output": "fat-jar",            // "classes" (default) | "jar" | "fat-jar"
       "mainClass": "com.example.Main" // only needed when more than one main() exists
     },
     "dependencies": {
       "implementation": { "com.google.code.gson:gson": "2.14.0" },
       "testImplementation": { "org.junit.jupiter:junit-jupiter": "6.1.1" }
     }
   }
   ```
4. **Pin the JDK (optional).** `"jdk": "temurin-21"` or `"corretto-21"`.
   `cappu install` downloads it into the global cache (`~/.cache/cappu`, override
   with `CAPPU_JDK_STORE`) and compiles with it. It beats `compilerOptions.javac`.
   Without it cappu uses whatever `javac` is on PATH.
5. **Install, build, test.** `cappu install`, `cappu compile`, `cappu test`. The
   `dependencies` and `build-test` skills cover those commands in depth.
6. **Wire the agent tooling (optional).** This plugin's `.mcp.json` already
   starts `cappu mcp`; without the plugin run `claude mcp add cappu -- cappu mcp`.
   The `code-intel` skill explains the tools.

## Layout

| Path | What | Commit? |
|------|------|---------|
| `cappu.json` | project config, JSONC (comments and trailing commas allowed) | yes |
| `cappu-lock.json` | resolved dependency set with a SHA-256 per jar | yes |
| `.cappu/` | installed jars (`lib/classes`, `lib/test-classes`, `lib/processors`), provisioned JDK, local state | no, gitignore and dockerignore it |
| `dist/` | every build output: classes, jars, POM, test reports, coverage | no |
| `src/main/java`, `src/generated/java` | sources (`compilerOptions.sourcePaths`) | yes |
| `src/main/resources` | copied verbatim into the classes tree or jar | yes |
| `src/test/java`, `src/test/resources` | tests; fixed location, not configurable | yes |

## Frequent config tasks

- **Annotation processors** go under `dependencies.annotationProcessor`
  (installed to `.cappu/lib/processors`, never on the compile classpath) and
  their API library under `implementation`. MapStruct: `org.mapstruct:mapstruct`
  in `implementation`, `org.mapstruct:mapstruct-processor` in
  `annotationProcessor`. Immutables: `org.immutables:value` in both.
- **Extra classpath entries** (a sibling module's jar, a vendored lib): set
  `compilerOptions.classPath`. It replaces the default list; `./.cappu/lib/classes`
  is re-added automatically but `./target/dependency`, `./build/libs`, `./lib`
  and `./libs` are not, so re-list what you still need.
- **Nullness (jspecify)**: `compilerOptions.nullness.enabled: true` plus the
  `org.jspecify:jspecify` dependency. Annotation lists match by simple name, so
  JSR-305 projects can repoint them. It is a `check`/LSP diagnostic, never a
  `compile` error.
- **Formatting and imports**: `formatterOptions.style` (`google` 2-space, `aosp`
  4-space), `ignore` globs, `importOrder`. See `build-test`.
- **Tests**: `testOptions.outputFormat: "junit"`, `coverage: true`. See `build-test`.
- **Package sources**: `packageSources` is a plain URL list (Google's Maven
  Central mirror, Maven Central, maven.google.com and the Gradle plugin portal
  by default; the mirror comes first because Central rate-limits). No credentials.

**Gotcha - `quiet` is not a config key.** The cappu repo's own `examples/*/cappu.json`
set `compilerOptions.quiet`; the loader tolerates unknown keys, so it does nothing.
Use `cappu compile -q`.

**Gotcha - strict values.** `license` must be a valid SPDX expression (`MIT`,
`(MIT OR Apache-2.0)`); a free-text name fails config loading. `groupId` and
`artifactId` match `[A-Za-z0-9_.-]+`. `release` is an integer, not `"21"`.

## Using cappu next to Maven or Gradle without telling anyone

Keep `cappu.json` out of the repo with `echo cappu.json >> .git/info/exclude`,
then use `cappu lsp`, `cappu mcp`, `cappu format` and `cappu audit` on the side.
The LSP works without any `cappu.json` when the layout is conventional, but
dependency resolution needs one. The default `classPath` already includes
`./target/dependency` and `./build/libs`, so jars that Maven or Gradle copied
there are visible to the language server.

## Agent mode

When any of `CLAUDECODE`, `AGENT`, `CURSOR_AGENT`, `GEMINI_CLI` (and similar
agent variables) is set, cappu detects an agent: `licenses`, `tree`, `search`
and `show` print JSON without a flag, `audit` prints SARIF, colour and progress
animation are off. There is no opt-out. `cappu audit --format text` gives the
human report; `cappu audit --json` is a usage error (exit 2). CI does not set
these variables, so pass `--json` or `--format sarif` explicitly there.

## Gotchas checklist

- [ ] `cappu.json` is JSONC at the module root; one module = one project.
- [ ] Every version literal; strict semver for `version`, SPDX for `license`.
- [ ] `.cappu/` and `dist/` ignored by git and Docker.
- [ ] Custom `classPath`? Re-list the conventional dirs you rely on.
- [ ] Processors: processor under `annotationProcessor`, its API under `implementation`.
- [ ] `jdk` set? Expect a JDK download on the first `cappu install`.
- [ ] Help is `cappu --help` and `cappu <command> --help`; `cappu help` is not a command.
