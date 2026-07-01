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
import { type Uri } from "../workspace.ts";

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

interface Iterable<T> {
  java.util.Iterator<T> iterator();
  void forEach(java.util.function.Consumer<? super T> action);
}

interface Runnable { void run(); }

interface AutoCloseable { void close(); }

class ClassLoader {
  public static ClassLoader getSystemClassLoader() { return null; }
  public Class<?> loadClass(String name) throws ClassNotFoundException { return null; }
}

class Class<T> {
  public String getName() { return null; }
  public String getSimpleName() { return null; }
  public boolean isInstance(Object o) { return false; }
  public boolean desiredAssertionStatus() { return false; }
  public java.lang.reflect.Constructor<T> getConstructor(Class<?>... parameterTypes) { return null; }
  public Package getPackage() { return null; }
}

class Package {
  public String getName() { return null; }
}

class Enum<E> implements Comparable<E> {
  protected Enum(String name, int ordinal) {}
  public final String name() { return null; }
  public final int ordinal() { return 0; }
  public String toString() { return null; }
  public final int compareTo(E o) { return 0; }
  public static <T> T valueOf(Class<T> enumType, String name) { return null; }
}

abstract class Record {
  protected Record() {}
  public abstract boolean equals(Object o);
  public abstract int hashCode();
  public abstract String toString();
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
  public String() {}
  public String(char[] value) {}
  public String(char[] value, int offset, int count) {}
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
  public String replaceAll(String regex, String replacement) { return null; }
  public String replaceFirst(String regex, String replacement) { return null; }
  public char[] toCharArray() { return null; }
  public byte[] getBytes() { return null; }
  public boolean isBlank() { return false; }
  public static String valueOf(Object o) { return null; }
  public static String valueOf(int i) { return null; }
  public static String format(String fmt, Object... args) { return null; }
  public static String format(java.util.Locale l, String fmt, Object... args) { return null; }
  public String formatted(Object... args) { return null; }
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
  public StringBuilder append(long l) { return null; }
  public StringBuilder append(float f) { return null; }
  public StringBuilder append(double d) { return null; }
  public StringBuilder append(char c) { return null; }
  public StringBuilder append(char[] c) { return null; }
  public StringBuilder append(boolean b) { return null; }
  public StringBuilder append(CharSequence s) { return null; }
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
  public static final int MAX_VALUE = 2147483647;
  public static final int MIN_VALUE = -2147483648;
  public static int sum(int a, int b) { return 0; }
  public static int max(int a, int b) { return 0; }
  public static int min(int a, int b) { return 0; }
  public int compareTo(Integer o) { return 0; }
  public static int parseInt(String s) { return 0; }
  public static int parseInt(String s, int radix) { return 0; }
  public static Integer valueOf(int i) { return null; }
  public static Integer valueOf(String s) { return null; }
  public static String toString(int i) { return null; }
  public static String toHexString(int i) { return null; }
  public static String toBinaryString(int i) { return null; }
  public static String toOctalString(int i) { return null; }
  public static int max(int a, int b) { return 0; }
  public static int min(int a, int b) { return 0; }
  public static int compare(int a, int b) { return 0; }
  public static int signum(int i) { return 0; }
  public static int bitCount(int i) { return 0; }
  public static int numberOfLeadingZeros(int i) { return 0; }
  public static int numberOfTrailingZeros(int i) { return 0; }
  public static int highestOneBit(int i) { return 0; }
  public static int reverse(int i) { return 0; }
}

class Long extends Number implements Comparable<Long> {
  public static long sum(long a, long b) { return 0L; }
  public static final long MAX_VALUE = 9223372036854775807L;
  public static final long MIN_VALUE = -9223372036854775808L;
  public int compareTo(Long o) { return 0; }
  public static long parseLong(String s) { return 0; }
  public static long parseLong(String s, int radix) { return 0; }
  public static Long valueOf(long l) { return null; }
  public static Long valueOf(String s) { return null; }
  public static String toHexString(long l) { return null; }
  public static String toBinaryString(long l) { return null; }
  public static long max(long a, long b) { return 0; }
  public static long min(long a, long b) { return 0; }
  public static int compare(long a, long b) { return 0; }
  public static int signum(long l) { return 0; }
  public static int bitCount(long l) { return 0; }
}

class Double extends Number implements Comparable<Double> {
  public static double sum(double a, double b) { return 0.0; }
  public int compareTo(Double o) { return 0; }
  public static double parseDouble(String s) { return 0; }
  public static Double valueOf(double d) { return null; }
}

class Float extends Number implements Comparable<Float> {
  public int compareTo(Float o) { return 0; }
}

class Short extends Number implements Comparable<Short> {
  public int compareTo(Short o) { return 0; }
  public static short parseShort(String s) { return 0; }
  public static short parseShort(String s, int radix) { return 0; }
  public static Short valueOf(String s) { return null; }
}

class Byte extends Number implements Comparable<Byte> {
  public int compareTo(Byte o) { return 0; }
  public static byte parseByte(String s) { return 0; }
  public static byte parseByte(String s, int radix) { return 0; }
  public static Byte valueOf(String s) { return null; }
}

