---
name: code-intel
description: "Use when you need to understand or navigate Java code in a cappu project (find definitions, references, callers, implementations, type hierarchies, members, Javadoc, diagnostics, deprecated uses), refactor through the cappu MCP tools (rename_symbol, organize_imports, code_actions, resolve_import), format a file (format), or read compiled .class files and dependency classes (decompile). Not for building or testing - see build-test."
---

# Navigate and refactor Java with the cappu MCP server

`cappu mcp` exposes cappu's language server as MCP tools (`mcp__cappu__*`). It
is strictly read-only: it never writes files, compiles or runs code. Tools that
"refactor" (`rename_symbol`, `organize_imports`, `code_actions`) return edits
and `format` returns the formatted text; you apply them with your editing tool
and verify with `cappu check`. Prefer
these tools over grep for anything symbol-shaped; grep is for strings, config
and comments.

## Setup

- Tools present? Look for `mcp__cappu__diagnostics` in your tool list. If not:
  `claude mcp add cappu -- cappu mcp` (this plugin's `.mcp.json` does that for
  you). The server must start in the project directory.
- The 6 project tools (`audit`, `licenses`, `search_packages`, `outdated`,
  `latest_version`, `dependency_tree`) exist only when a `cappu.json` was found
  at startup. The 15 language tools always exist.
- The server re-reads changed `.java` files and `cappu.json` on every call; no
  restart after editing or after `cappu install`.
- The server banner mentions `cappu help`; the real command is `cappu --help`.

## `ref` grammar

Most tools take `ref`: a type FQN (`com.acme.OrderService`), a simple name
(`OrderService`, resolved when unambiguous), or `Type#member`
(`OrderService#place`, `java.util.List#add`). `file` arguments must be
absolute paths; a relative path returns an empty result instead of an error.

## Tools

| Need | Tool | Notes |
|---|---|---|
| where is a type | `search_symbols(query)` | substring match on indexed fully-qualified names, JDK and dependency types included |
| what is this symbol | `describe_symbol(ref)` | kind, signature, Javadoc, definition location |
| jump to declaration | `find_definition(ref)` | |
| file overview | `outline(file)` | top-level types and members |
| members incl. inherited | `list_members(ref)` | each flagged declared or inherited |
| supertypes and subtypes | `type_hierarchy(ref)` | |
| every use | `find_references(ref)` | workspace-wide |
| who calls this method | `find_callers(ref)` | call hierarchy, one level |
| who implements or overrides | `find_implementations(ref)` | subtypes for a type, overrides for a method |
| compile-like errors | `diagnostics(files?)` | same engine as `cappu check`; omit `files` for the whole workspace |
| deprecated API uses | `deprecated_uses(files?)` | with `since` and `forRemoval` of each declaration |
| which import for `List` | `resolve_import(name)` | FQN candidates |
| rename | `rename_symbol(ref, newName)` | returns the full workspace edit set |
| tidy imports | `organize_imports(file)` | sorts, groups, drops unused; matches `formatterOptions.importOrder` and `cappu format` |
| quick fixes, refactorings | `code_actions(file, startLine, startColumn, endLine?, endColumn?)` | positions are 1-based; end defaults to start |
| format one file | `format(file)` | `{formatted, changed}` as `cappu format --write` would leave it; nothing written |
| source of a dependency class | `decompile(className)` or `decompile(file)` | binary name on the project classPath (`com.acme.Foo$Bar`) or a `.class` path; `disasm: true` for `javap -c -p` layout |

The dependency-facing tools (`dependency_tree`, `audit`, `licenses`,
`search_packages`, `outdated`, `latest_version`) are covered in the
`dependencies` skill.

## Procedure: change a method safely

1. **Scope the impact.** `find_callers(Type#method)` and
   `find_references(Type#method)`; for an interface method also
   `find_implementations`.
2. **Read before editing.** `describe_symbol` for signature and Javadoc,
   `list_members` for what the type already offers.
3. **Edit** with your editing tool.
4. **Verify.** `diagnostics([changed files])`, then `cappu check`, then
   `cappu compile -q` (javac has the final word; see `build-test`).

## Procedure: rename

1. `rename_symbol("OrderService#place", "placeOrder")`.
2. Apply **every** returned edit, file by file, bottom-up within a file so
   earlier edits do not shift later positions. Partial application leaves the
   workspace inconsistent.
3. `diagnostics()` over the touched files, then `cappu compile -q`.

## Procedure: fix imports

`organize_imports(file)` returns one edit replacing the import block; apply it
verbatim. For a missing import, `resolve_import("Duration")` lists candidates;
add the chosen line, then run `organize_imports` again.

## Reading compiled code

For a dependency class without sources, `decompile(className: "com.acme.Foo")`
finds the class on the project's classPath (jars and directories) and returns
reconstructed Java source; `disasm: true` returns bytecode in `javap -c -p`
layout instead. A method the decompiler cannot reconstruct is left as a
commented disassembly plus a `throw`. Without an MCP server:
`unzip -o some.jar 'com/acme/Foo.class' -d /tmp/x`, then
`cappu decompile /tmp/x/com/acme/Foo.class` (`--disasm` for bytecode). No JDK
needed. `describe_symbol` on a dependency type already shows signature and
Javadoc when the jar carries them, so decompile only when you need method
bodies.

## Gotchas checklist

- [ ] MCP answers, you edit: nothing from `rename_symbol`, `organize_imports`, `code_actions` or `format` is written for you.
- [ ] `code_actions` positions are 1-based lines and columns; `file` arguments are absolute paths (`format` and `decompile` also take one relative to the server's working directory).
- [ ] Project tools missing? The server started outside a `cappu.json` project; restart it in the project root.
- [ ] `diagnostics` is cappu's checker, not javac; confirm with `cappu compile`.
- [ ] Prefer `Type#member` refs; simple names fail when ambiguous.
- [ ] Anything that writes or runs (compile, test, format -w, add, install) is CLI via Bash.
