# TODO - feature and conformance gaps

Tracks what the compiler backend (and supporting checker/binder) does not yet
handle. Update this list whenever a feature lands or a new gap is found. Each
item references the relevant JLS / JVMS section. In-source `TODO:` comments at
the implementation sites mirror these entries.

Anything unsupported degrades safely: an unhandled method body falls back to a
verifiable placeholder, never a crash.

## Statements

- [x] `synchronized` statement (JLS 14.19): monitorenter, then the body under a
      finally that runs monitorexit on every exit (normal, return/break, and the
      catch-all exception path).
- [x] `assert` statement (JLS 14.10): synthetic `$assertionsDisabled` field +
      `<clinit>` prologue (`!Class.desiredAssertionStatus()`) + guard/throw
      `AssertionError`. Message uses the `(Object)` constructor (boxing a
      primitive); javac's type-specific message ctors are not matched.
- [ ] Pattern / guarded `switch` labels (JLS 14.11.1 / 14.30): `case Type t`,
      record patterns, `case ... when guard`.

## Expressions

- [ ] `instanceof` type-pattern binding `x instanceof T t` (JLS 14.30.1 / 15.20.2).
- [ ] Reference equality `==` / `!=` (JLS 15.21.3, if_acmpeq/if_acmpne).
- [ ] Boolean bitwise `&` / `|` / `^` on `Boolean`/`boolean` operands (JLS 15.22.2).
- [ ] Array constructor references `T[]::new` (JLS 15.13.3).

## Classes and members

- [ ] Anonymous classes `new T(){...}` (JLS 15.9.5) and local classes (JLS 14.3):
      emit as their own `Outer$N` class files (currently skipped).
- [ ] Explicit constructor invocations (JLS 8.8.7.1): a leading `super(args)` or
      `this(args)` (only an implicit no-arg `super()` is emitted today).

## Try-with-resources (JLS 14.20.3) - partially done

- [x] Resource open/close, reverse-order close on every exit, suppressed
      exceptions via `Throwable.addSuppressed`.
- [x] Resource variable binding, so the body can reference the resource.
- [ ] Null guard `if (r != null) r.close()` (JLS 14.20.3.1); resources are
      assumed non-null.
- [ ] Variable-access resource form `try (existingVar)` (SE9).

## Checker

- [ ] A `return` inside a lambda is not typed against the SAM's return type
      (JLS 15.27.2 / 9.8).

## Class-file attributes javac emits that we omit (byte-equivalence)

These do not affect correctness but widen the diff vs javac's output.

- [ ] `InnerClasses` (JVMS 4.7.6), `NestHost` / `NestMembers` (4.7.28 / 4.7.29)
      for nested types.
- [ ] `Signature` (JVMS 4.7.9) for generic signatures.
- [ ] `LineNumberTable` (JVMS 4.7.12) and `LocalVariableTable` (4.7.13).
- [ ] `RuntimeVisibleAnnotations` (JVMS 4.7.16).

## Done (recent)

- [x] Labeled `break` / `continue` (JLS 14.7).
- [x] `try` / `catch` / `finally` with suppressed-exception finally (JLS 14.20.2).
- [x] Try-with-resources core (JLS 14.20.3).
- [x] Enhanced `for` over arrays and `Iterable` (JLS 14.14.2).
- [x] Switch statements/expressions incl. string and enum dispatch (JLS 14.11).
- [x] Lambdas, method references, autoboxing, enums, arrays.
