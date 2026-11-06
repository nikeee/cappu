---
name: dependencies
description: "Use when adding, removing, upgrading or looking up Java dependencies in a cappu project, resolving 'version X loses to Y' conflicts, refreshing or verifying cappu-lock.json, checking dependencies for CVEs (cappu audit) or listing their licenses. Covers add, remove, update, outdated, search, show, tree, install, verify, cache, audit, licenses. Not for cappu.json fields in general - see setup."
---

# Manage dependencies in a cappu project

Dependencies are `"group:artifact": "version"` entries in four maps of
`cappu.json`, plus `dependencies.platform`, the BOMs (`"group:artifact":
"version"`) whose `<dependencyManagement>` manages versions. A version is
literal, or `""` to take it from a platform (an error when no platform manages
that artifact). An entry may be an object instead of the version string:

```jsonc
"dependencies": {
  "platform": { "org.springframework.boot:spring-boot-dependencies": "4.1.0" },
  "implementation": {
    "org.springframework.boot:spring-boot-starter-web": "",      // version from the platform
    "com.google.code.gson:gson": "2.13.1",
    "org.apache.commons:commons-lang3": { "version": "3.17.0", "exclude": ["commons-logging:commons-logging", "org.slf4j:*"] },
    "io.netty:netty-transport-native-epoll": { "classifier": "linux-x86_64" }  // no version: managed
  }
}
```

`exclude` patterns are `group:artifact`, either side may be `*`; they prune
the subtree below that dependency. `classifier` selects the classified jar;
it is part of the identity, so the plain and the classified jar of one
artifact can both be on the classpath. No `${property}` placeholders.

Resolution reads POMs transitively from Maven Central style sources with Maven
semantics: parent chain, `<dependencyManagement>` and imported BOMs fill
versions, scopes and exclusions; `<exclusions>` prune the subtree below their
edge; `<classifier>` and `<type>` are honored (`test-jar` is the `tests`
classifier, a `pom`-typed dependency contributes its dependencies and no jar;
`cappu tree` labels these `g:a:v:classifier` / `g:a:v:pom`). A platform
version overrides whatever a transitive POM declares (Maven
`dependencyManagement` semantics; `cappu tree` marks such nodes `(managed)`),
while a version written in cappu.json always wins over the platform (its
transitive requests then mediate against the pin as usual). When two platforms
manage the same artifact the first declared wins (file order; `cappu add
platform` appends, so a new BOM has the lowest precedence). Everything else
resolves nearest-wins and conflicts are reported as warnings. `api`,
`implementation`, `compileOnly` and `runtimeOnly` resolve as ONE graph (Maven
`compile`/`provided`/`runtime` style): nearest wins across them, a
`compileOnly`/`runtimeOnly` root keeps its scope even when a compile
dependency also requests that artifact (so `compileOnly servlet-api:6` beats a
transitive `servlet-api:4` and lands in `lib/compile-only`, not in
`lib/classes`), and whatever only such a root reaches follows it into
`lib/compile-only` / `lib/runtime-only`. Coming from Maven or Gradle? Use the
`migrate` skill.

## Procedure

1. **Find the coordinate.** `cappu search <terms>` lists `group:artifact:version`
   matches; `cappu show group:artifact[:version]` prints a card (description,
   license, versions, direct dependencies, known vulnerabilities, how this
   project depends on it). Both print JSON under an agent (see Agent mode).
2. **Add it.** `cappu add implementation com.google.code.gson:gson:2.14.0`.
   Omit the version to take the latest stable, or, when a platform manages
   the artifact, to write `""` (the platform's version; printed as
   `added implementation g:a (version from platform)`).
   `cappu add platform org.springframework.boot:spring-boot-dependencies:4.1.0`
   adds a BOM. `--classifier <c>` and `--exclude <group:artifact>` (repeatable)
   write the object form; a platform entry takes neither (exit 2).

   | Configuration | Use for |
   |---|---|
   | `implementation` | anything the code compiles against and needs at runtime (Maven `compile`; also `optional`) |
   | `api` | like `implementation`, but the type appears in your public API; published as Maven `compile` scope, whereas `implementation` is published as `runtime` |
   | `compileOnly` | needed to compile, present at runtime from elsewhere (servlet API, Lombok, an SPI the container provides): on javac's classpath, never in a fat jar, a `run` or a debuggee; published as Maven `provided` |
   | `runtimeOnly` | loaded by name, never referenced in code (JDBC drivers, logging backends): off javac's classpath, in the fat jar, `run`, tests and the debuggee; published as Maven `runtime` |
   | `annotationProcessor` | the processor artifact; its API library still goes under `implementation` |
   | `testImplementation` | test-only libraries |
   | `platform` | a BOM (`spring-boot-dependencies`, `junit-bom`, `jackson-bom`, ...); not a classpath entry, published as a `scope=import` entry in `<dependencyManagement>` |

   Aliases: `a`, `i`, `co`, `ro`, `ap`, `ti`, `p`. `add` edits `cappu.json` (comments
   preserved; an existing entry keeps its classifier and exclusions unless
   the flags replace them, so `cappu add i g:a:1.1` is a version bump),
   re-resolves, downloads into `.cappu/lib/`, and rewrites `cappu-lock.json`.
3. **Read the install output.** A line
   `warning: <artifact>: version A (via X) loses to B` means a version conflict
   was resolved against a nearer or declared version. If tests misbehave at
   runtime, pin the losing transitive dependency explicitly under
   `implementation` at the version you want. `cappu tree` shows the resolved
   graph per configuration; the MCP tool `dependency_tree` with a `coord`
   answers "what pulls this onto the classpath".
