// A synthetic JDK so common types (java.lang, java.io, java.util,
// java.util.function) resolve without indexing the real JDK. The stubs are
// ordinary Java source fed through the normal parse+bind pipeline as project
// files, so the resolver and checker treat them like any other code.
//
// Coverage is broad but not complete: it models the most common API surface so
// that member access on these types resolves (and the unresolved-member
// diagnostic does not false-positive on everyday code). Methods that return a
// type we do not model (e.g. streams) are intentionally omitted.

import type { Program } from "./program.ts";

const JAVA_LANG = `package java.lang;

class Object {
  public String toString() { return null; }
  public boolean equals(Object o) { return false; }
  public int hashCode() { return 0; }
  public final Class<?> getClass() { return null; }
  protected Object clone() { return null; }
}

interface CharSequence {
  int length();
  char charAt(int index);
  boolean isEmpty();
  CharSequence subSequence(int start, int end);
  String toString();
}

interface Comparable<T> { int compareTo(T o); }

interface Iterable<T> { java.util.Iterator<T> iterator(); }

interface Runnable { void run(); }

interface AutoCloseable { void close(); }

class Class<T> {
  public String getName() { return null; }
  public String getSimpleName() { return null; }
  public boolean isInstance(Object o) { return false; }
}

class Number {
  public int intValue() { return 0; }
  public long longValue() { return 0; }
  public float floatValue() { return 0; }
  public double doubleValue() { return 0.0; }
  public byte byteValue() { return 0; }
  public short shortValue() { return 0; }
}

class String implements CharSequence, Comparable<String> {
  public int length() { return 0; }
  public boolean isEmpty() { return false; }
  public char charAt(int index) { return ' '; }
  public CharSequence subSequence(int start, int end) { return null; }
  public int compareTo(String o) { return 0; }
  public int compareToIgnoreCase(String o) { return 0; }
  public boolean equalsIgnoreCase(String s) { return false; }
  public String substring(int begin) { return null; }
  public String substring(int begin, int end) { return null; }
  public String concat(String s) { return null; }
  public String trim() { return null; }
  public String strip() { return null; }
  public String toUpperCase() { return null; }
  public String toLowerCase() { return null; }
  public String replace(CharSequence target, CharSequence replacement) { return null; }
  public String repeat(int count) { return null; }
  public boolean contains(CharSequence s) { return false; }
  public boolean startsWith(String prefix) { return false; }
  public boolean endsWith(String suffix) { return false; }
  public int indexOf(String s) { return 0; }
  public int indexOf(int ch) { return 0; }
  public int lastIndexOf(String s) { return 0; }
  public boolean matches(String regex) { return false; }
  public String[] split(String regex) { return null; }
  public char[] toCharArray() { return null; }
  public byte[] getBytes() { return null; }
  public boolean isBlank() { return false; }
  public static String valueOf(Object o) { return null; }
  public static String valueOf(int i) { return null; }
  public static String format(String fmt, Object... args) { return null; }
  public static String join(CharSequence delimiter, CharSequence... elements) { return null; }
}

class StringBuilder implements CharSequence {
  public int length() { return 0; }
  public boolean isEmpty() { return false; }
  public char charAt(int index) { return ' '; }
  public CharSequence subSequence(int start, int end) { return null; }
  public StringBuilder append(String s) { return null; }
  public StringBuilder append(Object o) { return null; }
  public StringBuilder append(int i) { return null; }
  public StringBuilder append(char c) { return null; }
  public StringBuilder append(boolean b) { return null; }
  public StringBuilder insert(int offset, String s) { return null; }
  public StringBuilder reverse() { return null; }
  public StringBuilder deleteCharAt(int index) { return null; }
  public String toString() { return null; }
}

class Boolean implements Comparable<Boolean> {
  public boolean booleanValue() { return false; }
  public int compareTo(Boolean o) { return 0; }
  public static boolean parseBoolean(String s) { return false; }
  public static Boolean valueOf(boolean b) { return null; }
  public static String toString(boolean b) { return null; }
}

class Integer extends Number implements Comparable<Integer> {
  public int compareTo(Integer o) { return 0; }
  public static int parseInt(String s) { return 0; }
  public static Integer valueOf(int i) { return null; }
  public static Integer valueOf(String s) { return null; }
  public static String toString(int i) { return null; }
  public static int max(int a, int b) { return 0; }
  public static int min(int a, int b) { return 0; }
}

class Long extends Number implements Comparable<Long> {
  public int compareTo(Long o) { return 0; }
  public static long parseLong(String s) { return 0; }
  public static Long valueOf(long l) { return null; }
}

class Double extends Number implements Comparable<Double> {
  public int compareTo(Double o) { return 0; }
  public static double parseDouble(String s) { return 0; }
  public static Double valueOf(double d) { return null; }
}

class Float extends Number implements Comparable<Float> {
  public int compareTo(Float o) { return 0; }
}

class Short extends Number implements Comparable<Short> {
  public int compareTo(Short o) { return 0; }
}

class Byte extends Number implements Comparable<Byte> {
  public int compareTo(Byte o) { return 0; }
}

class Character implements Comparable<Character> {
  public char charValue() { return ' '; }
  public int compareTo(Character o) { return 0; }
  public static boolean isDigit(char c) { return false; }
  public static boolean isLetter(char c) { return false; }
  public static boolean isWhitespace(char c) { return false; }
  public static char toUpperCase(char c) { return ' '; }
  public static char toLowerCase(char c) { return ' '; }
}

class Void {}

class Math {
  public static int abs(int a) { return 0; }
  public static long abs(long a) { return 0; }
  public static double abs(double a) { return 0; }
  public static int max(int a, int b) { return 0; }
  public static int min(int a, int b) { return 0; }
  public static double max(double a, double b) { return 0; }
  public static double min(double a, double b) { return 0; }
  public static double sqrt(double a) { return 0; }
  public static double pow(double a, double b) { return 0; }
  public static double floor(double a) { return 0; }
  public static double ceil(double a) { return 0; }
  public static long round(double a) { return 0; }
  public static double random() { return 0; }
}

class System {
  public static final java.io.PrintStream out = null;
  public static final java.io.PrintStream err = null;
  public static final java.io.InputStream in = null;
  public static long currentTimeMillis() { return 0; }
  public static long nanoTime() { return 0; }
  public static void exit(int code) {}
  public static String getProperty(String key) { return null; }
  public static String lineSeparator() { return null; }
}

class Thread implements Runnable {
  public void run() {}
  public void start() {}
  public String getName() { return null; }
  public static Thread currentThread() { return null; }
  public static void sleep(long millis) {}
}

class Throwable {
  public String getMessage() { return null; }
  public String getLocalizedMessage() { return null; }
  public Throwable getCause() { return null; }
  public void printStackTrace() {}
  public StackTraceElement[] getStackTrace() { return null; }
}
class StackTraceElement {}
class Error extends Throwable {}
class AssertionError extends Error {}
class Exception extends Throwable {}
class RuntimeException extends Exception {}
class IllegalArgumentException extends RuntimeException {}
class IllegalStateException extends RuntimeException {}
class NullPointerException extends RuntimeException {}
class IndexOutOfBoundsException extends RuntimeException {}
class ArrayIndexOutOfBoundsException extends IndexOutOfBoundsException {}
class ClassCastException extends RuntimeException {}
class UnsupportedOperationException extends RuntimeException {}
class NumberFormatException extends IllegalArgumentException {}
class ArithmeticException extends RuntimeException {}
class InterruptedException extends Exception {}

@interface Override {}
@interface Deprecated {}
@interface SuppressWarnings { String[] value(); }
@interface FunctionalInterface {}
@interface SafeVarargs {}
`;