class Character implements Comparable<Character> {
  public char charValue() { return ' '; }
  public int compareTo(Character o) { return 0; }
  public static boolean isDigit(char c) { return false; }
  public static boolean isLetter(char c) { return false; }
  public static boolean isLetterOrDigit(char c) { return false; }
  public static boolean isWhitespace(char c) { return false; }
  public static boolean isUpperCase(char c) { return false; }
  public static boolean isLowerCase(char c) { return false; }
  public static char toUpperCase(char c) { return ' '; }
  public static char toLowerCase(char c) { return ' '; }
  public static int getNumericValue(char c) { return 0; }
  public static int digit(char c, int radix) { return 0; }
  public static String toString(char c) { return null; }
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
  public static long max(long a, long b) { return 0; }
  public static long min(long a, long b) { return 0; }
  public static float max(float a, float b) { return 0; }
  public static float min(float a, float b) { return 0; }
  public static float abs(float a) { return 0; }
  public static double pow(double a, double b) { return 0; }
  public static double floor(double a) { return 0; }
  public static double ceil(double a) { return 0; }
  public static long round(double a) { return 0; }
  public static int round(float a) { return 0; }
  public static double signum(double d) { return 0; }
  public static double log(double a) { return 0; }
  public static double log10(double a) { return 0; }
  public static double sin(double a) { return 0; }
  public static double cos(double a) { return 0; }
  public static double cbrt(double a) { return 0; }
  public static double hypot(double a, double b) { return 0; }
  public static int floorDiv(int a, int b) { return 0; }
  public static int floorMod(int a, int b) { return 0; }
  public static int addExact(int a, int b) { return 0; }
  public static int toIntExact(long a) { return 0; }
  public static double random() { return 0; }
}

class System {
  public static final java.io.PrintStream out = null;
  public static final java.io.PrintStream err = null;
  public static final java.io.InputStream in = null;
  public static void arraycopy(Object src, int srcPos, Object dest, int destPos, int length) {}
  public static long currentTimeMillis() { return 0; }
  public static long nanoTime() { return 0; }
  public static void exit(int code) {}
  public static String getProperty(String key) { return null; }
  public static String lineSeparator() { return null; }
  public static java.io.Console console() { return null; }
}

class Thread implements Runnable {
  public void run() {}
  public void start() {}
  public String getName() { return null; }
  public static Thread currentThread() { return null; }
  public static void sleep(long millis) {}
  public static boolean holdsLock(Object obj) { return false; }
}

class Throwable {
  public Throwable() {}
  public Throwable(String message) {}
  public Throwable(String message, Throwable cause) {}
  public Throwable(Throwable cause) {}
  public String getMessage() { return null; }
  public String getLocalizedMessage() { return null; }
  public Throwable getCause() { return null; }
  public void printStackTrace() {}
  public StackTraceElement[] getStackTrace() { return null; }
}
class StackTraceElement {
  public String getClassName() { return null; }
  public String getMethodName() { return null; }
  public String getFileName() { return null; }
  public int getLineNumber() { return 0; }
  public String toString() { return null; }
}
class Error extends Throwable { public Error() {} public Error(String m) {} }
class AssertionError extends Error { public AssertionError() {} public AssertionError(Object m) {} }
class Exception extends Throwable {
  public Exception() {}
  public Exception(String m) {}
  public Exception(String m, Throwable c) {}
  public Exception(Throwable c) {}
}
class RuntimeException extends Exception {
  public RuntimeException() {}
  public RuntimeException(String m) {}
  public RuntimeException(String m, Throwable c) {}
  public RuntimeException(Throwable c) {}
}
class IllegalArgumentException extends RuntimeException { public IllegalArgumentException() {} public IllegalArgumentException(String m) {} }
class IllegalStateException extends RuntimeException { public IllegalStateException() {} public IllegalStateException(String m) {} }
class NullPointerException extends RuntimeException { public NullPointerException() {} public NullPointerException(String m) {} }
class IndexOutOfBoundsException extends RuntimeException { public IndexOutOfBoundsException() {} public IndexOutOfBoundsException(String m) {} }
class ArrayIndexOutOfBoundsException extends IndexOutOfBoundsException { public ArrayIndexOutOfBoundsException() {} public ArrayIndexOutOfBoundsException(String m) {} }
class ClassCastException extends RuntimeException { public ClassCastException() {} public ClassCastException(String m) {} }
class UnsupportedOperationException extends RuntimeException { public UnsupportedOperationException() {} public UnsupportedOperationException(String m) {} }
class NumberFormatException extends IllegalArgumentException { public NumberFormatException() {} public NumberFormatException(String m) {} }
class ArithmeticException extends RuntimeException { public ArithmeticException() {} public ArithmeticException(String m) {} }
class ArrayStoreException extends RuntimeException { public ArrayStoreException() {} public ArrayStoreException(String m) {} }
class NegativeArraySizeException extends RuntimeException { public NegativeArraySizeException() {} public NegativeArraySizeException(String m) {} }
class StringIndexOutOfBoundsException extends IndexOutOfBoundsException { public StringIndexOutOfBoundsException() {} public StringIndexOutOfBoundsException(String m) {} }
class CloneNotSupportedException extends Exception { public CloneNotSupportedException() {} public CloneNotSupportedException(String m) {} }
class StackOverflowError extends Error { public StackOverflowError() {} public StackOverflowError(String m) {} }
class OutOfMemoryError extends Error { public OutOfMemoryError() {} public OutOfMemoryError(String m) {} }
class InterruptedException extends Exception { public InterruptedException() {} public InterruptedException(String m) {} }

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
  public PrintStream format(String format, Object... args) { return null; }
  public PrintStream append(CharSequence s) { return null; }
}

