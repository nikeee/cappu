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
- [x] Pattern `switch` (JLS 14.11.1 / 14.30), arrow form (statement + expression):
      type patterns `case Type t`, guards `when`, `case null`, and `default`,
      lowered to an if/else-instanceof chain (selector evaluated once, NPE when
      null with no `case null`). Record/nested patterns and the colon form remain.

## Expressions

- [x] `instanceof` type-pattern binding `x instanceof T t` (JLS 14.30.1) as the
      matched condition of an `if`/`&&`. The when-true direction (negation, `||`,
      plain value context) still degrades and does not bind.
- [x] Array constructor references `T[]::new` (JLS 15.13.3): bound to a synthetic
      `(int) -> new T[len]` helper via invokedynamic (REF_invokeStatic).

## Classes and members

- [x] Local classes (JLS 14.3) **without capture**: emitted as `Outer$Name`
      (binaryName recovers the enclosing type from the AST; the local-class
      declaration statement is a no-op in a method body). Language service
      (resolution/hover/completion/references) already works for them.
- [x] Local classes capturing enclosing **locals/parameters**: synthetic final
      `val$x` fields, a synthesized constructor that stores them, body reads
      rewritten to `getfield`, and the `new` site passing the captured values.
      Limited to the clean case (no declared constructor, no instance field
      initializers); otherwise capture is skipped (those bodies degrade).
- [ ] Local classes capturing the enclosing instance (`this$0` for outer fields/
      methods), declared-constructor augmentation, and instance field initializers
      alongside capture.
- [x] Anonymous classes implementing an **interface** (JLS 15.9.5): the classBody
      is emitted as `Outer$N` (numbered by position) implementing the interface,
      capturing enclosing locals (reuses the local-class capture machinery), with
      a synthesized `super()`+store-captures constructor. Method bodies resolve
      via lexical scope.
- [x] Anonymous classes **extending a class**: the synthesized constructor takes
      the super-constructor arguments as trailing parameters (resolved via the
      matching super ctor) and the `new` site passes them after the captures.
- [x] Anonymous classes accessing the **enclosing instance** (`this$0`): a
      non-static enclosing context where the body reads a non-static outer field
      or calls a non-static outer method captures `this$0` (a synthetic field +
      leading constructor parameter), and implicit-`this` access to an enclosing
      member routes through it.
- [x] **Local-class** `this$0`: same machinery as anonymous, wired through the
      synthesized constructor and the `new`-site (alongside local captures).
- [ ] Anonymous/local classes with **own fields, initializer blocks, declared
      constructors alongside capture, or inherited-member access** (needs
      binder/checker scoping of the body and constructor augmentation).
- [x] User-defined interfaces are now emitted (ACC_INTERFACE|ACC_ABSTRACT, super
      Object, `extends` as super-interfaces): abstract methods (no Code), default
      and static methods (with Code), and implicitly public-static-final constant
      fields (ConstantValue). Interface fields are treated as static at use sites.
- [x] Explicit constructor invocations (JLS 8.8.7.1): a leading `super(args)` or
      `this(args)`; `this(...)` skips this constructor's field initializers. The
      target overload is resolved by argument count (as for `new`).

## Try-with-resources (JLS 14.20.3) - partially done

- [x] Resource open/close, reverse-order close on every exit, suppressed
      exceptions via `Throwable.addSuppressed`.
- [x] Resource variable binding, so the body can reference the resource.
- [x] Null guard `if (r != null) r.close()` (JLS 14.20.3.1), on both the normal
      and exceptional close paths; elided for a `new` resource (definitely
      non-null), as javac does.
- [x] Variable-access resource form `try (existingVar)` (SE9): the resource
      value is materialized into a slot and closed like the declaration form.

## Checker

- [ ] A `return` inside a lambda is not typed against the SAM's return type
      (JLS 15.27.2 / 9.8).

## Class-file attributes javac emits that we omit (byte-equivalence)

These do not affect correctness but widen the diff vs javac's output.

- [x] `NestHost` / `NestMembers` (JVMS 4.7.28 / 4.7.29): each top-level type
      emits NestMembers listing its nested/local/anonymous classes, and each of
      those emits NestHost - so nestmates share private access (an inner class
      reading a private outer field, etc.).
- [ ] `InnerClasses` (JVMS 4.7.6) for reflection (getEnclosingClass /
      isAnonymousClass); not required for access control under nestmates.
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
- [x] `synchronized` (JLS 14.19) and `assert` (JLS 14.10).
- [x] Reference equality `==`/`!=` incl. null, and boolean `&`/`|`/`^` - verified
      working (via emitBoolean/emitBranch and the integer bitwise path).
