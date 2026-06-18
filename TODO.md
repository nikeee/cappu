# TODO - feature and conformance gaps

Tracks what the compiler backend (and supporting checker/binder) does not yet
handle. Update this list whenever a feature lands or a new gap is found. Each
item references the relevant JLS / JVMS section. In-source `TODO:` comments at
the implementation sites mirror these entries.

Anything unsupported degrades safely: an unhandled method body falls back to a
verifiable placeholder, never a crash.

## Statements

- [x] Instance and static initializer blocks (JLS 8.6 / 8.7): collected alongside
      field initializers in source order and run in the constructor (`<init>`,
      every non-`this()`-delegating ctor) or `<clinit>` respectively. Works for
      top-level/nested and local classes; anonymous classes still reject a body
      with an initializer block.
- [x] `synchronized` statement (JLS 14.19): monitorenter, then the body under a
      finally that runs monitorexit on every exit (normal, return/break, and the
      catch-all exception path).
- [x] `assert` statement (JLS 14.10): synthetic `$assertionsDisabled` field +
      `<clinit>` prologue (`!Class.desiredAssertionStatus()`) + guard/throw
      `AssertionError`. Message uses the `(Object)` constructor (boxing a
      primitive); javac's type-specific message ctors are not matched.
- [x] Pattern `switch` (JLS 14.11.1 / 14.30), arrow form (statement + expression):
      type patterns `case Type t`, record deconstruction `case Point(int x, int y)`
      (incl. nested record patterns and unnamed `_` components), guards `when`,
      `case null`, and `default`, lowered to an if/else-instanceof chain (selector
      evaluated once, NPE when null with no `case null`). Both the arrow form and
      the colon form (`case Integer i:` ... `break;` / `yield`) are emitted; the
      switch end is the break/yield target. Pattern cases do not fall through (JLS
      14.11.1), so each arm is self-contained.

## Expressions

- [x] `instanceof` type-pattern binding `x instanceof T t` (JLS 14.30.1) as the
      matched condition of an `if`/`&&`. The when-true direction (negation, `||`,
      plain value context) still degrades and does not bind.
- [x] `instanceof` record deconstruction `x instanceof Point(int a, int b)` (JLS
      14.30.1) as the matched condition: tests the record type, then binds each
      component pattern via the accessors (nested record patterns recurse). Shares
      the deconstruction machinery with pattern switch.
- [x] `super.m(...)` calls (JLS 15.12.3): non-virtual invokespecial against the
      resolved superclass method on `this`. The checker types `super` as the
      enclosing class's direct superclass so the (overridden) member resolves.
- [x] `super.f` field access (JLS 15.11.2): reads the (hidden) superclass field
      off `this` - field access is non-virtual, so the resolved owner is used.
- [x] Qualified `Outer.this` (JLS 15.8.4): typed as the named enclosing class and
      emitted by routing through `this$0` (the inner class gains `this$0` when its
      body uses a qualified `this`). Qualified `Type.super.m()` still degrades.
- [x] `switch` over a boxed `Integer`/`Short`/`Byte`/`Character` selector: the
      selector is unboxed to int before the int dispatch (JLS 14.11 / 5.1.8).
- [x] Static methods declared in an interface are invoked via an
      InterfaceMethodref (JVMS 4.4.2), not a Methodref.
- [x] Static imports (JLS 7.5.3/7.5.4): a field or method used by its simple name
      resolves through `import static T.member` / `import static T.*` (the member
      is looked up on the named type).
- [x] Array `clone()` (JLS 10.7): `invokevirtual` on the array type with the
      declared `()Ljava/lang/Object;` descriptor, then a checkcast back to the
      array type (covariant) - as javac does.
- [x] Conditional `?:` over unrelated reference arms (JLS 15.25): the checker now
      computes a simplified lub (numeric promotion, the more general arm, null arm
      yields the other, else `Object`) instead of just the then-arm, so both the
      result type and the stack-map frame at the join are a true common supertype.
- [x] Varargs calls (JLS 15.12.4.2): the trailing arguments of a call to a
      `T... xs` method are packed into a fresh `T[]` (with element box/widen), an
      empty varargs slot becomes `new T[0]`, and a single array argument assignable
      to `T[]` (incl. reference-array covariance) is passed as-is (exact form).
      A varargs parameter is also correctly typed as `T[]` inside the method body
      (previously `T`, which degraded e.g. a for-each over the parameter).
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
- [x] Local classes with **instance field initializers alongside capture**: the
      synthesized constructor runs them after the super/this$0/capture prologue
      (shared `emitSynthCtorWithInits` with anonymous classes).
- [x] Local classes with a **declared constructor** alongside capture: the
      captured locals (and this$0) are spliced into the declared constructor as
      leading synthetic parameters - this$0 stored before super(), captures after -
      and the `new` site passes them ahead of the user arguments (shared
      `ctorLeading` machinery with member inner classes). A `this(...)`-delegating
      constructor cannot forward them yet, so such a class is not synthesizable and
      its captures degrade.
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
- [x] **Non-static member inner classes** (`class Outer { class Inner {...} }`)
      that access the enclosing instance: a synthetic `this$0` field (added only
      when the body uses the enclosing instance), spliced into each declared
      constructor as a leading parameter and stored before `super()`; enclosing
      instance field reads/writes and method calls route through `this$0`, and a
      `new Inner(args)` site passes the enclosing instance. An inner class with a
      `this(...)`-delegating constructor gets no `this$0` and its enclosing access
      degrades; accessing an enclosing instance member with no `this$0` route
      degrades to a placeholder instead of emitting invalid bytecode.
