package compiler

import (
	"fmt"
	"reflect"
	"testing"
)

// Empirical binding-coverage check, independent of ForEachChild: walk the tree
// by reflecting over each node payload's own child fields and assert every child
// node's Parent points back at it. A child that forEachChild forgets to visit is
// still present in the tree (the parser stored it in a field) but never gets a
// parent pointer set by the binder, so this catches such gaps for real.
// Port of src/compiler/binding.test.ts.

func reflectChildNodes(node *Node) []*Node {
	out := []*Node{}
	if node.data == nil {
		return out
	}
	v := reflect.ValueOf(node.data)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return out
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanInterface() {
			continue // unexported field: never a child node
		}
		switch child := f.Interface().(type) {
		case *Node:
			if child != nil {
				out = append(out, child)
			}
		case *NodeArray:
			if child != nil {
				for _, e := range child.Nodes {
					if e != nil {
						out = append(out, e)
					}
				}
			}
		}
	}
	return out
}

const bindingSample = `package com.example.app;

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
`

func TestNoBindingGaps(t *testing.T) {
	sf := ParseSourceFile("file:///Sample.java", bindingSample)
	BindSourceFile(sf)

	var gaps []string
	var visit func(node *Node)
	visit = func(node *Node) {
		for _, child := range reflectChildNodes(node) {
			if child.Parent != node {
				gaps = append(gaps, fmt.Sprintf("kind %v under kind %v [%d..%d]", child.Kind, node.Kind, child.Pos, child.End))
			}
			visit(child)
		}
	}
	// The source file is the root; its own parent is left unset by the binder.
	visit(sf)

	if len(gaps) != 0 {
		t.Errorf("binding gaps: %v", gaps)
	}
}
