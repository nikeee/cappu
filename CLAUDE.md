# CLAUDE.md
This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## General Rules
### 1. Think Before Coding
**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

### 2. Simplicity First
**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

### 3. Surgical Changes
**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

### 4. Goal-Driven Execution
**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

## Two codebases: TypeScript (`src/`) + Go port (`togo/`)

This repo holds two implementations of cappu. The TypeScript build under `src/`
is the original and the reference. The Go build under `togo/` is an in-progress
port (issue #18) that produces a single statically linked binary. `GO-PATTERNS.md`
at the repo root records the migration patterns (branded types, JSONC editing,
static linking, library mapping) - read it before working in `togo/`, and
**amend it whenever you discover a new pattern**.

**Every feature lives in BOTH codebases.** When you add or change a command,
config field, validation rule, or behaviour:
- Implement it in `src/` (TypeScript) AND `togo/` (Go), with tests in both.
- **Watch for diverging behaviour.** The two builds must behave identically:
  same flags, same exit codes, same stdout/stderr text, same config defaults and
  validation. When editing one side, diff it against the other and reconcile any
  drift. The Go ports carry `// Port of src/...` comments pointing at their TS
  source - keep those accurate.

### Go build commands (run inside `togo/`)
```bash
go test ./...                                              # all Go tests
go build ./...                                             # compile
go vet ./...
make fmt           # gofmt -w .   (format)
make lint          # golangci-lint run
make build         # static host binary -> dist/cappu (CGO_ENABLED=0, stripped)
make build-all     # cross-compile every release target
```
The Go CI (`.github/workflows/CI-go.yaml`) runs parallel to the Node CI; both
must stay green.

## Testing

Tests use the Node test runner via `tsx` (TypeScript sources run directly).

### Run everything
```bash
node --run test            # all src/**/*.test.ts
```

### Run a single file or a single test
```bash
node_modules/.bin/tsx --test ./src/compiler/emitter.test.ts
node_modules/.bin/tsx --test --test-name-pattern="synchronized" ./src/compiler/emitter.test.ts
```

### The emitter backend tests (`src/compiler/emitter.ts` / `src/compiler/bytecode.ts`)

These validate emitted JVM bytecode three ways. Two need a JDK on PATH
(`java`, `javap`); the heavy `javac` step is only needed when regenerating
baselines:

1. **Binary baselines** - exact emitted `.class` bytes, stored under
   `test-fixtures/emitter/emit-baselines/*.class`. No JDK needed.
2. **Byte-match vs javac** - our normalized disassembly (`javap -c -p`,
   constant-pool indices stripped) must equal javac's, stored as plain-text
   JSON under `test-fixtures/emitter/javac-baselines/*.json`. At test time only
   `javap` runs (over our output); the javac reference is read from disk.
3. **Run-equivalence** (`runsLikeJavac`) - our class is run under `java` and
   its stdout compared to the expected text (which is the javac-verified
   reference). Only `java` runs at test time.

### Regenerating baselines (UPDATE_BASELINES)

When an intentional change alters emitted bytecode, regenerate both baseline
kinds. This requires `javac`, `java`, and `javap` on PATH:
```bash
UPDATE_BASELINES=1 node_modules/.bin/tsx --test ./src/compiler/emitter.test.ts
```
This rewrites the binary `.class` baselines and the `emitter/javac-baselines/*.json`
references (recompiling each fixture with `javac --release 21`), and re-runs
`runsLikeJavac` against a live `javac` to confirm the hard-coded expected
stdout still matches. Commit the regenerated fixtures. Without the flag, a
missing baseline is auto-created (when a JDK is present) but existing ones are
asserted against, never overwritten.

### Corpus robustness tests (`src/compiler/emit-corpus.test.ts`)

Auto-discovers every git submodule under `test-fixtures/emitter/corpus/` and asserts the emitter
produces class bytes for every `.java` file without throwing (degrading to a
placeholder is fine, crashing is not). No JDK needed. Initialize the corpus
submodules first:
```bash
node --run corpus:init
```
This runs `git submodule update --init` (the submodules are marked
`shallow = true` in `.gitmodules`, so only depth-1 history is cloned) and then
restricts each corpus working tree to `*.java` via sparse-checkout - the only
files the tests read. Sparse-checkout cannot be expressed in `.gitmodules`
(it is per-clone config), which is why it lives in this script rather than the
submodule spec; a plain `git submodule update --init` also works but leaves the
non-Java files on disk. The corpus tests are skipped when no submodule is
checked out, so CI without them still passes.

A second tier, `corpus bytecode matches javac`, checks our emitted bytecode
against javac for real-world code. javac cannot build these projects (external
deps) and we degrade methods using unstubbed types, so the baseline
(`test-fixtures/emitter/corpus-baselines/*.json`) records only the
(class, method) pairs we currently match javac on; the test is then a
regression guard over that set. Regenerating it (`UPDATE_BASELINES=1`)
recompiles every JDK-only corpus file with javac and is slow (~10 min); the
normal run just reads the JSON and disassembles the baselined classes (seconds).

## Linting / Formatting
- **oxlint** + **oxfmt** for backend/frontend/ingest (config: `.oxlintrc.json`, `.oxfmtrc.json`, `.editorconfig`). Use `node --run lint` and `node --run format` to execute.
- **lefthook**: pre-commit formats staged files; pre-push lints all components in parallel

## Prompt log
- `PROMPTS.md` is a verbatim, chronological record of the user's prompts. Append
  each new prompt to it (verbatim, typos included) prefixed with a local
  timestamp, e.g. `- 2026-06-08 12:41 — <prompt>` (run `date "+%Y-%m-%d %H:%M"`).
- Add the triggering prompt(s) verbatim to the bottom of every commit message,
  after a `---` separator, prefixed with `Prompt:`.

## Final Notices
- NEVER use the `npx` command under any circumstances. It is strictly blocked by security policies on our system.
- ALWAYS use allowed `npm` scripts defined in `package.json`.
- Instead of `npx tsc` -> YOU MUST RUN: `node --run typecheck`
- Never use en or em dashes. Avoid using those dashes in general. If you need one, use a normal minus (-)

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