- [x] Anonymous classes **accessing inherited members**: overriding a super
      method and calling an inherited (non-overridden) method both work via
      normal virtual dispatch on the emitted subclass. Inherited members
      referenced by **simple name** inside the body (`size`, `describe()` for an
      anon extending an abstract class) now resolve too: the resolver looks names
      up on the anonymous class's supertype (its `new T(){...}` target), since the
      body is not itself a binder container.
- [x] **Anonymous** classes with **own instance fields** (+ initializers): the
      declared fields are emitted and the synthesized constructor runs their
      initializers after the super/this$0/capture prologue (via the body emitter,
      `emitSynthCtorWithInits`); methods read the own fields through the same
      implicit-`this` getfield path as captures. An unsupported initializer
      degrades the ctor to prologue-only (fields keep defaults) rather than
      crashing. Own-field *writes* in methods are emitted too (routed through the
      same implicit-`this` path). Initializer blocks and declared constructors are
      still unsupported.
- [x] User-defined interfaces are now emitted (ACC_INTERFACE|ACC_ABSTRACT, super
      Object, `extends` as super-interfaces): abstract methods (no Code), default
      and static methods (with Code), and implicitly public-static-final constant
      fields (ConstantValue). Interface fields are treated as static at use sites.
- [x] Record declarations (JLS 8.10): final class extending `java.lang.Record`,
      a private final field + accessor per component, a synthesized canonical
      constructor (`super()` then store components), and `equals`/`hashCode`/
      `toString` via the `ObjectMethods` bootstrap (invokedynamic). Emits the
      Record attribute (JVMS 4.7.30); declared (static) fields and methods are
      kept; `new R(...)` resolves the canonical ctor; `r.x()` accessors resolve
      via a binder-synthesized accessor symbol. A **compact constructor** (JLS
      8.10.4) is emitted: its body runs (with the components bound as the
      parameters, so it can validate/reassign them), then each component field is
      assigned from its final parameter value; an unsupported body degrades to the
      implicit canonical ctor. A **full explicit canonical constructor** (params
      matching the components) and **alternate `this(...)`-delegating
      constructors** are emitted as ordinary constructors over java/lang/Record.
      An **explicit accessor override** (`public int x() {...}`) is emitted as a
      declared method, suppressing the implicit accessor for that component; a
      bare reference to the component name inside it reads the field.
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

- [x] A `return` inside a lambda is typed against the SAM's return type (JLS
      15.27.2 / 9.8): `enclosingReturnType` resolves the nearest enclosing
      lambda's instantiated SAM return type (not the outer method's), so a value
      or nested lambda returned from a lambda body is target-typed by the
      functional interface. (Target-typed *inference* of a bare generic call
      from that type - JLS 18.5.2 - is still a separate gap.)
- [x] Type variables erase to their leftmost bound (JLS 4.6): descriptors use the
      bound (`<T extends CharSequence>` -> `Ljava/lang/CharSequence;`), member
      lookup on a type-variable receiver resolves via the bound (JLS 4.4), and the
      synthetic checkcast generalizes from Object to any erased-vs-instantiated
      descriptor mismatch (JLS 5.2). `TypeVariable` carries its bound (resolved
      lazily with a cycle guard for `T extends Comparable<T>`).

## Class-file attributes javac emits that we omit (byte-equivalence)

These do not affect correctness but widen the diff vs javac's output.

- [x] `NestHost` / `NestMembers` (JVMS 4.7.28 / 4.7.29): each top-level type
      emits NestMembers listing its nested/local/anonymous classes, and each of
      those emits NestHost - so nestmates share private access (an inner class
      reading a private outer field, etc.).
- [x] `InnerClasses` (JVMS 4.7.6) for reflection (getEnclosingClass /
      isAnonymousClass); not required for access control under nestmates. Each
      class lists the nested classes referenced while writing its body (intern
      order) followed by its declared-member tree breadth-first (each level in
      reverse-declaration order), every entry preceded by its enclosing class's
      entry, and `MethodHandles$Lookup` appended when the class uses
      invokedynamic - byte-matching javac's order (verified by
      `innerclasses-baselines.json`). Types nested in an interface get the
      implicit public+static flags. An enum with constant bodies is not matched
      (the per-constant `E$N` subclass is not emitted yet).
- [x] `Signature` (JVMS 4.7.9): emitted for generic classes/interfaces/enums
      (type parameters with class/interface bounds, generic supertypes), methods
      and constructors (own type params or generic param/return types; skipped
      when synthetic this$0/capture params were spliced in), and fields. The
      strings byte-match javac's (javap reprints `T get();` etc. identically).
      Records and synthetic members (lambda impls, accessors) are not covered.
- [x] `LineNumberTable` (JVMS 4.7.12): an entry per statement start (1-based,
      trivia-skipped), so stack traces carry source lines - verified equal to
      javac's at runtime via getStackTrace(). Synthetic bodies (no positions)
      simply emit no table.
- [x] `LocalVariableTable` (JVMS 4.7.13): javac emits it only under `-g`, so it
      is gated behind `compilerOptions.experimentalCompiler.debugInfo` (off by
      default, keeping the output byte-identical to default-flags javac). When
      on, each parameter/`this` spans the whole method and each local enters
      scope after its initializing store; entries are ordered by (scope-close
      pc, slot), which reproduces javac's order. Where our bytecode already
      matches javac the table byte-matches `javac -g`
      (`localvariabletable-baselines.json`); where codegen diverges (e.g. string
      concat) the table stays internally correct and JVM-valid. Synthetic
      parameters (this$0/captures/enum name+ordinal) get no entry (no source
      name); `LocalVariableTypeTable` for generic locals is not emitted.
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
