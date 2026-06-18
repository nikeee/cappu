# cappu MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `cappu mcp` subcommand that exposes the existing Java semantic engine (scanner/parser/binder/checker/resolver) to AI agents as an MCP (Model Context Protocol) server over stdio.

**Architecture:** A thin MCP layer over the existing `Program` + `Checker`. The engine is position-addressed (line/col); agents are name-addressed. The new code is (1) a symbol-reference resolver that turns names like `java.util.List` or `com.foo.Bar#method` into engine `Symbol`s, and (2) six tool handlers that wrap existing engine functions and return plain JSON. The transport is the official `@modelcontextprotocol/sdk` over stdio, mirroring how `cappu lsp` wires `vscode-languageserver`.

**Tech Stack:** TypeScript (ESM, `.ts` imports), `@modelcontextprotocol/sdk`, `zod` (tool input schemas), `node:test` + `expect` for tests, `tsx` runner.

## Global Constraints

- TypeScript-only cappu (this repo). The Go port ("togo") is explicitly out of scope.
- ESM with explicit `.ts` extensions on relative imports (match existing files).
- NEVER use `npx`. Use `npm` / `node --run` scripts only. Install deps with `npm install`.
- Run typecheck with `node --run typecheck` (never `npx tsc`).
- Run tests with `tsx --test` (the `test` script: `tsx --test "./src/**/*.test.ts"`). Single file: `node --test src/<file>.test.ts` via tsx, i.e. `tsx --test src/<file>.test.ts`.
- No en/em dashes anywhere (use `-`).
- Simplicity first: minimum code, no speculative features, match existing style.
- Tool handlers MUST be pure over the current `Program` state (no disk I/O, no transport). Disk freshness and transport live only in `src/mcpServer.ts`.
- Agent-facing locations use **1-based** line/column and filesystem **paths** (via `uriToPath`), not `file://` URIs or 0-based LSP coordinates.

---

## File Structure

- `src/mcpResolve.ts` (new) - `resolveSymbolRef(ref, index): Symbol[]`. Turns a name/FQN/`Type#member` string into engine symbols. One responsibility: name -> symbol.
- `src/mcp.ts` (new) - `createMcpTools(program, checker)` returning the six pure tool handlers, plus JSON-formatting helpers (`nodeLocation`, `formatDiagnostic`). No I/O, no transport.
- `src/mcpServer.ts` (new) - transport wiring. Loads the workspace from `cwd`, builds program/checker/tools, registers each tool on an `McpServer` with a zod input schema, refreshes changed files before each call, connects `StdioServerTransport`. Side-effect module (like `server.ts`).
- `src/program.ts` (modify) - add `getAllTypeFqns(): string[]` to `GlobalIndex` (needed by `search_symbols`).
- `src/cli.ts` (modify) - add the `mcp` command.
- `package.json` (modify) - add `@modelcontextprotocol/sdk` and `zod` deps.

---

### Task 1: Add `getAllTypeFqns` to the global index

**Files:**
- Modify: `src/program.ts` (interface `GlobalIndex` ~line 20-30; the `globalIndex` literal ~line 195-208)
- Test: `src/program.test.ts` (append)

**Interfaces:**
- Produces: `GlobalIndex.getAllTypeFqns(): string[]` - all top-level type fully-qualified names currently indexed. Consumed by `search_symbols` in Task 4.

- [ ] **Step 1: Write the failing test**

Append to `src/program.test.ts`:

```ts
test("getAllTypeFqns lists every top-level type fqn", () => {
  const program = createProgram();
  program.addProjectFile("file:///a/Foo.java", "package a; class Foo {}");
  program.addProjectFile("file:///b/Bar.java", "package a.b; class Bar {} class Baz {}");
  const index = program.getGlobalIndex();
  expect(new Set(index.getAllTypeFqns())).toEqual(
    new Set(["a.Foo", "a.b.Bar", "a.b.Baz"]),
  );
});
```

