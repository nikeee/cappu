---
name: build-test
description: "Use when compiling, type-checking, running, testing or formatting Java in a cappu project: choosing between cappu compile and cappu check, reading their diagnostics and exit codes, producing a jar or fat jar, running the main class, configuring JUnit XML reports or JaCoCo coverage, or fixing formatting and import order. Covers compile, check, run, test, format."
---

# Build, check, run, test and format with cappu

`cappu compile` builds with javac and writes to `./dist`. `cappu check` runs
cappu's own type checker (the same engine behind the LSP and the MCP
`diagnostics` tool), writes nothing, and reports more than javac does, so the
two can disagree in both directions. `cappu test` has no flags at all: there is
no way to run a single test class or method, and coverage is config-only.

## Commands

| Command | Does | Exit |
|---|---|---|
| `cappu compile [files] [-o classes\|jar\|fat-jar] [--artifact <name>] [-q]` | javac over `sourcePaths` (or the given files), resources copied in, output in `./dist` | 0 ok, 1 compile error, 2 bad flag |
| `cappu check [files]` | cappu's checker over `sourcePaths` (or the files); includes jspecify nullness when enabled | 0 no errors, 1 error diagnostics, 2 no files |
| `cappu run [-- args...]` | `compile`, then `java` with the main class; arguments after `--` reach the program verbatim | the program's exit code; 1 compile error; 2 no or ambiguous main class |
| `cappu test` | compile main, compile `src/test/java`, run the JUnit Platform launcher (JUnit 5 and Vintage) | the launcher's exit code, 0 = all green; 1 no tests under `./src/test/java` or compile error |
| `cappu format [files]` | check mode: prints each unformatted file on stdout | 1 if any file is unformatted, 0 if clean, 2 read error |
| `cappu format -w [files]` | rewrites in place, prints the rewritten files | always 0 |

`-q` hides the per-class listing of `compile`. A `done in ...` timing line goes
to stderr on every command, so piping stdout is safe.

## Procedure: the edit loop

1. **Inner loop with `check`.** After editing, `cappu check` (or the MCP
   `diagnostics` tool with the changed files) gives fast diagnostics without
   producing artifacts. Fix what it reports.
2. **Truth with `compile`.** `cappu compile -q` is what CI and the jar use. A
   clean `check` does not guarantee a clean javac and vice versa; when they
   disagree, javac decides whether the build is green.
3. **Run.** `cappu run -- --port 8080`. The main class comes from
   `compilerOptions.mainClass`, otherwise the single class declaring
   `public static void main(String[])`; with none or several, exit 2 names the
   candidates. There is no `--no-compile`; `run` always compiles first.
4. **Test.** `cappu test`. Success shows `N tests successful` and `0 tests failed`
   in the text summary. Reports and coverage are configured, not flagged:
   ```jsonc
   "testOptions": {
     "outputFormat": "junit",             // also write JUnit XML (the text summary still streams)
     "reportsDir": "./dist/test-results", // default
     "coverage": true                     // JaCoCo agent, writes jacoco.exec into reportsDir
   }
   ```
   To run a subset there is no filter: use `@Disabled` or `@Tag` in the sources,
   or a temporary copy of the project with the other tests removed.
5. **Format.** `cappu format` lists offenders (exit 1); `cappu format -w` fixes
   them. Output is google-java-format compatible. Configure via:
   ```jsonc
   "formatterOptions": {
     "style": "google",                     // or "aosp" (4-space)
     "ignore": ["src/generated/**"],        // globs relative to cappu.json
     "importOrder": ["com.*", "", "java.*", "javax.*", "", "*"]
   }
   ```
   `importOrder` entries are package prefixes ending in `*`, or `""` for a blank
   line; the longest prefix wins, static imports always come first, unmatched
   imports form a trailing group. The MCP tool `organize_imports` follows the
   same settings.

## Outputs in `./dist`

| `output` / `-o` | Result |
|---|---|
| `classes` (default) | package tree, runnable with `java -cp ./dist com.example.Main` |
| `jar` | `<artifactId>-<version>.jar` when both are set, else `<directory name>.jar`; with full coordinates also the generated POM |
| `fat-jar` | the jar plus every dependency jar's contents; same-path `META-INF/services/*` and `spring.factories` are merged, so Spring Boot runs from one jar |

`--artifact app` forces `dist/app.jar`, handy for Dockerfiles. WAR is not
supported. Test results and `jacoco.exec` land in `testOptions.reportsDir`.

## CI annotations

With `GITHUB_ACTIONS=true`, `FORGEJO_ACTIONS=true` or `GITEA_ACTIONS=true`,
diagnostics and unformatted files are additionally printed as
`::error file=...,line=...::message` so the platform annotates the diff. A bare
`CI=true` does not trigger this. The `ci-release` skill has the full pipeline.

## Gotchas checklist

- [ ] `check` passing is not `compile` passing; run `compile` before declaring done.
- [ ] Nullness diagnostics come only from `check` and the LSP; `compile` ignores them.
- [ ] `format -w` always exits 0; the gate is flagless `format`.
- [ ] No test filter, no single-test run, no `--coverage` flag; use `testOptions`.
- [ ] Tests must live under `./src/test/java`; other locations are not found.
- [ ] Program arguments go after `--`: `cappu run -- arg1 arg2`.
- [ ] Ambiguous main class? Set `compilerOptions.mainClass`.
- [ ] Jar name depends on `artifactId` and `version`; use `--artifact` for a fixed name.
