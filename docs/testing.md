# Testing

Tests use the Node test runner via `tsx` (TypeScript sources run directly).

## Run everything
```bash
node --run test # all src/**/*.test.ts
```

## Run a single file or a single test
```bash
node_modules/.bin/tsx --test ./src/compiler/emitter.test.ts
node_modules/.bin/tsx --test --test-name-pattern="synchronized" ./src/compiler/emitter.test.ts
```

## The emitter backend tests (`src/compiler/emitter.ts` / `src/compiler/bytecode.ts`)

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
UPDATE_BASELINES=1 node_modules/.bin/tsx --test ./src/compiler/emitter.test.ts
```
This rewrites the binary `.class` baselines and the `emitter/javac-baselines/*.json`
references (recompiling each fixture with `javac --release 21`), and re-runs
`runsLikeJavac` against a live `javac` to confirm the hard-coded expected
stdout still matches. Commit the regenerated fixtures. Without the flag, a
missing baseline is auto-created (when a JDK is present) but existing ones are
asserted against, never overwritten.

## Corpus robustness tests (`src/compiler/emit-corpus.test.ts`)

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