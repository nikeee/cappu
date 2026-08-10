# Configurable import ordering

## Goal

Let a project decide how `cappu format` groups and orders its import block, by
listing package prefixes in `cappu.json`. Today the order is fixed: statics first,
then every other import in one lexicographic block, which is what
google-java-format produces in google style.

Two things motivate this:

- Teams that follow a house order (framework imports first, `java.*` last) have
  no way to express it short of not using the formatter.
- google-java-format's own AOSP order is not implemented, so `--aosp` output
  differs from gjf's. The same mechanism covers it.

## Configuration

```jsonc
"formatterOptions": {
  "style": "google",
  "importOrder": ["android.*", "com.*", "", "org.*", "*", "", "java.*", "javax.*"]
}
```

`importOrder` is an optional array of strings. Each entry is either a pattern or
the empty string.

- A **pattern** is a package prefix ending in `*`, matched against the import's
  full name: `java.*` selects everything whose name starts with `java.`, and
  `*` selects everything. The `*` is only ever the last character - there is no
  matching inside a name - so the whole rule is
  `name.startsWith(pattern without its trailing "*")` and no glob engine is
  involved. A pattern with a `*` anywhere else, or with none at all, is a
  config error naming the offending entry.
- The **empty string** inserts a blank line at that position. Consecutive
  patterns share one block with no blank line between them.
- **The longest matching prefix wins.** `com.acme.` (9 characters) beats `com.`
  (4), and `*` has an empty prefix, so it loses to everything and works as the
  catch-all wherever it sits. Equal-length prefixes are broken by list order.

  Precedence is deliberately independent of list position, so the list controls
  only *where a block appears*. `["com.*", "", "com.acme.*"]` puts
  `com.acme.Widget` in the second block and every other `com` import in the
  first; if the first match won instead, that configuration could not be
  expressed at all - the general pattern would swallow the specific one and
  leave an empty block. IntelliJ and spotless resolve it the same way.

### Fixed rules, not configurable

- **Static imports** always form their own block, first, sorted
  lexicographically. `importOrder` governs the non-static imports only. This is
  what google-java-format does and what every other Java tool defaults to.
- **Unmatched imports** - those matching no prefix, when the list has no `*` -
  form a final block of their own, separated by a blank line. Adding a
  dependency in an unfamiliar top-level package can then never silently join an
  unrelated group, and formatting never fails over a missing entry.
- **Within a block**: lexicographic by the import's text, as today.
- **Duplicates** are still dropped, and a duplicate's trailing comment still
  moves to the surviving line (existing behaviour).

### Defaults

`importOrder` unset keeps the built-in order for the configured style, so
existing projects see no change and unset always means "byte-identical to
google-java-format":

| style | unset behaviour |
| --- | --- |
| `google` | one block, lexicographic - today's output |
| `aosp` | gjf's AOSP order: android, then third-party, then `java`/`javax`, with a blank line wherever the top-level package changes |

The AOSP order is deliberately **not** expressed as a default `importOrder`
list, because gjf inserts a blank line whenever the top-level package changes -
`com.foo` and `io.bar` split even though both are third-party - and a static
list of prefixes cannot say that. It stays a built-in comparator
(`ImportOrderer.AOSP_IMPORT_COMPARATOR` plus `shouldInsertBlankLineAosp`).
Setting `importOrder` replaces whichever built-in applies.

## Components

**`src/format/import-order.ts` and `togo/internal/format/importorder.go`** - one
pure function taking the parsed imports (name, `isStatic`) and the formatter
options, returning the blocks to print. It owns every rule above: grouping,
blank lines, statics, the unmatched bucket, and the two built-in orders. It has
no dependency on the Doc IR, so it is unit-testable on its own.

**Consumers.** The printer's `importGroup` renders whatever the function
returns, instead of sorting itself. The LSP `source.organizeImports` code
action calls the same function, which also settles an existing contradiction:
the action currently sorts static imports *last* while the formatter puts them
*first*, so organizing a file and then formatting it produced two different
orders.

**Matching** needs no glob engine: strip the trailing `*` once when the config
is read, then compare with `startsWith`. The pattern's shape is validated at
that point, so a malformed entry is reported against `cappu.json` rather than
silently matching nothing.

**Config plumbing**: `importOrder?: string[]` in the zod schema and in the Go
`FormatterOptions` mirror, an entry in the `cappu init` template, and the
regenerated `cappu.schema.json`. The existing test that parses the template
against the schema keeps them in sync.

## Testing

- Unit tests for the ordering function in both builds, one per rule: prefix
  grouping, blank-line entries, longest-prefix precedence (including a specific
  group placed *after* a general one, and a `*` that is not last), statics
  first, the unmatched bucket, duplicate removal, and both built-in orders.
- A golden fixture pinning `--aosp` import order against real
  google-java-format 1.25.2, alongside the existing fixtures that pin the google
  default.
- Code-action tests asserting that organizing imports agrees with formatting
  them.
- Config tests: the schema accepts a valid `importOrder`, rejects a non-string
  entry and a pattern whose `*` is not final, and the template stays
  schema-valid.

## Out of scope

- Choosing where static imports go (always first).
- Wildcard/on-demand collapsing (`import java.util.*`), which cappu never
  introduces.
- Per-directory overrides: one `importOrder` per project.
