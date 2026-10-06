# Testing

Tests are Go tests, run from `togo/`.

## Run everything
```bash
cd togo && go test ./...
```

## Run a single package or a single test
```bash
go test ./internal/compiler/
go test ./internal/compiler/ -run TestEmitterBaselines/EnumMixed
```

## The emitter backend tests (`togo/internal/compiler/emitter.go` / `bytecode.go`)

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

## Regenerating baselines (UPDATE_BASELINES)

`UPDATE_BASELINES=1` blesses an intentional change by writing what the code now
produces instead of asserting the committed baseline. A baseline that does not
exist yet is always written, with or without the flag, so deleting one and
re-running the suite recreates it.

```bash
cd togo && UPDATE_BASELINES=1 go test ./...        # every writable family
UPDATE_BASELINES=1 go test ./internal/compiler/ -run TestEmitterBaselines
```

These families are rewritten:

| fixture | test |
|---|---|
| `emitter/emit-baselines/*.class` | `TestEmitterBaselines` (internal/compiler) |
| `parser/baselines/*.txt` | `TestParserBaselines` (internal/compiler) |
| `decompiler/disasm-baselines/*.txt` | `TestDisasmTextBaselines` (internal/compiler) |
| `decompiler/source-baselines/*.java` | `TestDecompileMatchesSourceBaselines` (internal/cli) |
| `language-service/fourslash-baselines/*.txt` | `TestFourslashCompletions` (internal/services) |
| `language-service/fourslash-hover-baselines/*.txt` | `TestFourslashHover` (internal/services) |
| `format/baselines/<style>/*.output` | `TestFormatGolden` (internal/format), needs a gjf jar |

Two families are deliberately read-only:

- **`emitter/javac-baselines/*.json`** is javac's own output. A leg that
  disagrees with it is reporting a difference from javac, not a stale baseline,
  so rewriting it would erase the finding.
- **`emitter/corpus-baselines/*.json`** records only the (class, method) pairs
  the emitter currently matches javac on, so regenerating re-derives that set
  from whichever emitter runs - a narrow capability gap would silently shrink
  the guard instead of failing it (`corpusKnownGaps` in
  `togo/internal/compiler/emitcorpus_test.go` is an entry that would vanish).
  The committed JSON is also keyed in javac/javap discovery order, which
  `encoding/json` cannot reproduce (it sorts map keys). Growing that baseline is
  a reviewed, manual act.

### The format baselines need a real google-java-format

`format/baselines/<style>/*.output` is only ever written from the REAL
google-java-format, so regenerating needs a jar. Either point `GJF_JAR` at the
all-deps jar, or - when only the maven repo jar is present, which is not
all-deps - point `GJF_CP` at a resolved classpath
(`mvn dependency:build-classpath -Dmdep.outputFile=cp.txt`):

```bash
GJF_JAR=/path/to/google-java-format-all-deps.jar \
  UPDATE_BASELINES=1 go test ./internal/format/
GJF_CP=$(cat cp.txt) UPDATE_BASELINES=1 go test ./internal/format/
```

With neither set, `UPDATE_BASELINES=1` cannot regenerate and the committed
baselines are asserted against instead (a *missing* baseline with no jar is
fatal - there is nothing to compare against). Regeneration is gjf-version
sensitive, so always review the diff: gjf 1.34.1 disagrees with the committed
`72-text-block-deindent` and `73-text-block-dot-chain` baselines over whether a
text block's content is de-indented to column 0.

## Corpus robustness tests (`togo/internal/compiler/emitcorpus_test.go`)

Auto-discovers every git submodule under `test-fixtures/emitter/corpus/` and asserts the emitter
produces class bytes for every `.java` file without throwing (degrading to a
placeholder is fine, crashing is not). No JDK needed. Initialize the corpus
submodules first:
```bash
cd togo && make corpus-init
```
This runs `git submodule update --init` (the submodules are marked
`shallow = true` in `.gitmodules`, so only depth-1 history is cloned) and then
restricts each corpus working tree to `*.java` via sparse-checkout - the only
files the tests read. Sparse-checkout cannot be expressed in `.gitmodules`
(it is per-clone config), which is why it lives in this target rather than the
submodule spec; a plain `git submodule update --init` also works but leaves the
non-Java files on disk. The corpus tests are skipped when no submodule is
checked out, so CI without them still passes.

A second tier, `corpus bytecode matches javac`, checks our emitted bytecode
against javac for real-world code. javac cannot build these projects (external
deps) and we degrade methods using unstubbed types, so the baseline
(`test-fixtures/emitter/corpus-baselines/*.json`) records only the
(class, method) pairs we currently match javac on; the test is then a
regression guard over that set. It has no `UPDATE_BASELINES` write mode (see
"Regenerating baselines" above for why); the run just reads the JSON and
disassembles the baselined classes (seconds).