class IOException extends java.lang.Exception { public IOException() {} public IOException(String m) {} }
class UncheckedIOException extends java.lang.RuntimeException {}
class FileNotFoundException extends IOException { public FileNotFoundException() {} public FileNotFoundException(String m) {} }
class EOFException extends IOException { public EOFException() {} public EOFException(String m) {} }
class UnsupportedEncodingException extends IOException { public UnsupportedEncodingException(String m) {} }

interface Serializable {}

class File implements Serializable, java.lang.Comparable<File> {
  public File(String pathname) {}
  public File(File parent, String child) {}
  public File(String parent, String child) {}
  public String getName() { return null; }
  public String getPath() { return null; }
  public String getAbsolutePath() { return null; }
  public File getParentFile() { return null; }
  public File getAbsoluteFile() { return null; }
  public String getParent() { return null; }
  public boolean exists() { return false; }
  public boolean isFile() { return false; }
  public boolean isDirectory() { return false; }
  public boolean delete() { return false; }
  public boolean mkdir() { return false; }
  public boolean mkdirs() { return false; }
  public long length() { return 0; }
  public long lastModified() { return 0; }
  public File[] listFiles() { return null; }
  public String[] list() { return null; }
  public boolean renameTo(File dest) { return false; }
  public boolean canRead() { return false; }
  public boolean canWrite() { return false; }
  public java.nio.file.Path toPath() { return null; }
  public int compareTo(File other) { return 0; }
}

class Reader implements Closeable {
  public int read() { return 0; }
  public int read(char[] cbuf) { return 0; }
  public int read(char[] cbuf, int off, int len) { return 0; }
  public void close() {}
}
class BufferedReader extends Reader {
  public BufferedReader(Reader in) {}
  public String readLine() { return null; }
}
class StringReader extends Reader { public StringReader(String s) {} }
class InputStreamReader extends Reader {
  public InputStreamReader(InputStream in) {}
  public InputStreamReader(InputStream in, String charsetName) {}
  public InputStreamReader(InputStream in, java.nio.charset.Charset cs) {}
}

class Writer implements Closeable, Flushable {
  public void write(int c) {}
  public void write(char[] cbuf) {}
  public void write(String str) {}
  public void write(String str, int off, int len) {}
  public Writer append(CharSequence csq) { return null; }
  public void flush() {}
  public void close() {}
}
class StringWriter extends Writer { public StringWriter() {} public String toString() { return null; } }
class OutputStreamWriter extends Writer {
  public OutputStreamWriter(OutputStream out) {}
  public OutputStreamWriter(OutputStream out, java.nio.charset.Charset cs) {}
}
class BufferedWriter extends Writer { public BufferedWriter(Writer out) {} public void newLine() {} }
class PrintWriter extends Writer {
  public PrintWriter(Writer out) {}
  public PrintWriter(OutputStream out) {}
  public void print(String s) {}
  public void print(Object o) {}
  public void print(int i) {}
  public void println() {}
  public void println(String s) {}
  public void println(Object o) {}
  public void println(int i) {}
  public PrintWriter printf(String format, Object... args) { return null; }
  public PrintWriter format(String format, Object... args) { return null; }
}

class Console {
  public Console printf(String format, Object... args) { return null; }
  public Console format(String format, Object... args) { return null; }
  public String readLine() { return null; }
}

class ByteArrayInputStream extends InputStream {
  public ByteArrayInputStream(byte[] buf) {}
  public ByteArrayInputStream(byte[] buf, int offset, int length) {}
}
class ByteArrayOutputStream extends OutputStream {
  public ByteArrayOutputStream() {}
  public ByteArrayOutputStream(int size) {}
  public byte[] toByteArray() { return null; }
  public int size() { return 0; }
  public String toString() { return null; }
}
class FileInputStream extends InputStream { public FileInputStream(File file) {} public FileInputStream(String name) {} }
class FileOutputStream extends OutputStream { public FileOutputStream(File file) {} public FileOutputStream(String name) {} }
class FilterInputStream extends InputStream { protected FilterInputStream(InputStream in) {} }
class FilterOutputStream extends OutputStream { public FilterOutputStream(OutputStream out) {} }
class BufferedInputStream extends FilterInputStream { public BufferedInputStream(InputStream in) {} }
class BufferedOutputStream extends FilterOutputStream { public BufferedOutputStream(OutputStream out) {} }
class FileReader extends Reader { public FileReader(File file) {} public FileReader(String fileName) {} }
class FileWriter extends Writer { public FileWriter(File file) {} public FileWriter(String fileName) {} }
`;

const JAVA_UTIL = `package java.util;

