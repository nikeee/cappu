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
| in-house `mapPool` bounded download pool | `p-limit` (TS) / `golang.org/x/sync/errgroup` `SetLimit` (Go) | done |

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

### semver libraries evaluated and rejected (keep the hand bump)

The version bump (`internal/semver/semver.go`, `src/version.ts`) stays
hand-rolled. npm `semver`'s `inc()` does not match our behaviour - on a
prerelease it strips without incrementing (`inc('1.2.3-rc1','patch')` ->
`1.2.3`), whereas we drop prerelease *and* bump (-> `1.2.4`). And
`golang.org/x/mod/semver` has no increment function at all (parse/compare/
canonical only), so the Go side would still hand-write the bump. Two deps for a
behaviour change on one side and nothing on the other: not worth it.

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

## Env-gated CI annotations (GitHub workflow commands)

Issue #21: when running under a GitHub-Actions-compatible runner, every
error/warning that already goes to stderr is *also* echoed as a workflow command
(`::error file=...,line=...::msg`) so it surfaces as an inline annotation.

- **Detection is an injected env lookup, not a global read** -
  `AnnotationsEnabled(env func(string) string)` (Go) / `annotationsEnabled(env)`
  (TS), mirroring the existing `ColorEnabled`/`colorEnabled` split so it stays
  unit-testable without mutating the real environment. Triggers on any of
  `GITHUB_ACTIONS`/`FORGEJO_ACTIONS`/`GITEA_ACTIONS` == `"true"` (Forgejo/Gitea
  Actions speak the same syntax); bare `CI=true` is deliberately not a trigger.
- **The annotation is additive, never a replacement.** Each emission site keeps
  its existing `Fprintf(os.Stderr, ...)` and adds one `emitAnnotation(...)` call
  right after; the single `renderDiagnostics` chokepoint covers compile/test/
  publish diagnostics, the rest are location-less `emitAnnotation("error"|...)`.
- **Escaping is two-tier** (`strings.NewReplacer`): message *data* escapes
  `% \r \n`; property *values* (file/line/col) additionally escape `: ,`.
- **`emitAnnotation` is unexported; one exported `EmitErrorAnnotation` wrapper**
  exists only for the config-load error path in package `main` (the lazy
  `appState.config()` lives outside package `cli`).
- **E2E test trap:** cappu's own CI sets `GITHUB_ACTIONS=true`, which inherits
  into the cappu processes the example tests spawn and (since Go captures
  `CombinedOutput()`) leaks annotation lines into exact-output assertions. The
  fix is a `childEnv()` helper in `cmd/cappu/examples_test.go` that strips the
  three markers from `os.Environ()` before spawning. (The TS e2e tests read only
  stdout, where annotations never go, so no scrub is needed there.)

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

## Per-OS strategy via build tags (syscalls)

When a feature needs a different syscall per platform (issue #35: clonefile on
macOS, hardlink on Linux, plain copy elsewhere), don't branch on `runtime.GOOS`
at runtime - split the implementation across `//go:build`-tagged files so each
binary compiles exactly one path (the Go analogue of the TS "strategy chosen at
startup"). See `internal/copyfile`: a shared `copyfile.go` (rm-then-chmod
wrapper + `plainCopy` fallback) plus `copyfile_darwin.go`
(`unix.Clonefile`), `copyfile_linux.go` (`os.Link`), and `copyfile_other.go`
(`//go:build !darwin && !linux`), each exporting the same `materializeImpl`.

`golang.org/x/sys/unix` (for `Clonefile`) is pure syscall wrappers, so it stays
`CGO_ENABLED=0`-compatible - static linking is unaffected. Adding it promotes
`golang.org/x/sys` from indirect to a direct require (`go mod tidy`).

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

## Compiler front end (internal/compiler)

The Java front end (scanner → parser → binder → checker → services) is ported
step by step, each step with its tests. Patterns in use:

