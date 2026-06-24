# nullness-app

A one-file project demonstrating cappu's [jspecify](https://jspecify.dev/docs/spec/)
nullness checking (nikeee/cappu#25). It is enabled in `cappu.json`:

```jsonc
"compilerOptions": {
  "nullness": { "enabled": true }
}
```

`src/main/java/example/Main.java` is `@NullMarked`, so unannotated reference types
are non-null and `@Nullable` marks the exceptions. Open it in an editor connected
to the cappu language server:

- `shout(lookup("greeting"))` is **flagged** - `lookup` returns `@Nullable String`
  but `shout` requires a non-null argument.
- after `if (found != null)` the same `found` is **accepted** - the guard proves it
  non-null (flow-aware narrowing; `Objects.requireNonNull`, early `return`, `&&`,
  `instanceof` and reassignment narrow the same way).

The program builds and runs normally - nullness is a language-server diagnostic, not
a build error:

```sh
cappu install                  # downloads org.jspecify:jspecify into .cappu/lib/classes
cappu compile                  # builds dist/
java -cp dist example.Main     # prints HELLO! then WORLD!
```
