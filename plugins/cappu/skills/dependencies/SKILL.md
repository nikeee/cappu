---
name: dependencies
description: "Use when adding, removing, upgrading or looking up Java dependencies in a cappu project, resolving 'version X loses to Y' conflicts, refreshing or verifying cappu-lock.json, checking dependencies for CVEs (cappu audit) or listing their licenses. Covers add, remove, update, outdated, search, show, tree, install, verify, cache, audit, licenses. Not for cappu.json fields in general - see setup."
---

# Manage dependencies in a cappu project

Dependencies are `"group:artifact": "version"` entries in four maps of
`cappu.json`; every version is literal (no BOM, parent POM or `${property}`).
Resolution reads POMs transitively from Maven Central style sources; conflicts
resolve nearest-wins and are reported as warnings. There are no exclusions,
forced versions, or `provided`/`runtime`/`optional` scopes. Coming from Maven
or Gradle? Use the `migrate` skill.

## Procedure

1. **Find the coordinate.** `cappu search <terms>` lists `group:artifact:version`
   matches; `cappu show group:artifact[:version]` prints a card (description,
   license, versions, direct dependencies, known vulnerabilities, how this
   project depends on it). Both print JSON under an agent (see Agent mode).
2. **Add it.** `cappu add implementation com.google.code.gson:gson:2.14.0`.
   Omit the version to take the latest stable.

   | Configuration | Use for |
   |---|---|
   | `implementation` | anything the code compiles against (also what Maven calls `provided`, `runtime`, `optional`) |
   | `api` | like `implementation`, but the type appears in your public API; published as Maven `compile` scope, whereas `implementation` is published as `runtime` |
   | `annotationProcessor` | the processor artifact; its API library still goes under `implementation` |
   | `testImplementation` | test-only libraries |

   `add` edits `cappu.json` (comments preserved), re-resolves, downloads into
   `.cappu/lib/`, and rewrites `cappu-lock.json`.
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
| `cappu outdated` | table of current, wanted and latest version per declared dep | 0 whether or not anything is outdated, so it cannot gate CI (1 no config, 2 lookup failed) |
| `cappu update` | bumps every declared dep to the newest conflict-free stable, rewrites `cappu.json` and the lock, installs | 0, also when nothing changed |
| `cappu add <cfg> g:a:<newer>` | bump one dependency | 0 ok, 1 not found |
| `cappu remove <cfg> g:a` | drop one and re-resolve | 0 |

`outdated` has no `--json`; the MCP tool `outdated` returns the same data as JSON.

## Security and compliance

| Command | Output | Exit |
|---|---|---|
| `cappu audit [--no-cache] [--format text\|sarif]` | OSV advisories per resolved transitive dep, with the tree path to each | 0 clean, **1 any finding**, 2 scan failed |
| `cappu licenses [--json]` | every resolved dep with a best-effort SPDX id | 0 |
| `cappu show g:a` | includes the OSV findings of that one package | 0 |

`audit` needs no lockfile and no installed jars, only `cappu.json`. Never pass
`--json` to `audit`; that is exit 2. Fixing a finding means bumping the
dependency that pulls the vulnerable version in, or pinning the vulnerable
transitive artifact to a fixed version under `implementation`.

## Cache

`cappu cache verify` checks the global download cache (`~/.cache/cappu`,
override with `CAPPU_PACKAGE_STORE`) against the recorded hashes;
`cappu cache clean` removes it. Both are safe; the next `install` re-downloads.

## MCP equivalents

When the cappu MCP server is attached (`mcp__cappu__*` tools), prefer these
read-only tools over shelling out; they exist only when the server found a
`cappu.json`: `search_packages(query)`, `latest_version("g:a")`,
`dependency_tree(coord?)`, `outdated()`, `audit()`, `licenses()`. Anything that
changes `cappu.json` or `.cappu/` (`add`, `remove`, `update`, `install`) is CLI
only.

## Agent mode

Under Claude Code (`CLAUDECODE` set) `search`, `show`, `tree` and `licenses`
print JSON without `--json`, and `audit` prints SARIF. There is no opt-out. Use
`cappu audit --format text` for the readable report. CI does not set these
variables; pass `--json` or `--format sarif` explicitly there.

## Gotchas checklist

- [ ] Wrong groupId? Install fails with `not found in any package source`; check with `cappu search`.
- [ ] `loses to` warnings read and, where relevant, the transitive dep pinned explicitly.
- [ ] `cappu-lock.json` committed; CI runs `cappu install --locked`.
- [ ] Missing-package errors in tests may be an undeclared transitive test dep; add it under `testImplementation`.
- [ ] Processor added under `annotationProcessor` AND its API under `implementation`.
- [ ] `outdated` is informational only; `audit` is the gate.
- [ ] Private repositories: resolution has no auth, only anonymous URLs in `packageSources`.
