package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The emitter's run-equivalence tier: emit a class with our own backend, run it
// under `java`, and require the stdout javac's build of the same source
// produced. Port of the 77 runsLikeJavac(...) cases in src/compiler/
// emitter.test.ts - the one emitter tier the Go build had no equivalent of. The
// expected text is javac-verified (the TS test re-derived it from a live javac
// under UPDATE_BASELINES), so a mismatch here is our codegen disagreeing with
// javac on observable behaviour, not a stale fixture.
//
// Only `java` runs at test time; the whole test skips without it.

type runsLikeJavacCase struct {
	name string
	// mainClass is the class `java` is pointed at; it defaults to name, and is
	// only set where two cases share a main class (two different `Outer`s).
	mainClass  string
	source     string
	wantStdout string
}

func (c runsLikeJavacCase) main() string {
	if c.mainClass != "" {
		return c.mainClass
	}
	return c.name
}

var runsLikeJavacCases = []runsLikeJavacCase{
	{
		// folded overflow constants
		name: "Overflow",
		source: `public class Overflow {
  public static void main(String[] args) {
    System.out.println(2147483647 + 1);
    System.out.println(-8 >>> 1);
    System.out.println(9223372036854775807L + 1L);
    System.out.println(1 << 33);
    System.out.println(2147483647 * 2);
  }
}`,
		wantStdout: "-2147483648\n2147483644\n-9223372036854775808\n2\n-2\n",
	},
	{
		// enum constant bodies
		name: "EnumBody",
		source: `public class EnumBody {
  enum Op {
    PLUS("+") { public int apply(int a, int b) { return a + b; } },
    TIMES("*") { public int apply(int a, int b) { return a * b; } },
    IDENT("=");
    private final String sym;
    Op(String sym) { this.sym = sym; }
    public int apply(int a, int b) { return a; }
    public String sym() { return sym; }
  }
  public static void main(String[] args) {
    for (Op o : Op.values()) System.out.println(o.name() + o.sym() + o.apply(6, 7));
  }
}`,
		wantStdout: "PLUS+13\nTIMES*42\nIDENT=6\n",
	},
	{
		// lambda expressions (invokedynamic / LambdaMetafactory)
		name: "Lam",
		source: `import java.util.function.Supplier;
import java.util.function.Predicate;
import java.util.function.Consumer;
public class Lam {
  public static void main(String[] a){
    Runnable r = () -> System.out.println("ran"); r.run();
    Supplier<String> s = () -> "hello"; System.out.println(s.get());
    String msg = "cap"; Supplier<String> s2 = () -> msg + "!"; System.out.println(s2.get());
    Predicate<String> p = str -> str.isEmpty();
    System.out.println(p.test("")); System.out.println(p.test("x"));
    Consumer<String> c = x -> System.out.println("got:" + x); c.accept("z");
  }
}`,
		wantStdout: "ran\nhello\ncap!\ntrue\nfalse\ngot:z\n",
	},
	{
		// try/finally (return, catch, rethrow, ordering)
		name: "Fy",
		source: `public class Fy {
  static StringBuilder log = new StringBuilder();
  static int a(int n){ try { if(n<0) throw new RuntimeException(); return n*2; } finally { log.append("a"); } }
  static int b(int n){ try { return n; } catch (RuntimeException e) { return -1; } finally { log.append("b"); } }
  static int c(int n){ int r=0; try { r=10/n; } catch (ArithmeticException e) { r=-1; } finally { log.append("c"); r+=100; } return r; }
  static String d(int n){ try { if(n==0) throw new RuntimeException("z"); return "ok"; } finally { log.append("d"); } }
  public static void main(String[] x){
    System.out.println(a(5));
    System.out.println(b(7));
    System.out.println(c(2)); System.out.println(c(0));
    try { a(-1); } catch (RuntimeException e) { System.out.println("rethrown"); }
    System.out.println(d(3));
    System.out.println(log.toString());
  }
}`,
		wantStdout: "10\n7\n105\n99\nrethrown\nok\nabccad\n",
	},
	{
		// try/catch (multi-catch, exception flow)
		name: "Tc",
		source: `public class Tc {
  static int parse(String s){ try { return Integer.parseInt(s); } catch (NumberFormatException e) { return -1; } }
  static String classify(int n){
    try { if (n<0) throw new IllegalArgumentException("neg"); if (n==0) throw new RuntimeException("zero"); return "pos"; }
    catch (IllegalArgumentException e) { return "iae:" + e.getMessage(); }
    catch (RuntimeException e) { return "rte:" + e.getMessage(); }
  }
  static int withFlow(int[] a, int i){ int r=0; try { r = a[i]; } catch (ArrayIndexOutOfBoundsException e) { r = -1; } return r + 1; }
  public static void main(String[] x){
    System.out.println(parse("42")); System.out.println(parse("zz"));
    System.out.println(classify(5)); System.out.println(classify(-1)); System.out.println(classify(0));
    System.out.println(withFlow(new int[]{7,8}, 1)); System.out.println(withFlow(new int[]{7}, 5));
  }
}`,
		wantStdout: "42\n-1\npos\niae:neg\nrte:zero\n9\n0\n",
	},
	{
		// throw statements
		name: "Tw",
		source: `public class Tw {
  static int checked(int n){ if (n < 0) throw new IllegalArgumentException("neg"); return n * 2; }
  static int half(int n){ if (n == 0) throw new RuntimeException(); return 100 / n; }
  public static void main(String[] a){
    System.out.println(checked(5));
    System.out.println(half(4));
  }
}`,
		wantStdout: "10\n25\n",
	},
	{
		// explicit super(args)/this(args) constructor invocations
		name: "Ctor",
		source: `public class Ctor {
  static class Base {
    int b;
    Base(int b){ this.b = b; }
  }
  static class Derived extends Base {
    int d = 100;
    Derived(int x){ super(x); d = d + x; }
    Derived(){ this(7); }
    int sum(){ return b + d; }
  }
  public static void main(String[] a){
    Derived p = new Derived(5);
    System.out.println(p.b + " " + p.d + " " + p.sum());
    Derived q = new Derived();
    System.out.println(q.b + " " + q.d + " " + q.sum());
  }
}`,
		wantStdout: "5 105 110\n7 107 114\n",
	},
	{
		// instanceof type-pattern binding
		name: "Iof",
		source: `public class Iof {
  static String describe(Object o){
    if (o instanceof String s) { return "str:" + s.length(); }
    if (o instanceof Integer i && i.intValue() > 0) { return "posint:" + i; }
    return "other";
  }
  public static void main(String[] a){
    System.out.println(describe("hello"));
    System.out.println(describe(Integer.valueOf(42)));
    System.out.println(describe(Integer.valueOf(-1)));
    System.out.println(describe(new Object()));
  }
}`,
		wantStdout: "str:5\nposint:42\nother\nother\n",
	},
	{
		// synchronized statement
		name: "Sync",
		source: `public class Sync {
  static final Object lock = new Object();
  static int counter;
  static void inc(){ synchronized (lock) { counter = counter + 1; } }
  static int withReturn(){ synchronized (lock) { return counter; } }
  static int viaException(){
    try { synchronized (lock) { throw new RuntimeException(); } }
    catch (RuntimeException e) { return -1; }
  }
  public static void main(String[] a){
    inc(); inc(); inc();
    System.out.println(counter);
    System.out.println(withReturn());
    System.out.println(viaException());
    System.out.println(Thread.holdsLock(lock));
  }
}`,
		wantStdout: "3\n3\n-1\nfalse\n",
	},
	{
		// try-with-resources runs identically to javac (order, return, suppression)
		name: "Twr",
		source: `public class Twr {
  static class R implements AutoCloseable {
    String n; boolean failClose;
    R(String n){ this.n = n; System.out.println("open " + n); }
    R(String n, boolean f){ this.n = n; this.failClose = f; System.out.println("open " + n); }
    public void close(){
      System.out.println("close " + n);
      if (failClose) throw new RuntimeException("close-" + n);
    }
  }
  static void normal(){
    try (R r = new R("a")) { System.out.println("body " + r.n); }
  }
  static int withReturn(){
    try (R r = new R("b")) { return 7; }
  }
  static void multi(){
    try (R x = new R("x"); R y = new R("y")) { System.out.println("body"); }
  }
  static void suppressed(){
    try {
      try (R r = new R("s", true)) { throw new IllegalStateException("body-boom"); }
    } catch (Exception e) {
      System.out.println("caught " + e.getMessage());
    }
  }
  public static void main(String[] a){
    normal();
    System.out.println("ret " + withReturn());
    multi();
    suppressed();
  }
}`,
		wantStdout: "open a\nbody a\nclose a\nopen b\nclose b\nret 7\nopen x\nopen y\nbody\nclose y\nclose x\nopen s\nclose s\ncaught body-boom\n",
	},
	{
		// try-with-resources null resource skips close (JLS 14.20.3.1)
		name: "TwrNull",
		source: `public class TwrNull {
  static class R implements AutoCloseable {
    public void close(){ System.out.println("close"); }
  }
  static R nothing(){ return null; }
  public static void main(String[] a){
    try (R r = nothing()) { System.out.println("body"); }
    System.out.println("done");
  }
}`,
		wantStdout: "body\ndone\n",
	},
	{
		// try-with-resources variable-access form
		name: "TwrVar",
		source: `public class TwrVar {
  static class R implements AutoCloseable {
    String n; R(String n){ this.n = n; }
    public void close(){ System.out.println("close " + n); }
  }
  static void useExisting(){
    R r = new R("v");
    try (r) { System.out.println("body"); }
  }
  public static void main(String[] a){ useExisting(); }
}`,
		wantStdout: "body\nclose v\n",
	},
	{
		// labeled break and continue
		name: "Lb",
		source: `public class Lb {
  static int firstPair(int[][] g, int t){
    int found = -1;
    outer:
    for (int i = 0; i < g.length; i++) {
      for (int j = 0; j < g[i].length; j++) {
        if (g[i][j] == t) { found = i * 10 + j; break outer; }
      }
    }
    return found;
  }
  static int skipRows(int n){
    int sum = 0;
    next:
    for (int i = 0; i < n; i++) {
      for (int j = 0; j < n; j++) {
        if (j == 1) continue next;
        sum += i * 100 + j;
      }
    }
    return sum;
  }
  static int labeledBlock(int n){
    int r = 0;
    done:
    {
      r = 1;
      if (n > 0) break done;
      r = 2;
    }
    return r;
  }
  public static void main(String[] a){
    int[][] grid = {{1,2,3},{4,5,6},{7,8,9}};
    System.out.println(firstPair(grid, 5));
    System.out.println(firstPair(grid, 42));
    System.out.println(skipRows(3));
    System.out.println(labeledBlock(1));
    System.out.println(labeledBlock(-1));
  }
}`,
		wantStdout: "11\n-1\n300\n1\n2\n",
	},
	{
		// enum switch (statement and exhaustive expression)
		name: "Es",
		source: `public class Es {
  enum Color { RED, GREEN, BLUE }
  static String name(Color c){ switch (c) { case RED: return "r"; case GREEN: return "g"; default: return "?"; } }
  static int code(Color c){ return switch (c) { case RED -> 1; case GREEN -> 2; case BLUE -> 3; }; }
  static int viaStmt(Color c){ int r=0; switch(c){ case RED: r=1; break; case BLUE: r=3; break; default: r=-1; } return r; }
  public static void main(String[] a){
    System.out.println(name(Color.RED)); System.out.println(name(Color.BLUE));
    System.out.println(code(Color.GREEN)); System.out.println(code(Color.BLUE));
    System.out.println(viaStmt(Color.RED)); System.out.println(viaStmt(Color.GREEN));
  }
}`,
		wantStdout: "r\n?\n2\n3\n1\n-1\n",
	},
	{
		// switch expressions (arrow, yield, block, string)
		name: "Sx",
		source: `public class Sx {
  static int arrow(int n){ return switch(n){ case 1 -> 10; case 2,3 -> 20; default -> 0; }; }
  static String yld(int n){ return switch(n){ case 0: yield "zero"; default: yield "many"; }; }
  static int blk(int n){ return switch(n){ case 1 -> { int t = n*100; yield t+1; } default -> -1; }; }
  static String strsw(String s){ return switch(s){ case "a" -> "A"; case "b" -> "B"; default -> "?"; }; }
  public static void main(String[] a){
    System.out.println(arrow(1)); System.out.println(arrow(3)); System.out.println(arrow(9));
    System.out.println(yld(0)); System.out.println(yld(5));
    System.out.println(blk(1)); System.out.println(blk(2));
    System.out.println(strsw("b")); System.out.println(strsw("z"));
  }
}`,
		wantStdout: "10\n20\n0\nzero\nmany\n101\n-1\nB\n?\n",
	},
	{
		// for-each over a collection (Iterator)
		name: "Fe",
		source: `import java.util.ArrayList;
import java.util.List;
public class Fe {
  static int total(List<Integer> xs){ int s=0; for(int v : xs) s+=v; return s; }
  public static void main(String[] args){
    List<String> names = new ArrayList<String>();
    names.add("alice"); names.add("bob");
    for (String n : names) System.out.println(n);
    List<Integer> nums = new ArrayList<Integer>();
    nums.add(10); nums.add(20); nums.add(12);
    System.out.println(total(nums));
    for (Object o : names) System.out.println(o);
  }
}`,
		wantStdout: "alice\nbob\n42\nalice\nbob\n",
	},
	{
		// arrays (creation, access, length, store, foreach, multidim)
		name: "Arr",
		source: `public class Arr {
  static int sum(int[] a){ int s=0; for(int i=0;i<a.length;i++) s+=a[i]; return s; }
  static int sumEach(int[] a){ int s=0; for(int x : a) s+=x; return s; }
  public static void main(String[] args){
    int[] a = new int[]{3,4,5};
    System.out.println(a.length);
    System.out.println(a[1]);
    a[1] = 40; System.out.println(a[1]);
    a[2] += 100; System.out.println(a[2]);
    a[0]++; System.out.println(a[0]);
    System.out.println(sum(a));
    System.out.println(sumEach(a));
    int[] b = new int[3]; b[0]=7; System.out.println(b[0] + " " + b[2]);
    String[] s = {"x","y"}; System.out.println(s[0] + s[1] + " " + s.length);
    int[][] m = new int[2][3]; m[1][2] = 9;
    System.out.println(m[1][2] + " " + m.length + " " + m[0].length);
  }
}`,
		wantStdout: "3\n4\n40\n105\n4\n149\n149\n7 0\nxy 2\n9 2 3\n",
	},
	{
		// enum declarations
		name: "En",
		source: `public class En {
  enum Color { RED, GREEN, BLUE }
  enum Planet {
    EARTH(5.976e24), MARS(6.421e23);
    private final double mass;
    Planet(double mass){ this.mass = mass; }
    double getMass(){ return mass; }
  }
  public static void main(String[] a){
    System.out.println(Color.RED.name() + " " + Color.RED.ordinal());
    System.out.println(Color.BLUE.ordinal());
    System.out.println(Color.valueOf("GREEN").name());
    System.out.println(Planet.EARTH.getMass());
    System.out.println(Planet.MARS.name());
    System.out.println(Color.RED == Color.RED);
    System.out.println(Color.RED == Color.BLUE);
  }
}`,
		wantStdout: "RED 0\n2\nGREEN\n5.976E24\nMARS\ntrue\nfalse\n",
	},
	{
		// autoboxing and unboxing
		name: "Box",
		source: `import java.util.function.Supplier;
public class Box {
  static int unboxAdd(Integer a, int b){ return a + b; }
  static Integer boxRet(int x){ return x; }
  static double widenUnbox(Integer a){ return a + 0.5; }
  static boolean cmp(Integer a, int b){ return a < b; }
  static boolean eqMixed(Integer a, int b){ return a == b; }
  static Supplier<Integer> sup(int base){ return () -> base + 1; }
  static String show(Object o){ return o.toString(); }
  public static void main(String[] a){
    System.out.println(unboxAdd(40, 2));
    Integer r = boxRet(7); System.out.println(r);
    System.out.println(widenUnbox(3));
    System.out.println(cmp(3, 5)); System.out.println(cmp(9, 5));
    System.out.println(eqMixed(5, 5)); System.out.println(eqMixed(5, 6));
    System.out.println(sup(10).get());
    System.out.println(show(42));
  }
}`,
		wantStdout: "42\n7\n3.5\ntrue\nfalse\ntrue\nfalse\n11\n42\n",
	},
	{
		// method references (static/bound/unbound/constructor)
		name: "Mr",
		source: `import java.util.function.Supplier;
import java.util.function.Predicate;
import java.util.function.Function;
import java.util.function.Consumer;
public class Mr {
  public static void main(String[] a){
    Predicate<String> empty = String::isEmpty;
    System.out.println(empty.test("")); System.out.println(empty.test("x"));
    Consumer<String> pr = System.out::println;
    pr.accept("bound!");
    Function<String,Integer> len = String::length;
    System.out.println(len.apply("hello"));
    Supplier<Mr> ctor = Mr::new;
    System.out.println(ctor.get() != null);
  }
}`,
		wantStdout: "true\nfalse\nbound!\n5\ntrue\n",
	},
	{
		// this-capturing lambdas (instance context)
		name: "Th",
		source: `import java.util.function.Supplier;
public class Th {
  String label = "L";
  String suffix(String s){ return label + s; }
  Supplier<String> labeler(){ return () -> label + "!"; }
  Supplier<String> withLocal(String x){ return () -> label + x; }
  Supplier<String> viaMethod(String y){ return () -> suffix(y); }
  public static void main(String[] a){
    Th t = new Th();
    System.out.println(t.labeler().get());
    System.out.println(t.withLocal("X").get());
    System.out.println(t.viaMethod("Y").get());
  }
}`,
		wantStdout: "L!\nLX\nLY\n",
	},
	{
		// static nested classes and field ++
		name: "Nest",
		source: `public class Nest {
  static class Point { int x, y; Point(int x, int y){ this.x=x; this.y=y; } int sum(){ return x+y; } }
  static class Counter { static int total; int n; void tick(){ n++; total++; } int get(){ return n; } }
  static int helper(int a){ return a*2; }
  public static void main(String[] a){
    Point p = new Point(3,4); System.out.println(p.sum());
    Counter c = new Counter(); c.tick(); c.tick();
    System.out.println(c.get()); System.out.println(Counter.total);
    System.out.println(helper(21));
  }
}`,
		wantStdout: "7\n2\n2\n42\n",
	},
	{
		// definite assignment: uninitialized locals across branches verify like javac
		name: "Da",
		source: `public class Da {
  static int ifElse(int c){ int r; if(c>0){ r=1; } else { r=2; } return r; }
  static int viaLoop(int n){ int r; if(n<0){ r=-1; } else { r=0; for(int i=0;i<n;i++){ r=r+i; } } return r; }
  public static void main(String[] a){
    System.out.println(ifElse(5)); System.out.println(ifElse(-3));
    System.out.println(viaLoop(4)); System.out.println(viaLoop(-1));
  }
}`,
		wantStdout: "1\n2\n6\n-1\n",
	},
	{
		// arrow and string switch
		name: "Sw2",
		source: `public class Sw2 {
  static int arrowI(int n){ int r; switch(n){ case 1 -> r=10; case 2,3 -> r=20; default -> r=99; } return r; }
  static String arrowStr(int n){ switch(n){ case 0 -> { return "zero"; } case 1 -> { return "one"; } default -> { return "many"; } } }
  static String colonStr(String s){ switch(s){ case "a": return "A"; case "b": case "c": return "BC"; default: return "?"; } }
  static String arrowStrSel(String s){ switch(s){ case "x" -> { return "X"; } default -> { return "Z"; } } }
  public static void main(String[] a){
    System.out.println(arrowI(1)); System.out.println(arrowI(3)); System.out.println(arrowI(7));
    System.out.println(arrowStr(0)); System.out.println(arrowStr(5));
    System.out.println(colonStr("a")); System.out.println(colonStr("c")); System.out.println(colonStr("z"));
    System.out.println(arrowStrSel("x")); System.out.println(arrowStrSel("q"));
  }
}`,
		wantStdout: "10\n20\n99\nzero\nmany\nA\nBC\n?\nX\nZ\n",
	},
	{
		// compound assignment (+=, bitwise, narrowing, fields, string)
		name: "Ca",
		source: `public class Ca {
  static int f; int g;
  static int locals(int n){ int s=0; for(int i=0;i<n;i++){ s+=i; s*=2; s-=1; } return s; }
  static int bits(int x){ x<<=2; x|=1; x^=3; x&=0xFE; x>>=1; return x; }
  static int narrow(){ byte b=10; b+=300; return b; }
  static double dbl(double d, int i){ d+=i; d*=1.5; return d; }
  static int idivd(int i){ i+=2.7; return i; }
  static int statics(int n){ f=5; f+=n; return f; }
  int inst(int n){ g=1; g+=n; g*=3; return g; }
  static String str(){ String s="a"; s+="b"; s+=1; s+=true; return s; }
  public static void main(String[] a){
    System.out.println(locals(4)); System.out.println(bits(255)); System.out.println(narrow());
    System.out.println(dbl(2.0,3)); System.out.println(idivd(5)); System.out.println(statics(10));
    System.out.println(new Ca().inst(4)); System.out.println(str());
  }
}`,
		wantStdout: "7\n127\n54\n7.5\n7\n15\n15\nab1true\n",
	},
	{
		// break and continue in loops
		name: "Lp",
		source: `public class Lp {
  static int sumEven(int n){ int s=0; for(int i=0;i<n;i++){ if(i%2==1) continue; s=s+i; } return s; }
  static int firstGt(int n, int t){ int r=-1; for(int i=0;i<n;i++){ if(i>t){ r=i; break; } } return r; }
  static int whileBreak(int n){ int c=0; while(true){ c=c+1; if(c>=n) break; } return c; }
  static int doCont(int n){ int s=0,i=0; do { i=i+1; if(i==3) continue; s=s+i; } while(i<n); return s; }
  static int nested(int n){ int c=0; for(int i=0;i<n;i++){ for(int j=0;j<n;j++){ if(j==2) break; c=c+1; } } return c; }
  public static void main(String[] a){
    System.out.println(sumEven(6)); System.out.println(firstGt(10,4));
    System.out.println(whileBreak(5)); System.out.println(doCont(5)); System.out.println(nested(4));
  }
}`,
		wantStdout: "6\n5\n5\n12\n8\n",
	},
	{
		// bit manipulation
		name: "Bits",
		source: `public class Bits {
  static int setBit(int x, int i){ return x | (1 << i); }
  static int clearBit(int x, int i){ return x & ~(1 << i); }
  static boolean isSet(int x, int i){ return (x & (1 << i)) != 0; }
  static int countOnes(int x){ int c = 0; while (x != 0) { c += x & 1; x >>>= 1; } return c; }
  static long mix(long a, int s){ return (a << s) ^ (a >>> s) | (a & 0xFFL); }
  public static void main(String[] a){
    System.out.println(setBit(0, 3));
    System.out.println(clearBit(15, 1));
    System.out.println(isSet(8, 3));
    System.out.println(countOnes(-1));
    System.out.println(mix(123456789L, 5));
  }
}`,
		wantStdout: "8\n13\ntrue\n32\n3947068637\n",
	},
	{
		// multidimensional arrays
		name: "Mat",
		source: `public class Mat {
  static int rectSum(int n, int m){
    int[][] g = new int[n][m];
    for (int i = 0; i < n; i++) for (int j = 0; j < m; j++) g[i][j] = i * m + j;
    int s = 0;
    for (int i = 0; i < n; i++) for (int j = 0; j < m; j++) s += g[i][j];
    return s;
  }
  static int jagged(){
    int[][] t = new int[3][];
    for (int i = 0; i < 3; i++) { t[i] = new int[i + 1]; for (int j = 0; j <= i; j++) t[i][j] = j; }
    int s = 0;
    for (int[] row : t) for (int v : row) s += v;
    return s;
  }
  public static void main(String[] a){
    System.out.println(rectSum(3, 4));
    System.out.println(jagged());
  }
}`,
		wantStdout: "66\n4\n",
	},
	{
		// array element compound assignment
		name: "ArrAssign",
		source: `public class ArrAssign {
  public static void main(String[] a){
    int[] x = {1, 2, 3, 4};
    x[0] += 10;
    x[1] *= 5;
    x[2]++;
    x[3] <<= 2;
    int s = 0;
    for (int v : x) s = s * 100 + v;
    System.out.println(s);
  }
}`,
		wantStdout: "11100416\n",
	},
	{
		// char and digit arithmetic
		name: "Chars",
		source: `public class Chars {
  static int parse(String s){
    int n = 0;
    for (int i = 0; i < s.length(); i++) { char c = s.charAt(i); n = n * 10 + (c - '0'); }
    return n;
  }
  static String shift(String s){
    char[] out = new char[s.length()];
    for (int i = 0; i < s.length(); i++) out[i] = (char) (s.charAt(i) + 1);
    return new String(out);
  }
  public static void main(String[] a){
    System.out.println(parse("2026"));
    System.out.println(shift("abc"));
  }
}`,
		wantStdout: "2026\nbcd\n",
	},
	{
		// recursion
		name: "Rec",
		source: `public class Rec {
  static long fact(int n){ return n <= 1 ? 1L : n * fact(n - 1); }
  static int fib(int n){ return n < 2 ? n : fib(n - 1) + fib(n - 2); }
  static boolean even(int n){ return n == 0 ? true : odd(n - 1); }
  static boolean odd(int n){ return n == 0 ? false : even(n - 1); }
  public static void main(String[] a){
    System.out.println(fact(10));
    System.out.println(fib(15));
    System.out.println(even(10));
    System.out.println(odd(7));
  }
}`,
		wantStdout: "3628800\n610\ntrue\ntrue\n",
	},
	{
		// do-while with break and continue
		name: "DoW",
		source: `public class DoW {
  static int run(int n){
    int i = 0, s = 0;
    do {
      i++;
      if (i % 2 == 0) continue;
      if (i > n) break;
      s += i;
    } while (i < 100);
    return s;
  }
  public static void main(String[] a){ System.out.println(run(9)); }
}`,
		wantStdout: "25\n",
	},
	{
		// common JDK library calls
		name: "Lib",
		source: `public class Lib {
  public static void main(String[] a){
    System.out.println(Math.max(3L, 7L) + Math.min(2L, 9L));
    System.out.println(Integer.toHexString(255));
    System.out.println(Integer.bitCount(255));
    System.out.println(Long.toHexString(4096L));
    System.out.println(Character.getNumericValue('7'));
    int[] src = {1, 2, 3, 4, 5};
    int[] dst = new int[5];
    System.arraycopy(src, 1, dst, 0, 3);
    StringBuilder sb = new StringBuilder();
    sb.append("x=").append(42).append(",").append(3.5).append(",").append(99L);
    System.out.println(sb.toString());
    System.out.println(dst[0] + dst[1] + dst[2]);
    System.out.println(Integer.MAX_VALUE);
  }
}`,
		wantStdout: "9\nff\n8\n1000\n7\nx=42,3.5,99\n9\n2147483647\n",
	},
	{
		// array constructor references (T[]::new)
		name: "ArrNew",
		source: `import java.util.function.IntFunction;
public class ArrNew {
  public static void main(String[] a){
    IntFunction<int[]> f = int[]::new;
    int[] x = f.apply(5);
    System.out.println(x.length);
    IntFunction<String[]> g = String[]::new;
    String[] s = g.apply(3);
    System.out.println(s.length + " " + (s[0] == null));
  }
}`,
		wantStdout: "5\n3 true\n",
	},
	{
		// Arrays utility calls on primitive arrays
		name: "ArrUtil",
		source: `import java.util.Arrays;
public class ArrUtil {
  public static void main(String[] a){
    int[] x = {5, 3, 1, 4, 2};
    Arrays.sort(x);
    System.out.println(Arrays.toString(x));
    System.out.println(Arrays.binarySearch(x, 4));
    int[] y = Arrays.copyOf(x, 3);
    System.out.println(Arrays.toString(y));
    int[] z = new int[3];
    Arrays.fill(z, 7);
    System.out.println(Arrays.toString(z));
  }
}`,
		wantStdout: "[1, 2, 3, 4, 5]\n3\n[1, 2, 3]\n[7, 7, 7]\n",
	},
	{
		// local classes (no capture)
		name: "Local",
		source: `public class Local {
  static int counter(){
    class Counter { int n; int inc(){ n = n + 1; return n; } }
    Counter c = new Counter();
    c.inc();
    return c.inc();
  }
  static int viaInterface(int v){
    class Doubler implements java.util.function.IntUnaryOperator {
      public int applyAsInt(int x){ return x * 2; }
    }
    java.util.function.IntUnaryOperator op = new Doubler();
    return op.applyAsInt(v);
  }
  public static void main(String[] a){
    System.out.println(counter());
    System.out.println(viaInterface(21));
  }
}`,
		wantStdout: "2\n42\n",
	},
	{
		// user-defined interfaces (abstract + default methods, constants)
		name: "Iface",
		source: `public class Iface {
  interface Shape {
    int SIDES = 4;
    int area();
    default String describe(){ return "area=" + area(); }
  }
  static class Square implements Shape {
    int s; Square(int s){ this.s = s; }
    public int area(){ return s * s; }
  }
  public static void main(String[] a){
    Shape sh = new Square(3);
    System.out.println(sh.area());
    System.out.println(sh.describe());
    System.out.println(Shape.SIDES);
  }
}`,
		wantStdout: "9\narea=9\n4\n",
	},
	{
		// local classes capturing enclosing locals
		name: "Cap",
		source: `public class Cap {
  static int adder(int base){
    int bonus = 100;
    class Adder { int add(int x){ return base + x + bonus; } }
    Adder a = new Adder();
    return a.add(10);
  }
  static int viaOp(int factor){
    class Mul implements java.util.function.IntUnaryOperator {
      public int applyAsInt(int x){ return x * factor; }
    }
    java.util.function.IntUnaryOperator op = new Mul();
    return op.applyAsInt(7);
  }
  public static void main(String[] z){
    System.out.println(adder(5));
    System.out.println(viaOp(3));
  }
}`,
		wantStdout: "115\n21\n",
	},
	{
		// anonymous interface classes (with capture)
		name: "Anon",
		source: `public class Anon {
  static int adder(int base){
    java.util.function.IntUnaryOperator op = new java.util.function.IntUnaryOperator(){
      public int applyAsInt(int x){ return x + base; }
    };
    return op.applyAsInt(10);
  }
  static String greet(){
    Runnable r = new Runnable(){ public void run(){ System.out.println("hi"); } };
    r.run();
    return "done";
  }
  public static void main(String[] a){
    System.out.println(adder(5));
    System.out.println(greet());
  }
}`,
		wantStdout: "15\nhi\ndone\n",
	},
	{
		// anonymous class extending a class (super args + capture)
		name: "AnonExt",
		source: `public class AnonExt {
  static class Base {
    Base(int v){ System.out.println("base " + v); }
    int get(){ return 0; }
  }
  static int run(int cap){
    Base b = new Base(99){ public int get(){ return cap + 1; } };
    return b.get();
  }
  public static void main(String[] a){ System.out.println(run(5)); }
}`,
		wantStdout: "base 99\n6\n",
	},
	{
		// anonymous class accessing the enclosing instance (this$0)
		name: "Outer",
		source: `public class Outer {
  int field = 10;
  int outerMethod(){ return 5; }
  Runnable makeRunnable(){
    return new Runnable(){ public void run(){ System.out.println(field + outerMethod()); } };
  }
  public static void main(String[] a){ new Outer().makeRunnable().run(); }
}`,
		wantStdout: "15\n",
	},
	{
		// local class accessing enclosing instance and a captured local
		name: "LThis0",
		source: `public class LThis0 {
  int base = 100;
  int helper(){ return 7; }
  int run(int p){
    class Calc { int compute(){ return base + helper() + p; } }
    return new Calc().compute();
  }
  public static void main(String[] a){ System.out.println(new LThis0().run(5)); }
}`,
		wantStdout: "112\n",
	},
	{
		// pattern switch (type patterns, guard, null, default)
		name: "PSw",
		source: `public class PSw {
  static String describe(Object o){
    return switch (o) {
      case String s -> "str:" + s.length();
      case Integer i when i > 0 -> "pos:" + i;
      case Integer i -> "int:" + i;
      case null -> "null";
      default -> "other";
    };
  }
  static int classify(Object o){
    int r;
    switch (o) {
      case String s -> r = s.length();
      case Integer i -> r = i;
      default -> r = -1;
    }
    return r;
  }
  public static void main(String[] a){
    System.out.println(describe("hi"));
    System.out.println(describe(42));
    System.out.println(describe(-1));
    System.out.println(describe(null));
    System.out.println(describe(3.5));
    System.out.println(classify("abc") + "," + classify(7) + "," + classify(3.5));
  }
}`,
		wantStdout: "str:2\npos:42\nint:-1\nnull\nother\n3,7,-1\n",
	},
	{
		// private cross-nest access (nestmates)
		name: "Nestmate",
		source: `public class Nestmate {
  private int secret = 42;
  private int hidden(){ return 8; }
  Runnable r(){
    return new Runnable(){ public void run(){ System.out.println(secret + hidden()); } };
  }
  static class Helper { static int peek(Nestmate n){ return n.secret; } }
  public static void main(String[] a){
    Nestmate n = new Nestmate();
    n.r().run();
    System.out.println(Helper.peek(n));
  }
}`,
		wantStdout: "50\n42\n",
	},
	{
		// record declarations
		name: "RecMain",
		source: `record Point(int x, int y) {
  int sum(){ return x + y; }
}
public class RecMain {
  public static void main(String[] a){
    Point p = new Point(3, 4);
    System.out.println(p.x() + "," + p.y());
    System.out.println(p);
    System.out.println(p.sum());
    System.out.println(p.equals(new Point(3, 4)));
    System.out.println(p.equals(new Point(3, 5)));
    System.out.println(p.hashCode() == new Point(3, 4).hashCode());
  }
}`,
		wantStdout: "3,4\nPoint[x=3, y=4]\n7\ntrue\nfalse\ntrue\n",
	},
	{
		// record deconstruction patterns in switch
		name: "RecPat",
		source: `record Point(int x, int y) {}
record Line(Point from, Point to) {}
public class RecPat {
  static String f(Object o) {
    return switch (o) {
      case Line(Point(int x1, int y1), Point(int x2, int y2)) -> "line:" + (x1+y1+x2+y2);
      case Point(int x, int y) when x == y -> "diag:" + x;
      case Point(int x, int y) -> "pt:" + (x+y);
      case String s -> "str:" + s;
      default -> "other";
    };
  }
  public static void main(String[] a){
    System.out.println(f(new Line(new Point(1,2), new Point(3,4))));
    System.out.println(f(new Point(5,5)));
    System.out.println(f(new Point(2,3)));
    System.out.println(f("hi"));
    System.out.println(f(42));
  }
}`,
		wantStdout: "line:10\ndiag:5\npt:5\nstr:hi\nother\n",
	},
	{
		// instanceof record deconstruction
		name: "InstRec",
		source: `record Point(int x, int y) {}
public class InstRec {
  static int f(Object o) {
    if (o instanceof Point(int x, int y)) return x + y;
    return -1;
  }
  public static void main(String[] a){
    System.out.println(f(new Point(3,4)));
    System.out.println(f("nope"));
  }
}`,
		wantStdout: "7\n-1\n",
	},
	{
		// colon-form pattern switch
		name: "ColonPat",
		source: `public class ColonPat {
  static String f(Object o) {
    String r;
    switch (o) {
      case Integer i: r = "int:" + i; break;
      case String s: r = "str:" + s; break;
      default: r = "other"; break;
    }
    return r;
  }
  static int g(Object o) {
    return switch (o) {
      case Integer i: yield i * 2;
      case String s: yield s.length();
      default: yield -1;
    };
  }
  public static void main(String[] a){
    System.out.println(f(42));
    System.out.println(f("hi"));
    System.out.println(f(3.5));
    System.out.println(g(21));
    System.out.println(g("abcd"));
    System.out.println(g(2.0));
  }
}`,
		wantStdout: "int:42\nstr:hi\nother\n42\n4\n-1\n",
	},
	{
		// anonymous class with own field initializers
		name: "AnonOwnField",
		source: `interface Sup { int get(); }
public class AnonOwnField {
  int base = 100;
  Sup make(int k) {
    int local = 5;
    return new Sup() {
      int v = k + local + base;
      public int get() { return v; }
    };
  }
  public static void main(String[] a){
    System.out.println(new AnonOwnField().make(7).get());
  }
}`,
		wantStdout: "112\n",
	},
	{
		// local class with own field initializer and capture
		name: "LocalField",
		source: `public class LocalField {
  int run(int k) {
    int local = 3;
    class C { int v = k + local; int get(){ return v + k; } }
    return new C().get();
  }
  public static void main(String[] a){ System.out.println(new LocalField().run(10)); }
}`,
		wantStdout: "23\n",
	},
	{
		// anonymous class writing its own field
		name: "AnonWrite",
		source: `interface Counter { int next(); }
public class AnonWrite {
  static Counter make() {
    return new Counter() {
      int n = 0;
      public int next() { n = n + 1; return n; }
    };
  }
  public static void main(String[] a){
    Counter c = make();
    System.out.println(c.next());
    System.out.println(c.next());
    System.out.println(c.next());
  }
}`,
		wantStdout: "1\n2\n3\n",
	},
	{
		// instance and static initializer blocks
		name: "InitBlocks",
		source: `public class InitBlocks {
  static int s;
  static { s = 7; }
  int a;
  int b = 1;
  { a = b + 10; }
  int c = a + 100;
  public static void main(String[] x){
    InitBlocks o = new InitBlocks();
    System.out.println(s);
    System.out.println(o.a);
    System.out.println(o.c);
  }
}`,
		wantStdout: "7\n11\n111\n",
	},
	{
		// record compact constructor
		name: "CompactRec",
		source: `record Range(int lo, int hi) {
  Range {
    if (lo > hi) { int t = lo; lo = hi; hi = t; }
  }
}
public class CompactRec {
  public static void main(String[] a){
    Range r = new Range(5, 2);
    System.out.println(r.lo() + "," + r.hi());
    Range s = new Range(1, 9);
    System.out.println(s.lo() + "," + s.hi());
  }
}`,
		wantStdout: "2,5\n1,9\n",
	},
	{
		// record explicit canonical and alternate constructors
		name: "CanonRec",
		source: `record Frac(int num, int den) {
  Frac(int num, int den) {
    if (den == 0) throw new ArithmeticException();
    this.num = num;
    this.den = den;
  }
  Frac(int whole) { this(whole, 1); }
}
public class CanonRec {
  public static void main(String[] a){
    Frac f = new Frac(3, 4);
    System.out.println(f.num() + "/" + f.den());
    Frac g = new Frac(7);
    System.out.println(g.num() + "/" + g.den());
  }
}`,
		wantStdout: "3/4\n7/1\n",
	},
	{
		// record explicit accessor override
		name: "AccRec",
		source: `record Name(String first, String last) {
  public String first() { return first.toUpperCase(); }
}
public class AccRec {
  public static void main(String[] a){
    Name n = new Name("ann", "lee");
    System.out.println(n.first());
    System.out.println(n.last());
  }
}`,
		wantStdout: "ANN\nlee\n",
	},
	{
		// non-static member inner class accessing the enclosing instance
		name:      "OuterInner",
		mainClass: "Outer",
		source: `public class Outer {
  int base = 10;
  class Inner {
    int v;
    Inner(int x) { v = x; }
    int sum() { return v + base; }
    void bump() { base = base + v; }
  }
  int run() {
    Inner i = new Inner(5);
    i.bump();
    return i.sum();
  }
  public static void main(String[] a){ System.out.println(new Outer().run()); }
}`,
		wantStdout: "20\n",
	},
	{
		// qualified anonymous outer.new Inner(){...}
		name: "QAnon",
		source: `public class QAnon {
  int x;
  QAnon(int x) { this.x = x; }
  class Inner {
    int v;
    Inner(int a) { v = a; }
    int get() { return v + x; }
  }
  public static void main(String[] a) {
    QAnon one = new QAnon(10);
    QAnon two = new QAnon(20);
    QAnon.Inner plain = one.new Inner(1);
    QAnon.Inner anon = two.new Inner(2) { int get() { return v * 100; } };
    System.out.println(plain.get());
    System.out.println(anon.get());
  }
}`,
		wantStdout: "11\n200\n",
	},
	{
		// qualified outer.new Inner()
		name: "QNew",
		source: `public class QNew {
  int x;
  QNew(int x) { this.x = x; }
  class Inner {
    int v;
    Inner(int a) { v = a; }
    int sum() { return v + x; }
  }
  public static void main(String[] a) {
    QNew one = new QNew(10);
    QNew two = new QNew(20);
    System.out.println(one.new Inner(1).sum());
    System.out.println(two.new Inner(2).sum());
  }
}`,
		wantStdout: "11\n22\n",
	},
	{
		// local class with a declared constructor and capture
		name: "LCtor",
		source: `public class LCtor {
  int run(int k) {
    int local = 3;
    class C {
      int v;
      C(int mult) { v = (k + local) * mult; }
      int get(){ return v + k; }
    }
    return new C(2).get();
  }
  public static void main(String[] a){ System.out.println(new LCtor().run(10)); }
}`,
		wantStdout: "36\n",
	},
	{
		// varargs calls and bodies
		name: "Varargs",
		source: `public class Varargs {
  static int sum(int... xs){ int s = 0; for (int v : xs) s += v; return s; }
  static String join(String sep, String... parts){
    StringBuilder b = new StringBuilder();
    for (int i = 0; i < parts.length; i++){ if (i > 0) b.append(sep); b.append(parts[i]); }
    return b.toString();
  }
  public static void main(String[] a){
    System.out.println(sum(1, 2, 3, 4));
    System.out.println(sum());
    int[] arr = {5, 6, 7};
    System.out.println(sum(arr));
    System.out.println(join("-", "a", "b", "c"));
  }
}`,
		wantStdout: "10\n0\n18\na-b-c\n",
	},
	{
		// super.method() calls
		name: "SuperCall",
		source: `class Base {
  int f() { return 1; }
  int g(int x) { return x * 2; }
}
public class SuperCall extends Base {
  int f() { return super.f() + 10; }
  int h() { return super.g(super.f()); }
  public static void main(String[] a){
    SuperCall s = new SuperCall();
    System.out.println(s.f());
    System.out.println(s.h());
    System.out.println(s.g(5));
  }
}`,
		wantStdout: "11\n2\n10\n",
	},
	{
		// super field access
		name: "SuperField",
		source: `class B { int x = 5; }
public class SuperField extends B {
  int x = 99;
  int hidden() { return super.x; }
  int own() { return x; }
  public static void main(String[] a){
    SuperField s = new SuperField();
    System.out.println(s.hidden());
    System.out.println(s.own());
  }
}`,
		wantStdout: "5\n99\n",
	},
	{
		// switch over a boxed Integer selector
		name: "BoxedSwitch",
		source: `public class BoxedSwitch {
  static String f(Integer n) {
    switch (n) {
      case 1: return "one";
      case 2: return "two";
      default: return "many";
    }
  }
  public static void main(String[] a){
    System.out.println(f(1));
    System.out.println(f(2));
    System.out.println(f(9));
  }
}`,
		wantStdout: "one\ntwo\nmany\n",
	},
	{
		// qualified Outer.this access
		name: "QualThis",
		source: `public class QualThis {
  int v = 7;
  class Inner {
    int v = 3;
    int outer() { return QualThis.this.v; }
    int inner() { return v; }
    int both() { return QualThis.this.v + this.v; }
  }
  int run() { Inner i = new Inner(); return i.outer() * 100 + i.inner() * 10 + i.both(); }
  public static void main(String[] a){ System.out.println(new QualThis().run()); }
}`,
		wantStdout: "740\n",
	},
	{
		// interface static methods and unrelated-reference conditionals
		name: "IfaceCond",
		source: `interface Calc { static int twice(int x){ return x * 2; } }
public class IfaceCond {
  public static void main(String[] a){
    System.out.println(Calc.twice(21));
    boolean f = true;
    Object o = f ? "str" : Integer.valueOf(1);
    System.out.println(o);
    Object p = f ? Integer.valueOf(1) : "str";
    System.out.println(p);
  }
}`,
		wantStdout: "42\nstr\n1\n",
	},
	{
		// anonymous class using inherited members by simple name
		name: "AbstractAnon",
		source: `abstract class Shape {
  int size;
  Shape(int s) { size = s; }
  int describe() { return 1000; }
  abstract int area();
}
public class AbstractAnon {
  public static void main(String[] a){
    Shape sq = new Shape(5) {
      int area() { return size * size + describe(); }
    };
    System.out.println(sq.area());
  }
}`,
		wantStdout: "1025\n",
	},
	{
		// static imports and array clone
		name: "StaticImportClone",
		source: `import static java.lang.Math.max;
import static java.lang.Math.min;
public class StaticImportClone {
  public static void main(String[] a){
    System.out.println(max(3, 7) + min(3, 7));
    int[] x = {1, 2, 3};
    int[] y = x.clone();
    y[0] = 9;
    System.out.println(x[0] + y[0]);
  }
}`,
		wantStdout: "10\n10\n",
	},
	{
		// increment/decrement as a value
		name: "IncDec",
		source: `public class IncDec {
  static int id(int x){ return x; }
  public static void main(String[] a){
    int i = 5;
    int j = i++;
    int k = ++i;
    System.out.println(i + " " + j + " " + k);
    int[] arr = new int[3];
    int n = 0;
    arr[n++] = 10; arr[n++] = 20;
    System.out.println(arr[0] + " " + arr[1] + " " + n);
    System.out.println(id(n--) + " " + n);
    long p = 100; long q = p--;
    System.out.println(p + " " + q);
    double d = 1.5; double e = ++d;
    System.out.println(d + " " + e);
  }
}`,
		wantStdout: "7 5 7\n10 20 2\n2 1\n99 100\n2.5 2.5\n",
	},
	{
		// boolean bitwise operators as values
		name: "BoolBitwise",
		source: `public class BoolBitwise {
  public static void main(String[] a){
    boolean x = true, y = false;
    System.out.println(x & y);
    System.out.println(x | y);
    System.out.println(x ^ y);
    System.out.println("" + (x & y) + (x | y));
    boolean r = (x | y) & !(x ^ y);
    System.out.println(r);
  }
}`,
		wantStdout: "false\ntrue\ntrue\nfalsetrue\nfalse\n",
	},
	{
		// shifts with wide distances and byte/char increment overflow
		name: "ShiftIncEdge",
		source: `public class ShiftIncEdge {
  public static void main(String[] a){
    int r = 1 << 32L;
    System.out.println(r);
    int x = 1; long n = 40;
    System.out.println(x << n);
    int y = 1; long m = 8; y <<= m;
    System.out.println(y);
    byte b = 127; b++;
    System.out.println(b);
    byte c = 127; byte d = ++c;
    System.out.println(d);
    char ch = 65; ch++;
    System.out.println(ch);
  }
}`,
		wantStdout: "1\n256\n256\n-128\n-128\nB\n",
	},
	{
		// abrupt completion inside finally
		name: "FinallyAbrupt",
		source: `public class FinallyAbrupt {
  static int overrideReturn() { try { return 1; } finally { return 2; } }
  static int swallowThrow() { try { throw new RuntimeException(); } finally { return 3; } }
  static int breakFromFinally() {
    int s = 0;
    for (int i = 0; i < 3; i++) { try { s += i; } finally { if (i == 1) break; } }
    return s;
  }
  public static void main(String[] a){
    System.out.println(overrideReturn());
    System.out.println(swallowThrow());
    System.out.println(breakFromFinally());
  }
}`,
		wantStdout: "2\n3\n1\n",
	},
	{
		// catching a (newly stubbed) ArrayStoreException
		name: "ArrStore",
		source: `public class ArrStore {
  public static void main(String[] a){
    Object[] o = new String[2];
    try { o[0] = Integer.valueOf(1); }
      catch (ArrayStoreException e) { System.out.println("ase"); }
  }
}`,
		wantStdout: "ase\n",
	},
	{
		// reading a generic field/return at a more specific type checkcasts (JLS 5.2)
		name: "GenericErasure",
		source: `class Box<T> { T v; Box(T t){ v = t; } T get(){ return v; } }
public class GenericErasure {
  public static void main(String[] a){
    Box<String> b = new Box<>("hello");
    System.out.println(b.v.length());
    System.out.println(b.get().length());
  }
}`,
		wantStdout: "5\n5\n",
	},
	{
		// bounded type parameters
		name: "Bounded",
		source: `class Sorter<T extends Comparable<T>> {
  T best(T a, T b) { return a.compareTo(b) >= 0 ? a : b; }
}
class Holder<T extends CharSequence> {
  T v;
  Holder(T t) { v = t; }
  int len() { return v.length(); }
}
public class Bounded {
  static <T extends Comparable<T>> T max(T a, T b) { return a.compareTo(b) >= 0 ? a : b; }
  public static void main(String[] x){
    System.out.println(max(3, 7));
    System.out.println(max("ab", "aa"));
    System.out.println(new Sorter<Integer>().best(5, 2));
    System.out.println(new Holder<>("abc").len());
  }
}`,
		wantStdout: "7\nab\n5\n3\n",
	},
	{
		// stack traces carry source line numbers (LineNumberTable)
		name: "TraceLines",
		source: `public class TraceLines {
  static void boom() {
    int x = 1;
    if (x > 0) {
      throw new RuntimeException("here");
    }
  }
  public static void main(String[] a) {
    try { boom(); } catch (RuntimeException e) {
      StackTraceElement top = e.getStackTrace()[0];
      System.out.println(top.getMethodName() + ":" + top.getLineNumber());
    }
  }
}`,
		wantStdout: "boom:5\n",
	},
	{
		// explicit this(...) forwards this$0 and captures like javac
		name: "Delegate",
		source: `public class Delegate {
  int base = 40;
  class Inner {
    int v;
    Inner() { this(2); }
    Inner(int x) { v = base + x; }
  }
  int run() { return new Inner().v; }
  public static void main(String[] args) {
    System.out.println(new Delegate().run());
    final int cap = 5;
    class L {
      int v;
      L() { this(10); }
      L(int x) { v = cap + x; }
    }
    System.out.println(new L().v);
  }
}`,
		wantStdout: "42\n15\n",
	},
	{
		// same-arity constructor overloads pick by argument type
		name: "CtorOverload",
		source: `public class CtorOverload {
  static class A {
    A(int x) { System.out.println("int"); }
    A(String s) { System.out.println("str"); }
  }
  static class B extends A {
    B() { super("s"); }
  }
  public static void main(String[] args) {
    new B();
    new A(1);
  }
}`,
		wantStdout: "str\nint\n",
	},
}

func TestEmitterRunsLikeJavac(t *testing.T) {
	if !hasTool("java") {
		t.Skip("no JDK (java)")
	}
	for _, tc := range runsLikeJavacCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, cls := range emitClasses(t, tc.main(), tc.source) {
				at := filepath.Join(dir, cls.Name+".class")
				if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(at, cls.Bytes, 0o644); err != nil {
					t.Fatalf("write %s: %v", cls.Name, err)
				}
			}
			out, err := exec.Command("java", "-cp", dir, tc.main()).Output()
			if err != nil {
				stderr := ""
				if ee, ok := err.(*exec.ExitError); ok {
					stderr = string(ee.Stderr)
				}
				t.Fatalf("java -cp %s %s: %v\n%s", dir, tc.main(), err, stderr)
			}
			if string(out) != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", out, tc.wantStdout)
			}
		})
	}
}