4. **Keep the lock honest.** Commit `cappu-lock.json`. `cappu install` follows
   it exactly and verifies every download against its SHA-256; it only resolves
   afresh when the lock is missing. `cappu install --locked` fails without
   downloading anything when the lock is stale or missing: use it in CI and to
   detect a drifted lock. `cappu verify` checks the jars already in
   `.cappu/lib` against the lock (exit 1 on modified or missing; all output on
   stderr).

## Upgrading

| Command | Effect | Exit |
|---|---|---|
| `cappu outdated` | table of current, wanted and latest version per declared dep and platform; `""` (managed) entries are skipped, their version moves with the platform | 0 whether or not anything is outdated, so it cannot gate CI (1 no config, 2 lookup failed) |
| `cappu update` | bumps every platform, then every declared dep, to the newest conflict-free stable within its major, rewrites `cappu.json` and the lock, installs | 0, also when nothing changed |
| `cappu add <cfg> g:a:<newer>` | bump one dependency (or `cappu add platform g:bom:<newer>` a BOM) | 0 ok, 1 not found |
| `cappu remove <cfg> g:a` | drop one and re-resolve (`platform` works here too) | 0 |

`outdated` has no `--json`; the MCP tool `outdated` returns the same data as JSON.

## Security and compliance

| Command | Output | Exit |
|---|---|---|
| `cappu audit [--no-cache] [--format text\|sarif] [--allow-stale-ignores]` | OSV advisories per resolved transitive dep, with the tree path to each | 0 clean or every finding ignored, **1 any unignored finding or stale ignore entry**, 2 scan failed |
| `cappu licenses [--json]` | every resolved dep with a best-effort SPDX id | 0 |
| `cappu show g:a` | includes the OSV findings of that one package | 0 |

`audit` needs no lockfile and no installed jars, only `cappu.json`. Never pass
`--json` to `audit`; that is exit 2. Fixing a finding means bumping the
dependency that pulls the vulnerable version in, or pinning the vulnerable
transitive artifact to a fixed version under `implementation`.

Accepting a finding instead (Go build of cappu only; the Node build ignores the
field and rejects the flag): list it under `auditOptions.ignore` in
`cappu.json` with a reason.

```jsonc
"auditOptions": {
  "ignore": [{ "id": "CVE-2021-44228", "reason": "JNDI lookups disabled" }]
}
```

- `id` is the advisory's OSV id (`GHSA-...`) or one of its `CVE-` aliases,
  case-insensitive. Other alias kinds do not match.
- `reason` is required. Duplicate ids are a config error.
- An ignored finding is still printed, suffixed `(ignored: <reason>)`, and the
  summary ends in `, N ignored`. In SARIF it keeps its result with a
  `suppressions` entry. It no longer counts towards exit 1.
- An entry that matches no finding is an `error:` on stderr and exit 1, so the
  list cannot rot: remove the entry once the dependency is fixed.
  `--allow-stale-ignores` turns that into a `warning:`.
- Only add an entry when the user decided to accept the risk; never to make a
  failing audit pass.

## Cache

`cappu cache verify` checks the global download cache (`~/.cache/cappu`,
override with `CAPPU_PACKAGE_STORE`) against the recorded hashes;
`cappu cache clean` removes it. Both are safe; the next `install` re-downloads.

## MCP equivalents

When the cappu MCP server is attached (`mcp__cappu__*` tools), prefer these
read-only tools over shelling out; they exist only when the server found a
`cappu.json`: `search_packages(query)`, `latest_version("g:a")`,
`dependency_tree(coord?)`, `outdated()`, `audit()`, `licenses()`. In the Go
build, `audit()` marks an ignored advisory with `ignored: "<reason>"` and lists
unmatched entries in `staleIgnores`. Anything that
changes `cappu.json` or `.cappu/` (`add`, `remove`, `update`, `install`) is CLI
only.

## Agent mode

Under Claude Code (`CLAUDECODE` set) `search`, `show`, `tree` and `licenses`
print JSON without `--json`, and `audit` prints SARIF. There is no opt-out. Use
`cappu audit --format text` for the readable report. CI does not set these
variables; pass `--json` or `--format sarif` explicitly there.

## Gotchas checklist

- [ ] Wrong groupId? Install fails with `not found in any package source`; check with `cappu search`.
- [ ] `g:a: no version given and no platform manages it`: an entry is `""` but no platform lists that artifact; pin a version or add the right BOM.
- [ ] `platform g:a:v: not found in any package source`: the BOM coordinate or version is wrong.
- [ ] A transitive version differs from what the library declares? Check `cappu tree` for `(managed)`: the platform decided it.
- [ ] `-SNAPSHOT` versions are mutable: never served from the global store, never metadata-cached, re-downloaded on every install, and the lock's SHA-256 is recorded but not enforced for them (`cappu verify` passes a present snapshot). Rebuild the sibling, run `cappu install`, done.
- [ ] `loses to` warnings read and, where relevant, the transitive dep pinned explicitly.
- [ ] `cappu-lock.json` committed; CI runs `cappu install --locked`.
- [ ] Missing-package errors in tests may be an undeclared transitive test dep; add it under `testImplementation`.
- [ ] Processor added under `annotationProcessor` AND its API under `implementation`.
- [ ] `outdated` is informational only; `audit` is the gate.
- [ ] Private repository? `packageSources` entry `{ "url": ..., "auth": "NEXUS" }` plus `CAPPU_SOURCE_NEXUS_TOKEN` (or `_USERNAME` + `_PASSWORD`) in the environment; `no credentials: set CAPPU_SOURCE_NEXUS_...` means they are missing. A local repo (`~/.m2/repository`, a path) is a plain entry.
