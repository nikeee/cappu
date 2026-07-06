# Maven/Gradle -> cappu migration findings

Dogfooding cappu (`0.1.13`, Go build) against three real-world Maven projects,
per issue #37. Each project was cloned, translated to `cappu.json` by hand (there
is no `cappu migrate` command), stripped of its Maven files, and built with
`cappu install` / `cappu compile` / `cappu test`. This records what worked, what
broke, and the feature gaps that surfaced.

## Results per project

| Project | Module | Build (`compile`) | Tests (`test`) | Notes |
|---------|--------|-------------------|----------------|-------|
| microexpressions | (single) | ✅ 56 sources | ✅ 19/19 pass | Textbook migration. 4 compile deps, junit5 test dep, release 17. Nothing special. |
| commons-lang | (single) | ✅ 263 sources -> 417 classes, release 8 | ⚠️ compiles, ~94k/96k fail at runtime | Main library has **zero** compile deps and built clean. Test suite needed an undeclared `org.ow2.asm:asm` dep; then compiles but mass-fails at runtime (see below). |
| mapstruct | core | ✅ 53 sources -> `mapstruct-1.7.0-SNAPSHOT.jar` (54 classes), release 8 | not run | Only test deps (junit5, assertj); main compiles with no deps. |
| mapstruct | core-jdk8 | ➖ nothing to build | ➖ | `mapstruct-jdk8` is a deprecated, **empty relocation artifact** (pom only, no sources). No cappu analog; skip it. |
| mapstruct | processor | ✅ 360 sources -> 576 classes + 40 generated Gems, release 21 | not run | Annotation-processor-heavy. gem-processor ran and generated the `*Gem` sources correctly once the classpath was fixed (see gap #3). |

Overall: **4 of 4 buildable targets compile successfully**, including a real
annotation-processor pipeline and a hand-wired multi-module build. The only
runtime problem is commons-lang's test suite.

## commons-lang test mass-failure

`cappu test` compiles all 360 test files (after adding the undeclared asm dep),
runs, and reports:

```
[     95753 tests found           ]
[      1580 tests successful      ]
[     94166 tests failed          ]
```

Nearly every test fails with the same mangled assertion signature, e.g.:

```
Expected null, actual: {A=null, []=null} ==> expected: <true> but was: <false>
```

Not investigated to root cause (issue #37 says "note them down"). Leading
suspect: **dependency version-conflict resolution downgraded JUnit**. Install
emitted:

```
warning: org.junit.jupiter:junit-jupiter-api: version 5.14.3 (via junit-jupiter) loses to 5.11.2
warning: org.junit.platform:junit-platform-engine: version 1.14.3 (via junit-jupiter-engine) loses to 1.11.2
```

i.e. the declared `junit-jupiter:5.14.3` was resolved *down* to 5.11.2 by some
other constraint, producing a jupiter API/engine skew that plausibly breaks
assertion/parameterized evaluation at runtime. Unconfirmed - flagged for
follow-up. Under Maven these versions come pinned from `commons-parent`, which
cappu does not read (gap #2).

## Feature gaps & migration pain points (prioritized)

### 1. No `cappu migrate` command  (highest value)
The entire `pom.xml` -> `cappu.json` translation is manual: coordinates,
scopes, java release, source layout, annotation processors. Every project here
needed hand translation. This is the single biggest opportunity - an automated
importer would remove ~90% of the effort and most of the error surface below.
The migration skill (`.claude/skills/migrate-to-cappu/`) is the interim manual
procedure.

### 2. No BOM / parent-POM / `dependencyManagement` / `${property}` resolution
cappu.json needs every version pinned literally. In practice we had to fetch and
grep external POMs by hand to resolve managed versions:
- commons-lang inherits junit/mockito/jmh versions from `commons-parent:102` -
  had to download `commons-parent-102.pom` to find `commons.mockito.version` etc.
- mapstruct modules inherit ~40 managed versions from `parent/pom.xml` and BOM
  imports (junit-bom, kotlin-bom, arquillian-bom).
This is the most time-consuming part of a real migration.

### 3. `compilerOptions.classPath` **replaces** the defaults, it does not append
Setting any custom `classPath` silently drops the default entries - most
importantly `./.cappu/lib/classes`, where installed dependencies live. On the
mapstruct processor, adding the sibling `core` jar to the classpath knocked all
resolved dependencies (freemarker, kotlin-metadata, gem-api, jaxb) off the
compile path, producing 85+ `cannot find symbol` errors that looked like missing
deps but were a classpath-shadowing bug. Fix was to re-list the default:
```jsonc
"classPath": ["./.cappu/lib/classes", "../core/dist/mapstruct-1.7.0-SNAPSHOT.jar"]
```
Either `classPath` should append to defaults, or the docs/schema must shout this.
High-severity footgun for any project needing a custom classpath entry.

### 4. No multi-module / workspace concept
mapstruct is one reactor build; cappu modeled it as three independent projects
with a **hand-maintained build order** (core -> processor) and the upstream
module wired in as a sibling jar path (gap #3). There is no `cappu build` across
modules, no inter-module dependency declaration, no shared version catalog.
Workable, but entirely manual.

### 5. Aggregator / relocation modules have no analog
`mapstruct-jdk8` is a pom-only relocation stub. Maven "builds" it (produces a
relocation POM); cappu has nothing to represent it. Migration answer is "skip,"
but a migrator needs to recognize `<packaging>pom</packaging>` and
`<relocation>` modules and not try to build them.

### 6. Maven scopes collapse to `implementation`
cappu buckets are `api` / `implementation` / `annotationProcessor` /
`testImplementation`. Maven `provided`, `optional`, and `runtime` have no
equivalent, so mapstruct processor's `provided` deps (core, kotlin-metadata,
jaxb-api) all became `implementation` - semantically lossy (they'd be bundled
where Maven would exclude them from downstream consumers). Fine for building,
wrong for publishing.

### 7. Undeclared-but-transitively-needed test deps
commons-lang's `TestClassBuilder` imports `org.objectweb.asm` although no asm
dependency is declared in its POM at this commit (it arrives transitively / via
a profile under Maven). A literal scope translation misses it; we had to add
`org.ow2.asm:asm` by hand. A migrator should compile-check and surface missing
packages.

### 8. Cosmetic: SPDX license-mapping warnings
Common licenses warn on every install and clutter output:
```
warning: org.openjdk.jmh:jmh-core:1.37: license "GNU General Public License (GPL), version 2, with the Classpath exception" has no SPDX mapping
warning: jakarta.xml.bind:jakarta.xml.bind-api:3.0.1: license "Eclipse Distribution License - v 1.0" has no SPDX mapping
```
Harmless, but the SPDX table should cover GPL-2.0-with-classpath-exception and
EDL/EPL.

## Things that worked well
- Transitive dependency resolution from Maven Central is correct and fast
  (cappu reads POMs internally even though the user-facing config can't).
- Annotation processors work end-to-end: gem-processor generated 40 `*Gem`
  sources for mapstruct and they compiled into the 576-class output.
- `cappu test` runs JUnit 5 (incl. the Vintage engine) with a clean tree view;
  microexpressions passed 19/19 untouched.
- Standard `src/main/java` + `src/test/java` layout needs zero configuration.
- A nonexistent coordinate version fails install loudly (exit 1), it is not
  silently skipped.

## Reproduction
Per project: write `cappu.json` (see the migrate-to-cappu skill), remove Maven
files (`pom.xml .mvn mvnw mvnw.cmd`), then `cappu install && cappu compile`
(and `cappu test`). The concrete configs used are in the skill's examples.
