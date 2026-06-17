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
| `fast-xml-parser` (POM / maven-metadata) | `encoding/xml` (stdlib) | later (Maven resolve) |
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

## CLI: per-command exit codes, not process.exit

The Node code calls `process.exit` inside handlers. In Go, each `RunX` returns
an `int` exit code and `main` calls `os.Exit` once (`cmd/cappu/main.go`). Arg
parsing is a small hand-rolled parser that interleaves flags and positionals
like Node's `util.parseArgs`; error messages mirror parseArgs for parity (hence
capitalized strings, ST1005 disabled for the package).

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
  equivalent. Network is faked with `httptest` (see `packages/maven_test.go`);
  `testcontainers-go` arrives with the commands that need it.

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
