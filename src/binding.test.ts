import { test } from "node:test";
import { expect } from "expect";

import { bindSourceFile } from "./binder.ts";
import { parseSourceFile } from "./parser.ts";
import type { Node } from "./types.ts";

// Empirical binding-coverage check, independent of forEachChild: walk the tree
// by reflecting over each node's own child fields and assert every child node's
// `parent` points back at it. A child that forEachChild forgets to visit is
// still present in the tree (the parser stored it in a field) but never gets a
// parent pointer set by the binder, so this catches such gaps for real.

// Non-child meta fields on a Node that must not be treated as children.
const SKIP = new Set(["parent", "symbol", "locals", "flags", "hasTrailingComma", "pos", "end"]);

function isNode(x: unknown): x is Node {
  return (
    typeof x === "object" &&
    x !== null &&
    typeof (x as Node).kind === "number" &&
    typeof (x as Node).pos === "number" &&
    typeof (x as Node).end === "number"
  );
}

function childNodes(node: Node): Node[] {
  const out: Node[] = [];
  for (const key of Object.keys(node)) {
    if (SKIP.has(key)) continue;
    const value = (node as unknown as Record<string, unknown>)[key];
    if (isNode(value)) out.push(value);
    else if (Array.isArray(value)) for (const e of value) if (isNode(e)) out.push(e);
  }
  return out;
}

// A wide sample exercising most node kinds (types, generics, statements,
// expressions, patterns, modules-adjacent constructs, lambdas, switch).
const SAMPLE = `package com.example.app;

import java.util.List;
import java.util.*;
import static java.lang.Math.max;

@Deprecated
public sealed class Outer<T extends Comparable<T> & Cloneable> extends Base<T>
    implements Iterable<T>, Runnable permits Sub {

  static final int CONST = 1 + 2 * 3;
  private List<? extends Number> nums = new ArrayList<>();
  String[] names = { "a", "b" };

  static { CONST_INIT(); }
  { instanceInit(); }

  <R> R apply(java.util.function.Function<T, R> f, T input) throws Exception {
    R result = f.apply(input);
    return result;
  }

  void control(int n, Object o) {
    for (int i = 0; i < n; i++) { use(i); }
    for (String s : names) { use(s); }
    while (n > 0) { n--; }
    do { n++; } while (n < 10);
    if (n == 0) { return; } else { use(n); }
    switch (n) {
      case 1, 2 -> use(n);
      default -> { use(0); }
    }
    int y = switch (n) { case 0 -> 1; default -> { yield 2; } };
    try (var r = open()) { r.read(); }
    catch (IllegalStateException | IllegalArgumentException ex) { use(ex); }
    finally { cleanup(); }
    synchronized (this) { use(o); }
    assert n >= 0 : "neg";
    Runnable lam = () -> use(n);
    java.util.function.Function<T, T> id = (T x) -> x;
    var ref = Outer::new;
    Object cast = (List<String> & Runnable) o;
    boolean b = o instanceof String str && str.length() > 0;
    if (o instanceof Point(int px, int py)) { use(px + py); }
    label: for (;;) { break label; }
  }

  record Point(int px, int py) implements Cloneable {}
  enum Color { RED, GREEN { void m() {} }; void m() {} }
  interface Ifc { default int d() { return 1; } }
  @interface Marker { String value() default "x"; }
}
`;

test("every child node has its parent pointer set (no binding gaps)", () => {
  const sf = parseSourceFile("file:///Sample.java", SAMPLE);
  bindSourceFile(sf);

  const gaps: string[] = [];
  const visit = (node: Node): void => {
    for (const child of childNodes(node)) {
      if (child.parent !== node) {
        gaps.push(`kind ${child.kind} under kind ${node.kind} [${child.pos}..${child.end}]`);
      }
      visit(child);
    }
  };
  // The source file is the root; its own parent is left unset by the binder.
  visit(sf);

  expect(gaps).toEqual([]);
});
