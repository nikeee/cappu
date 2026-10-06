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

## One codebase: Go (`togo/`)

cappu is the Go build under `togo/`: a single statically linked binary. It
began as a port (issue #18) of a TypeScript build under `src/`, which was
removed once the port overtook it - so the Go files carry `// Port of src/...`
comments naming where their behaviour came from. Those paths no longer exist;
they are provenance, not a place to look. `GO-PATTERNS.md` at the repo root
records the patterns that came out of the migration (branded types, JSONC
editing, static linking, library mapping) - read it before working in `togo/`,
and **amend it whenever you discover a new pattern**.

What the TypeScript build used to generate is now checked in and edited by
hand, each with a Go test that fails when it drifts from the code:
`togo/internal/config/cappu.schema.json` (the cappu.json schema) and
`togo/internal/cli/completion/cappu.{bash,zsh}`.

The JavaScript that is left is the VS Code extension and
`scripts/sync-go-version.mjs`, which the `npm version` lifecycle hook runs to
mirror the bumped version into `togo/internal/meta/meta.go`. `node --run lint`
and `node --run format` (oxlint, oxfmt) cover it.

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
`.github/workflows/CI-go.yaml` is the suite (including the JDK matrix: no JDK,
21, 25, and one cappu provisions itself); `CI.yaml` only lints the leftover
JavaScript. Both must stay green.

## Claude Code plugin (`plugins/cappu/`)

`plugins/cappu/` is a Claude Code plugin (marketplace manifest in
`.claude-plugin/marketplace.json`) whose skills teach an agent how to USE cappu
in a Java project: `setup`, `dependencies`, `build-test`, `ci-release`,
`code-intel`, `debug`, `migrate`. They state CLI flags, exit codes, `cappu.json`
fields and MCP tool names verbatim. When you add, rename or change a command,
flag, config field, exit code or MCP tool, update the affected skill in the same
change. Try them locally with `claude --plugin-dir plugins/cappu`.

## Testing
See `./docs/testing.md`.

## Linting / Formatting
- **gofmt** + **golangci-lint** for the Go build: `make fmt` and `make lint` inside `togo/`.
- **oxlint** + **oxfmt** for the leftover JavaScript (config: `.oxlintrc.json`, `.oxfmtrc.json`, `.editorconfig`). Use `node --run lint` and `node --run format` to execute.
- **lefthook**: pre-commit formats staged files; pre-push lints all components in parallel

## Prompt log
- `PROMPTS.md` is a verbatim, chronological record of the user's prompts. Append
  each new prompt to it (verbatim, typos included) prefixed with a local
  timestamp, e.g. `- 2026-06-08 12:41 — <prompt>` (run `date "+%Y-%m-%d %H:%M"`).
- Add the triggering prompt(s) verbatim to the bottom of every commit message,
  after a `---` separator, prefixed with `Prompt:`.
- When a commit resolves a GitHub issue, close it via a `Resolves #<nr>` (or
  `Closes #<nr>`) clause in the commit message, never by posting a comment.

## Final Notices
- NEVER use the `npx` command under any circumstances. It is strictly blocked by security policies on our system.
- ALWAYS use allowed `npm` scripts defined in `package.json`.
- Never use en or em dashes. Avoid using those dashes in general. If you need one, use a normal minus (-)

## graphify
This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships. See `./docs/graphify.md`.
