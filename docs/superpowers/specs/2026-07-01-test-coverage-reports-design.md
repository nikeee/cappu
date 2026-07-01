# `cappu test` coverage reports (JaCoCo `.exec`)

## Goal

Let `cappu test` optionally produce a JaCoCo coverage data file (`jacoco.exec`)
alongside the JUnit run. Scope is the **raw `.exec` only** - no HTML/XML/CSV
report generation. CI consumers (Codecov, SonarQube) read `.exec`/JaCoCo XML
directly; local human-readable reports are a deferred second rung.

Lands in both builds: `src/` (TypeScript) and `togo/` (Go), with tests in both.

## Why coverage is not another `outputFormat`

`testOptions.outputFormat` (`text` | `junit`) describes the *test-result*
format written by the JUnit Console Launcher. Coverage is an orthogonal axis:
you can want junit-XML results **and** coverage in the same run. So coverage is
a separate boolean, not a new enum value.

## How JaCoCo attaches

JaCoCo instruments bytecode at runtime via a Java agent. The usable agent is a
**classifier-qualified** artifact: `org.jacoco:org.jacoco.agent:0.8.12:runtime`
- this is the real `jacocoagent.jar` carrying the `Premain-Class` manifest. The
plain (classifier-less) `org.jacoco.agent` jar is only a wrapper that nests the
real agent inside as `/jacocoagent.jar`.

Running `java -javaagent:jacocoagent.jar=destfile=<dir>/jacoco.exec ...` over the
tests yields the binary `jacoco.exec`. That single JVM arg is the whole runtime
integration.

## Design

### 1. Config

Add to `TestOptionsSchema` (`src/config.ts`, `togo/internal/config/config.go`):

```
coverage: boolean = false
```

Reuse the existing `reportsDir` (default `./dist/test-results`) for the `.exec`
location; `jacoco.exec` and junit-XML coexist in that directory. No new path
constant.

### 2. Package layer: classifier support

`Coordinates` currently has no classifier, and `getArtifact` hardcodes the jar
URL as `${artifactId}-${version}.jar` (`src/packages/maven.ts:525`). This blocks
fetching a classified artifact cleanly.

Add an **optional** `classifier` to `Coordinates`:

- `src/packages/types.ts`: `classifier?: Classifier` on the interface; when
  present, `getArtifact` (and the store path) use
  `${artifactId}-${version}-${classifier}.jar`; when absent, behaviour is
  byte-for-byte unchanged.
- `togo/internal/packages/`: the same optional field and URL/path suffix.
- `storePathFor` gains the `-${classifier}` suffix so classified and plain
  artifacts of the same GAV do not collide on disk.

Only the classifier-present path is new; every existing (classifier-less)
call site keeps its current URL and store path. This is the alternative chosen
over extracting the nested `jacocoagent.jar`, which would need a zip reader
(no Node stdlib zip → a new dependency) and risk TS/Go divergence.

### 3. Testing module: agent download

In `src/testing/testing.ts` / `togo/internal/testing/testing.go`, mirror the
existing `CONSOLE_LAUNCHER` + `consoleLauncherJar` pair:

```
JACOCO_AGENT = org.jacoco:org.jacoco.agent:0.8.12  (classifier: runtime)
jacocoAgentJar(config, sources?) -> downloads to the package store on first use,
                                    returns the local path
```

Pinned like the launcher (a tool, never in `cappu.json` or the lockfile).

### 4. Run wiring

`-javaagent` is a JVM argument and must precede `-jar`. So when coverage is on,
`testRunArgs` prepends it:

```
java -javaagent:<agentJar>=destfile=<reportsDir>/jacoco.exec \
     -jar <launcher> execute --class-path ... --scan-class-path
```

`testRunArgs` currently takes `(config, launcherJar)`. Add an optional
`agentJar` parameter (TS) / equivalent (Go); when set, emit the `-javaagent`
prefix. The CLI (`src/cli/test.ts`, `togo/internal/cli/test.go`) downloads the
agent (like it already downloads the launcher) only when
`config.testOptions.coverage` is true, ensures `reportsDir` exists, and passes
the path in.

JaCoCo's default instrumentation scope is all loaded classes - no
include/exclude tuning in this iteration (add if a consumer asks).

### 5. Behaviour matrix

| `outputFormat` | `coverage` | Output |
| --- | --- | --- |
| `text` | `false` | stdout summary only (unchanged) |
| `junit` | `false` | + junit-XML in `reportsDir` (unchanged) |
| `text` | `true` | + `jacoco.exec` in `reportsDir` |
| `junit` | `true` | + junit-XML **and** `jacoco.exec` in `reportsDir` |

Coverage never changes stdout streaming or the process exit code (still the
launcher's).

## Testing

- **Config**: `coverage` defaults to `false`; parses `true`. Both builds.
- **`testRunArgs`**: with `coverage`/agent set, args begin with the
  `-javaagent:...=destfile=<reportsDir>/jacoco.exec` prefix before `-jar`;
  without it, args are unchanged. Both builds.
- **Package layer**: `getArtifact`/store path include `-${classifier}` when a
  classifier is set, and are unchanged when it is not. Both builds.
- **e2e** (both builds, mirroring the junit e2e): fetch the real console
  launcher **and** JaCoCo agent, compile and run a sample JUnit 5 test with
  `coverage: true`, assert `jacoco.exec` exists and begins with JaCoCo's magic
  header bytes `0xC0 0xC0`.

## Out of scope (deferred)

- HTML/XML/CSV report generation (`org.jacoco.cli` second pass).
- Include/exclude / package-scope tuning.
- Coverage thresholds / fail-under gates.
- TAP output (already tracked separately on `outputFormat`).
