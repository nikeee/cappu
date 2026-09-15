# cappu.json field reference

Taken from `cappu config-schema` (cappu 0.1.23) and the defaults in the cappu
sources. Everything is optional; an unset section takes the defaults below.
Unknown keys are ignored silently, so a typo does nothing rather than fail.

## Top level

| Field | Type | Default | Notes |
|---|---|---|---|
| `groupId` | string | unset | `[A-Za-z0-9_.-]+`. Needed for `publish`; together with `artifactId` and `version` it names the jar `<artifactId>-<version>.jar` |
| `artifactId` | string | unset | same pattern |
| `version` | string | unset | strict semver `MAJOR.MINOR.PATCH[-prerelease]`. `cappu version major|minor|patch` bumps it |
| `license` | string | unset | valid SPDX expression, otherwise config loading fails |
| `jdk` | string | unset | `temurin-<n>` or `corretto-<n>`; provisioned by `cappu install`, used for compile, run and test; beats `compilerOptions.javac` |
| `packageSources` | string[] | `https://maven-central-eu.storage-download.googleapis.com/maven2` (Central mirror), Maven Central, `https://maven.google.com`, `https://plugins.gradle.org/m2` | anonymous URLs only, no auth; the mirror is first because Central rate-limits |
| `publishRepository` | URL | unset | falls back to Maven Central; `--repo` and `CAPPU_PUBLISH_REGISTRY` override it |
| `dependencies` | object | `{}` | four maps, see below |
| `compilerOptions` | object | `{}` | |
| `testOptions` | object | `{}` | |
| `formatterOptions` | object | `{}` | |
| `lspOptions` | object | `{}` | |
| `dapOptions` | object | `{}` | |

## dependencies

Each configuration is a map `"group:artifact": "version"`.

| Configuration | Installed to | Compile classpath | Meaning |
|---|---|---|---|
| `api` | `.cappu/lib/classes` | yes | like `implementation`, but the type is part of your public API; published with Maven `compile` scope |
| `implementation` | `.cappu/lib/classes` | yes | normal compile and runtime dependency; Maven `compile`, `provided`, `runtime` and `optional` all map here; published with Maven `runtime` scope |
| `annotationProcessor` | `.cappu/lib/processors` | no, processor path only | the processor artifact itself; not published in the POM |
| `testImplementation` | `.cappu/lib/test-classes` | tests only | JUnit, Mockito, ...; published with Maven `test` scope |

## compilerOptions

| Field | Type | Default | Notes |
|---|---|---|---|
| `classPath` | string[] | `["./.cappu/lib/classes", "./target/dependency", "./build/libs", "./lib", "./libs"]` | directories of `.class` files or jars; a custom list replaces the defaults (only `./.cappu/lib/classes` is re-added). Missing dirs are ignored |
| `sourcePaths` | string[] | `["./src/main/java", "./src/generated/java"]` | |
| `resourcePaths` | string[] | `["./src/main/resources"]` | copied verbatim into the output |
| `output` | `"classes"` \| `"jar"` \| `"fat-jar"` | `"classes"` | what `compile` writes to `./dist`; `-o` overrides per call. `fat-jar` merges `META-INF/services/*` and `spring.factories` from all jars |
| `javac` | string | `"javac"` | binary to compile with; a provisioned `jdk` wins |
| `release` | integer >= 8 | javac's own | `javac --release`: language level and class file version |
| `mainClass` | string | the single `main(String[])` found | required by `run`, `dap` and executable jars when there are 0 or more than 1 candidates |
| `experimentalCompiler.enabled` | boolean | `false` | cappu's own bytecode emitter instead of javac. Leave off unless testing it |
| `experimentalCompiler.failOnDegrade` | boolean | `true` | fail when a method body degrades to a placeholder |
| `experimentalCompiler.validate` | boolean | `false` | also run javac and require identical normalized bytecode; needs `output: "classes"` |
| `experimentalCompiler.debugInfo` | boolean | `false` | emit `LocalVariableTable` like `javac -g` |
| `nullness.enabled` | boolean | `false` | jspecify flow-aware nullness diagnostics in `check` and the LSP; never a javac error |
| `nullness.nullableAnnotations` | string[] | `["org.jspecify.annotations.Nullable"]` | matched by simple name |
| `nullness.nonNullAnnotations` | string[] | `["org.jspecify.annotations.NonNull"]` | |
| `nullness.nullMarkedAnnotations` | string[] | `["org.jspecify.annotations.NullMarked"]` | |
| `nullness.nullUnmarkedAnnotations` | string[] | `["org.jspecify.annotations.NullUnmarked"]` | |

Not a field: `quiet`. Use `cappu compile -q`.

## testOptions

| Field | Type | Default | Notes |
|---|---|---|---|
| `outputFormat` | `"text"` \| `"junit"` | `"text"` | `junit` additionally writes JUnit XML into `reportsDir`; the text summary always streams to stdout |
| `reportsDir` | string | `"./dist/test-results"` | |
| `coverage` | boolean | `false` | attaches the JaCoCo agent and writes `jacoco.exec` into `reportsDir` |

`cappu test` has no CLI flags; these are the only knobs. There is no test filter.

## formatterOptions

| Field | Type | Default | Notes |
|---|---|---|---|
| `style` | `"google"` \| `"aosp"` | `"google"` | 2-space vs 4-space google-java-format styles |
| `ignore` | string[] | `[]` | globs relative to the config directory, skipped by `format` |
| `importOrder` | string[] | unset | each entry is a package prefix ending in `*` (`java.*`, `*`) or `""` for a blank line; longest prefix wins; static imports always form the first group; unmatched imports form a trailing group. Example: `["android.*", "com.*", "", "org.*", "*", "", "java.*", "javax.*"]` |

The MCP tool `organize_imports` follows the same settings, so it agrees with `cappu format`.

## lspOptions

| Field | Type | Default |
|---|---|---|
| `inlayHints.parameterNames` | boolean | `true` |
| `inlayHints.varTypes` | boolean | `true` |

## dapOptions

| Field | Type | Default | Notes |
|---|---|---|---|
| `enableAssertions` | boolean | `false` | passes `-ea` to every debug launch; a launch request's `vmArgs` (`-da`) still override |

## Environment variables

| Variable | Effect |
|---|---|
| `CAPPU_PACKAGE_STORE` | global jar cache directory (default under `~/.cache/cappu`) |
| `CAPPU_JDK_STORE` | global directory for provisioned JDKs |
| `CAPPU_PUBLISH_REGISTRY`, `CAPPU_PUBLISH_USERNAME`, `CAPPU_PUBLISH_PASSWORD`, `CAPPU_PUBLISH_TOKEN` | `publish` target and credentials; see the `ci-release` skill |
| `CLAUDECODE`, `AGENT`, `AI_AGENT`, `CURSOR_AGENT`, `GEMINI_CLI`, `AUGMENT_AGENT`, `CLINE_ACTIVE`, `OPENCODE_CLIENT`, `TRAE_AI_SHELL_ID`, `CODEX_SANDBOX` | any non-empty value switches on agent mode (JSON or SARIF output, no colour) |
| `GITHUB_ACTIONS`, `FORGEJO_ACTIONS`, `GITEA_ACTIONS` | `true` makes diagnostics also print `::error file=...,line=...::msg` annotations |