const JAVA_IO = `package java.io;

interface Closeable extends java.lang.AutoCloseable { void close(); }
interface Flushable { void flush(); }

class InputStream implements Closeable {
  public int read() { return 0; }
  public void close() {}
}

class OutputStream implements Closeable, Flushable {
  public void write(int b) {}
  public void flush() {}
  public void close() {}
}

class PrintStream extends OutputStream {
  public void print(String s) {}
  public void print(Object o) {}
  public void print(int i) {}
  public void print(char c) {}
  public void print(boolean b) {}
  public void print(long l) {}
  public void print(double d) {}
  public void println() {}
  public void println(String s) {}
  public void println(Object o) {}
  public void println(int i) {}
  public void println(char c) {}
  public void println(boolean b) {}
  public void println(long l) {}
  public void println(float f) {}
  public void println(double d) {}
  public PrintStream printf(String format, Object... args) { return null; }
  public PrintStream append(CharSequence s) { return null; }
}

class IOException extends java.lang.Exception {}
class UncheckedIOException extends java.lang.RuntimeException {}
`;

const JAVA_UTIL = `package java.util;

interface Iterator<E> {
  boolean hasNext();
  E next();
  void remove();
}

interface Comparator<T> {
  int compare(T a, T b);
}

interface Collection<E> extends Iterable<E> {
  int size();
  boolean isEmpty();
  boolean contains(Object o);
  boolean add(E e);
  boolean remove(Object o);
  boolean addAll(Collection<? extends E> c);
  void clear();
  Object[] toArray();
}

interface List<E> extends Collection<E> {
  E get(int index);
  E set(int index, E element);
  void add(int index, E element);
  E remove(int index);
  int indexOf(Object o);
  int lastIndexOf(Object o);
  List<E> subList(int from, int to);
}

interface Set<E> extends Collection<E> {}

interface Queue<E> extends Collection<E> {
  E peek();
  E poll();
  boolean offer(E e);
}

interface Map<K, V> {
  int size();
  boolean isEmpty();
  V get(Object key);
  V getOrDefault(Object key, V defaultValue);
  V put(K key, V value);
  V putIfAbsent(K key, V value);
  V remove(Object key);
  boolean containsKey(Object key);
  boolean containsValue(Object value);
  void clear();
  Set<K> keySet();
  Collection<V> values();
  Set<Map.Entry<K, V>> entrySet();
  interface Entry<K, V> {
    K getKey();
    V getValue();
    V setValue(V value);
  }
}

class ArrayList<E> implements List<E> {
  public int size() { return 0; }
  public boolean isEmpty() { return false; }
  public boolean contains(Object o) { return false; }
  public boolean add(E e) { return false; }
  public boolean remove(Object o) { return false; }
  public boolean addAll(Collection<? extends E> c) { return false; }
  public void clear() {}
  public Object[] toArray() { return null; }
  public E get(int index) { return null; }
  public E set(int index, E element) { return null; }
  public void add(int index, E element) {}
  public E remove(int index) { return null; }
  public int indexOf(Object o) { return 0; }
  public int lastIndexOf(Object o) { return 0; }
  public List<E> subList(int from, int to) { return null; }
  public java.util.Iterator<E> iterator() { return null; }
}

class LinkedList<E> implements List<E>, Queue<E> {
  public int size() { return 0; }
  public boolean isEmpty() { return false; }
  public boolean contains(Object o) { return false; }
  public boolean add(E e) { return false; }
  public boolean remove(Object o) { return false; }
  public boolean addAll(Collection<? extends E> c) { return false; }
  public void clear() {}
  public Object[] toArray() { return null; }
  public E get(int index) { return null; }
  public E set(int index, E element) { return null; }
  public void add(int index, E element) {}
  public E remove(int index) { return null; }
  public int indexOf(Object o) { return 0; }
  public int lastIndexOf(Object o) { return 0; }
  public List<E> subList(int from, int to) { return null; }
  public E peek() { return null; }
  public E poll() { return null; }
  public boolean offer(E e) { return false; }
  public java.util.Iterator<E> iterator() { return null; }
}

class HashSet<E> implements Set<E> {
  public int size() { return 0; }
  public boolean isEmpty() { return false; }
  public boolean contains(Object o) { return false; }
  public boolean add(E e) { return false; }
  public boolean remove(Object o) { return false; }
  public boolean addAll(Collection<? extends E> c) { return false; }
  public void clear() {}
  public Object[] toArray() { return null; }
  public java.util.Iterator<E> iterator() { return null; }
}

class TreeSet<E> implements Set<E> {
  public int size() { return 0; }
  public boolean isEmpty() { return false; }
  public boolean contains(Object o) { return false; }
  public boolean add(E e) { return false; }
  public boolean remove(Object o) { return false; }
  public boolean addAll(Collection<? extends E> c) { return false; }
  public void clear() {}
  public Object[] toArray() { return null; }
  public java.util.Iterator<E> iterator() { return null; }
}

class HashMap<K, V> implements Map<K, V> {
  public int size() { return 0; }
  public boolean isEmpty() { return false; }
  public V get(Object key) { return null; }
  public V getOrDefault(Object key, V defaultValue) { return null; }
  public V put(K key, V value) { return null; }
  public V putIfAbsent(K key, V value) { return null; }
  public V remove(Object key) { return null; }
  public boolean containsKey(Object key) { return false; }
  public boolean containsValue(Object value) { return false; }
  public void clear() {}
  public Set<K> keySet() { return null; }
  public Collection<V> values() { return null; }
  public Set<Map.Entry<K, V>> entrySet() { return null; }
}

class TreeMap<K, V> implements Map<K, V> {
  public int size() { return 0; }
  public boolean isEmpty() { return false; }
  public V get(Object key) { return null; }
  public V getOrDefault(Object key, V defaultValue) { return null; }
  public V put(K key, V value) { return null; }
  public V putIfAbsent(K key, V value) { return null; }
  public V remove(Object key) { return null; }
  public boolean containsKey(Object key) { return false; }
  public boolean containsValue(Object value) { return false; }
  public void clear() {}
  public Set<K> keySet() { return null; }
  public Collection<V> values() { return null; }
  public Set<Map.Entry<K, V>> entrySet() { return null; }
}

class Optional<T> {
  public static <T> Optional<T> of(T value) { return null; }
  public static <T> Optional<T> ofNullable(T value) { return null; }
  public static <T> Optional<T> empty() { return null; }
  public T get() { return null; }
  public boolean isPresent() { return false; }
  public boolean isEmpty() { return false; }
  public T orElse(T other) { return null; }
}

class Objects {
  public static boolean equals(Object a, Object b) { return false; }
  public static int hashCode(Object o) { return 0; }
  public static int hash(Object... values) { return 0; }
  public static String toString(Object o) { return null; }
  public static <T> T requireNonNull(T obj) { return null; }
  public static <T> T requireNonNull(T obj, String message) { return null; }
  public static boolean isNull(Object o) { return false; }
  public static boolean nonNull(Object o) { return false; }
}

class Arrays {
  public static <T> List<T> asList(T... a) { return null; }
  public static String toString(Object[] a) { return null; }
  public static void sort(Object[] a) {}
  public static <T> T[] copyOf(T[] original, int newLength) { return null; }
  public static void fill(Object[] a, Object val) {}
}

class Collections {
  public static <T> List<T> emptyList() { return null; }
  public static <T> Set<T> emptySet() { return null; }
  public static <T> void sort(List<T> list) {}
  public static <T> List<T> unmodifiableList(List<? extends T> list) { return null; }
}

class NoSuchElementException extends java.lang.RuntimeException {}
class ConcurrentModificationException extends java.lang.RuntimeException {}
`;

const JAVA_UTIL_FUNCTION = `package java.util.function;

interface Function<T, R> { R apply(T t); }
interface BiFunction<T, U, R> { R apply(T t, U u); }
interface Supplier<T> { T get(); }
interface Consumer<T> { void accept(T t); }
interface BiConsumer<T, U> { void accept(T t, U u); }
interface Predicate<T> { boolean test(T t); }
interface BiPredicate<T, U> { boolean test(T t, U u); }
interface UnaryOperator<T> { T apply(T t); }
interface BinaryOperator<T> { T apply(T a, T b); }
`;

export const JDK_STUB_FILES: ReadonlyArray<{ uri: string; text: string }> = [
  { uri: "jdk:///java/lang.java", text: JAVA_LANG },
  { uri: "jdk:///java/io.java", text: JAVA_IO },
  { uri: "jdk:///java/util.java", text: JAVA_UTIL },
  { uri: "jdk:///java/util/function.java", text: JAVA_UTIL_FUNCTION },
];

/** Register the synthetic JDK stub into a program. */
export function loadJdkStub(program: Program): void {
  for (const file of JDK_STUB_FILES) {
    program.addProjectFile(file.uri, file.text);
  }
}
