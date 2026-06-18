// Branded (nominal) types: a primitive carrying a compile-time-only tag so
// domain values cannot be mixed up even though they share a runtime
// representation. The brand exists purely in the type system - `as` at the
// producing boundary is the only ceremony, and a branded value still flows
// freely INTO plain number/string parameters (the brand only stops the
// reverse: passing an arbitrary primitive where the domain type is required).
//
//   type CpIndex = Brand<number, "CpIndex">;
//   function utf8(text: string): CpIndex { return intern(...) as CpIndex; }
//   function ldc(index: CpIndex): void { ... }   // ldc(42) is now a type error
//
// Candidate inventory (see the commit that introduced this file): constant
// pool indices, bytecode offsets (pc), local slots, source offsets vs
// line/character, JVM descriptors vs internal names vs FQNs, uris vs fs paths.

declare const brand: unique symbol;

export type Brand<T, Name extends string> = T & { readonly [brand]: Name };