(If `src/program.test.ts` does not yet import `test`/`expect`/`createProgram`, add `import { test } from "node:test";`, `import { expect } from "expect";`, `import { createProgram } from "./program.ts";` at the top - match the style in `src/resolver.test.ts`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `tsx --test src/program.test.ts`
Expected: FAIL - `index.getAllTypeFqns is not a function`.

- [ ] **Step 3: Add the interface member**

In `src/program.ts`, inside `export interface GlobalIndex { ... }`, add after `findFqnsBySimpleName`:

```ts
  /** Fully-qualified names of every indexed top-level type. */
  getAllTypeFqns(): string[];
```

- [ ] **Step 4: Implement it**

In `src/program.ts`, inside the `const globalIndex: GlobalIndex = { ... }` literal, add after the `findFqnsBySimpleName` entry:

```ts
    getAllTypeFqns: () => [...typesByFqn.keys()],
```

- [ ] **Step 5: Run test to verify it passes**

Run: `tsx --test src/program.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add src/program.ts src/program.test.ts
git commit -m "feat(program): expose getAllTypeFqns on the global index"
```

---

### Task 2: Symbol-reference resolver

**Files:**
- Create: `src/mcpResolve.ts`
- Test: `src/mcpResolve.test.ts`

**Interfaces:**
- Consumes: `GlobalIndex` from `./program.ts` (`getType`, `findFqnsBySimpleName`); `Symbol`, `SymbolTable` from `./types.ts`.
- Produces:
  - `resolveSymbolRef(ref: string, index: GlobalIndex): Symbol[]` - returns matching symbols. A `ref` is either a type reference (`com.foo.Bar` FQN, or bare simple name `Bar`) or a member reference `Type#member` (e.g. `java.util.List#add` or `List#add`). Returns `[]` when nothing matches; multiple entries on ambiguity (e.g. a bare simple name shared by two packages, or an overloaded/duplicated member name).

A `Symbol`'s declared members live in `symbol.members` (a `SymbolTable = Map<string, Symbol>`). Only declared members are resolved (no inherited members) - this is a documented first-cut limitation.

- [ ] **Step 1: Write the failing tests**

Create `src/mcpResolve.test.ts`:

```ts
import { test } from "node:test";
import { expect } from "expect";

import { createProgram } from "./program.ts";
import { resolveSymbolRef } from "./mcpResolve.ts";
import { SymbolFlags } from "./types.ts";

function indexFor(files: Record<string, string>) {
  const program = createProgram();
  for (const [uri, text] of Object.entries(files)) program.addProjectFile(uri, text);
  return program.getGlobalIndex();
}

test("resolves a fully-qualified type name", () => {
  const index = indexFor({ "file:///Foo.java": "package a; class Foo {}" });
  const syms = resolveSymbolRef("a.Foo", index);
  expect(syms).toHaveLength(1);
  expect(syms[0].escapedName).toBe("Foo");
  expect(syms[0].flags & SymbolFlags.Class).toBeTruthy();
});

test("resolves a bare simple type name", () => {
  const index = indexFor({ "file:///Foo.java": "package a; class Foo {}" });
  const syms = resolveSymbolRef("Foo", index);
  expect(syms).toHaveLength(1);
  expect(syms[0].escapedName).toBe("Foo");
});

test("returns every candidate for an ambiguous simple name", () => {
  const index = indexFor({
    "file:///a/Foo.java": "package a; class Foo {}",
    "file:///b/Foo.java": "package b; class Foo {}",
  });
  const syms = resolveSymbolRef("Foo", index);
  expect(syms).toHaveLength(2);
});

test("resolves a member via Type#member", () => {
  const index = indexFor({ "file:///Foo.java": "package a; class Foo { int bar() { return 0; } }" });
  const syms = resolveSymbolRef("a.Foo#bar", index);
  expect(syms).toHaveLength(1);
  expect(syms[0].escapedName).toBe("bar");
  expect(syms[0].flags & SymbolFlags.Method).toBeTruthy();
});

test("returns empty for an unknown ref", () => {
  const index = indexFor({ "file:///Foo.java": "package a; class Foo {}" });
  expect(resolveSymbolRef("a.Nope", index)).toEqual([]);
  expect(resolveSymbolRef("a.Foo#nope", index)).toEqual([]);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `tsx --test src/mcpResolve.test.ts`
Expected: FAIL - cannot find module `./mcpResolve.ts`.

- [ ] **Step 3: Implement the resolver**

Create `src/mcpResolve.ts`:

```ts
// Turns an agent-supplied symbol reference into engine symbols. References are
// name-addressed (agents do not have file offsets): either a type (FQN
// "com.foo.Bar" or a bare simple name "Bar") or a member "Type#member". Only
// declared members are resolved; inherited members are out of scope for now.

import type { GlobalIndex } from "./program.ts";
import type { Symbol } from "./types.ts";

function resolveType(typeRef: string, index: GlobalIndex): Symbol[] {
  const direct = index.getType(typeRef);
  if (direct) return [direct];
  const byFqn = index
    .findFqnsBySimpleName(typeRef)
    .map(fqn => index.getType(fqn))
    .filter((s): s is Symbol => s !== undefined);
  return byFqn;
}

export function resolveSymbolRef(ref: string, index: GlobalIndex): Symbol[] {
  const hash = ref.indexOf("#");
  if (hash < 0) return resolveType(ref, index);

  const typeRef = ref.slice(0, hash);
  const memberName = ref.slice(hash + 1);
  const members: Symbol[] = [];
  for (const type of resolveType(typeRef, index)) {
    const member = type.members?.get(memberName);
    if (member) members.push(member);
  }
  return members;
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `tsx --test src/mcpResolve.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add src/mcpResolve.ts src/mcpResolve.test.ts
git commit -m "feat(mcp): add name-based symbol reference resolver"
```

---

### Task 3: Formatting helpers + `diagnostics` tool

**Files:**
- Create: `src/mcp.ts`
- Test: `src/mcp.test.ts`

**Interfaces:**
- Consumes: `Program` (`./program.ts`), `Checker` (`./checker.ts`), `computeLineStarts`/`getLineAndCharacterOfPosition` (`./lineMap.ts`), `uriToPath` (`./workspace.ts`), `getSourceFileOfNode`/`getDeclarationNameNode`/`findReferences` (`./resolver.ts`), `skipTrivia` (`./utilities.ts`), `getDocumentSymbols` (`./documentSymbols.ts`), `getHoverText` (`./hover.ts`), `resolveSymbolRef` (`./mcpResolve.ts`), `DiagnosticCategory`/`Node`/`Symbol` (`./types.ts`).
- Produces:
  - `interface McpLocation { file: string; line: number; column: number; endLine: number; endColumn: number }` (1-based, `file` is a path).
  - `interface McpDiagnostic { file: string; severity: "error" | "warning" | "hint" | "info"; code: number; message: string; line: number; column: number; endLine: number; endColumn: number }`.
  - `nodeLocation(node: Node): McpLocation`.
  - `createMcpTools(program: Program, checker: Checker): McpTools` (this task adds only `diagnostics`; Tasks 4-5 extend the same factory).
  - `McpTools.diagnostics(args: { files?: string[] }): { diagnostics: McpDiagnostic[] }` - reports syntax + bind + semantic diagnostics. With `files` (paths), reports only those (converted to URIs via `pathToUri`); without, reports across all known URIs (`program.getAllUris()`).

`DiagnosticCategory` is `Warning, Error, Suggestion, Message` (in that declaration order). Map: `Error -> "error"`, `Warning -> "warning"`, `Suggestion -> "hint"`, else `"info"`.

- [ ] **Step 1: Write the failing tests**

Create `src/mcp.test.ts`:

```ts
import { test } from "node:test";
import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { createProgram } from "./program.ts";
import { createMcpTools } from "./mcp.ts";

function toolsFor(files: Record<string, string>) {
  const program = createProgram();
  for (const [uri, text] of Object.entries(files)) program.addProjectFile(uri, text);
  const checker = createChecker(program);
  return createMcpTools(program, checker);
}

test("diagnostics reports a syntax error with a 1-based location", () => {
  const tools = toolsFor({ "file:///Bad.java": "class Bad { void m( }" });
  const { diagnostics } = tools.diagnostics({});
  expect(diagnostics.length).toBeGreaterThan(0);
  const d = diagnostics[0];
  expect(d.file).toBe("/Bad.java");
  expect(d.severity).toBe("error");
  expect(d.line).toBeGreaterThanOrEqual(1);
  expect(d.column).toBeGreaterThanOrEqual(1);
});

test("diagnostics is empty for a valid file", () => {
  const tools = toolsFor({ "file:///Ok.java": "class Ok { int m() { return 1; } }" });
  expect(tools.diagnostics({}).diagnostics).toEqual([]);
});

test("diagnostics honors an explicit file path filter", () => {
  const tools = toolsFor({
    "file:///Bad.java": "class Bad { void m( }",
    "file:///Ok.java": "class Ok {}",
  });
  const { diagnostics } = tools.diagnostics({ files: ["/Ok.java"] });
  expect(diagnostics).toEqual([]);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `tsx --test src/mcp.test.ts`
Expected: FAIL - cannot find module `./mcp.ts`.

- [ ] **Step 3: Implement the helpers and the `diagnostics` tool**

Create `src/mcp.ts`:

```ts
// MCP tool handlers: a thin, name-addressed, JSON-returning layer over the
// engine (Program + Checker). Handlers are pure over the current Program state;
// disk freshness and transport live in mcpServer.ts. Locations are 1-based and
// use filesystem paths so agents can act on them directly.

import type { Checker } from "./checker.ts";
import { getDocumentSymbols } from "./documentSymbols.ts";
import { getHoverText } from "./hover.ts";
import { computeLineStarts, getLineAndCharacterOfPosition } from "./lineMap.ts";
import { resolveSymbolRef } from "./mcpResolve.ts";
import type { Program } from "./program.ts";
import { findReferences, getDeclarationNameNode, getSourceFileOfNode } from "./resolver.ts";
import { DiagnosticCategory, type Diagnostic, type Node, type Symbol } from "./types.ts";
import { skipTrivia } from "./utilities.ts";
import { pathToUri, uriToPath } from "./workspace.ts";

export interface McpLocation {
  file: string;
  line: number;
  column: number;
  endLine: number;
  endColumn: number;
}

export interface McpDiagnostic {
  file: string;
  severity: "error" | "warning" | "hint" | "info";
  code: number;
  message: string;
  line: number;
  column: number;
  endLine: number;
  endColumn: number;
}

function severityOf(category: DiagnosticCategory): McpDiagnostic["severity"] {
  switch (category) {
    case DiagnosticCategory.Error:
      return "error";
    case DiagnosticCategory.Warning:
      return "warning";
    case DiagnosticCategory.Suggestion:
      return "hint";
    default:
      return "info";
  }
}

// node.pos includes leading trivia; advance to the token's real start so the
// reported location points at the name, mirroring server.ts:rangeOf.
export function nodeLocation(node: Node): McpLocation {
  const file = getSourceFileOfNode(node);
  const lineStarts = computeLineStarts(file.text);
  const start = getLineAndCharacterOfPosition(lineStarts, skipTrivia(file.text, node.pos));
  const end = getLineAndCharacterOfPosition(lineStarts, node.end);
  return {
    file: uriToPath(file.fileName),
    line: start.line + 1,
    column: start.character + 1,
    endLine: end.line + 1,
    endColumn: end.character + 1,
  };
}

function formatDiagnostic(
  uri: string,
  d: Diagnostic,
  lineStarts: readonly number[],
): McpDiagnostic {
  const start = getLineAndCharacterOfPosition(lineStarts, d.pos);
  const end = getLineAndCharacterOfPosition(lineStarts, d.end);
  return {
    file: uriToPath(uri),
    severity: severityOf(d.category),
    code: d.code,
    message: d.messageText,
    line: start.line + 1,
    column: start.character + 1,
    endLine: end.line + 1,
    endColumn: end.character + 1,
  };
}

export interface McpTools {
  diagnostics(args: { files?: string[] }): { diagnostics: McpDiagnostic[] };
}

export function createMcpTools(program: Program, checker: Checker): McpTools {
  function diagnostics(args: { files?: string[] }): { diagnostics: McpDiagnostic[] } {
    const uris = args.files?.length
      ? args.files.map(pathToUri)
      : program.getAllUris();
    const out: McpDiagnostic[] = [];
    for (const uri of uris) {
      const sourceFile = program.getSourceFile(uri);
      if (!sourceFile) continue;
      const lineStarts = computeLineStarts(sourceFile.text);
      const all = [
        ...sourceFile.parseDiagnostics,
        ...(sourceFile.bindDiagnostics ?? []),
        ...checker.getSemanticDiagnostics(sourceFile),
      ];
      for (const d of all) out.push(formatDiagnostic(uri, d, lineStarts));
    }
    return { diagnostics: out };
  }

  return { diagnostics };
}
```

(`resolveSymbolRef`, `getDocumentSymbols`, `getHoverText`, `findReferences`, `getDeclarationNameNode`, `Symbol` are imported now because Tasks 4-5 extend this file; if your linter flags unused imports between tasks, leave them - they are consumed before the feature is complete. If that blocks a commit, add the imports in the task that first uses them instead.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `tsx --test src/mcp.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Run typecheck**

Run: `node --run typecheck`
Expected: no errors. (If unused-import errors appear from the Task 4-5 imports, move those imports into Tasks 4/5 per the note above and re-run.)

- [ ] **Step 6: Commit**

```bash
git add src/mcp.ts src/mcp.test.ts
git commit -m "feat(mcp): add diagnostics tool and location formatting"
```

---

### Task 4: `outline` and `search_symbols` tools

**Files:**
- Modify: `src/mcp.ts` (extend `McpTools` and `createMcpTools`)
- Test: `src/mcp.test.ts` (append)

**Interfaces:**
- Consumes: `getDocumentSymbols` (`./documentSymbols.ts`), `GlobalIndex.getAllTypeFqns` (Task 1), `pathToUri` (`./workspace.ts`).
- Produces:
  - `McpTools.outline(args: { file: string }): { symbols: DocumentSymbol[] }` - the document outline for one file (path). `DocumentSymbol` is the `vscode-languageserver-types` shape returned by `getDocumentSymbols` (JSON-serializable; `kind` is a numeric LSP `SymbolKind`). Returns `{ symbols: [] }` for an unknown file.
  - `McpTools.searchSymbols(args: { query: string }): { matches: string[] }` - case-insensitive substring match of `query` against every indexed top-level type FQN.

- [ ] **Step 1: Write the failing tests**

Append to `src/mcp.test.ts`:

```ts
test("outline returns the top-level types of a file", () => {
  const tools = toolsFor({ "file:///Foo.java": "class Foo { int x; void m() {} }" });
  const { symbols } = tools.outline({ file: "/Foo.java" });
  expect(symbols).toHaveLength(1);
  expect(symbols[0].name).toBe("Foo");
});

test("outline is empty for an unknown file", () => {
  const tools = toolsFor({ "file:///Foo.java": "class Foo {}" });
  expect(tools.outline({ file: "/Missing.java" }).symbols).toEqual([]);
});

test("searchSymbols matches type fqns case-insensitively by substring", () => {
  const tools = toolsFor({
    "file:///UserService.java": "package app; class UserService {}",
    "file:///Repo.java": "package app; class Repo {}",
  });
  expect(tools.searchSymbols({ query: "service" }).matches).toEqual(["app.UserService"]);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `tsx --test src/mcp.test.ts`
Expected: FAIL - `tools.outline is not a function`.

- [ ] **Step 3: Extend the tools**

In `src/mcp.ts`, add the `DocumentSymbol` import to the existing `./documentSymbols.ts` import line:

```ts
import { type DocumentSymbol, getDocumentSymbols } from "./documentSymbols.ts";
```

(If `getDocumentSymbols` re-exports the type from `vscode-languageserver`, import `DocumentSymbol` from there instead: `import type { DocumentSymbol } from "vscode-languageserver";`. Use whichever the existing `documentSymbols.ts` exposes - check its imports first.)

Extend the `McpTools` interface:

```ts
  outline(args: { file: string }): { symbols: DocumentSymbol[] };
  searchSymbols(args: { query: string }): { matches: string[] };
```

In `createMcpTools`, before the `return`, add:

```ts
  function outline(args: { file: string }): { symbols: DocumentSymbol[] } {
    const sourceFile = program.getSourceFile(pathToUri(args.file));
    if (!sourceFile) return { symbols: [] };
    return { symbols: getDocumentSymbols(sourceFile, computeLineStarts(sourceFile.text)) };
  }

  function searchSymbols(args: { query: string }): { matches: string[] } {
    const q = args.query.toLowerCase();
    const matches = program
      .getGlobalIndex()
      .getAllTypeFqns()
      .filter(fqn => fqn.toLowerCase().includes(q));
    return { matches };
  }
```

Update the return:

```ts
  return { diagnostics, outline, searchSymbols };
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `tsx --test src/mcp.test.ts`
Expected: PASS (6 tests total).

- [ ] **Step 5: Commit**

```bash
git add src/mcp.ts src/mcp.test.ts
git commit -m "feat(mcp): add outline and search_symbols tools"
```

---

### Task 5: `describe_symbol`, `find_definition`, `find_references` tools

**Files:**
- Modify: `src/mcp.ts` (extend `McpTools` and `createMcpTools`)
- Test: `src/mcp.test.ts` (append)

**Interfaces:**
- Consumes: `resolveSymbolRef` (`./mcpResolve.ts`), `getHoverText` (`./hover.ts`), `findReferences`/`getDeclarationNameNode` (`./resolver.ts`), `nodeLocation` (this file), `checker.signatureOfSymbol`/`checker.getDocumentation` (`./checker.ts`).
- Produces:
  - `interface McpMatch { kind: string; label: string; signature?: string; documentation?: string; definition?: McpLocation }`.
  - `McpTools.describeSymbol(args: { ref: string }): { matches: McpMatch[] }` - one `McpMatch` per resolved candidate. `kind` is the engine kind word (`symbolKindWord`); `label` is the one-line hover text (`getHoverText`); `signature` is `signatureOfSymbol` when present (methods/constructors); `documentation` is the cleaned Javadoc when present; `definition` is the declaration name location. Empty `matches` when nothing resolves.
  - `McpTools.findDefinition(args: { ref: string }): { definitions: McpLocation[] }` - declaration name locations for every resolved candidate.
  - `McpTools.findReferences(args: { ref: string }): { references: McpLocation[]; ambiguous?: boolean; candidates?: number }` - if the ref resolves to exactly one symbol, all reference locations; if it resolves to more than one, `{ references: [], ambiguous: true, candidates: N }` so the agent narrows the ref; if zero, `{ references: [] }`.

`kind` strings come from `symbolKindWord(symbol.flags)` in `./hover.ts` (e.g. `"class"`, `"method"`, `"field"`).

- [ ] **Step 1: Write the failing tests**

Append to `src/mcp.test.ts`:

```ts
test("describeSymbol returns kind, label and definition for a type", () => {
  const tools = toolsFor({ "file:///Foo.java": "package a; class Foo {}" });
  const { matches } = tools.describeSymbol({ ref: "a.Foo" });
  expect(matches).toHaveLength(1);
  expect(matches[0].kind).toBe("class");
  expect(matches[0].label).toBe("class Foo");
  expect(matches[0].definition?.file).toBe("/Foo.java");
});

test("describeSymbol resolves a method member and includes a signature", () => {
  const tools = toolsFor({
    "file:///Foo.java": "package a; class Foo { int add(int x) { return x; } }",
  });
  const { matches } = tools.describeSymbol({ ref: "a.Foo#add" });
  expect(matches).toHaveLength(1);
  expect(matches[0].kind).toBe("method");
  expect(matches[0].signature).toContain("add");
});

test("findDefinition returns the declaration location", () => {
  const tools = toolsFor({ "file:///Foo.java": "package a; class Foo {}" });
  const { definitions } = tools.findDefinition({ ref: "a.Foo" });
  expect(definitions).toHaveLength(1);
  expect(definitions[0].file).toBe("/Foo.java");
  expect(definitions[0].line).toBe(1);
});

test("findReferences returns every use of a field", () => {
  const tools = toolsFor({
    "file:///Foo.java": "package a; class Foo { int f; void m() { f = f + 1; } }",
  });
  const { references } = tools.findReferences({ ref: "a.Foo#f" });
  // declaration + two uses
  expect(references.length).toBe(3);
});

test("findReferences reports ambiguity instead of guessing", () => {
  const tools = toolsFor({
    "file:///a/Foo.java": "package a; class Foo {}",
    "file:///b/Foo.java": "package b; class Foo {}",
  });
  const result = tools.findReferences({ ref: "Foo" });
  expect(result.ambiguous).toBe(true);
  expect(result.candidates).toBe(2);
  expect(result.references).toEqual([]);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `tsx --test src/mcp.test.ts`
Expected: FAIL - `tools.describeSymbol is not a function`.

- [ ] **Step 3: Extend the tools**

In `src/mcp.ts`, update the `./hover.ts` import to also bring in `symbolKindWord`:

```ts
import { getHoverText, symbolKindWord } from "./hover.ts";
```

Add the `McpMatch` interface near the other interfaces:

```ts
export interface McpMatch {
  kind: string;
  label: string;
  signature?: string;
  documentation?: string;
  definition?: McpLocation;
}
```

Extend the `McpTools` interface:

```ts
  describeSymbol(args: { ref: string }): { matches: McpMatch[] };
  findDefinition(args: { ref: string }): { definitions: McpLocation[] };
  findReferences(args: {
    ref: string;
  }): { references: McpLocation[]; ambiguous?: boolean; candidates?: number };
```

In `createMcpTools`, before the `return`, add:

```ts
  function describe(symbol: Symbol): McpMatch {
    const declaration = getDeclarationNameNode(symbol);
    const signature = checker.signatureOfSymbol(symbol);
    const documentation = checker.getDocumentation(symbol);
    return {
      kind: symbolKindWord(symbol.flags),
      label: getHoverText(checker, symbol),
      ...(signature ? { signature } : {}),
      ...(documentation ? { documentation } : {}),
      ...(declaration ? { definition: nodeLocation(declaration) } : {}),
    };
  }

  function describeSymbol(args: { ref: string }): { matches: McpMatch[] } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    return { matches: symbols.map(describe) };
  }

  function findDefinition(args: { ref: string }): { definitions: McpLocation[] } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    const definitions: McpLocation[] = [];
    for (const symbol of symbols) {
      const declaration = getDeclarationNameNode(symbol);
      if (declaration) definitions.push(nodeLocation(declaration));
    }
    return { definitions };
  }

  function findReferencesTool(args: {
    ref: string;
  }): { references: McpLocation[]; ambiguous?: boolean; candidates?: number } {
    const symbols = resolveSymbolRef(args.ref, program.getGlobalIndex());
    if (symbols.length === 0) return { references: [] };
    if (symbols.length > 1) {
      return { references: [], ambiguous: true, candidates: symbols.length };
    }
    const references = findReferences(symbols[0], program, checker.resolveName).map(nodeLocation);
    return { references };
  }