interface Iterator<E> {
  boolean hasNext();
  E next();
  void remove();
}

interface Comparator<T> {
  int compare(T a, T b);
  default Comparator<T> reversed() { return null; }
  default Comparator<T> thenComparing(Comparator<? super T> other) { return null; }
  static <T, U extends java.lang.Comparable<U>> Comparator<T> comparing(java.util.function.Function<? super T, ? extends U> keyExtractor) { return null; }
  static <T> Comparator<T> naturalOrder() { return null; }
}

interface Collection<E> extends Iterable<E> {
  java.util.stream.Stream<E> stream();
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
  void sort(Comparator<? super E> c);
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
  E remove();
  E element();
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
  void forEach(java.util.function.BiConsumer<? super K, ? super V> action);
  V computeIfAbsent(K key, java.util.function.Function<? super K, ? extends V> mappingFunction);
  V computeIfPresent(K key, java.util.function.BiFunction<? super K, ? super V, ? extends V> remappingFunction);
  V merge(K key, V value, java.util.function.BiFunction<? super V, ? super V, ? extends V> remappingFunction);
  interface Entry<K, V> {
    K getKey();
    V getValue();
    V setValue(V value);
  }
}

class ArrayList<E> implements List<E> {
  public ArrayList() {}
  public ArrayList(int initialCapacity) {}
  public ArrayList(Collection<? extends E> c) {}
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
  public LinkedList() {}
  public LinkedList(Collection<? extends E> c) {}
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
  public HashSet() {}
  public HashSet(int initialCapacity) {}
  public HashSet(Collection<? extends E> c) {}
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

class LinkedHashSet<E> implements Set<E> {
  public LinkedHashSet() {}
  public LinkedHashSet(int initialCapacity) {}
  public LinkedHashSet(Collection<? extends E> c) {}
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

interface SortedSet<E> extends Set<E> {
  E first();
  E last();
  SortedSet<E> headSet(E toElement);
  SortedSet<E> tailSet(E fromElement);
  SortedSet<E> subSet(E fromElement, E toElement);
  java.util.Comparator<? super E> comparator();
}

class TreeSet<E> implements SortedSet<E> {
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
  public HashMap() {}
  public HashMap(int initialCapacity) {}
  public HashMap(Map<? extends K, ? extends V> m) {}
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

class LinkedHashMap<K, V> implements Map<K, V> {
  public LinkedHashMap() {}
  public LinkedHashMap(int initialCapacity) {}
  public LinkedHashMap(Map<? extends K, ? extends V> m) {}
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

interface SortedMap<K, V> extends Map<K, V> {
  K firstKey();
  K lastKey();
  SortedMap<K, V> headMap(K toKey);
  SortedMap<K, V> tailMap(K fromKey);
  SortedMap<K, V> subMap(K fromKey, K toKey);
  java.util.Comparator<? super K> comparator();
}

class TreeMap<K, V> implements SortedMap<K, V> {
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

class OptionalInt {
  public static OptionalInt of(int value) { return null; }
  public static OptionalInt empty() { return null; }
  public int getAsInt() { return 0; }
  public boolean isPresent() { return false; }
  public boolean isEmpty() { return false; }
  public int orElse(int other) { return 0; }
}

class Optional<T> {
  public static <T> Optional<T> of(T value) { return null; }
  public static <T> Optional<T> ofNullable(T value) { return null; }
  public static <T> Optional<T> empty() { return null; }
  public T get() { return null; }
  public boolean isPresent() { return false; }
  public boolean isEmpty() { return false; }
  public T orElse(T other) { return null; }
  public T orElseGet(java.util.function.Supplier<? extends T> supplier) { return null; }
  public <U> Optional<U> map(java.util.function.Function<? super T, ? extends U> mapper) { return null; }
  public Optional<T> filter(java.util.function.Predicate<? super T> predicate) { return null; }
  public void ifPresent(java.util.function.Consumer<? super T> action) {}
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
  public static <T> java.util.stream.Stream<T> stream(T[] array) { return null; }
  public static java.util.stream.IntStream stream(int[] array) { return null; }
  public static String toString(Object[] a) { return null; }
  public static String toString(int[] a) { return null; }
  public static String toString(long[] a) { return null; }
  public static String toString(double[] a) { return null; }
  public static String toString(char[] a) { return null; }
  public static String toString(boolean[] a) { return null; }
  public static void sort(Object[] a) {}
  public static void sort(int[] a) {}
  public static void sort(long[] a) {}
  public static void sort(double[] a) {}
  public static void sort(char[] a) {}
  public static void sort(int[] a, int from, int to) {}
  public static <T> T[] copyOf(T[] original, int newLength) { return null; }
  public static int[] copyOf(int[] original, int newLength) { return null; }
  public static int[] copyOfRange(int[] original, int from, int to) { return null; }
  public static void fill(Object[] a, Object val) {}
  public static void fill(int[] a, int val) {}
  public static void fill(char[] a, char val) {}
  public static void fill(boolean[] a, boolean val) {}
  public static boolean equals(int[] a, int[] b) { return false; }
  public static boolean equals(Object[] a, Object[] b) { return false; }
  public static int binarySearch(int[] a, int key) { return 0; }
  public static int hashCode(int[] a) { return 0; }
}

class Collections {
  public static <T> List<T> emptyList() { return null; }
  public static <T> Set<T> emptySet() { return null; }
  public static <K, V> Map<K, V> emptyMap() { return null; }
  public static <T> void sort(List<T> list) {}
  public static <T> List<T> unmodifiableList(List<? extends T> list) { return null; }
  public static <T> Set<T> unmodifiableSet(Set<? extends T> set) { return null; }
  public static <K, V> Map<K, V> unmodifiableMap(Map<? extends K, ? extends V> map) { return null; }
}

class NoSuchElementException extends java.lang.RuntimeException {}
class ConcurrentModificationException extends java.lang.RuntimeException {}
class EmptyStackException extends java.lang.RuntimeException {}

interface ListIterator<E> extends Iterator<E> {
  boolean hasPrevious();
  E previous();
  int nextIndex();
  int previousIndex();
  void remove();
  void set(E e);
  void add(E e);
}

class Locale {
  public static final Locale ROOT = null;
  public static final Locale ENGLISH = null;
  public static final Locale US = null;
  public static final Locale GERMAN = null;
  public static final Locale GERMANY = null;
  public Locale(String language) {}
  public Locale(String language, String country) {}
  public static Locale getDefault() { return null; }
  public String getLanguage() { return null; }
  public String getCountry() { return null; }
  public String toLanguageTag() { return null; }
}

class Random {
  public Random() {}
  public Random(long seed) {}
  public int nextInt() { return 0; }
  public int nextInt(int bound) { return 0; }
  public long nextLong() { return 0; }
  public double nextDouble() { return 0; }
  public float nextFloat() { return 0; }
  public boolean nextBoolean() { return false; }
  public void nextBytes(byte[] bytes) {}
  public void setSeed(long seed) {}
}

class Date implements java.lang.Comparable<Date> {
  public Date() {}
  public Date(long date) {}
  public long getTime() { return 0; }
  public void setTime(long time) {}
  public boolean before(Date when) { return false; }
  public boolean after(Date when) { return false; }
  public int compareTo(Date anotherDate) { return 0; }
}

class UUID implements java.lang.Comparable<UUID> {
  public static UUID randomUUID() { return null; }
  public static UUID fromString(String name) { return null; }
  public long getMostSignificantBits() { return 0; }
  public long getLeastSignificantBits() { return 0; }
  public int compareTo(UUID val) { return 0; }
}

class StringJoiner {
  public StringJoiner(CharSequence delimiter) {}
  public StringJoiner(CharSequence delimiter, CharSequence prefix, CharSequence suffix) {}
  public StringJoiner add(CharSequence newElement) { return null; }
  public int length() { return 0; }
}

class BitSet {
  public BitSet() {}
  public BitSet(int nbits) {}
  public void set(int bitIndex) {}
  public void set(int bitIndex, boolean value) {}
  public boolean get(int bitIndex) { return false; }
  public void clear(int bitIndex) {}
  public void clear() {}
  public int cardinality() { return 0; }
  public int length() { return 0; }
  public int size() { return 0; }
  public int nextSetBit(int fromIndex) { return 0; }
}
`;

const JAVA_UTIL_FUNCTION = `package java.util.function;

interface Function<T, R> {
  R apply(T t);
  static <T> Function<T, T> identity() { return null; }
}
interface BiFunction<T, U, R> { R apply(T t, U u); }
interface Supplier<T> { T get(); }
interface Consumer<T> { void accept(T t); }
interface BiConsumer<T, U> { void accept(T t, U u); }
interface Predicate<T> { boolean test(T t); }
interface BiPredicate<T, U> { boolean test(T t, U u); }
interface UnaryOperator<T> { T apply(T t); }
interface BinaryOperator<T> { T apply(T a, T b); }
interface IntFunction<R> { R apply(int value); }
interface ToIntFunction<T> { int applyAsInt(T value); }
interface IntUnaryOperator { int applyAsInt(int operand); }
`;

const JAVA_UTIL_STREAM = `package java.util.stream;

interface Collector<T, A, R> {}

interface Stream<T> extends java.lang.AutoCloseable {
  <A> A[] toArray(java.util.function.IntFunction<A[]> generator);
  Stream<T> filter(java.util.function.Predicate<T> predicate);
  <R> Stream<R> map(java.util.function.Function<T, R> mapper);
  IntStream mapToInt(java.util.function.ToIntFunction<? super T> mapper);
  void forEach(java.util.function.Consumer<T> action);
  <R> R collect(Collector<T, ?, R> collector);
  java.util.List<T> toList();
  long count();
  boolean anyMatch(java.util.function.Predicate<T> predicate);
  boolean allMatch(java.util.function.Predicate<T> predicate);
  boolean noneMatch(java.util.function.Predicate<T> predicate);
  Stream<T> sorted();
  Stream<T> sorted(java.util.Comparator<? super T> comparator);
  <R> Stream<R> flatMap(java.util.function.Function<? super T, ? extends Stream<? extends R>> mapper);
  Stream<T> distinct();
  Stream<T> limit(long maxSize);
  Stream<T> skip(long n);
  java.util.Optional<T> findFirst();
  java.util.Optional<T> findAny();
  java.util.Optional<T> reduce(java.util.function.BinaryOperator<T> accumulator);
  Object[] toArray();
  void close();
  static <T> Stream<T> of(T... values) { return null; }
  static <T> Stream<T> empty() { return null; }
}

interface IntStream extends java.lang.AutoCloseable {
  int sum();
  long count();
  java.util.OptionalInt max();
  java.util.OptionalInt min();
  IntStream filter(java.util.function.IntPredicate predicate);
  IntStream map(java.util.function.IntUnaryOperator mapper);
  <U> Stream<U> mapToObj(java.util.function.IntFunction<U> mapper);
  void forEach(java.util.function.IntConsumer action);
  int[] toArray();
  void close();
  static IntStream range(int startInclusive, int endExclusive) { return null; }
  static IntStream rangeClosed(int startInclusive, int endInclusive) { return null; }
  static IntStream of(int... values) { return null; }
}

class Collectors {
  public static <T> Collector<T, ?, java.util.List<T>> toList() { return null; }
  public static <T> Collector<T, ?, java.util.Set<T>> toSet() { return null; }
  public static Collector<CharSequence, ?, String> joining() { return null; }
  public static Collector<CharSequence, ?, String> joining(CharSequence delimiter) { return null; }
  public static <T> Collector<T, ?, Long> counting() { return null; }
  public static <T, K> Collector<T, ?, java.util.Map<K, java.util.List<T>>> groupingBy(java.util.function.Function<? super T, ? extends K> classifier) { return null; }
  public static <T, K, A, D> Collector<T, ?, java.util.Map<K, D>> groupingBy(java.util.function.Function<? super T, ? extends K> classifier, Collector<? super T, A, D> downstream) { return null; }
  public static <T, U, A, R> Collector<T, ?, R> mapping(java.util.function.Function<? super T, ? extends U> mapper, Collector<? super U, A, R> downstream) { return null; }
  public static <T, K, U> Collector<T, ?, java.util.Map<K, U>> toMap(java.util.function.Function<? super T, ? extends K> keyMapper, java.util.function.Function<? super T, ? extends U> valueMapper) { return null; }
}
`;

const JAVA_LANG_REFLECT = `package java.lang.reflect;

class Constructor<T> {
  public T newInstance(Object... initargs) { return null; }
  public String getName() { return null; }
  public Class<?>[] getParameterTypes() { return null; }
}

class Method {
  public String getName() { return null; }
  public Object invoke(Object obj, Object... args) { return null; }
}
`;

const JAVA_UTIL_REGEX = `package java.util.regex;

class Pattern {
  public static Pattern compile(String regex) { return null; }
  public static Pattern compile(String regex, int flags) { return null; }
  public static boolean matches(String regex, CharSequence input) { return false; }
  public static String quote(String s) { return null; }
  public Matcher matcher(CharSequence input) { return null; }
  public String pattern() { return null; }
  public String[] split(CharSequence input) { return null; }
}

class Matcher {
  public boolean matches() { return false; }
  public boolean find() { return false; }
  public boolean lookingAt() { return false; }
  public String group() { return null; }
  public String group(int group) { return null; }
  public int groupCount() { return 0; }
  public int start() { return 0; }
  public int end() { return 0; }
  public String replaceAll(String replacement) { return null; }
  public String replaceFirst(String replacement) { return null; }
}

class PatternSyntaxException extends java.lang.IllegalArgumentException {
  public PatternSyntaxException(String desc, String regex, int index) {}
}
`;

const JAVA_UTIL_CONCURRENT = `package java.util.concurrent;

enum TimeUnit {
  NANOSECONDS, MICROSECONDS, MILLISECONDS, SECONDS, MINUTES, HOURS, DAYS;
  public long toMillis(long duration) { return 0; }
  public long toSeconds(long duration) { return 0; }
  public long toNanos(long duration) { return 0; }
  public void sleep(long timeout) {}
}

class TimeoutException extends java.lang.Exception { public TimeoutException() {} public TimeoutException(String m) {} }
class ExecutionException extends java.lang.Exception { public ExecutionException(java.lang.Throwable cause) {} }

interface Callable<V> { V call(); }
interface Future<V> {
  boolean cancel(boolean mayInterruptIfRunning);
  boolean isDone();
  V get();
}
`;

const JAVA_UTIL_CONCURRENT_ATOMIC = `package java.util.concurrent.atomic;

class AtomicInteger extends java.lang.Number {
  public AtomicInteger() {}
  public AtomicInteger(int initialValue) {}
  public int get() { return 0; }
  public void set(int newValue) {}
  public int incrementAndGet() { return 0; }
  public int decrementAndGet() { return 0; }
  public int getAndIncrement() { return 0; }
  public int getAndDecrement() { return 0; }
  public int addAndGet(int delta) { return 0; }
  public int getAndAdd(int delta) { return 0; }
  public boolean compareAndSet(int expectedValue, int newValue) { return false; }
  public int intValue() { return 0; }
  public long longValue() { return 0; }
  public float floatValue() { return 0; }
  public double doubleValue() { return 0; }
}

class AtomicLong extends java.lang.Number {
  public AtomicLong() {}
  public AtomicLong(long initialValue) {}
  public long get() { return 0; }
  public void set(long newValue) {}
  public long incrementAndGet() { return 0; }
  public long getAndIncrement() { return 0; }
  public long addAndGet(long delta) { return 0; }
  public boolean compareAndSet(long expectedValue, long newValue) { return false; }
  public int intValue() { return 0; }
  public long longValue() { return 0; }
  public float floatValue() { return 0; }
  public double doubleValue() { return 0; }
}

class AtomicBoolean {
  public AtomicBoolean() {}
  public AtomicBoolean(boolean initialValue) {}
  public boolean get() { return false; }
  public void set(boolean newValue) {}
  public boolean compareAndSet(boolean expectedValue, boolean newValue) { return false; }
  public boolean getAndSet(boolean newValue) { return false; }
}

class AtomicReference<V> {
  public AtomicReference() {}
  public AtomicReference(V initialValue) {}
  public V get() { return null; }
  public void set(V newValue) {}
  public boolean compareAndSet(V expectedValue, V newValue) { return false; }
  public V getAndSet(V newValue) { return null; }
}
`;

const JAVA_NIO = `package java.nio;

class ByteOrder {
  public static final ByteOrder BIG_ENDIAN = null;
  public static final ByteOrder LITTLE_ENDIAN = null;
  public static ByteOrder nativeOrder() { return null; }
}

class ByteBuffer {
  public static ByteBuffer allocate(int capacity) { return null; }
  public static ByteBuffer wrap(byte[] array) { return null; }
  public byte get() { return 0; }
  public byte get(int index) { return 0; }
  public ByteBuffer put(byte b) { return null; }
  public int getInt() { return 0; }
  public ByteBuffer putInt(int value) { return null; }
  public long getLong() { return 0; }
  public ByteBuffer putLong(long value) { return null; }
  public ByteBuffer order(ByteOrder bo) { return null; }
  public int position() { return 0; }
  public int limit() { return 0; }
  public int remaining() { return 0; }
  public boolean hasRemaining() { return false; }
  public byte[] array() { return null; }
  public ByteBuffer flip() { return null; }
  public ByteBuffer rewind() { return null; }
}

class BufferOverflowException extends java.lang.RuntimeException {}
class BufferUnderflowException extends java.lang.RuntimeException {}
`;

const JAVA_NIO_CHARSET = `package java.nio.charset;

class Charset implements java.lang.Comparable<Charset> {
  public static Charset forName(String charsetName) { return null; }
  public static Charset defaultCharset() { return null; }
  public String name() { return null; }
  public String displayName() { return null; }
  public int compareTo(Charset that) { return 0; }
}

class StandardCharsets {
  public static final Charset US_ASCII = null;
  public static final Charset ISO_8859_1 = null;
  public static final Charset UTF_8 = null;
  public static final Charset UTF_16 = null;
  public static final Charset UTF_16BE = null;
  public static final Charset UTF_16LE = null;
}
`;

const JAVA_NIO_FILE = `package java.nio.file;

interface Path extends java.lang.Comparable<Path> {
  Path getFileName();
  Path getParent();
  Path resolve(String other);
  Path resolve(Path other);
  Path toAbsolutePath();
  java.io.File toFile();
  int compareTo(Path other);
}

class Paths {
  public static Path get(String first, String... more) { return null; }
}

interface OpenOption {}

enum StandardOpenOption implements OpenOption {
  READ, WRITE, APPEND, TRUNCATE_EXISTING, CREATE, CREATE_NEW, DELETE_ON_CLOSE;
}

class Files {
  public static java.io.BufferedReader newBufferedReader(Path path) { return null; }
  public static java.io.BufferedWriter newBufferedWriter(Path path, OpenOption... options) { return null; }
  public static boolean exists(Path path) { return false; }
  public static boolean isDirectory(Path path) { return false; }
  public static boolean isRegularFile(Path path) { return false; }
  public static byte[] readAllBytes(Path path) { return null; }
  public static String readString(Path path) { return null; }
  public static java.util.List<String> readAllLines(Path path) { return null; }
  public static Path createDirectories(Path dir) { return null; }
  public static void delete(Path path) {}
  public static boolean deleteIfExists(Path path) { return false; }
  public static long size(Path path) { return 0; }
  public static java.io.InputStream newInputStream(Path path) { return null; }
  public static java.io.OutputStream newOutputStream(Path path) { return null; }
}

class NoSuchFileException extends java.io.IOException { public NoSuchFileException(String file) {} }
`;

const JAVA_MATH = `package java.math;

class BigInteger extends java.lang.Number implements java.lang.Comparable<BigInteger> {
  public static final BigInteger ZERO = null;
  public static final BigInteger ONE = null;
  public static final BigInteger TWO = null;
  public static final BigInteger TEN = null;
  public BigInteger(String val) {}
  public BigInteger(String val, int radix) {}
  public BigInteger(byte[] val) {}
  public static BigInteger valueOf(long val) { return null; }
  public BigInteger add(BigInteger val) { return null; }
  public BigInteger subtract(BigInteger val) { return null; }
  public BigInteger multiply(BigInteger val) { return null; }
  public BigInteger divide(BigInteger val) { return null; }
  public BigInteger mod(BigInteger m) { return null; }
  public BigInteger remainder(BigInteger val) { return null; }
  public BigInteger pow(int exponent) { return null; }
  public BigInteger negate() { return null; }
  public BigInteger abs() { return null; }
  public BigInteger gcd(BigInteger val) { return null; }
  public BigInteger shiftLeft(int n) { return null; }
  public BigInteger shiftRight(int n) { return null; }
  public BigInteger and(BigInteger val) { return null; }
  public BigInteger or(BigInteger val) { return null; }
  public BigInteger xor(BigInteger val) { return null; }
  public int signum() { return 0; }
  public int bitLength() { return 0; }
  public boolean testBit(int n) { return false; }
  public int compareTo(BigInteger val) { return 0; }
  public int intValue() { return 0; }
  public long longValue() { return 0; }
  public float floatValue() { return 0; }
  public double doubleValue() { return 0; }
  public String toString(int radix) { return null; }
}

class BigDecimal extends java.lang.Number implements java.lang.Comparable<BigDecimal> {
  public static final BigDecimal ZERO = null;
  public static final BigDecimal ONE = null;
  public static final BigDecimal TEN = null;
  public BigDecimal(String val) {}
  public BigDecimal(int val) {}
  public BigDecimal(long val) {}
  public BigDecimal(double val) {}
  public static BigDecimal valueOf(long val) { return null; }
  public static BigDecimal valueOf(double val) { return null; }
  public BigDecimal add(BigDecimal augend) { return null; }
  public BigDecimal subtract(BigDecimal subtrahend) { return null; }
  public BigDecimal multiply(BigDecimal multiplicand) { return null; }
  public BigDecimal divide(BigDecimal divisor) { return null; }
  public BigDecimal negate() { return null; }
  public BigDecimal abs() { return null; }
  public int scale() { return 0; }
  public int signum() { return 0; }
  public BigDecimal setScale(int newScale) { return null; }
  public BigDecimal stripTrailingZeros() { return null; }
  public int compareTo(BigDecimal val) { return 0; }
  public int intValue() { return 0; }
  public long longValue() { return 0; }
  public float floatValue() { return 0; }
  public double doubleValue() { return 0; }
  public String toPlainString() { return null; }
}
`;

const JAVA_LANG_ANNOTATION = `package java.lang.annotation;

interface Annotation {}

enum RetentionPolicy { SOURCE, CLASS, RUNTIME }
enum ElementType { TYPE, FIELD, METHOD, PARAMETER, CONSTRUCTOR, LOCAL_VARIABLE, ANNOTATION_TYPE, PACKAGE, TYPE_PARAMETER, TYPE_USE, MODULE, RECORD_COMPONENT }

@interface Retention { RetentionPolicy value(); }
@interface Target { ElementType[] value(); }
@interface Documented {}
@interface Inherited {}
@interface Repeatable { Class value(); }
`;

const JAVA_TIME_FORMAT = `package java.time.format;
class DateTimeFormatter {
  public static DateTimeFormatter ofPattern(String pattern) { return null; }
  public static DateTimeFormatter ofPattern(String pattern, java.util.Locale locale) { return null; }
}
`;

export const JDK_STUB_FILES: ReadonlyArray<{ uri: Uri; text: string }> = [
  { uri: "jdk:///java/lang.java" as Uri, text: JAVA_LANG },
  { uri: "jdk:///java/io.java" as Uri, text: JAVA_IO },
  { uri: "jdk:///java/util.java" as Uri, text: JAVA_UTIL },
  { uri: "jdk:///java/util/function.java" as Uri, text: JAVA_UTIL_FUNCTION },
  { uri: "jdk:///java/util/stream.java" as Uri, text: JAVA_UTIL_STREAM },
  { uri: "jdk:///java/lang/reflect.java" as Uri, text: JAVA_LANG_REFLECT },
  { uri: "jdk:///java/util/regex.java" as Uri, text: JAVA_UTIL_REGEX },
  { uri: "jdk:///java/util/concurrent.java" as Uri, text: JAVA_UTIL_CONCURRENT },
  { uri: "jdk:///java/util/concurrent/atomic.java" as Uri, text: JAVA_UTIL_CONCURRENT_ATOMIC },
  { uri: "jdk:///java/nio.java" as Uri, text: JAVA_NIO },
  { uri: "jdk:///java/nio/charset.java" as Uri, text: JAVA_NIO_CHARSET },
  { uri: "jdk:///java/nio/file.java" as Uri, text: JAVA_NIO_FILE },
  { uri: "jdk:///java/math.java" as Uri, text: JAVA_MATH },
  { uri: "jdk:///java/lang/annotation.java" as Uri, text: JAVA_LANG_ANNOTATION },
  { uri: "jdk:///java/time/format.java" as Uri, text: JAVA_TIME_FORMAT },
];

/** Register the synthetic JDK stub into a program. */
export function loadJdkStub(program: Program): void {
  for (const file of JDK_STUB_FILES) {
    program.addProjectFile(file.uri, file.text);
  }
}
