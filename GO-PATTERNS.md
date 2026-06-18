# GO-PATTERNS.md

Migration patterns for the TypeScript -> Go port of cappu (see issue #18). The
Go build lives in `./togo`. Patterns here are drawn from the `tsgo`
(Microsoft's TypeScript-Go, vendored at `./TypeScript-Go`) study plus decisions
made for this project. **Amend this file whenever a new pattern is found.**

## Project layout

Mirror tsgo: a thin `cmd/` entry point and all real code under `internal/` so
the import surface stays private.

```
togo/
  go.mod                module github.com/nikeee/cappu  (go 1.26)
  cmd/cappu/main.go     arg parse + dispatch (mirrors src/cli/main.ts)
  internal/
    cli/        one file per subcommand (rage.go, cache.go, version.go, ...) + stubs.go
    config/     cappu.json load/validate (config.go), JSONC edits (edit.go), SPDX (spdx.go)
    cache/      per-user download cache dir
    semver/     npm-style version bumping
    packages/   Maven coordinate domain types + sources (search path so far)
    lockfile/   cappu-lock.json model + verify
    meta/       version / issue-tracker constants (come from package.json in Node)
  .golangci.yml  .editorconfig  Makefile
```

tsgo reference: `TypeScript-Go/cmd/tsgo`, `TypeScript-Go/internal/*`,
`TypeScript-Go/go.mod` (module `github.com/microsoft/typescript-go`, `go 1.26`).

## Library / builtin mapping

The first migration step (issue #18) is to replace bespoke code with a stdlib
or common library. Current decisions:

| TypeScript (Node) | Go replacement | Status |
|---|---|---|
| `comment-json` (round-trip edit) | `tidwall/sjson` (write) + `tidwall/jsonc` (read) | done (M1) |
| `zod` (validate + JSON-Schema gen) | `encoding/json` + struct defaults + hand validation; `invopop/jsonschema` for schema gen | M1 (schema gen deferred with `init`) |
| `fast-xml-parser` (POM / maven-metadata) | `encoding/xml` (stdlib) | done (M2) |
| `cli-progress` | `schollz/progressbar/v3` | later (install) |
| `@inquirer/prompts` | `charmbracelet/huh` | later (`init`) |
| `vscode-languageserver` | TBD (`go.lsp.dev/protocol` vs roll-our-own like tsgo) | later (lsp) |
| `testcontainers` (JS) | `testcontainers-go` | later (publish e2e) |
| in-house `zipReader.ts` / `zipWriter.ts` | `archive/zip` (stdlib) | later (compile) |
| sha256 / glob / semver | `crypto/sha256`, `path/filepath.WalkDir`, hand semver | M1 where touched |

### comment-json has no Go equivalent

`comment-json` round-trips comments **and** formatting through a programmatic
edit; no Go library does this. The chosen stand-in: read with `tidwall/jsonc`
(strips comments + trailing commas to plain JSON for `encoding/json`), and write
edits with `tidwall/sjson`, which rewrites only the targeted value's byte span
and leaves the surrounding bytes - comments included - intact. See
`internal/config/edit.go` and its test asserting a comment survives a
`version` bump. This works for the flat top-level edits cappu makes
(`version`, dependency map entries); a structural rewrite would need a real CST.

Known divergence: sjson *replaces* existing values in place (formatting kept),
but *inserts* a new key compactly (`,"k":"v"}` with no surrounding whitespace).
So `cappu add`'s newly-added dependency line is not re-indented like the Node
build's comment-json output - it stays valid JSONC with comments intact, just
compact. `cappu update` (which only overwrites existing values) is unaffected.
Pretty-printing a freshly inserted key would require reimplementing
comment-json's CST, which is deferred.

## Branded types -> Go named types (NOT aliases)

The TS code uses type-only brands (`type GroupId = string & {__brand}`). Issue
#18 guessed "type aliases"; tsgo does use aliases (`type Expression = Node`).
**We deviate**: brands become real Go *named types* (`type GroupID string`,
`type Sha256 string`, `type Offset int`), because aliases give zero nominal
safety while named types make the compiler refuse to mix a `GroupID` with an
`ArtifactID` - matching the project's "always brand new domain primitives" rule.

- Conversions are explicit at the producing boundary: `GroupID(s)`,
  `string(g)`. Keep a single constructor (`NewCoordinates`) as the cast point.
- Use Go initialism casing: `GroupID`, not `GroupId` (staticcheck ST1003).
- Examples: `internal/packages/types.go`, `internal/lockfile/lockfile.go` (`Sha256`).

tsgo uses aliases because its "brands" are *semantic groupings of one shared
`Node` type* (Expression/Statement are all `*ast.Node`), where a named type
would break assignability. Our brands wrap distinct primitives, so named types
fit. Reach for tsgo's alias pattern only when porting the compiler AST.

## XML: fast-xml-parser -> encoding/xml (+ custom map unmarshal)

POMs parse into Go structs with `xml:"..."` tags matching local element names
(namespaces ignored). The one place stdlib `encoding/xml` cannot map directly is
`<properties>`, whose child element names are arbitrary keys: implement
`UnmarshalXML` on a `map[string]string` named type that walks tokens and
`DecodeElement`s each child into the map. See `internal/packages/maven.go`
(`xmlProperties`). The effective-POM logic (parent-chain merge, `${...}`
interpolation, `<dependencyManagement>` fill, scope=import BOMs) ports
straight across as plain Go - see `effectiveMetadata` / `importedManaged`.

Determinism note: Go `map` iteration is unordered, so anything whose output
order is observable sorts its keys first - `sources.rootsOf` sorts dependency
keys, and `InMemoryPackageSource` keeps an insertion-order key slice for
`Search` (the Node build relied on `Map`/object insertion order).

## Codegen JSON for fixed on-disk schemas (easyjson)

cappu-lock.json has a fixed schema we own, so its marshalers are generated by
`easyjson` (reflection-free) rather than hand-written or reflected. The tagged
types (`//easyjson:json` on `Lockfile`/`LockedPackage`/`coordinates`) drive
`//go:generate easyjson lockfile.go`, producing `lockfile_easyjson.go`. The
generated `MarshalJSON`/`UnmarshalJSON` satisfy the stdlib interfaces, so
`json.Marshal`/`json.Unmarshal`/`MarshalIndent` pick them up transparently - no
call-site changes, byte-identical output.

Rules:
- **Commit the generated file** (Go never auto-runs `go generate`; uncommitted
  generators break `go build`/`go get`). golangci-lint and vet skip it via its
  `DO NOT EDIT` header.
- A CI step (`make generate-check`) regenerates and fails on any diff, so a
  struct change without a matching regenerate cannot land.
- This is reserved for fixed schemas we own. cappu.json stays on stdlib +
  tidwall/jsonc (it needs comment-tolerant reading and sjson edits, not speed).

Considered and deferred: codegen for the POM parser. There is no easyjson-style
codegen marshaler for XML in common use (`encoding/xml` is reflection-based; the
XSD->struct generators emit structs, not faster parsers). Generating from the
Maven POM XSD would bloat a struct with hundreds of fields when we read ~8, and
POM parsing is network-bound cold-path - so it would hurt maintainability for no
perf gain. The hand-written subset in `maven.go` stays.

## Config: zod schema -> struct + manual defaults/validation

zod does parse-time defaults + refinements. In Go:
- Model each section as a struct with `json` tags (`internal/config/config.go`).
- A nil slice/map after `json.Unmarshal` means the key was absent (matches
  zod's `.default()` firing only on `undefined`); apply defaults in
  `applyDefaults()`. Present-but-empty (`[]`) is preserved, like zod.
- Booleans that default to `true` use `*bool` so unset != false.
- Port regex/refinements (`MavenID`, `Semver`, SPDX) into `validate()`.
- Do NOT set `DisallowUnknownFields`: an unknown key (e.g. `$schema`) is
  ignored, matching zod.

## CLI: kong (declarative) + per-command exit codes

Arg parsing is **kong** (`alecthomas/kong`): the whole command surface is one
`CLI` struct in `cmd/cappu/main.go`, one field per subcommand, flags/args/help
as struct tags. kong parses `os.Args` into it and generates `--help`/`--version`,
so neither a parse loop nor a `USAGE` string is hand-maintained. There is no
good *codegen* CLI parser in Go; declarative struct tags are the idiomatic
"schema-based" equivalent (reflection at startup, negligible for a CLI).

The Node handlers call `process.exit` with specific codes (0/1/2). Here each
`cli.RunX` still returns an `int`; a command's `Run` wraps a non-zero code in a
`cmdErr` so `main` can recover it with `errors.As` (kong wraps the returned
error, so a bare type assertion misses it) and `os.Exit` with the right code.
Config loads lazily via `appState.config()` so the pre-config commands (init,
cache, self-upgrade, rage) never touch a possibly-broken cappu.json.

Accepted divergences from the Node CLI (we let kong own formatting): `--help`
layout differs, and kong exits usage errors with its own code (80) rather than
the Node build's 2. Command behaviour, stdout, and the 0/1/2 codes our own
handlers return are unchanged.

## Static linking / build

Per issue #18 the binary must run on any platform regardless of libc. Build
with cgo off and stripped, exactly like tsgo (`TypeScript-Go/Herebyfile.mjs`
sets `CGO_ENABLED=0`, `-ldflags=-s -w`, `-trimpath`):

```
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/cappu ./cmd/cappu
```

`file dist/cappu` reports "statically linked"; `ldd` says "not a dynamic
executable". Cross-compile targets (mirroring the Node SEA targets): linux
amd64/arm64, darwin arm64, windows amd64 - see `Makefile` `build-all`.

## Tooling

- **Format**: `gofmt` (standard tooling). CI fails if `gofmt -l .` is non-empty.
- **Lint**: `golangci-lint` v2 (`.golangci.yml`). tsgo's custom AST linters
  (`_tools/customlint`) are dropped - they are TypeScript-AST specific.
- **Vet**: `go vet ./...`.
- **Test**: `go test ./...`, table-driven; every Node `*.test.ts` gets a Go
  equivalent. Network is faked with `httptest` (see `packages/maven_test.go`).
  `testcontainers-go` mirrors the Node testcontainers e2e: `publish`'s
  round-trip (`internal/publish/e2e_test.go`) spins a Reposilite container,
  publishes a javac-built jar, and installs it back. It self-skips when Docker
  or javac is missing, so CI without them still passes; the Go CI installs both.

## javac-delegation jar build (internal/build)

`cappu publish` (and later `cappu compile`'s default path) build a jar by
delegating to `javac` and zipping the `.class` output with `archive/zip` and a
minimal manifest - the Node build's non-experimental compile path. It lives in
`internal/build` (`BuildJar`/`SourceJavaFiles`) so the eventual `compile` port
extends it rather than duplicating it. Requires javac on PATH; the experimental
in-house compiler is a separate, later effort.

## Patterns to fill in later (compiler / LSP milestones)

Seeded from tsgo; not yet exercised here.

- **Enums / SyntaxKind**: `type Kind int16` + `iota`, `//go:generate stringer`
  for `String()`. tsgo: `internal/ast/kind_generated.go`.
- **Tagged unions**: one `Node` struct with a `Kind` discriminator + an
  interface `data` field + generated `As*()` casts; union "types" as aliases to
  the base struct. tsgo: `internal/ast/ast.go`, `ast_generated.go`.
- **Arena allocator**: batch node allocation via a backing slice, not
  `sync.Pool`. tsgo: `internal/core/arena.go`.
- **Node factory + visitor**: arena-backed factory methods with update-if-changed
  structural sharing; callback `ForEachChild`/`VisitEachChild`. tsgo:
  `internal/ast/visitor.go`. (Issue #18: no multi-threading yet.)
