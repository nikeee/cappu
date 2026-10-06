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

When an intentional change alters emitted bytecode, regenerate both baseline
kinds. This requires `javac`, `java`, and `javap` on PATH:
```bash
UPDATE_BASELINES=1 go test ./internal/compiler/ -run TestEmitterBaselines
```
This rewrites the binary `.class` baselines with what the emitter now produces;
commit them. The `emitter/javac-baselines/*.json` references are javac's own
output and are never rewritten - a leg that disagrees with them is reporting a
difference from javac, not a stale baseline. Without the flag the committed
bytes are asserted against, never overwritten.

## Corpus robustness tests (`togo/internal/compiler/emitcorpus_test.go`)

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