```

Update the return:

```ts
  return {
    diagnostics,
    outline,
    searchSymbols,
    describeSymbol,
    findDefinition,
    findReferences: findReferencesTool,
  };
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `tsx --test src/mcp.test.ts`
Expected: PASS (11 tests total).

- [ ] **Step 5: Run typecheck**

Run: `node --run typecheck`
Expected: no errors (every import in `mcp.ts` is now consumed).

- [ ] **Step 6: Commit**

```bash
git add src/mcp.ts src/mcp.test.ts
git commit -m "feat(mcp): add describe_symbol, find_definition, find_references tools"
```

---

### Task 6: Transport wiring + CLI command + dependencies

**Files:**
- Modify: `package.json` (add deps)
- Create: `src/mcpServer.ts`
- Modify: `src/cli.ts` (add `mcp` command)

**Interfaces:**
- Consumes: `createMcpTools` (`./mcp.ts`), `createProgram` (`./program.ts`), `createChecker` (`./checker.ts`), `loadJdkStub` (`./jdkStub.ts`), `findJavaFiles`/`pathToUri` (`./workspace.ts`), `McpServer` + `StdioServerTransport` (`@modelcontextprotocol/sdk`), `z` (`zod`).
- Produces: a runnable `cappu mcp` stdio server. No automated test (covered by manual verification at the end - transport over real stdio is an e2e concern; the tool logic is fully unit-tested in Tasks 3-5).