- **SyntaxKind**: `type SyntaxKind int` + `iota` in the exact order of
  `src/compiler/types.ts`, so numeric values (and baselines) match the Node
  build. stringer can't be used because the `First*/Last*` range markers alias
  real kinds (duplicate values), so `String()` reads a hand-written name table
  (`syntaxKindNames`, markers excluded); a test asserts `len == kindCount`.
- **Scanner** (tsgo structure): a `Scanner` struct with methods (the TS original
  is a closure), reporting via an `ErrorCallback` rather than panicking.
  `LookAhead`/`TryScan` are generic free functions (`func[T any](*Scanner, func() T) T`)
  since Go methods can't be generic; `TryScan` restores on a falsy (zero) result.
- **Positions are byte offsets** (tsgo's model), not the TS scanner's UTF-16
  `charCodeAt` units. All fixtures are ASCII, so they match the Node build's
  offsets; `charCodeAt(i)` returns -1 past end (mirroring JS NaN comparisons).
  Non-ASCII identifier bytes (>0x7f) all pass `isIdentifierStart`, so UTF-8
  identifiers scan correctly; the UTF-16 boundary (if ever needed) is an LSP
  concern, as in tsgo.

## Patterns to fill in later (parser / checker)

Seeded from tsgo; not yet exercised here.

- **Enums / SyntaxKind**: see above (done for the front end).
- **Tagged unions**: one `Node` struct with a `Kind` discriminator + an
  interface `data` field + generated `As*()` casts; union "types" as aliases to
  the base struct. tsgo: `internal/ast/ast.go`, `ast_generated.go`.
- **Arena allocator**: batch node allocation via a backing slice, not
  `sync.Pool`. tsgo: `internal/core/arena.go`.
- **Node factory + visitor**: arena-backed factory methods with update-if-changed
  structural sharing; callback `ForEachChild`/`VisitEachChild`. tsgo:
  `internal/ast/visitor.go`. (Issue #18: no multi-threading yet.)

## Binder / resolver / checker (semantic front end)

- **Symbols on the Node struct**: the binder attaches `Symbol *Symbol` and
  `Locals SymbolTable` directly to the shared `Node` struct (the TS build sets
  `node.symbol` / `node.locals` ad hoc). `SymbolTable` is `map[string]*Symbol`;
  `SymbolFlags` is a bitset (`type SymbolFlags int` + `1 << iota`).
- **Module-level state -> struct or package var**: the TS binder uses module-level
  `let file/parent/container`; ported as a `binder` struct threaded through the
  walk. The resolver's TS module-level `WeakMap` memos and `resolvingSupertypes`
  guard become package-level maps keyed by `*Node`/`*Symbol` (faithful to the TS
  module scope; safe because keys are pointer-identity and reparses make fresh
  nodes). The checker instead holds its caches on a `Checker` struct (it is
  per-program, created by `NewChecker`).
- **WeakMap -> map keyed by pointer**: `WeakMap<Node, X>` becomes `map[*Node]X`.
  A "null = resolved to nothing" sentinel becomes a small `{val, computed bool}`
  entry struct so a cached nil is distinct from "not yet computed".
- **The Type model is a tagged union like Node**: one `Type` struct with a
  `TypeKind` discriminator and per-kind fields (`checker_types.go`), not an
  interface hierarchy - mirrors the AST `Node`/`data` choice. `errorType` /
  `nullType` are shared singletons; primitives are interned in a package map.
- **Duck-typed field reads -> explicit kind switches**: the TS checker reads
  `declaration as {typeParameters?, extendsType?, ...}` generically; Go ports
  these as helper funcs (`nodeTypeParameters`, `checkerSuperTypeNodes`,
  `nodeModifiers`, `declarationParameters`) that switch on `node.Kind` and call
  the typed `As*()` accessor.
- **Closures returning an object -> struct with methods**: `createChecker`
  returns an object capturing locals; ported as a `Checker` struct whose ~40
  closures become methods, with the shared `program`/caches as fields.
- **JDK stub**: `jdkstub.go` is generated from `jdkStub.ts` - the synthetic JDK
  Java sources are copied verbatim into Go raw-string consts and registered as
  project files via `LoadJdkStub`. Regenerate by re-running the conversion if the
  TS stub changes.
- **services/nodeAtPosition**: lives in the compiler package for now (it is a
  pure AST utility the resolver/checker tests need); it can move when the
  language-services layer is ported.

## Language services + LSP server

- **LSP types are hand-rolled** (`internal/lsp`), like tsgo - no external LSP
  library. `protocol.go` mirrors the vscode-languageserver request/response
  shapes with `encoding/json` struct tags; `omitempty`/pointer fields reproduce
  the "absent vs present" distinction the TS optional properties carry.
- **JSON-RPC connection** (`internal/lsp/conn.go`): a `Content-Length`-framed
  reader/writer with `OnRequest`/`OnNotification` handler maps, single-threaded
  dispatch (issue #18 defers concurrency). Handlers return `(any, *ResponseError)`;
  a nil result marshals to JSON `null` (matching the TS `return null`).
- **Services take the compiler types directly**: the `services` package operates
  on `*compiler.Node` / `*compiler.Checker` / `*compiler.Program` and returns
  offset-based, position-free results (`TextChange{Start,End,NewText}`,
  `SemanticTokenEntry{Offset,...}`); the server converts offsets to LSP
  line/character at the boundary via `compiler.ComputeLineStarts`. This keeps the
  service logic pure and unit-testable, exactly as `src/services/*` split it.
- **Closures-returning-handlers -> methods on a Server struct**: `startServer`'s
  closure state (program, checker, config, inlay settings, dep-lens cache)
  becomes `Server` fields; each `connection.onX` closure becomes an `onX` method
  registered in `register()`.
- **Ported-from-TS exports**: when a service needs an internal compiler helper
  (`typeToString`, `skipTrivia`, `entityNameToString`, `isValidIdentifier`),
  export a thin `PascalCase` wrapper rather than duplicating it - keeps the one
  implementation authoritative.
- **In-process server test**: the TS build has no server test (the services are
  tested directly), but the Go port adds one - two `io.Pipe`s with a background
  drain goroutine so the synchronous pipe never blocks the server's diagnostic
  writes; it drives a real initialize/hover/completion/rename round-trip.
- **Type / call hierarchy re-resolve from the item, not a data payload**: the
  hierarchy requests are split into a pure service module (`type_hierarchy.go`,
  `call_hierarchy.go`) like the others; rather than stashing symbol identity in
  the LSP item's opaque `data`, the supertypes/subtypes/incoming/outgoing calls
  re-resolve the symbol from the identifier at the item's `selectionRange` start.
  `TypeHierarchyItem` / `CallHierarchyItem` are hand-rolled in `internal/lsp` like
  the rest. Both backends do this identically.

## Emitter-domain library pieces (without the bytecode emitter)

The bytecode emitter (`bytecode.ts`, ~294KB) stays a stub per issue #18, but its
self-contained helpers port independently:

- **Zip read/write -> `archive/zip`** (`internal/compiler/zip.go`). The TS build
  hand-rolls stored-only entries to avoid a write-side `node:zlib` dependency;
  Go has `archive/zip` in the standard library, so `WriteZip` uses `zip.Store`
  (reproducible) and `ReadZipEntries` returns `nil` for non-zip bytes (the TS
  `undefined`). Lazy `ZipEntry.Read()` mirrors the TS lazy `read()`.
- **Classfile reader** (`internal/compiler/classfile_reader.go`, port of
  `classfileReader.ts`): a constant-pool/header/member parser plus a
  `signatureReader` (JVMS 4.7.9 generic signatures) that regenerates a Java stub
  source. It depends only on the zip reader and `Program.AddProjectFile` - not on
  emission - so it ports cleanly. The descriptor/signature scanners are written
  with bounds-safe `charAt` (returns 0 past the end, like TS `?? ""`) so a
  truncated/hostile signature terminates instead of looping (cappu#70).
- **Testing emitter-domain readers with no JDK at runtime**: the TS test feeds
  the reader bytes from *our* emitter (so no JDK). With the emitter stubbed, the
  Go test instead reads committed `.class` fixtures compiled once with `javac`
  into `internal/compiler/testdata/classfiles/` - the test itself needs no JDK.
  Where the TS test re-emits a consumer to prove the stub resolves, the Go test
  checks the stub registers in the global index and the consumer type-checks with
  zero `GetSemanticDiagnostics` (the emitter is not involved).

## Bytecode emitter (bytecode.ts -> bytecode.go)

The 7,917-line JVM bytecode backend is ported and produces **byte-identical**
output to the TS reference (verified against the committed `.class` baselines in
`test-fixtures/emitter/emit-baselines`, no JDK needed).

- **generateBody -> a `bodyGen` struct.** The TS `generateBody` is one giant
  function whose ~40 nested closures share mutable state (the `code` buffer, the
  typed operand `stack`, `locals`, `assigned`, `activeLocals`, label fixups,
  break/continue/finally stacks). In Go that shared state becomes the fields of a
  `bodyGen` struct and every closure becomes a method on it. A top-level
  `generateBody(method, cp, program, checker, thisInternalName, opts)` builds the
  struct, runs the body, backpatches branches, and serializes the StackMapTable.
- **Optional/positional TS params -> a `bodyGenOptions` struct.** generateBody's
  dozen optional trailing parameters (ctorSuper, fieldInits, lambdaSpec, enumCtor,
  ctorPrologue, ctorLeading, ...) map to fields of one options struct; "" / nil /
  false stand in for `undefined`.
- **`throw new UnsupportedEmit()` -> `panic(unsupportedEmit{})` + recover.** The
  degrade path (emit a verifiable placeholder for an unhandled construct) is a
  `defer`/`recover` that only swallows `unsupportedEmit` and re-panics anything
  else - the exact semantics of the TS `catch (e) { if (!(e instanceof ...)) throw }`.
- **Insertion-ordered maps where javac's order is observable.** `computeInnerClassInfo`
  returns an `innerClassMap` (slice of keys + map) because the InnerClasses
  attribute order depends on declaration order, which a Go `map` would randomize.
- **Float/long bit layout:** `math.Float32bits`/`Float64bits` and
  `uint64(int64)` reinterpretation reproduce the TS `DataView.setFloat32` /
  `BigInt.asIntN` constant-pool encoding exactly. Modified-UTF-8 iterates UTF-16
  code units (`utf16.Encode`) to match the TS `charCodeAt` loop byte-for-byte.
- **Synthetic AST nodes** (a `<clinit>`, a default/compact constructor) are built
  with `&NodeFactory{}` (`NewMethodDeclaration`/`NewConstructorDeclaration`/
  `newToken`) rather than faked - generateBody reads real `.Kind`/`.Body` fields.
- **Name clashes with existing package symbols:** the emitter's `methodBody`,
  `numericCat`, `isStringType` collided with test helpers / checker functions;
  renamed to `compiledMethod`, `numericCat` (the unused brand type was removed),
  and `exprIsString`. Access flags (`accPublic`...) are shared with
  classfile_reader.go.
- **Multi-class emit results.** Where one TS emit function returns several classes
  (`emitEnum` now returns the enum plus one `E$N` per constant body), the Go twin
  returns `[]EmittedClass` and the driver spreads it (`append(result, emitEnum(...)...)`).
  Enum constant bodies reuse the anonymous-class machinery (`emitSynthCtor` with an
  added `accessFlags` arg for the private forwarding ctor; the shared `bodyClassName`
  numbers anonymous classes and enum bodies in one per-enclosing-type counter).
- **Annotations (`annotations.go`).** The element-value encoder (JVMS 4.7.16.1)
  ports directly; the only adaptations are Go-isms: reuse the existing
  `parseIntLiteral` (returns `uint64` - wrap to `int32`/`int64` and apply the sign
  yourself) and `lastSegment` rather than redeclaring them, and resolve an enum
  element's type via `ResolveTypeEntityName(receiver, ...)` instead of fabricating a
  TypeReference node. Optional TS params (the class-level `annotations?: {...}`
  bag) become an `*annotationSource` struct passed to `buildClassAttributes`, nil
  for synthetic classes. Element-value tags are picked from the element's declared
  type (resolved from the source `@interface`) with a literal-form fallback, so
  `byte`/`short`/`char` vs `int` and the enum/`Class`/annotation reference cases
  byte-match javac. Verified by emitting the shared `AnnAll` baseline byte-for-byte.

## Debug adapter: JDWP client + DAP server (services/dap -> internal/dapserver)

`cappu dap` bridges the Debug Adapter Protocol to JDWP (the JVM debugger wire
protocol). Both wire protocols are hand-rolled on both sides (no
`@vscode/debugadapter`, no `google/go-dap`, no JDWP library) so the two builds
stay byte-comparable, like the LSP pair.

- **8-byte binary IDs: TS `bigint` -> Go `uint64`.** JDWP sizes its reference IDs
  (objectID/methodID/...) per the VM's `IDSizes` reply (up to 8 bytes). The TS
  codec reads them as `bigint`; Go uses `uint64` (simpler, no boxing). The
  ID-size-aware reader/writer (`internal/jdwp/idcodec.go`) loops byte-by-byte
  rather than `binary.BigEndian.Uint64` because the width is dynamic.
- **DAP transport = the LSP transport.** `internal/dap/conn.go` is
  `internal/lsp/conn.go` retyped to DAP's envelope (`seq`/`type`/`request_seq`/
  `command`/`event` instead of JSON-RPC `id`/`jsonrpc`); same `Content-Length`
  framer and `readMessage`. Handlers return `(any, error)`; a returned error
  becomes a `success:false` response carrying `err.Error()`.
- **TS single event loop -> Go mutex + event goroutine (the key concurrency
  trap).** In TS, DAP request handlers and JDWP event callbacks both run on the
  one event loop, so they never race. In Go they run on different goroutines (the
  DAP read loop vs the JDWP read goroutine). Serialize them with one `sync.Mutex`
  on the `Session` **plus** a buffered event channel: the JDWP `OnEvent` callback
  only does `s.eventCh <- data` (never takes the lock), and a dedicated
  `processEvents` goroutine drains the channel and handles each event under the
  mutex. This avoids the deadlock where a request handler holds the lock while
  awaiting a JDWP reply and the JDWP read goroutine blocks in a lock-taking event
  callback (so the reply is never delivered). The channel decouples them.
- **`initialized` after the response:** the DAP `initialize` reply must precede
  the `initialized` event. TS used `setImmediate`; Go uses `go s.conn.SendEvent(...)`
  so the synchronous response write wins the connection write-lock first.
- **`StdoutPipe`/`Wait` ordering:** the debuggee's stdout/stderr are pumped to DAP
  `output` events by two goroutines; a `sync.WaitGroup` waits for both to hit EOF
  **before** `cmd.Wait()` (Go forbids `Wait` before pipe reads complete).
- **`ResolveJava` parity fix.** The Go `testing.ResolveJava` only matched the
  javac sibling when javac contained a slash; the TS `resolveJava` realpaths a
  bare `javac` via PATH first. Reconciled (PATH lookup + `EvalSymlinks` + sibling
  `java`) so a system whose `javac` (JDK 25) and default `java` (JDK 21) differ
  runs the debuggee with the compiling JDK - otherwise the JVM throws a
  `LinkageError`/`UnsupportedClassVersionError`. The debugger surfaced a latent
  `cappu test` divergence.
- **JDWP "Listening" line is on stdout, not stderr.** `-agentlib:jdwp=...,server=y`
  prints `Listening for transport dt_socket at address: NNNNN` to **stdout**; both
  `launch.go`s parse it off stdout, then forward the rest of stdout as program
  output (safe because `suspend=y` means no program output until resumed).

## Source formatter (src/format -> internal/format)

The google-java-format-compatible formatter (nikeee/cappu#24) is a Wadler/Leijen
Doc IR plus an AST->Doc lowering. Porting notes:
- **Doc IR is a tagged struct, not an interface.** The TS `Doc` union includes
  bare `string`; Go has no untagged union, so `Doc` is one struct with a `kind`
  field and a `text()` helper wraps string literals. The printer builds many
  small Docs, so a struct (value type, no boxing) beats an interface. `concat`
  is variadic (`concat(a, b, c)`); a built `[]Doc` slice is spread with
  `concat(parts...)`.
- **JS `String.slice(from, to)` tolerates `from > to` (returns ""); Go panics.**
  `blankBeforePos` slices `text[from:pos]`; comment-bookkeeping can leave
  `prevEnd > pos`, which is a no-op in JS but an out-of-range panic in Go. Guard
  with `if from >= pos { return false }`. Watch for this in every ported
  `.slice()`/`.substring()` over computed offsets.
- **`tokenToString` had to be exported.** The printer needs operator/keyword
  spellings; added `compiler.TokenToString` (wrapper over the private
  `tokenToString`). It returns `""` (not nil) for non-token kinds, so the
  fallback is `if s := ...; s != "" { ... } else { raw }`, mirroring TS `?? raw`.
- **Tests share the fixtures.** `internal/format/format_test.go` reads the same
  `test-fixtures/format` golden `.input`/`.output` files as the TS suite (no JDK
  needed at test time), so the Go port is asserted byte-identical to the
  google-java-format baselines and to the TS build at once.
- **Ignore globs: no glob dep.** `formatterOptions.ignore` matching uses a tiny
  `globToRegexp` (`*`, `**`, `?`) in `internal/build/jar.go` instead of adding a
  minimatch-style dependency for Node's `path.matchesGlob`.
- **Javadoc reflow port (gjf's `javadoc/` package).** Faithful port of gjf's
  comment-reflow engine to `internal/format/javadoc/` (charStream, token, lexer,
  nestingStack, writer, formatter) plus `comment_rewrite.go`. Gotchas:
  - **No sticky regex.** TS uses `/.../y` (sticky) to match only at the cursor;
    Go's RE2 has no sticky flag, so `charStream.tryConsumeRegex` anchors patterns
    with `^` and slices the input from the cursor (`input[position:]`) so `^`
    binds there. Every lexer pattern is `^`-anchored.
  - **No negative lookahead.** TS `MISSING_SPACE_PREFIX` uses `(?!noinspection|...)`;
    RE2 has no lookahead, so it splits into `missingSpacePrefix` + a separate
    `allowedNoSpace` guard checked with `&&`.
  - **DOTALL** = `(?s)` inline flag (HTML-comment and literal patterns).
  - The reflow hook is generic: `doc.go` gained a `reflowDoc` leaf and a
    `commentRewriter(raw, column)` field on `printOptions`; the writer tracks the
    running column and calls the rewriter at write time, so `doc.go` stays
    Java/javadoc-agnostic (mirrors how gjf calls `CommentsHelper.rewrite`).
- **Line-wrapping engine port (gjf's Doc algorithm).** When `doc.ts` was rewritten
  to gjf's Level/Break/FillMode break algorithm, the Go `Doc` became an `interface`
  with pointer concrete types (`*token`/`*concatDoc`/`*brkDoc`/`*levelDoc`) because
  the algorithm mutates a Level's break decisions in place during compute - value
  structs would copy. TS uses `string` as a Doc leaf; Go can't, so `text()` wraps
  every literal in a `*token` (and `concat` stays variadic). The shared `line`/
  `hardline` are package-level singleton `*brkDoc`s, safe to reuse only because a
  break's per-occurrence decision lives in the controlling Level's parallel
  `broken[]`/`newIndent[]` arrays, not on the break (gjf mints a fresh Break each
  time). The Printer now carries the indent multiplier so the method-chain
  "small receiver" threshold is decided at build time. The shared
  `test-fixtures/format` golden test asserts the Go output byte-identical to the
  TS build and to real gjf across every wrapping fixture.
