// A minimal synthetic JDK so common types (java.lang.*, java.util.*) resolve
// without indexing the real JDK. The stubs are ordinary Java source fed through
// the normal parse+bind pipeline as project files, so the resolver and checker
// treat them like any other code. Coverage is intentionally small; unknown
// types degrade to ErrorType rather than producing false diagnostics.

import type { Program } from "./program.ts";

const JAVA_LANG = `package java.lang;
class Object {
  public String toString() { return null; }
  public boolean equals(Object o) { return false; }
  public int hashCode() { return 0; }
  public final Class<?> getClass() { return null; }
}
interface CharSequence { int length(); char charAt(int index); }
interface Comparable<T> { int compareTo(T o); }
interface Iterable<T> { java.util.Iterator<T> iterator(); }
interface Runnable { void run(); }
class Class<T> {}
class Number {
  public int intValue() { return 0; }
  public long longValue() { return 0; }
  public double doubleValue() { return 0.0; }
}
class String implements CharSequence, Comparable<String> {
  public int length() { return 0; }
  public char charAt(int index) { return ' '; }
  public boolean isEmpty() { return false; }
  public String substring(int begin) { return null; }
  public String substring(int begin, int end) { return null; }
  public String concat(String s) { return null; }
  public String[] split(String regex) { return null; }
}
class Integer extends Number implements Comparable<Integer> {
  public static int parseInt(String s) { return 0; }
  public static Integer valueOf(int i) { return null; }
  public int compareTo(Integer o) { return 0; }
}
class Long extends Number {}
class Double extends Number {}
class Float extends Number {}
class Short extends Number {}
class Byte extends Number {}
class Boolean { public static boolean parseBoolean(String s) { return false; } }
class Character {}
class Void {}
class Throwable { public String getMessage() { return null; } }
class Exception extends Throwable {}
class RuntimeException extends Exception {}
class IllegalArgumentException extends RuntimeException {}
class IllegalStateException extends RuntimeException {}
class System { public static void exit(int code) {} }
`;

const JAVA_UTIL = `package java.util;
interface Iterator<E> { boolean hasNext(); E next(); }
interface Collection<E> extends Iterable<E> {
  int size();
  boolean isEmpty();
  boolean add(E e);
  boolean contains(Object o);
}
interface List<E> extends Collection<E> {
  E get(int index);
  E set(int index, E element);
}
interface Set<E> extends Collection<E> {}
interface Map<K, V> {
  V get(Object key);
  V put(K key, V value);
  int size();
  boolean containsKey(Object key);
}
class ArrayList<E> implements List<E> {
  public int size() { return 0; }
  public boolean isEmpty() { return false; }
  public boolean add(E e) { return false; }
  public boolean contains(Object o) { return false; }
  public E get(int index) { return null; }
  public E set(int index, E element) { return null; }
  public java.util.Iterator<E> iterator() { return null; }
}
class HashMap<K, V> implements Map<K, V> {
  public V get(Object key) { return null; }
  public V put(K key, V value) { return null; }
  public int size() { return 0; }
  public boolean containsKey(Object key) { return false; }
}
class Optional<T> {
  public static <T> Optional<T> of(T value) { return null; }
  public T get() { return null; }
  public boolean isPresent() { return false; }
}
`;

export const JDK_STUB_FILES: ReadonlyArray<{ uri: string; text: string }> = [
  { uri: "jdk:///java/lang.java", text: JAVA_LANG },
  { uri: "jdk:///java/util.java", text: JAVA_UTIL },
];

/** Register the synthetic JDK stub into a program. */
export function loadJdkStub(program: Program): void {
  for (const file of JDK_STUB_FILES) {
    program.addProjectFile(file.uri, file.text);
  }
}