This task is a single deliverable (a working server command) so its steps are not individually TDD-cycled; verification is the manual smoke test in Step 6.

- [ ] **Step 1: Add dependencies**

Run:

```bash
npm install @modelcontextprotocol/sdk zod
```

Expected: `package.json` `dependencies` gains `@modelcontextprotocol/sdk` and `zod`; `package-lock.json` updates. (Do not hand-edit versions; let `npm` pin them.)

- [ ] **Step 2: Verify the SDK import path**

Run:

```bash
node -e "import('@modelcontextprotocol/sdk/server/mcp.js').then(m => console.log(Object.keys(m)))"
```

Expected: output includes `McpServer`. If the subpath differs in the installed version, note the correct one (e.g. check `node_modules/@modelcontextprotocol/sdk/package.json` `exports`) and use it in Step 4. Also confirm the transport path:

```bash
node -e "import('@modelcontextprotocol/sdk/server/stdio.js').then(m => console.log(Object.keys(m)))"
```

Expected: output includes `StdioServerTransport`.

- [ ] **Step 3: Create the server module**

Create `src/mcpServer.ts`:

```ts
// MCP server over stdio. Exposes the Java semantic engine to agents as tools.
// Mirrors server.ts (the LSP entry) but speaks the Model Context Protocol.
// Tool logic lives in mcp.ts (pure, tested); this module owns disk freshness
// and transport. Run with: cappu mcp

import { readFileSync, statSync } from "node:fs";

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

import { createChecker } from "./checker.ts";
import { loadJdkStub } from "./jdkStub.ts";
import { createMcpTools } from "./mcp.ts";
import { createProgram } from "./program.ts";
import { findJavaFiles, pathToUri } from "./workspace.ts";

const program = createProgram();
loadJdkStub(program);
const checker = createChecker(program);
const tools = createMcpTools(program, checker);

// Agents edit files on disk between calls. Re-read any .java file whose mtime
// changed (or that is new) before each tool call so results stay current.
// addProjectFile clears that file's parse/bind cache, so the next query
// re-parses only what is stale.
const root = process.cwd();
const mtimes = new Map<string, number>();
function refresh(): void {
  for (const path of findJavaFiles(root)) {
    let mtime: number;
    try {
      mtime = statSync(path).mtimeMs;
    } catch {
      continue;
    }
    if (mtimes.get(path) === mtime) continue;
    try {
      program.addProjectFile(pathToUri(path), readFileSync(path, "utf8"));
      mtimes.set(path, mtime);
    } catch {
      // unreadable file: skip
    }
  }
}

const server = new McpServer({ name: "cappu", version: "1.0.0" });

function ok(data: unknown) {
  return { content: [{ type: "text" as const, text: JSON.stringify(data, null, 2) }] };
}

server.registerTool(
  "diagnostics",
  {
    description:
      "Java syntax, binding and type diagnostics. Omit `files` to check the whole workspace.",
    inputSchema: { files: z.array(z.string()).optional() },
  },
  async args => {
    refresh();
    return ok(tools.diagnostics(args));
  },
);

server.registerTool(
  "outline",
  {
    description: "Top-level type/member outline of one Java file.",
    inputSchema: { file: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.outline(args));
  },
);

server.registerTool(
  "search_symbols",
  {
    description: "Find indexed Java types whose fully-qualified name contains `query`.",
    inputSchema: { query: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.searchSymbols(args));
  },
);

server.registerTool(
  "describe_symbol",
  {
    description:
      "Describe a symbol (kind, signature, Javadoc, definition). `ref` is a type FQN or simple name, or `Type#member` (e.g. `java.util.List#add`).",
    inputSchema: { ref: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.describeSymbol(args));
  },
);

server.registerTool(
  "find_definition",
  {
    description: "Locate where a symbol is declared. `ref` as in describe_symbol.",
    inputSchema: { ref: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.findDefinition(args));
  },
);

server.registerTool(
  "find_references",
  {
    description: "Find every use of a symbol across the workspace. `ref` as in describe_symbol.",
    inputSchema: { ref: z.string() },
  },
  async args => {
    refresh();
    return ok(tools.findReferences(args));
  },
);

await server.connect(new StdioServerTransport());
```

(If Step 2 showed `registerTool`'s third argument or the config shape differs in the installed SDK version - older versions use `server.tool(name, schema, handler)` - adapt these calls to the installed API. The handler body, `refresh()` then `ok(tools.X(args))`, stays the same.)

- [ ] **Step 4: Add the `mcp` command to the CLI**

In `src/cli.ts`, add a `.command(...)` immediately after the existing `lsp` command block (before the `compile` command), mirroring the `lsp` side-effect-import pattern:

```ts
  .command(
    "mcp",
    "Start the MCP server for agents (Model Context Protocol over stdio)",
    {},
    async () => {
      await import("./mcpServer.ts");
    },
  )
```

Also update the `demandCommand` message to mention the new command:

```ts
  .demandCommand(1, "Specify a command: lsp, mcp or compile")
```

- [ ] **Step 5: Typecheck**

Run: `node --run typecheck`
Expected: no errors.

- [ ] **Step 6: Manual smoke test (stdio handshake + tools/list + a real call)**

Create a throwaway fixture and drive the server with a hand-written MCP session. Run from the repo root:

```bash
mkdir -p /tmp/cappu-mcp-smoke && printf 'package app;\nclass Greeter { String hello() { return "hi"; } }\n' > /tmp/cappu-mcp-smoke/Greeter.java
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_symbols","arguments":{"query":"greeter"}}}' \
  | (cd /tmp/cappu-mcp-smoke && tsx "$OLDPWD/src/cli.ts" mcp)
```

Expected: three JSON-RPC responses on stdout. Response `id:2` lists six tools (`diagnostics`, `outline`, `search_symbols`, `describe_symbol`, `find_definition`, `find_references`). Response `id:3` contains a text content block whose JSON is `{"matches":["app.Greeter"]}`.

(The server reads `cwd` for the workspace, hence the `cd` into the fixture. `$OLDPWD` points back at the repo so `tsx` finds `src/cli.ts`.)

- [ ] **Step 7: Run the full test suite**

Run: `node --run test`
Expected: all tests pass (existing suite + the new `mcpResolve.test.ts` and `mcp.test.ts`).

- [ ] **Step 8: Commit**

```bash
git add package.json package-lock.json src/mcpServer.ts src/cli.ts
git commit -m "feat(cli): add cappu mcp server command"
```

---

## Notes / First-cut limitations (intentional, per Simplicity First)

- **Declared members only.** `Type#member` resolves only members declared on that type, not inherited ones. Inherited-member resolution can layer on later via `getDirectSuperTypeSymbols` (`resolver.ts`) if agents need it.
- **No bytecode tool.** `compile` was deliberately left out of this cut (decision: "Semantic nav + diagnostics"). `runCompile` already exists if a `compile` tool is wanted later; it writes `.class` files, so it would add file-writing side effects to the MCP surface.
- **`outline` returns numeric LSP `SymbolKind`.** Kept raw to stay surgical. Add a `kindName` mapping if agents read it poorly.
- **Freshness is mtime-polled per call**, scanning `cwd` for `.java` files. Fine for typical project sizes; revisit with a watcher only if it shows up as slow.
- **`search_symbols` indexes only top-level types** (what `GlobalIndex` tracks). Member-level search is out of scope.

## Self-Review

- Spec coverage: all six chosen tools (diagnostics, outline, describe_symbol, find_definition, find_references, search_symbols) have tasks (3, 4, 5); transport + CLI + deps in Task 6; the name->symbol bridge in Task 2; the index gap it needs in Task 1. Decisions (Official MCP SDK; semantic-nav scope, no compile) honored.
- Placeholder scan: every code step shows complete code; commands have expected output. The two "if the installed API/path differs" notes are version-robustness guidance, not placeholders - the primary path is fully specified.
- Type consistency: `McpLocation`/`McpDiagnostic`/`McpMatch`/`McpTools` defined in Task 3 and extended in Tasks 4-5 with matching field names; `resolveSymbolRef(ref, index): Symbol[]` signature is stable across Tasks 2 and 5; `getAllTypeFqns` defined in Task 1 and consumed in Task 4.
```
