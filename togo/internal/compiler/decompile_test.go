package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Port of src/compiler/decompile.test.ts (tiers 2 and 3). The text baselines are
// shared with the TS build and compared in internal/cli, where the formatter is
// reachable; here the reconstruction itself is under test.

// Every class our emitter produced whose methods this phase reconstructs in
// full - straight-line arithmetic, conversions, fields, arrays and casts.
var fullyDecompiled = []string{
	"AnnAll",
	"Arithmetic",
	"ArrayLoad",
	"ArrayStore",
	"BoundErasure",
	"Boxing",
	"CastInstance",
	"Cl",
	"ClassLit",
	"Compute",
	"Concat",
	"Constants",
	"ControlFlow",
	"Empty",
	"EnumAbstract$1",
	"EnumAbstract$2",
	"EnumMixed$1",
	"EnumMixed$2",
	"EnumUnqualified",
	"Fields",
	"FloatArith",
	"FloatConst",
	"FloatConv",
	"Fold",
	"Hello",
	"ICast",
	"ICast$A",
	"ICast$B",
	"ISA",
	"ISB",
	"ImplicitSealed",
	"IntConv",
	"IntLiterals",
	"Invoke",
	"Locals",
	"LongArith",
	"Methods",
	"ModifiedFields",
	"Nest",
	"Nest$Counter",
	"Nest$Point",
	"NewArray",
	"PrivateCall",
	"Pt",
	"QualifiedAnon$1",
	"QualifiedAnon$Inner",
	"QualifiedNew",
	"QualifiedNew$Inner",
	"ReturnLiterals",
	"Returns",
	"Rt",
	"Sealed",
	"SealedI",
	"StaticFields",
	"SubA",
	"SubB",
	"SubC",
	"Switches",
	"VarargsAndAbstract",
	"VarargsPack",
}

// Classes kept for the bail-out rendering: an anonymous class and the members
// javac generates for an enum are not this phase's job, and must say so.
var notDecompiled = []string{"EnumAbstract", "EnumMixed", "QualifiedAnon"}

// `ClassLit.prim()` reads `java.lang.Integer.TYPE`, which javac accepts and the
// decompiler gets right, but our JDK stub does not declare - so re-emitting it
// degrades to aconst_null and checking it reports an unresolved symbol. Both
// oracles are only as good as the stub, so that class is held to its text
// baseline alone.
var stubGap = map[string]bool{"ClassLit": true}

// `Nest$Counter.tick()` reconstructs `this.n = this.n + 1` exactly, but our
// emitter writes it as `aload_0; aload_0; getfield` where javac used
// `aload_0; dup; getfield` - the same statement, a different codegen strategy,
// which this instruction-identical oracle cannot express.
// The class javac writes for an enum constant with a body is not expressible as
// source at all - `class X$1 extends X` where X is an enum is exactly what Java
// forbids anyone to write - so nothing can re-emit it.
// Reconstructions this oracle cannot judge, for reasons of its own:
//   - `ICast` names two nested types that live outside the one class the
//     decompiler writes, so re-emitting the file alone cannot resolve them;
//   - `BoundErasure` declares `T get()`, and the decompiler works off the
//     descriptor, so what comes back is the erased `CharSequence get()` - a
//     different member, not different code;
//   - `EnumUnqualified` declares a local inside a loop, which is hoisted to the
//     top of the method and shifts every slot after it;
//   - `Boxing` calls `Integer.intValue()`, and our emitter writes the
//     *declaring* class into the method ref (`Number.intValue`) where javac
//     writes the receiver's static type. That is an emitter bug, not a
//     decompiler one.
//   - `QualifiedAnon$Inner` and `QualifiedNew$Inner` fail for two reasons at
//     once: the constructor assigns the synthetic enclosing field *before* the
//     `super()`, an order Java source cannot express, so written back it
//     follows the implicit `super()` and the bytes differ; and `get()`/`sum()`
//     read a field of the enclosing class, which cannot be resolved when the
//     file is emitted alone, so they degrade to a constant the way `ICast`
//     does;
//   - `QualifiedNew` and `QualifiedAnon$1` write `outer.new Inner(5)` and
//     `outer.super(v)`, whose enclosing instance and null check our emitter
//     does not pass (it compiles the inner class without one).
var noRoundtrip = map[string]bool{
	"ClassLit": true, "Nest$Counter": true,
	"EnumAbstract$1": true, "EnumAbstract$2": true, "EnumMixed$1": true, "EnumMixed$2": true,
	"ICast": true, "BoundErasure": true, "EnumUnqualified": true, "Boxing": true,
	"QualifiedAnon$Inner": true, "QualifiedNew$Inner": true,
	"QualifiedNew": true, "QualifiedAnon$1": true,
}

func decompileBaseline(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(emitBaselinesDir, name+".class"))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	source, err := Decompile(b)
	if err != nil {
		t.Fatalf("decompile %s: %v", name, err)
	}
	return source
}

func TestDecompileReportsWhatItCannotReconstruct(t *testing.T) {
	// The marker is the comment, not the `throw`: a bailed-out static initializer
	// has no value to return, so it renders as the disassembly alone.
	for _, name := range fullyDecompiled {
		if strings.Contains(decompileBaseline(t, name), "/* cappu:") {
			t.Errorf("%s: expected a full reconstruction", name)
		}
	}
	for _, name := range notDecompiled {
		if !strings.Contains(decompileBaseline(t, name), "/* cappu:") {
			t.Errorf("%s: expected the bail-out body", name)
		}
	}
}

// The roundtrip: re-emit the decompiled source and require the same normalized
// instruction stream, which proves the output is valid Java that means what the
// input meant. Type arguments are stripped from the member signature - the
// decompiler works off descriptors, so the re-emitted class has erased generics
// by design.
func TestDecompileRecompilesToTheSameBytecode(t *testing.T) {
	for _, name := range fullyDecompiled {
		if noRoundtrip[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			original, err := os.ReadFile(filepath.Join(emitBaselinesDir, name+".class"))
			if err != nil {
				t.Fatalf("read baseline: %v", err)
			}
			reEmitted := emitClassBytes(t, name, decompileBaseline(t, name))
			want := instructionStreams(t, original, name)
			got := instructionStreams(t, reEmitted, name)
			for member, instructions := range want {
				if strings.Join(got[member], "\n") != strings.Join(instructions, "\n") {
					t.Errorf("%s %s:\n got %q\nwant %q", name, member, got[member], instructions)
				}
			}
		})
	}
}

func instructionStreams(t *testing.T, b []byte, name string) map[string][]string {
	t.Helper()
	text, err := Disassemble(b)
	if err != nil {
		t.Fatalf("disassemble %s: %v", name, err)
	}
	disasm := ParseJavapText(text)[name]
	if disasm == nil {
		t.Fatalf("no disassembly for %s", name)
	}
	out := map[string][]string{}
	for _, entry := range disasm.Code {
		out[eraseTypeArguments(entry.Signature)] = entry.Instructions
	}
	return out
}

// `java.lang.Class<?> ref();` -> `java.lang.Class ref();`
func eraseTypeArguments(member string) string {
	for {
		open := strings.IndexByte(member, '<')
		if open < 0 {
			return member
		}
		close := strings.IndexByte(member[open:], '>')
		if close < 0 {
			return member
		}
		member = member[:open] + member[open+close+1:]
	}
}

const debuggySource = "class Debuggy { int f(int seed) { int doubled = seed * 2; return doubled; } }"

func TestDecompileUsesLocalVariableTableNames(t *testing.T) {
	source, err := Decompile(emitClassBytes(t, "Debuggy", debuggySource))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{"int f(int seed)", "int doubled = seed * 2;"} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
}

func TestDecompileFallsBackToSlotNames(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Debuggy", debuggySource))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{"int f(int arg0)", "int var2 = arg0 * 2;"} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
}

// emitClassBytes emits with a LocalVariableTable; this is the -g-less variant.
func emitClassBytesNoDebug(t *testing.T, name, source string) []byte {
	t.Helper()
	program := NewProgram()
	LoadJdkStub(program)
	uri := URI("file:///" + name + ".java")
	program.SetOpenDocument(uri, source, 1)
	classes := EmitSourceFile(program.GetSourceFile(uri), program, NewChecker(program), false)
	for _, c := range classes {
		if strings.HasSuffix(c.Name, name) {
			return c.Bytes
		}
	}
	t.Fatalf("class %s was not emitted", name)
	return nil
}

// --- the output has to be valid Java --------------------------------------------------

// diagnosticsOf parses and type-checks a reconstruction the way `cappu check` would.
func diagnosticsOf(name, source string) []string {
	program := NewProgram()
	LoadJdkStub(program)
	uri := URI("file:///" + name + ".java")
	program.SetOpenDocument(uri, source, 1)
	sourceFile := program.GetSourceFile(uri)
	var out []string
	for _, d := range sourceFile.AsSourceFile().ParseDiagnostics {
		out = append(out, "parse: "+d.MessageText)
	}
	for _, d := range NewChecker(program).GetSemanticDiagnostics(sourceFile) {
		out = append(out, "semantic: "+d.MessageText)
	}
	return out
}

func TestDecompileOutputTypeChecks(t *testing.T) {
	for _, name := range append(append([]string{}, fullyDecompiled...), notDecompiled...) {
		if stubGap[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if found := diagnosticsOf(name, decompileBaseline(t, name)); len(found) > 0 {
				t.Errorf("%s: %v", name, found)
			}
		})
	}
}

// javac (and our emitter) reuse a slot once a variable goes out of scope, so a
// slot is not a variable: the second one needs its own name and type, or the
// output does not compile.
const reusedSlotSource = "public class Reuse { static int f(int n) {" +
	" { int a = n + 1; n = a; } { long b = n * 2L; n = (int) b; } return n; } }"

func TestDecompileDeclaresASecondVariableForAReusedSlot(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Reuse", reusedSlotSource))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{"int var1 = arg0 + 1;", "long var1_2 = (long) arg0 * 2L;"} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
	// Reusing the name would assign a long to an int; the checker says so.
	if found := diagnosticsOf("Reuse", source); len(found) > 0 {
		t.Errorf("reconstruction does not type-check: %v\n%s", found, source)
	}
}

func TestDecompileKeepsBothDebugNamesForAReusedSlot(t *testing.T) {
	source, err := Decompile(emitClassBytes(t, "Reuse", reusedSlotSource))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{"int a = n + 1;", "long b = (long) n * 2L;"} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
}

// --- constants javac inlines and javap prints unsourceably --------------------------

// Only javac produces these: NaN and the infinities reach the constant pool
// because `Float.NaN` and friends are constant variables, and our own emitter
// does not fold float division. javap prints them as `NaNf`/`Infinity`, which is
// not Java - the wrapper constants are.
const nonFiniteSource = `public class NonFinite {
  static float nan() { return Float.NaN; }
  static float inf() { return Float.POSITIVE_INFINITY; }
  static double negInf() { return Double.NEGATIVE_INFINITY; }
  static double dnan() { return Double.NaN; }
}`

func TestDecompileRendersNonFiniteConstants(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "NonFinite", nonFiniteSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, constant := range []string{
		"java.lang.Float.NaN", "java.lang.Float.POSITIVE_INFINITY",
		"java.lang.Double.NEGATIVE_INFINITY", "java.lang.Double.NaN",
	} {
		if !strings.Contains(source, constant) {
			t.Errorf("missing %q in:\n%s", constant, source)
		}
	}
	// Not checked with diagnosticsOf: our JDK stub declares no fields on Float
	// and Double, so our own checker calls these unresolved (the same gap that
	// exempts ClassLit above). javac is the oracle here instead - it inlines
	// them right back, so the bytecode has to come out identical.
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "NonFinite", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s", javapText(t, roundTripped))
	}
}

func compileWithJavac(t *testing.T, dir, name, source string) string {
	t.Helper()
	return compileWithJavacOn(t, dir, name, source, "")
}

func compileWithJavacOn(t *testing.T, dir, name, source, classPath string) string {
	t.Helper()
	return compileWithJavacFlags(t, dir, name, source, classPath, false)
}

// compileWithJavacFlags compiles with `-g` (a LocalVariableTable) when debug is set.
func compileWithJavacFlags(t *testing.T, dir, name, source, classPath string, debug bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	javaFile := filepath.Join(dir, name+".java")
	if err := os.WriteFile(javaFile, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	args := []string{"--release", "21"}
	if debug {
		args = append(args, "-g")
	}
	args = append(args, "-d", dir)
	if classPath != "" {
		args = append(args, "-cp", classPath)
	}
	if out, err := exec.Command("javac", append(args, javaFile)...).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, out)
	}
	return filepath.Join(dir, name+".class")
}

func javapText(t *testing.T, classFile string) string {
	t.Helper()
	out, err := exec.Command("javap", "-c", "-p", classFile).Output()
	if err != nil {
		t.Fatalf("javap: %v", err)
	}
	return string(out)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// --- shapes the reconstruction has to get right ---------------------------------------

// Each case is emitted by our own emitter, decompiled, and held to the text it
// has to produce; selfContained cases are type-checked on top (the others
// reference a class that lives in another file).
var reconstructions = []struct {
	name          string
	source        string
	want          []string
	reject        []string
	selfContained bool
}{
	{
		name:   "Neg",
		source: "class Neg { static int f(int a) { return -(-a); } }",
		// `--arg0` would decrement it
		want:          []string{"return -(-arg0);"},
		selfContained: true,
	},
	{
		name: "Jag",
		source: "class Jag { static java.lang.String[][] f(int n) " +
			"{ return new java.lang.String[n][]; } }",
		want:          []string{"new java.lang.String[arg0][]"},
		selfContained: true,
	},
	{
		// A no-arg constructor is only javac's when it is the only one.
		name:          "Ctors",
		source:        "class Ctors { int v; Ctors() { this.v = 1; } Ctors(int x) { this.v = x; } }",
		want:          []string{"Ctors() {", "Ctors(int arg0) {"},
		selfContained: true,
	},
	{
		name:   "Single",
		source: "class Single { private Single() {} }",
		// dropping it would make the class instantiable
		want:          []string{"private Single() {"},
		selfContained: true,
	},
	{
		// Only the use says these are not ints: the store opcode is the same.
		name: "Erased",
		source: "class Erased { static boolean b() { boolean v = true; return v; }" +
			" static char c() { char v = 'a'; return v; } }",
		want:          []string{"boolean var0 = true;", "char var0 = 'a';"},
		selfContained: true,
	},
	// --- phase 1.8: array initializers ---
	{
		// The `dup; index; value; store` chain is one literal, not three statements.
		name:          "ArrLit",
		source:        "class ArrLit { static int[] f() { return new int[]{1, 2}; } }",
		want:          []string{"return new int[]{1, 2};"},
		reject:        []string{"new int[2]"},
		selfContained: true,
	},
	{
		// No `dup`, so this stays the sized form with the write as a statement.
		name:          "ArrSized",
		source:        "class ArrSized { static int[] f() { int[] a = new int[2]; a[1] = 3; return a; } }",
		want:          []string{"int[] var0 = new int[2];", "var0[1] = 3;"},
		selfContained: true,
	},
	{
		name:          "ArrNest",
		source:        "class ArrNest { static int[][] f() { return new int[][]{{1}, {2}}; } }",
		want:          []string{"return new int[][]{new int[]{1}, new int[]{2}};"},
		selfContained: true,
	},
	// --- phase 1.4: acyclic control flow ---
	{
		name:          "IfOnly",
		source:        "class IfOnly { static int f(int a) { int r = 0; if (a > 0) { r = a; } return r; } }",
		want:          []string{"int var1 = 0;", "if (arg0 > 0) {", "var1 = arg0;"},
		selfContained: true,
	},
	{
		// The arm that leaves the method is the whole `if`; the rest follows it.
		name:          "IfElse",
		source:        "class IfElse { static int f(int a) { if (a > 0) return 1; else return 2; } }",
		want:          []string{"if (arg0 > 0) {", "return 1;", "}", "return 2;"},
		reject:        []string{"} else {"},
		selfContained: true,
	},
	{
		// Java scopes a variable to the branch it is declared in, the bytecode
		// does not: assigned in both arms, it has to be declared before the `if`.
		name: "Hoist",
		source: "class Hoist { static int f(boolean c) { int x; if (c) { x = 1; }" +
			" else { x = 2; } return x; } }",
		want:          []string{"int var1;", "if (arg0) {", "var1 = 1;", "} else {", "var1 = 2;"},
		selfContained: true,
	},
	{
		name: "Short",
		source: "class Short { static boolean f(int a, int b) { return a > 0 && b < 10; }" +
			" static boolean g(int a, int b) { return a > 0 || b < 10; } }",
		want:          []string{"return arg0 > 0 && arg1 < 10;", "return arg0 > 0 || arg1 < 10;"},
		selfContained: true,
	},
	{
		// Nested short-circuits share the block the value comes from, so the
		// parenthesization is the only thing that says which grouping was written.
		name: "Mixed",
		source: "class Mixed { static boolean f(int a, int b, int c) { return (a > 0 && b > 0) || c > 0; }" +
			" static boolean g(int a, int b, int c) { return a > 0 && (b > 0 || c > 0); } }",
		want: []string{
			"return arg0 > 0 && arg1 > 0 || arg2 > 0;",
			"return arg0 > 0 && (arg1 > 0 || arg2 > 0);",
		},
		selfContained: true,
	},
	{
		name: "Tern",
		source: "class Tern { static int f(int a, int b) { return (a > b ? a : b) + 1; }" +
			" static int g(boolean c) { int[] xs = new int[3]; xs[c ? 0 : 1] = 7; return xs[0]; } }",
		// The condition is a boolean, so an index has to ask for the int back -
		// and the int form keeps the branch's own arms, not the boolean reading.
		want:          []string{"return (arg0 > arg1 ? arg0 : arg1) + 1;", "var1[arg0 ? 0 : 1] = 7;"},
		selfContained: true,
	},
	{
		// `istore` is what an int uses, so a materialized condition starts as one -
		// and a use that needs a boolean (`return b`) is what narrows it. With no
		// such use it stays an int, which still compiles and still branches.
		name: "BoolVar",
		source: "class BoolVar { static boolean f(int a) { boolean b = a > 10;" +
			" if (b) { return b; } return false; }" +
			" static int g(int a) { boolean b = a > 10; if (b) { return 1; } return 0; }" +
			" static int h(int a) { int x = a > 0 ? 1 : 0; return x + 1; } }",
		want: []string{
			"boolean var1 = arg0 > 10;",
			"if (var1) {",
			// Our emitter branches on the true arm, so the int form reads
			// inverted - the same value, written the way this branch is laid out.
			"int var1 = arg0 <= 10 ? 0 : 1;",
			"if (var1 != 0) {",
			"int var1 = arg0 > 0 ? 1 : 0;",
			"return var1 + 1;",
		},
		selfContained: true,
	},
	{
		// A materialized condition in a position that wants a number: arithmetic
		// and an array index splice the text in as it stands, so it has to be the
		// ternary again rather than the boolean it reads as elsewhere.
		name: "AsNumber",
		source: "class AsNumber { static int f(boolean q, int[] xs) { return xs[q ? 2 : 0] + (q ? 1 : 0); }" +
			" static int g(int a) { return -(a > 0 ? 1 : 0); }" +
			" static long h(int a) { return (long) (a > 0 ? 1 : 0); } }",
		want: []string{
			"arg0 ? 2 : 0", "+ (arg0 ? 1 : 0)",
			"-(arg0 > 0 ? 1 : 0)", "(long) (arg0 > 0 ? 1 : 0)",
		},
		selfContained: true,
	},
	{
		// lcmp/dcmpg have no source form: the comparison they feed is what was written.
		name: "Cmp",
		source: "class Cmp { static boolean f(long a, long b) { return a < b; }" +
			" static boolean g(double a, double b) { return a >= b; } }",
		want:          []string{"return arg0 < arg1;", "return arg0 >= arg1;"},
		selfContained: true,
	},
	{
		name: "Throwing",
		source: "class Throwing { static int f(int a, java.lang.RuntimeException e) {" +
			" if (a < 0) throw e; return a; } }",
		want:          []string{"if (arg0 < 0) {", "throw arg1;"},
		selfContained: true,
	},
	{
		// Only the *use* says the slot is a boolean, and the use comes after the
		// branch the assignments sit in - so the rewrite has to reach into it.
		name: "Retype",
		source: "class Retype { static boolean f(int a) { boolean b; if (a > 0) { b = true; }" +
			" else { b = false; } return b; } }",
		want:          []string{"boolean var1;", "var1 = true;", "var1 = false;"},
		reject:        []string{"var1 = 1;", "var1 = 0;"},
		selfContained: true,
	},
	{
		name: "SwitchLabeled",
		// A `switch` catches an unlabeled `break`, so one that leaves the loop
		// around it needs a label, which names the loop.
		source: "class SwitchLabeled { static int f(int n, int x) { int r = 0;" +
			" outer: while (r < n) { switch (x) { case 1: r += 1; break; case 2: break outer;" +
			" default: r += 3; } r += 1; } return r; } }",
		want:          []string{"label1: while (var2 < arg0) {", "break label1;"},
		selfContained: true,
	},
	{
		name: "SwitchDefaultPad",
		// The gaps a tableswitch pads with its default target say nothing a
		// `default:` does not, so they are not written back as cases.
		source: "class SwitchDefaultPad { static int f(int x) { int r = 0;" +
			" switch (x) { case 1: r = 1; break; case 4: r = 4; break; default: r = 9; }" +
			" return r; } }",
		want:          []string{"case 1:", "case 4:", "default:"},
		reject:        []string{"case 2:", "case 3:", "not decompiled"},
		selfContained: true,
	},
	{
		// A `for` is a `while` whose update sits at the bottom of the body - the
		// same bytecode, so that is what it comes back as.
		name: "Loop",
		source: "class Loop { static int f(int n) { int s = 0; for (int i = 0; i < n; i++) s += i;" +
			" return s; } }",
		want:          []string{"while (var2 < arg0) {", "var1 = var1 + var2;", "var2++;"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		name: "WhileLoop",
		source: "class WhileLoop { static int f(int n) { int c = 0; while (n > 0) { c++; n--; }" +
			" return c; } }",
		want:          []string{"while (arg0 > 0) {"},
		reject:        []string{"while (true)", "not decompiled"},
		selfContained: true,
	},
	{
		// The test is at the foot, so the body runs before it is asked.
		name: "DoLoop",
		source: "class DoLoop { static int f(int n) { int i = 0; do { i += 3; } while (i < n);" +
			" return i; } }",
		want:          []string{"do {", "var1 = var1 + 3;", "} while (var1 < arg0);"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		name: "Forever",
		source: "class Forever { static int f(int n) { int i = 0; while (true) { i += 2;" +
			" if (i > n) { break; } i += n; } return i; } }",
		want:          []string{"while (true) {", "break;"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		name: "BreakOut",
		source: "class BreakOut { static int f(int[] xs, int stop) { int t = 0;" +
			" for (int i = 0; i < xs.length; i++) { if (xs[i] == stop) { break; } t += xs[i]; }" +
			" return t; } }",
		want:          []string{"while (var3 < arg0.length) {", "break;"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		name: "Nested",
		source: "class Nested { static int f(int n, int m) { int t = 0;" +
			" for (int i = 0; i < n; i++) { for (int j = 0; j < m; j++) { t += i * j; } }" +
			" return t; } }",
		want:          []string{"while (var3 < arg0) {", "while (var4 < arg1) {"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		// Both tests belong to the loop's own condition, not to an `if` inside it.
		name: "LoopAnd",
		source: "class LoopAnd { static int f(int a, int b) { int t = 0;" +
			" while (a > 0 && b > 0) { t++; a--; b--; } return t; } }",
		want:          []string{"while (arg0 > 0 && arg1 > 0) {"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		name: "LoopContinue",
		source: "class LoopContinue { static int f(int[] xs) { int t = 0; int i = 0;" +
			" while (i < xs.length) { int v = xs[i]; i++; if (v < 0) { continue; } t += v; }" +
			" return t; } }",
		want:          []string{"while (var2 < arg0.length) {"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		name: "Catching",
		source: "class Catching { static int f(int[] xs, int i) { try { return xs[i]; }" +
			" catch (java.lang.RuntimeException e) { return -1; } } }",
		want: []string{"try {", "return arg0[arg1];",
			"} catch (java.lang.RuntimeException e) {", "return -1;"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		// One clause per handler, one handler per `catch`: the two types of a
		// multi-catch share theirs, the two clauses of a chain do not.
		name: "MultiCatch",
		source: "class MultiCatch { static int f(int[] xs, int i) { try { return xs[i] / i; }" +
			" catch (java.lang.ArithmeticException | java.lang.NullPointerException e) { return 0; } }" +
			" static int g(int[] xs) { int r = 0; try { r = xs[0]; }" +
			" catch (java.lang.RuntimeException e) { r = 1; } catch (java.lang.Error e2) { r = 2; }" +
			" return r; } }",
		want: []string{
			"} catch (java.lang.ArithmeticException | java.lang.NullPointerException e) {",
			"} catch (java.lang.RuntimeException e) {",
			"} catch (java.lang.Error e_2) {",
		},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		// The `try` sits inside the loop, and what leaves the loop from a
		// handler is a `break` - not the statement's own end.
		name: "CatchBreak",
		source: "class CatchBreak { static int f(int[] xs) { int s = 0; int i = 0;" +
			" while (i < xs.length) { try { s += xs[i]; }" +
			" catch (java.lang.RuntimeException e) { break; } i++; } return s; } }",
		want:          []string{"while (var2 < arg0.length) {", "try {", "break;", "var2++;"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		name: "CatchEmpty",
		source: "class CatchEmpty { static void f(int a) { try { java.lang.System.out.println(a); }" +
			" catch (java.lang.RuntimeException e) { } } }",
		want:   []string{"} catch (java.lang.RuntimeException e) {"},
		reject: []string{"not decompiled"},
		// Not checked: an empty `catch` is what the source had, and our own
		// checker flags it as a swallowed exception.
	},
	{
		// A loop inside a handler: a handler is only reachable by throwing, so
		// the loop analysis has to see the throwing edges or it never finds this
		// one.
		name: "CatchLoop",
		source: "class CatchLoop { static int f(int[] xs) { int s = 0; try { s = xs[0]; }" +
			" catch (java.lang.RuntimeException e) { for (int k = 0; k < 3; k++) { s += k; } }" +
			" return s; } }",
		want:          []string{"} catch (java.lang.RuntimeException e) {", "while (var3 < 3) {"},
		reject:        []string{"not decompiled"},
		selfContained: true,
	},
	{
		// The catch parameter's scope is its clause: javac hands the slot to the
		// next variable, and with no debug table only the scope says so.
		name: "CatchSlot",
		source: "class CatchSlot { static int f(int a) { int r = 0; try { r = 100 / a; }" +
			" catch (java.lang.ArithmeticException e) { r = -1; } int q = r * 2; return q; } }",
		want:          []string{"} catch (java.lang.ArithmeticException e) {", "int var2 = var1 * 2;"},
		reject:        []string{"not decompiled", "e = "},
		selfContained: true,
	},
	{
		// The `finally` is copied into every exit path and the rest is guarded
		// by a catch-all that rethrows. Reading the copy back needs javac's
		// layout, where the copy sits right after the protected range; this
		// emitter lays the same method out differently, so it says so instead.
		name: "Finally",
		source: "class Finally { static int f(int a) { try { return a; }" +
			" finally { java.lang.System.out.println(a); } } }",
		want: []string{"cappu: a finally with more than one way out"},
	},
	{
		name: "Blank",
		source: "class Blank { static int v() { return 1; } static final int N;" +
			" static { N = v(); } }",
		// A blank `static final` is only assignable in the initializer, so the
		// initializer has to come back for the `final` to stand.
		want:          []string{"static final int N;", "N = v();"},
		reject:        []string{"UnsupportedOperationException"},
		selfContained: true,
	},
}

func TestDecompileReconstructions(t *testing.T) {
	for _, c := range reconstructions {
		t.Run(c.name, func(t *testing.T) {
			source, err := Decompile(emitClassBytesNoDebug(t, c.name, c.source))
			if err != nil {
				t.Fatalf("decompile: %v", err)
			}
			for _, want := range c.want {
				if !strings.Contains(source, want) {
					t.Errorf("missing %q in:\n%s", want, source)
				}
			}
			for _, reject := range c.reject {
				if strings.Contains(source, reject) {
					t.Errorf("unexpected %q in:\n%s", reject, source)
				}
			}
			if c.selfContained {
				if found := diagnosticsOf(c.name, source); len(found) > 0 {
					t.Errorf("does not type-check: %v\n%s", found, source)
				}
			}
		})
	}
}

// `i++` in a concatenation reads the variable before the increment and again
// after it: javac pushes the old value and increments behind it, so the value on
// top of the stack is the one the `++` belongs to.
func TestDecompileWritesAnIncrementBehindAValueOnTheStack(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Inc",
		`public class Inc { static String f(int i) { return "x" + i++ + i; } }`))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, `return "x" + arg0++ + arg0;`) {
		t.Errorf("expected the post-increment:\n%s", source)
	}
}

// A condition javac materialized as `1`/`0` reads as the condition itself, so
// every place that wants a *number* has to ask for the ternary back. A switch
// selector is one of them: `switch (flag)` is not Java.
// `|`, `&` and `^` take a boolean, so a materialized one on either side used to
// make the whole operation a boolean - `c & x` for `(c ? 1 : 0) & x`, which is
// not Java. Only an operand that is itself `1`/`0` or a boolean makes it one;
// where every operand is a boolean javac erased, the result carries the int
// form too, since `(a > b) ^ true` and `((a > b) ? 1 : 0) ^ 1` are the same
// bytecode and only the consumer knows. `==` against a boolean is the same
// story, and a conditional over two erased arms keeps its int form as well.
const bitwiseSource = `public class Bitwise {
  static int[] arr = {7, 8};
  static int and(boolean c, int x) { return (c ? 1 : 0) & x; }
  static int or(boolean c) { return (c ? 1 : 0) | 2; }
  static int xor(boolean c) { return (c ? 1 : 0) ^ 1; }
  static int not(boolean c) { return ~(c ? 1 : 0); }
  static int both(boolean c, boolean k) { return (c ? 1 : 0) & (k ? 1 : 0); }
  static boolean asBool(int a, int b) { return (a > b) ^ true; }
  static boolean eq(boolean c) { return c == (arr[0] > 5); }
  static int nested(boolean k, boolean c) { int r = k ? (c ? 1 : 0) : (c ? 0 : 1); return r; }
  static boolean proven(int a, int b, boolean f) { boolean x = (a > b) ^ true; return x ^ f; }
  static boolean provenEq(int a, int b, boolean f) { boolean x = (a > b) ^ (b > a); return x == f; }
  static int shortCircuit(boolean f, int a, int b) { return arr[f ? (a > b ? 1 : 0) : 0]; }
}`

const bitwiseDriverSource = `public class BitwiseDriver {
  public static void main(String[] args) {
    for (boolean c : new boolean[] { true, false })
      System.out.println(Bitwise.and(c, 3) + " " + Bitwise.or(c) + " " + Bitwise.xor(c)
        + " " + Bitwise.not(c) + " " + Bitwise.both(c, !c) + " " + Bitwise.asBool(2, 1)
        + " " + Bitwise.eq(c) + " " + Bitwise.nested(c, !c)
        + " " + Bitwise.proven(2, 1, c) + " " + Bitwise.provenEq(1, 2, c)
        + " " + Bitwise.shortCircuit(c, 2, 1));
  }
}`

func TestDecompileWritesAMaterializedBooleanAsANumberInABitwiseOperation(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Bitwise", bitwiseSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	for _, want := range []string{
		"(arg0 ? 1 : 0) & arg1", "(arg0 ? 1 : 0) | 2",
		// The same operation as a boolean, where the consumer says so.
		"arg0 > arg1 ^ true", "arg0 == arr[0] > 5",
		// An int-typed local against a real boolean: Java has no `int ^ boolean`,
		// so the use proves the local a boolean.
		"boolean var3 = arg0 > arg1 ^ true;", "var3 == arg2",
		// A short-circuit whose arms are both erased keeps its int form.
		"arr[arg0 ? arg1 > arg2 ? 1 : 0 : 0]",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Bitwise", source)
	compileWithJavacOn(t, dir, "BitwiseDriver", bitwiseDriverSource, dir)
	expected := runJava(t, dir, "BitwiseDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "BitwiseDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// A lambda has no type of its own, so where it is stored *as a value* the type
// has to come from the variable it is assigned to. That crosses the assignment
// path with the lambda one, and neither fixture covered the pair. cappu's own
// emitter cannot build this source, so the class has to come from javac.
func TestDecompileWritesALambdaStoredAsAValue(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "LamValue", `public class LamValue { static Object obj; static int log;
  static void assignLambda() { Runnable r; obj = (r = () -> { log += 5; }); r.run(); }
  static String methodRef() {
    java.util.function.Supplier<String> s; obj = (s = LamValue::name);
    return s.get(); }
  static String name() { return "abcd"; }
}`)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	// The target type is the variable's, so neither needs the interface named.
	for _, want := range []string{"obj = var0 = () ->", "obj = var0 = LamValue::name;"} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
}

func TestDecompileWritesAMaterializedBooleanWhereANumberIsWanted(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "AsInt",
		`public class AsInt { static int[] a = {10, 20};
  static int f(int v) { return v; }
  static int index(boolean c) { return a[c ? 1 : 0]; }
  static int length(boolean c) { return new int[c ? 1 : 0].length; }
  static int arith(boolean c) { return (c ? 1 : 0) + 5; }
  static int shift(boolean c) { return 1 << (c ? 1 : 0); }
  static int argument(boolean c) { return f(c ? 1 : 0); }
  static int selector(boolean c) {
    switch (c ? 1 : 0) { case 0: return 100; default: return 200; } }
}`))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	for _, want := range []string{
		// The selector is the one this missed: it used to write `switch (arg0)`.
		"switch (arg0 ? 1 : 0) {",
		"a[arg0 ? 1 : 0]",
		"new int[arg0 ? 1 : 0]",
		"(arg0 ? 1 : 0) + 5",
		"1 << (arg0 ? 1 : 0)",
		"f(arg0 ? 1 : 0)",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
}

// javac copies a value with `dup` and stores the copy where source wrote an
// assignment as a value: `while ((line = read()) != null)`. The store is the
// expression, in the place the value was, so nothing is written twice.
const assignySource = `import java.util.*;
public class Assigny {
  static int log;
  static String poll(Deque<String> q) { log++; return q.poll(); }
  static String whileAssign(Deque<String> q) {
    String s = "", line; while ((line = poll(q)) != null) { s += line; } return s; }
  static int ifAssign(Deque<String> q) {
    String line; if ((line = poll(q)) != null) { return line.length(); } return -1; }
  static int chain(Deque<String> q) {
    String a, b; a = b = poll(q);
    return (a == null ? 0 : a.length()) + (b == null ? 0 : 100); }
  static int arg(Deque<String> q) {
    String line; return len(line = poll(q)) + (line == null ? 7 : 0); }
  static int len(String s) { return s == null ? 0 : s.length(); }
  static int readsTarget() { int v = 7; return two(v, v = len("ab")) * 10 + v; }
  static int two(int a, int b) { return a * 100 + b; }
}`

// log counts the call, so a value copied instead of assigned prints twice.
const assignyDriverSource = `import java.util.*;
public class AssignyDriver {
  public static void main(String[] args) {
    for (int n = 0; n < 3; n++) {
      Deque<String> q = new ArrayDeque<>();
      for (int i = 0; i < n; i++) q.add("x" + i);
      Assigny.log = 0;
      System.out.println(Assigny.whileAssign(new ArrayDeque<>(q))
        + " " + Assigny.ifAssign(new ArrayDeque<>(q))
        + " " + Assigny.chain(new ArrayDeque<>(q))
        + " " + Assigny.arg(new ArrayDeque<>(q))
        + " " + Assigny.readsTarget() + " " + Assigny.log);
    }
  }
}`

func TestDecompileReconstructsAnAssignmentUsedAsAValue(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Assigny", assignySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	for _, want := range []string{"(var2 = poll(arg0)) != null", `two(var0, var0 = len("ab"))`} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Assigny", source)
	compileWithJavacOn(t, dir, "AssignyDriver", assignyDriverSource, dir)
	expected := runJava(t, dir, "AssignyDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "AssignyDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// Written as a value, an assignment's text is coerced to the type its variable
// has at that moment and can never be rewritten. Without a debug table an
// int-family variable's type is only inferred, and a later use that would
// narrow it to a boolean or a char has nothing to rewrite: it says so. The value
// form itself stays - a statement instead would run before what is already on
// the stack, and a copy would evaluate the value twice - and the type it hands
// on is the `int` it was erased to, not what the inner variable happened to be.
const untypedSource = `public class Untyped {
  static int counter;
  static int g() { throw new RuntimeException("boom"); }
  static void f(int a, int b) { counter += a + b; }
  static int[] arr() { counter++; return new int[counter]; }
  static int ordered() { int x = 0; try { f(g(), x = 5); } catch (RuntimeException e) {} return x; }
  static int once() { int x, y; x = y = arr().length; return x * 10 + y; }
  static String erased(String s) { char c; int i = (c = s.charAt(0)); return "" + c + i; }
  static char narrowed() { char c; int i = (c = 65); return c; }
  static int asBool(boolean k) { boolean b; if ((b = k) && counter > 0) return 1; return b ? 2 : 3; }
  static int boolChain(boolean k) { boolean a, b; a = b = k; return (a ? 1 : 0) + (b ? 10 : 0); }
  static int reused(boolean q) { { boolean b = q; f(b ? 1 : 0, 0); } { int y = 1; y++; f(y, 0); } return counter; }
  static int counterThenFlag(int n, boolean k) { int s = 0; for (int i = 1; i < n; i++) { s += i; } boolean d = k; f(d ? 1 : 0, s); return counter; }
  static boolean returned() { boolean b; return b = true; }
  static int toggles(int n, boolean k) { boolean w = k; int c = 0; for (int i = 0; i < n; i++) { if (w) c++; w = !w; } return c; }
  static int flag(int n, boolean k) { boolean rel = k; int c = 0; for (int i = 0; i < n; i++) { if (rel) c++; if (i == 1) rel = true; } return c; }
  static int partner(int n, boolean[] fl) { boolean a = fl[0]; boolean b = n == 2 || n == 4; if (b != a) return 1; return 0; }
  static int callThenFlag(int a, int b, boolean[] fl) { { int m = Math.min(a, b); f(m, 0); } boolean any = false; any = any | fl[0]; return any ? 1 : -1; }
  static String appended() { StringBuilder sb = new StringBuilder(); char c; sb.append(c = 'y'); return sb.toString(); }
}`

const untypedDriverSource = `public class UntypedDriver {
  public static void main(String[] args) {
    Untyped.counter = 0;
    System.out.println(Untyped.ordered() + " " + Untyped.once() + " " + Untyped.erased("z")
      + " " + Untyped.asBool(true) + Untyped.asBool(false) + " " + Untyped.boolChain(true)
      + " " + Untyped.counterThenFlag(4, true)
      + " " + Untyped.toggles(5, true) + Untyped.toggles(4, false) + " " + Untyped.flag(5, false)
      + " " + Untyped.partner(2, new boolean[] { true }) + Untyped.partner(3, new boolean[] { true })
      + " " + Untyped.counter);
  }
}`

func TestDecompileKeepsAnAssignmentAsAValueWithoutADebugTable(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Untyped", untypedSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		// The store must not move ahead of g(), which throws first.
		"f(g(), var0 = 5);",
		// arr() must run once, not once per variable.
		"int var0 = var1 = arr().length;",
		// The outer variable is an int, whatever c was inferred to be.
		"int var2 = var1 = arg0.charAt(0);",
		// `return c` would narrow c to a char after its assignment was written.
		"cappu: a retyped assignment used as a value",
		// A boolean is not erased: a Z-typed value proves the variable, and the
		// assignment is a boolean wherever it is used.
		"if ((var1 = arg0) && counter > 0)", "boolean var1 = var2 = arg0;",
		// A boolean reassigned in a loop is the same variable, and a variable
		// stored a boolean call result proves the int-typed one beside it.
		"var2 = !var2;", "var2 = true;", "if (var3 != var2)",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	// narrowed, returned and appended are the ones that say so: a use that would
	// narrow the assigned variable after the fact finds its text frozen.
	if strings.Count(source, "cappu: a retyped assignment used as a value") != 3 {
		t.Errorf("expected three frozen-text bails, got:\n%s", source)
	}
	// reused and callThenFlag are the reused slots.
	if strings.Count(source, "cappu: a variable used as both a number and a boolean") != 2 {
		t.Errorf("expected two reused-slot bails:\n%s", source)
	}
	if strings.Count(source, "cappu: ") != 10 {
		t.Errorf("expected five bailed methods, got:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Untyped", source)
	compileWithJavacOn(t, dir, "UntypedDriver", untypedDriverSource, dir)
	expected := runJava(t, dir, "UntypedDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "UntypedDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// A dead boolean's slot reused for an int, where the int's later use is not
// `++` but any other place a number belongs: an int argument, `return y + n`,
// an int field, a concatenation, a widening, or an operand of `|` beside a
// materialized boolean whose result an int method returns. javac never puts a
// boolean bare in any of those - it materializes one - so the value there is
// the int the slot's boolean type hid, and every one says so - as does one
// compared with anything but `0`/`1`, or ordered. `y == 0` alone reads the same
// either way and is reconstructed.
const reusedSource = `public class Reused {
  static int fld;
  static boolean g() { return true; }
  static void takeB(boolean b) {}
  static void takeI(int i) { fld += i; }
  static int arg(boolean q) { { boolean b = g(); takeB(b); } { int y = 1; takeI(y); } return fld; }
  static int ret(int n) { { boolean b = g(); takeB(b); } { int y = 1; return y + n; } }
  static int fieldStore() { { boolean b = g(); takeB(b); } { int y = 1; fld = y; } return fld; }
  static String concat() { { boolean b = g(); takeB(b); } { int y = 1; return "v" + y; } }
  static int eqZero() { { boolean b = g(); takeB(b); } { int y = 1; return y == 0 ? 5 : 6; } }
  static int eqFive() { { boolean b = g(); takeB(b); } { int y = 1; return y == 5 ? 5 : 6; } }
  static int eqParam(int p) { { boolean b = g(); takeB(b); } { int y = 1; return y == p ? 5 : 6; } }
  static int gtZero() { { boolean b = g(); takeB(b); } { int y = 1; return y > 0 ? 5 : 6; } }
  static int ltZero() { { boolean b = g(); takeB(b); } { int y = 1; if (y < 0) return 1; return 2; } }
  static int mat(int x) { { boolean b = g(); takeB(b); } { int y = x > 3 ? 1 : 0; takeI(y); } return fld; }
  static long widen() { { boolean b = g(); takeB(b); } { int y = 1; long l = y; return l; } }
  static int proven(boolean f) { { boolean b = g(); takeB(b); } { int y = 1; return y | (f ? 1 : 0); } }
}
`

func TestDecompileSaysSoWhereAReusedBooleanSlotsIntReachesANumber(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Reused", reusedSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	// Eleven bodies (the reason in the comment, then the throw).
	if strings.Count(source, "cappu: a variable used as both a number and a boolean") != 11 {
		t.Errorf("expected eleven reused-slot bails:\n%s", source)
	}
	if strings.Count(source, "cappu: ") != 22 {
		t.Errorf("expected eleven bailed methods, got:\n%s", source)
	}
	// The one that reads the same either way.
	if !strings.Contains(source, "return !var0 ? 5 : 6;") {
		t.Errorf("expected eqZero reconstructed:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Reused", source)
	if _, err := os.Stat(filepath.Join(again, "Reused.class")); err != nil {
		t.Fatalf("the decompiled class did not recompile: %v", err)
	}
	driver := `public class ReusedDriver {
  public static void main(String[] args) {
    System.out.println(Reused.eqZero());
  }
}`
	compileWithJavacOn(t, dir, "ReusedDriver", driver, dir)
	expected := runJava(t, dir, "ReusedDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "ReusedDriver")
	if actual != expected || actual != "6\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// With a debug table, a variable's slot is free once its range is over, and
// javac hands it to the next scope: `sUID` is declared after the first loop and
// takes its array copy's slot, so the second loop's unnamed index lands where
// the first loop's `boolean bit` was. The table has no row for the index, and the
// store is a boolean's slot no more - `b = 0; while (b < n)` was what came out
// of reading it as one.
const slotFreedSource = `public class SlotFreed {
  static String bits(boolean[] a, boolean[] b) {
    StringBuilder sb = new StringBuilder();
    boolean[] iUID = a;
    if (iUID != null) { for (boolean bit : iUID) { sb.append(bit ? 1 : 0); } }
    boolean[] sUID = b;
    if (sUID != null) { for (boolean bit : sUID) { sb.append(bit ? 1 : 0); } }
    return sb.toString();
  }
}
`

func TestDecompileStartsANewVariableWhereADebugTableRangeIsOver(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavacFlags(t, dir, "SlotFreed", slotFreedSource, "", true)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	for _, want := range []string{"boolean bit;", "boolean bit_2;"} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if regexp.MustCompile(`while \(bit(?:_2)? <`).MatchString(source) {
		t.Errorf("a boolean is the loop index:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "SlotFreed", source)
	if _, err := os.Stat(filepath.Join(again, "SlotFreed.class")); err != nil {
		t.Fatalf("the decompiled class did not recompile: %v", err)
	}
	driver := `public class SlotFreedDriver {
  public static void main(String[] args) {
    System.out.println(SlotFreed.bits(new boolean[] { true, false, true }, new boolean[] { false, true }));
  }
}`
	compileWithJavacOn(t, dir, "SlotFreedDriver", driver, dir)
	expected := runJava(t, dir, "SlotFreedDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "SlotFreedDriver")
	if actual != expected || actual != "10101\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// An inner class's constructor takes the enclosing instance first, and source
// passes it another way: implicitly, from a method of the enclosing class, or
// as the qualifier of `outer.new In(...)` / `outer.super(...)`. javac
// null-checks the qualifier (`dup; requireNonNull; pop`) and stores it before
// `super()`, neither of which source writes. The callers are top-level classes
// so each decompiled file recompiles on its own against the original classes.
const innerlySource = `public class Innerly {
  int f;
  Innerly(int f) { this.f = f; }
  class In { int g; In(int a) { g = a + f; } In() { this(0); } int plus() { return g + f; }
    In(String s) { this(new Object() {}.hashCode() & 0); }
    class Deep { int h() { return g * 10; } } }
  In own() { return new In(1); }
  static Runnable ref(Object x) { return x::notify; }
  static int rec() { record Q(int x) {} return new Q(3).x(); }
  static class SN { int v = 5; SN(Innerly o) { v += o.f; } }
  static SN sn(Innerly o) { return new SN(o); }
}
class InnerlySub extends Innerly.In {
  InnerlySub(Innerly o) { o.super(5); }
  InnerlySub(Innerly o, boolean c) { o.super(); if (c) { int t = g; System.out.println(t); } }
  InnerlySub(Innerly o, String s) { o.super(new Object() {}.hashCode() & 0); }
  int twice() { return g * 2; }
}
class InnerlyMk {
  static Innerly.In make(Innerly o) { return o.new In(2); }
  static Innerly.In.Deep deep(Innerly o) { return o.new In(3).new Deep(); }
  static int sum(Innerly o) {
    return make(o).g + deep(o).h() + new InnerlySub(o).twice() + o.own().plus(); }
}
`

func TestDecompilePassesAnInnerClassItsEnclosingInstance(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	orig := filepath.Join(dir, "orig")
	compileWithJavac(t, orig, "Innerly", innerlySource)
	sources := map[string]string{}
	for _, name := range []string{"Innerly", "Innerly$In", "Innerly$In$Deep", "InnerlySub", "InnerlyMk"} {
		source, err := Decompile(readFile(t, filepath.Join(orig, name+".class")))
		if err != nil {
			t.Fatalf("decompile %s: %v", name, err)
		}
		sources[name] = source
	}
	for _, want := range []struct{ name, text string }{
		{"Innerly", "return new Innerly.In(1);"},
		// A static nested class taking the outer type first: the InnerClasses
		// flag says so, and the argument stays one.
		{"Innerly", "return new Innerly.SN(arg0);"},
		// A bound method reference is written as a lambda, so the null check
		// javac put in front of it stays a statement.
		{"Innerly", "java.util.Objects.requireNonNull(arg0);"},
		// A local record is a local class: named for its method, not writable.
		{"Innerly", "cappu: a local class"},
		{"Innerly$In", "this.this$0 = arg0;"},
		// Its own constructors keep the parameter, so a `this(...)` passes it
		// on - the stub of one that gave up too.
		{"Innerly$In", "this(arg0, 0);"},
		{"Innerly$In", "this((Innerly) null, (int) 0);"},
		{"Innerly$In$Deep", "this.this$1 = arg0;"},
		{"InnerlySub", "arg0.super(5);"},
		// A qualified `super()` with no arguments is not the implicit one, and
		// the hoisted declaration follows it.
		{"InnerlySub", "arg0.super();\nint var3;"},
		// A chain call never reached is stubbed with its qualifier.
		{"InnerlySub", "((Innerly) null).super((int) 0);"},
		{"InnerlyMk", "return arg0.new In(2);"},
		{"InnerlyMk", "return arg0.new In(3).new Deep();"},
	} {
		if !strings.Contains(sources[want.name], want.text) {
			t.Errorf("%s: expected %q:\n%s", want.name, want.text, sources[want.name])
		}
	}
	if strings.Contains(sources["Innerly$In"], "requireNonNull") || strings.Contains(sources["InnerlyMk"], "/* cappu:") ||
		strings.Contains(sources["Innerly$In$Deep"], "/* cappu:") {
		t.Errorf("unexpected bail or null check:\n%s\n%s", sources["InnerlyMk"], sources["Innerly$In"])
	}
	// The nested files carry their binary names, which the enclosing file
	// cannot resolve on its own yet - and javac, seeing the original `Innerly`
	// name them in its InnerClasses, compiles a `class Innerly$In` as the inner
	// class itself, enclosing instance and all. The top-level callers recompile.
	again := filepath.Join(dir, "again")
	for _, name := range []string{"InnerlySub", "InnerlyMk"} {
		compileWithJavacOn(t, again, name, sources[name], orig)
		if _, err := os.Stat(filepath.Join(again, name+".class")); err != nil {
			t.Fatalf("%s did not recompile: %v", name, err)
		}
	}
	driver := `public class InnerlyDriver {
  public static void main(String[] args) {
    System.out.println(InnerlyMk.sum(new Innerly(7)) + " " + new InnerlySub(new Innerly(1), true).twice());
    try { Innerly.ref(null); System.out.println("no NPE"); } catch (NullPointerException e) { System.out.println("NPE"); }
  }
}`
	compileWithJavacOn(t, dir, "InnerlyDriver", driver, orig)
	sep := string(os.PathListSeparator)
	expected := runJava(t, orig+sep+dir, "InnerlyDriver")
	actual := runJava(t, again+sep+orig+sep+dir, "InnerlyDriver")
	// 9 + 100 + 24 + 15; the subclass prints its g first
	if actual != expected || actual != "1\n148 2\nNPE\n" {
		t.Errorf("the decompiled classes run differently: %q vs %q", actual, expected)
	}
}

// A condition is rendered against the types its locals have when it is
// written, and a local that is only inferred may turn out a boolean later. A
// condition stored into a variable is rewritten then, like one branched on -
// `w = !w` - but one written into anything else is text by then, and says so.
// A materialized condition is no constant, so it takes the cast an int would
// where a byte, short or char belongs.
const frozenSource = `public class Frozen {
  static int count;
  static void takeB(boolean b) {}
  static void takeByte(byte b) { count += b; }
  static boolean lv(int a) { boolean w = false; for (int i = 0; i < a; i++) { w = !w; } return w; }
  static boolean m(int a, int b) { boolean ok = a > b; count += ok ? 1 : 0; return ok; }
  static boolean arg(int a, int b) { boolean ok = a > b; takeB(ok == false); return ok; }
  static byte by(boolean flag) { byte b = flag ? (byte) 1 : (byte) 0; return b; }
  static short sh(boolean flag) { return flag ? (short) 1 : (short) 0; }
  static char ch(boolean flag) { return flag ? (char) 1 : (char) 0; }
  static void bya(boolean flag) { byte[] a = new byte[1]; a[0] = flag ? (byte) 1 : (byte) 0; takeByte(a[0]); }
}
`

func TestDecompileRewritesAStoredConditionOnARetype(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Frozen", frozenSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"boolean var1 = false;", "var1 = !var1;",
		"byte var1 = (byte) (arg0 ? 1 : 0);", "return (short) (arg0 ? 1 : 0);",
		"return (char) (arg0 ? 1 : 0);", "var1[0] = (byte) (arg0 ? 1 : 0);",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	// m and arg: the condition over ok went into an operand, an argument.
	if strings.Count(source, "cappu: a retyped variable in a condition already written") != 2 ||
		strings.Count(source, "cappu: ") != 4 {
		t.Errorf("expected two frozen-condition bails:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Frozen", source)
	if _, err := os.Stat(filepath.Join(again, "Frozen.class")); err != nil {
		t.Fatalf("the decompiled class did not recompile: %v", err)
	}
	driver := `public class FrozenDriver {
  public static void main(String[] args) {
    Frozen.bya(true);
    System.out.println(Frozen.lv(3) + " " + Frozen.lv(4) + " " + Frozen.by(true) + Frozen.sh(false)
      + (int) Frozen.ch(true) + " " + Frozen.count);
  }
}`
	compileWithJavacOn(t, dir, "FrozenDriver", driver, dir)
	expected := runJava(t, dir, "FrozenDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "FrozenDriver")
	if actual != expected || actual != "true false 101 1\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// An arm of a conditional expression may allocate - `r != null ? r : new
// SecureRandom()` - as it may call: the arm runs once, in its place. Only an
// arm two branches share (the merge of a `||`) has to be pure.
const ternewSource = `import java.security.SecureRandom;
public class Ternew {
  static int calls;
  static SecureRandom mk() { calls++; return new SecureRandom(); }
  static byte[] nonce(SecureRandom r) { byte[] n = new byte[12]; SecureRandom rng = (r != null) ? r : new SecureRandom(); rng.nextBytes(n); return n; }
  static Object pick(boolean c, Object o) { return c ? o : new int[3]; }
  static int len(boolean c) { return (c ? new int[2] : new int[5]).length; }
  static Object call(boolean c) { return c ? mk() : new Object(); }
  static boolean cond() { return new Object().hashCode() != 0 && calls >= 0; }
  static RuntimeException nul(boolean c) { RuntimeException e = c ? null : new IllegalStateException("x"); return e == null ? new IllegalArgumentException("y") : e; }
  static Exception ret(boolean c, java.io.IOException io) { return c ? new IllegalStateException("z") : io; }
  static Exception store(boolean c, java.io.IOException io) { Exception e = c ? new IllegalStateException("z") : io; return e; }
  Object impl; int[] cache = new int[4];
  Object compute() { calls++; return new Object(); }
  Object impl() { Object i = impl; return i != null ? i : (impl = compute()); }
  int cached(int k) { int v = cache[k]; return v != 0 ? v : (cache[k] = k * 7); }
  static int loc(boolean c, int x) { int r; return c ? (r = x + 1) : (r = 2); }
}
`

func TestDecompileAllocatesInATernaryArm(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Ternew", ternewSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"java.security.SecureRandom var2 = arg0 != null ? arg0 : new java.security.SecureRandom();",
		"return arg0 ? arg1 : new int[3];",
		"return (arg0 ? new int[2] : new int[5]).length;",
		"return arg0 ? mk() : new java.lang.Object();",
		"return new java.lang.Object().hashCode() != 0 && calls >= 0;",
		// A `null` arm is of the other arm's type; a return is target-typed.
		"java.lang.IllegalStateException var1 = arg0 ? null : new java.lang.IllegalStateException(\"x\");",
		"return var1 == null ? new java.lang.IllegalArgumentException(\"y\") : var1;",
		"return arg0 ? new java.lang.IllegalStateException(\"z\") : arg1;",
		// A variable typed from arms that differ needs their least upper bound,
		// which only a debug table can say.
		"cappu: a conditional whose arms differ in type",
		// An arm may carry an assignment used as a value: an expression.
		"return var1 != null ? var1 : (this.impl = this.compute());",
		"return var2 != 0 ? var2 : (this.cache[arg0] = arg0 * 7);",
		"return arg0 ? (var2 = arg1 + 1) : (var2 = 2);",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Count(source, "/* cappu:") != 1 {
		t.Errorf("expected one bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Ternew", source)
	driver := `public class TernewDriver {
  public static void main(String[] a) {
    System.out.println(Ternew.nonce(null).length + " " + ((int[]) Ternew.pick(false, null)).length + " "
      + Ternew.len(true) + " " + (Ternew.call(true) != null) + " " + Ternew.cond() + " " + Ternew.calls
      + " " + Ternew.nul(true).getMessage() + Ternew.nul(false).getMessage() + " " + Ternew.ret(false, null));
    Ternew t = new Ternew();
    System.out.println((t.impl() == t.impl()) + " " + t.cached(2) + t.cached(2) + " " + Ternew.loc(true, 5) + Ternew.loc(false, 5) + " " + Ternew.calls);
  }
}`
	compileWithJavacOn(t, dir, "TernewDriver", driver, dir)
	expected := runJava(t, dir, "TernewDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "TernewDriver")
	if actual != expected || actual != "12 3 2 true true 1 yx null\ntrue 1414 62 2\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// `null` is of every reference type: `T x = null; ... x = get();` is one
// variable, typed by the first real value, and `x = null` later does not begin
// another one. Without a debug table the store's type was all a slot had to go
// on, and either shape split the variable in two - the second read stale, or
// never initialized.
const nullishSource = `public class Nullish {
  static String a(boolean c) { String s = null; if (c) { s = "x"; } return s == null ? "-" : s; }
  static String b(boolean c) { String s = "y"; if (c) { s = null; } return s == null ? "-" : s; }
  static int[] arr(boolean c) { int[] a = null; if (c) { a = new int[2]; } return a == null ? new int[0] : a; }
  static Object caught(boolean c) { Object o = null; try { o = c ? "q" : Integer.valueOf(1); } catch (RuntimeException e) { return o; } return o; }
  static String f(Object o) { return "O"; }
  static String f(String s) { return "S"; }
  static void use(Object o) {}
  // Read before the first real value: what the reads ask for types it, not
  // the value - javac chose f(Object) by the declared type.
  static String readFirst(boolean c) { Object o = null; String r = f(o); if (c) o = "x"; return r + f(o); }
  // The value says nothing about the declared type either way: the use does.
  static String typedByUse(boolean c) { Object o = null; if (c) o = "s"; return f(o); }
  static String typedByUse2(boolean c) { String o = null; if (c) o = "s"; return f(o); }
  // Carried around a loop: the read before the store and the store are one variable.
  static int loop(String[] a) { Object x = null; int n = 0; for (String s : a) { if (x != null) n++; x = s.trim(); } return n; }
  static String carried(int[] a) { Object x = "a"; String r = ""; for (int i : a) { r += x; x = Integer.valueOf(i); } return r; }
  static String erased(String[] s) { int x = 0; String r = ""; for (int i = 0; i < s.length; i++) { r += x; x = s[i].charAt(0); } return r; }
  static boolean compared(boolean c, Integer i) { Object o = null; boolean r = o == i; if (c) o = "s"; return r; }
  // A dead variable's slot, reused: the new one is its own, and its type is
  // what its first use asks for.
  static String reused() { { String s = "a"; f(s); } { Object o = null; return f(o); } }
  static Integer afterLoop(java.util.List<String> l) { for (String s : l) { use(s); } Integer r = null; return r; }
  static Object nested(boolean c, boolean d, Integer i, String s, Integer j) { Object o = c ? (d ? i : s) : j; return o; }
  static Object bound(boolean c, Object p) { String s = "a"; use(s); Object o = c ? p : "s"; return o; }
}
`

func TestDecompileKeepsANullStoreInItsVariable(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Nullish", nullishSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"java.lang.String var1 = null;", "var1 = \"x\";",
		"java.lang.String var1 = \"y\";", "var1 = null;",
		"int[] var1 = null;", "var1 = new int[2];",
		"java.lang.Object var1 = null;", "var1 = arg0 ? \"q\" : java.lang.Integer.valueOf(1);",
		"java.lang.Object var2 = null;", "boolean var3 = var2 == arg1;",
		"java.lang.Object var0_2 = null;", "return f(var0_2);",
		"java.lang.Object var1 = null;\njava.lang.String var2 = f(var1);", "return var2 + f(var1);",
		"var1 = var6.trim();", "java.lang.Object var1 = \"a\";", "var1 = java.lang.Integer.valueOf(var6);", "int var1 = 0;", "var1 = arg0[var3].charAt(0);",
		"java.lang.Integer var1_2 = null;", "return var1_2;",
		"cappu: a conditional whose arms differ in type",
		"java.lang.Object var3 = arg0 ? arg1 : \"s\";",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	// nested: the store of a conditional whose arms differ.
	if strings.Count(source, "/* cappu:") != 1 {
		t.Errorf("expected one bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Nullish", source)
	driver := `public class NullishDriver {
  public static void main(String[] x) {
    System.out.println(Nullish.a(true) + Nullish.a(false) + Nullish.b(true) + Nullish.b(false) + Nullish.arr(true).length + Nullish.caught(false)
      + Nullish.compared(true, 1) + Nullish.reused() + Nullish.afterLoop(java.util.List.of("q")) + Nullish.bound(false, 2)
      + Nullish.readFirst(true) + Nullish.typedByUse(true) + Nullish.typedByUse2(true) + " " + Nullish.loop(new String[]{"a", "b", "c"})
      + Nullish.carried(new int[]{1, 2}) + Nullish.erased(new String[]{"q", "z"}));
  }
}`
	compileWithJavacOn(t, dir, "NullishDriver", driver, dir)
	expected := runJava(t, dir, "NullishDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "NullishDriver")
	if actual != expected || actual != "x--y21falseOnullsOOOS 2a10113\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// A literal `null` passed where `Object` is declared: the bytecode's choice of
// the `Object` overload (`String.valueOf`, `println`, `append` have a `char[]`
// one beside it) means source cast it, and the cast is written back. On a
// method of a call's result the `Object` is as likely a generic `T`, where the
// cast would not compile against the parameterized type and its absence may
// take another overload: that is a refusal. The receiver has to be the call's
// own, not whatever a static call's arguments happen to sit on, and not a
// `new`, which names its type arguments itself.
const nullArgSource = `import java.util.Optional;
public class NullArg {
  static String v() { return String.valueOf((Object) null); }
  static String o(Optional<String> op) { return op.orElse(null); }
  static Object w() { return new java.lang.ref.WeakReference<Object>(null).get(); }
  static long s() { return java.util.stream.Stream.of((Object) null).count(); }
  static String chained(java.util.Map<String, Optional<String>> m) { return m.get("k").orElse(null); }
  static Object direct(NullArgFinder f) { return f.find("k").orElse(null); }
  static String use(String a, String b) { return a + b; }
  static String foo() { return "f"; }
  static String underStatic() { return use(foo(), String.valueOf((Object) null)); }
  static String underConcat() { return foo() + String.valueOf((Object) null); }
  static String underAppend(StringBuilder sb) { return sb.append(foo()).append((Object) null).toString(); }
  String bar(Object o) { return "O"; }
  String bar(String s) { return "S"; }
  static NullArg make() { return new NullArg(); }
  String onNew() { return new NullArg().bar((Object) null); }
  String onCall() { return make().bar((Object) null); }
}
interface NullArgFinder { Optional<Object> find(String k); }
`

func TestDecompileCastsANullArgumentOnlyWhereAnOverloadWouldTakeIt(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "NullArg", nullArgSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"return java.lang.String.valueOf((java.lang.Object) null);",
		// A raw receiver takes the cast (a checkcast makes one raw too); a
		// call's receiver has the real parameterized type, where it would not
		// compile.
		"return (java.lang.String) arg0.orElse((java.lang.Object) null);",
		"return new java.lang.ref.WeakReference((java.lang.Object) null).get();",
		"return java.util.stream.Stream.of((java.lang.Object) null).count();",
		"return (java.lang.String) ((java.util.Optional) arg0.get(\"k\")).orElse((java.lang.Object) null);",
		"return use(foo(), java.lang.String.valueOf((java.lang.Object) null));",
		"return foo() + java.lang.String.valueOf((java.lang.Object) null);",
		"return new NullArg().bar((java.lang.Object) null);",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	// direct, underAppend and onCall: a null to a method of a call's result.
	if count := strings.Count(source, "/* cappu: a null argument to a method of a call's result"); count != 3 {
		t.Errorf("expected three refusals, got %d:\n%s", count, source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavacOn(t, again, "NullArg", source, dir)
	driver := `public class NullArgDriver {
  public static void main(String[] x) {
    System.out.println(NullArg.v() + " " + NullArg.o(java.util.Optional.empty()) + " " + NullArg.w() + " " + NullArg.s()
      + " " + NullArg.chained(java.util.Map.of("k", java.util.Optional.of("v"))) + " " + NullArg.underStatic() + " " + NullArg.underConcat()
      + " " + new NullArg().onNew());
  }
}`
	compileWithJavacOn(t, dir, "NullArgDriver", driver, dir)
	expected := runJava(t, dir, "NullArgDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "NullArgDriver")
	if actual != expected || actual != "null null null 1 v fnull fnull O\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// An assignment the stack still wants is the expression it is, in place: a
// long or double local (`dup2; lstore`), a field (`dup_x1; putfield`, `dup;
// putstatic`), an array element (`dup_x2; iastore`). And a receiver copied for
// a read-modify-write that cannot be written twice - `self().count += 5`,
// `arr[i()] ^= 3` - is written once, as the compound assignment it was; the
// narrowing conversion javac puts on a byte's is the assignment's own.
const rmwSource = `public class Rmw {
  int count; byte b; long l; String s; int[] arr = new int[4]; byte[] bs = new byte[3]; long[] la = new long[2];
  static long g; static int calls;
  Rmw self() { calls++; return this; }
  int idx() { calls++; return 1; }
  static long mk() { calls++; return 7L; }
  void f1() { self().count += 5; }
  void f2() { self().count -= 2 - 1; }
  void f3() { self().b += 1; }
  void f4() { self().l <<= 2; }
  void f5() { self().s += "x"; }
  void f6() { arr[idx()] ^= 3; }
  void f7() { self().arr[idx()] *= 4; }
  void f8() { bs[idx()] += 2; }
  int f9() { return self().count += 100; }
  int v1(int v) { int x = this.count = v; return x + count; }
  long v2() { long a; long m = a = mk(); return a + m; }
  static long v3() { long t = g = mk(); return t; }
  String v4(Rmw o) { return this.s = o.s = "q"; }
  int v5() { return arr[idx()] = 5; }
  long v6() { return la[idx() - 1] = 9L; }
  int v7() { return arr[idx()] += 7; }
}
`

func TestDecompileWritesCompoundAndValueAssignments(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Rmw", rmwSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"this.self().count += 5;", "this.self().count -= 1;", "this.self().b += 1;", "this.self().l <<= 2;",
		"this.self().s += \"x\";", "this.arr[this.idx()] ^= 3;", "this.self().arr[this.idx()] *= 4;",
		"this.bs[this.idx()] += 2;", "return this.self().count += 100;",
		"int var2 = this.count = arg0;", "long var3 = var1 = mk();", "long var0 = g = mk();",
		"return this.s = arg0.s = \"q\";", "return this.arr[this.idx()] = 5;",
		"return this.la[this.idx() - 1] = 9L;", "return this.arr[this.idx()] += 7;",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Rmw", source)
	driver := `public class RmwDriver {
  public static void main(String[] z) {
    Rmw c = new Rmw();
    c.f1(); c.f2(); c.f3(); c.f4(); c.f5(); c.f6(); c.f7(); c.f8();
    System.out.println(c.f9() + " " + c.count + " " + c.b + " " + c.l + " " + c.s + " " + c.arr[1] + " " + c.bs[1]
      + " " + c.v1(3) + " " + c.v2() + " " + Rmw.v3() + " " + c.v4(new Rmw()) + " " + c.v5() + " " + c.v6()
      + " " + c.v7() + " " + Rmw.calls);
  }
}`
	compileWithJavacOn(t, dir, "RmwDriver", driver, dir)
	expected := runJava(t, dir, "RmwDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "RmwDriver")
	if actual != expected || actual != "104 104 1 0 nullx 12 2 6 14 7 q 5 9 12 15\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// A jump that leaves or continues an enclosing loop needs that loop named:
// `break label;` and `continue label;` name the nearest enclosing loop the jump
// fits, and the label goes on its header - written after the body, so it can
// still take it. Every loop form takes one.
const labeledSource = `public class Labeled {
  static int find(int[][] m, int v) { int r = -1; outer: for (int i = 0; i < m.length; i++) { for (int j = 0; j < m[i].length; j++) { if (m[i][j] == v) { r = i * 10 + j; break outer; } } } return r; }
  static int skip(int[][] m) { int s = 0; rows: for (int i = 0; i < m.length; i++) { for (int j = 0; j < m[i].length; j++) { if (m[i][j] < 0) continue rows; s += m[i][j]; } s += 100; } return s; }
  static int fromSwitch(int[] a) { int n = 0; loop: for (int x : a) { switch (x) { case 0: break loop; case 1: continue loop; default: n += x; } n++; } return n; }
  static int three(int[][][] c) { int n = 0; a: for (int[][] p : c) { b: for (int[] q : p) { for (int r : q) { if (r == 7) break a; if (r == 5) continue b; if (r == 3) continue a; n += r; } n += 1000; } n += 100000; } return n; }
  static int whileTrue(int[] a) { int i = 0, n = 0; outer: while (true) { while (i < a.length) { if (a[i] == 9) break outer; n += a[i++]; } break; } return n; }
  static int doo(int[] a) { int i = 0, n = 0; outer: do { i++; int j = 0; do { if (a[j] == 4) continue outer; n += a[j]; j++; } while (j < a.length); n += 50; } while (i < 2); return n; }
}
`

func TestDecompileNamesTheLoopALabeledJumpLeaves(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Labeled", labeledSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"label1: while (var3 < arg0.length) {", "break label1;", "continue label1;",
		"label1: for (; var2 < arg0.length; var2++) {", "label2: for (; var8 < var7; var8++) {",
		"continue label2;", "label1: do {",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Labeled", source)
	driver := `public class LabeledDriver {
  public static void main(String[] z) {
    int[][] m = {{1,2},{3,4}};
    System.out.println(Labeled.find(m, 4) + " " + Labeled.find(m, 9) + " " + Labeled.skip(new int[][]{{1,-1,5},{2,3}}) + " "
      + Labeled.fromSwitch(new int[]{2,1,3,0,5}) + " " + Labeled.three(new int[][][]{{{1,2},{5,9},{3,8}},{{7}}}) + " "
      + Labeled.whileTrue(new int[]{1,2,9,4}) + " " + Labeled.doo(new int[]{1,4,2}));
  }
}`
	compileWithJavacOn(t, dir, "LabeledDriver", driver, dir)
	expected := runJava(t, dir, "LabeledDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "LabeledDriver")
	if actual != expected || actual != "11 -1 106 7 1003 3 2\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// A loop with several ways out has one end all the same. The `return`s and
// `throw`s go nowhere the loop comes back to, so what a test header leaves to
// is the end; `break`s out of an `if` or a `switch` in a `while (true)` meet at
// it; in a nested loop, the way out that stays in the outer body is it, and the
// others are `break outer`.
const returnsSource = `public class Returns {
  static int firstNeg(int[] a) { int i = 0; while (i < a.length) { if (a[i] == 0) { i++; continue; } if (a[i] < 0) return i; i++; } return -1; }
  static int thrower(int[] a) { for (int x : a) { if (x == 0) continue; if (x < 0) throw new IllegalArgumentException("neg"); if (x > 100) return x; } return 0; }
  static int ifs(int[] a) { int i = 0, n = 0; while (true) { int c = a[i++]; if (c == 0) { n += 5; break; } if (c == 1) { if (a[i] == 7) n = -1; break; } n += c; } return n * 10 + i; }
  static int mixed(int[] a) { int i = 0, n = 0; while (true) { int c = a[i++]; if (c == 0) { n += 5; break; } if (c == 1) { if (a[i] == 7) return -1; break; } if (c == 2) throw new IllegalStateException("two"); n += c; } return n * 10 + i; }
  static int sw(int[] a) { int i = 0, n = 0; while (true) { int c = a[i++]; switch (c) { case 0: case 1: break; case 2: if (a[i] == 9) break; n += 100; break; default: n += c; continue; } break; } return n * 10 + i; }
  static int nest(int[] a) { int i = 0, n = 0; outer: while (i < a.length) { while (true) { int c = a[i++]; if (c == 0) break; if (c == 9) break outer; n += c; } n += 1000; } return n * 10 + i; }
  static int nestReturn(int[] a) { int i = 0, n = 0; outer: while (i < a.length) { while (true) { int c = a[i++]; if (c == 0) return n; if (c == 9) break outer; n += c; } } return n * 10 + i; }
  static int preInc(int[] a, int from) { int i = from; while (++i < a.length) { if (a[i] != 0) { a[i] = 0; return i - from; } } return -1; }
}
`

func TestDecompileEndsALoopWhoseOtherExitsReturn(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Returns", returnsSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Returns", source)
	driver := `public class ReturnsDriver {
  public static void main(String[] z) {
    System.out.println(Returns.firstNeg(new int[]{0, 3, -1}) + " " + Returns.firstNeg(new int[]{0}) + " " + Returns.thrower(new int[]{0, 5, 200}));
    try { Returns.thrower(new int[]{-1}); } catch (IllegalArgumentException e) { System.out.println(e.getMessage()); }
    System.out.println(Returns.ifs(new int[]{3, 0}) + " " + Returns.ifs(new int[]{1, 7}) + " " + Returns.ifs(new int[]{4, 1, 2}) + " "
      + Returns.mixed(new int[]{3, 0}) + " " + Returns.mixed(new int[]{1, 7}) + " " + Returns.mixed(new int[]{4, 1, 2}) + " "
      + Returns.sw(new int[]{3, 4, 0}) + " " + Returns.sw(new int[]{2, 9}) + " " + Returns.sw(new int[]{2, 5, 1}) + " "
      + Returns.nest(new int[]{1, 0, 2, 9, 5}) + " " + Returns.nest(new int[]{1, 0, 2, 0}) + " " + Returns.nestReturn(new int[]{1, 2, 9}) + " " + Returns.nestReturn(new int[]{1, 0}) + " "
      + Returns.preInc(new int[]{0, 0, 4}, 0) + " " + Returns.preInc(new int[]{0, 0}, 0));
    try { Returns.mixed(new int[]{2}); } catch (IllegalStateException e) { System.out.println(e.getMessage()); }
  }
}`
	compileWithJavacOn(t, dir, "ReturnsDriver", driver, dir)
	expected := runJava(t, dir, "ReturnsDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "ReturnsDriver")
	if actual != expected || actual != "2 -1 200\nneg\n82 -9 42 82 -1 42 73 1 1001 10034 20034 33 1 2 -1\ntwo\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// A char where an int is asked for widens without an instruction, so the
// bytecode looks the same as `c` itself would - but source binds `c` to the
// char overload and prints it as a character. The `(int)` is written back on
// call arguments and concatenation parts; nowhere else does it change anything
// (and an int local a char was stored in is, without a debug table, a char).
const charIntSource = `public class CharInt {
  char c = 'b';
  static String f(int i) { return "i" + i; }
  static String f(char c) { return "c" + c; }
  String all(char p) { int i = c; long l = c; return f((int) c) + f(c) + f((int) p) + f(p) + " " + (int) c + c + (int) p + p + String.valueOf((int) c) + String.valueOf(c) + i + l + (char) (c + 1); }
  public static void main(String[] z) { System.out.println(new CharInt().all('q')); }
}
`

func TestDecompileCastsACharPassedAsAnInt(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "CharInt", charIntSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"f((int) this.c) + f(this.c) + f((int) arg0) + f(arg0)", "+ (int) this.c + this.c + (int) arg0 + arg0",
		"java.lang.String.valueOf((int) this.c)", "java.lang.String.valueOf(this.c)",
		"char var2 = this.c;", "long var3 = (long) this.c;", "+ (int) var2 + var3 +",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "CharInt", source)
	expected := runJava(t, dir, "CharInt")
	actual := runJava(t, again, "CharInt")
	if actual != expected || actual != "i98cbi113cq 98b113q98b9898c\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// An `if` whose last `else` returns, breaks or continues has no post-dominator
// - that arm never reaches the merge - while the other arms still come back
// together after it. The first block both arms reach is the end of the
// statement; walking there stops at the edges of every loop around it, so a
// `break outer` is not mistaken for a merge.
const elseExitsSource = `public class ElseExits {
  int m; int size = 5;
  int g1(int a, int b) { if (a > 0) { m += a; } else if (b > 0) { m += b; } else { return -1; } m += 100; return m; }
  int g3(int a, int b) { if (a > 0) { m += a; } else if (b > 0) { m += b; } else { throw new IllegalStateException(); } m += 100; return m; }
  void g4() { while (size > 1) { int n = size - 2; if (n > 0) { m += n; } else if (n == 0) { m += 7; } else { return; } size = size - 1; } }
  void g5() { while (size > 1) { int n = size - 2; if (n > 0) { m += n; } else if (n == 0) { m += 7; } else { break; } size = size - 1; } }
  void g6() { while (size > 1) { int n = size - 2; if (n > 0) { m += n; } else if (n == 0) { m += 7; } else { size--; continue; } size = size - 1; } }
  static int nested(int[][] c) { int n = 0; outer: for (int[] p : c) { for (int r : p) { if (r == 7) break outer; if (r == 5) continue outer; n += r; } n += 1000; } return n; }
  static boolean armsAsInts(boolean inarc, int a, int b) { boolean inside = a * b >= 0; return inarc ? !inside : inside; }
  public static void main(String[] z) {
    ElseExits t = new ElseExits();
    System.out.print(t.g1(1, 0) + " " + t.g1(0, 1) + " " + t.g1(0, 0) + " " + t.g3(0, 1));
    t.g4(); System.out.print(" " + t.m + t.size); t = new ElseExits(); t.g5(); System.out.print(" " + t.m + t.size); t = new ElseExits(); t.g6(); System.out.print(" " + t.m + t.size);
    System.out.println(" " + nested(new int[][]{{1, 2}, {5, 9}, {3, 7}, {4}}) + " " + armsAsInts(true, 1, 2));
  }
}
`

func TestDecompileEndsAnIfWhoseLastElseLeaves(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "ElseExits", elseExitsSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"return -1;\n}\n}\nthis.m = this.m + 100;",
		"break;\n}\n}\nthis.size = this.size - 1;",
		"break label1;", "continue label1;", "var1 += 1000;",
		// The one method that still bails, and why: the arms of the returned
		// conditional were written as the ints javac materialized.
		"/* cappu: a conditional with number arms where a boolean belongs",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if count := strings.Count(source, "/* cappu:"); count != 1 {
		t.Errorf("expected one bail, got %d:\n%s", count, source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "ElseExits", source)
	expected := runJava(t, dir, "ElseExits")
	if expected != "101 202 -1 303 3161 131 131 1006 false\n" {
		t.Fatalf("unexpected reference output %q", expected)
	}
	// armsAsInts bails, so the driver is run up to the line before it.
	source = strings.Replace(source, `+ " " + armsAsInts(true, 1, 2)`, "", 1)
	compileWithJavac(t, again, "ElseExits", source)
	actual := runJava(t, again, "ElseExits")
	if actual != strings.TrimSuffix(expected, " false\n")+"\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// A parameter is defined on entry, on every path: reassigned in a branch and
// read after it, the read is as unambiguous as any other, and not "written in
// more than one branch".
const paramsSource = `public class Params {
  static int clamp(int v, int lo) { if (v < lo) v = lo; return v * 2; }
  static String norm(String s) { if (s == null) s = ""; else s = s.trim(); return s + "!"; }
  static long both(long a, boolean c) { if (c) { a += 5; } else { a -= 1; } return a; }
  static int loop(int n) { while (n > 10) n /= 2; return n; }
}
`

func TestDecompileReadsAReassignedParameterAfterABranch(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Params", paramsSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{"arg0 = arg1;", "return arg0 * 2;", "arg0 = \"\";", "arg0 = arg0.trim();", "return arg0 + \"!\";"} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Params", source)
	driver := `public class ParamsDriver {
  public static void main(String[] z) {
    System.out.println(Params.clamp(3, 5) + " " + Params.norm(null) + Params.norm(" x ") + " " + Params.both(10L, true) + " " + Params.loop(100));
  }
}`
	compileWithJavacOn(t, dir, "ParamsDriver", driver, dir)
	expected := runJava(t, dir, "ParamsDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "ParamsDriver")
	if actual != expected || actual != "10 !x! 15 6\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// javac erases a boolean, char, byte or short to an int, so `c = 'a'` in one
// arm and `c = s.charAt(0)` in the other store an int literal and a char into
// one slot. Without a debug table that split the variable in two. A literal
// that fits the typed variable is one of its values, and a variable that has
// only held such literals, and was never read, takes the type of the first
// value that knows its own - whichever arm comes first. That type is a guess:
// `int i = s.charAt(0)` stores the same way. An `iinc`, or an int stored
// without javac's narrowing, says the variable was an int all along, and the
// variable widens back; passed or concatenated as an int, a char is cast, so
// the guess cannot change what runs.
const erasedSource = `public class Erased {
  static boolean flag() { return true; }
  static char ch(boolean x, String s) { char c; if (x) c = 'a'; else c = s.charAt(0); return c; }
  static char ch2(boolean x, String s) { char c; if (x) c = s.charAt(0); else c = 'q'; return c; }
  static boolean bo(boolean x) { boolean b; if (x) b = true; else b = flag(); return b; }
  static byte by(boolean x, byte[] a) { byte b; if (x) b = 5; else b = a[0]; return b; }
  static short sh(boolean x, short[] a) { short s; if (x) s = a[0]; else s = -300; return s; }
  static int inc(String s) { int i = s.charAt(0); i++; return i; }
  static int widened(String s) { int i = s.charAt(0); i = i + 1; return i; }  // two variables read the same
  static int mix(byte[] b) { int i = b[0]; i += 200; return i; }
  static String asInt(boolean x, String s) { int i; if (x) i = 65; else i = s.charAt(0); return String.valueOf(i) + i; }
  static String use(char c) { return "c"; }
  static String use(int i) { return "i"; }
  static String overload(byte[] b, String s) { char c = s.charAt(0); int i = b[0]; return use(c) + use((int) c) + use(i); }
}
`

func TestDecompileMergesAnErasedVariableAcrossBranches(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Erased", erasedSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"char var2;", "var2 = 'a';", "var2 = arg1.charAt(0);", "var2 = 'q';",
		"boolean var1;", "var1 = true;", "var1 = flag();",
		"byte var2;", "var2 = 5;", "short var2;", "var2 = -300;",
		"int var1 = arg0.charAt(0);\nvar1++;", "char var1 = arg0.charAt(0);\nint var1_2 = var1 + 1;", "int var1 = arg0[0];\nvar1 += 200;",
		"return java.lang.String.valueOf((int) var2) + (int) var2;", "return use(var2) + use((int) var2) + use((int) var3);",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") || strings.Contains(source, "var2_2") {
		t.Errorf("expected one variable per method and no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Erased", source)
	driver := `public class ErasedDriver {
  public static void main(String[] z) {
    System.out.println(Erased.ch(true, "z") + "" + Erased.ch(false, "z") + Erased.ch2(true, "z") + Erased.ch2(false, "z") + " "
      + Erased.bo(true) + Erased.bo(false) + " " + Erased.by(true, new byte[]{9}) + Erased.by(false, new byte[]{9}) + " "
      + Erased.sh(true, new short[]{3}) + Erased.sh(false, new short[]{3}) + " " + Erased.inc("\uffff") + " " + Erased.widened("\uffff")
      + " " + Erased.mix(new byte[]{100}) + " " + Erased.asInt(false, "q") + " " + Erased.overload(new byte[]{1}, "z"));
  }
}`
	compileWithJavacOn(t, dir, "ErasedDriver", driver, dir)
	expected := runJava(t, dir, "ErasedDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "ErasedDriver")
	if actual != expected || actual != "azzq truetrue 59 3-300 65536 65536 300 113113 cii\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// Two arms of one `if` that store differently-typed values into one slot are
// one variable when the types allow: `Object` is every reference type's bound,
// so a variable that holds one and takes another is an Object, and one typed by
// one arm's value becomes an Object once the other arm stores one (if nothing
// read it as the narrower type yet); `null` fits whatever the other arm stored.
// Two stores are one variable when they flow to one read of the slot - which is
// what definite assignment guarantees for a variable read after the arms join,
// and what two arm-local variables in one slot (`Object o` in one `else if`
// arm, `Node c` in the next) never do.
const boundSource = `import java.util.*;
public class Bound {
  static Object first(boolean c, Map<String, Object> m) { Object x; if (c) x = m.get("k"); else x = new ArrayList<String>(); return x; }
  static Object second(boolean c, Map<String, Object> m) { Object x; if (c) x = new StringBuilder("s"); else x = m.get("k"); return x; }
  static int[] arr(boolean c, Object o) { Object x; if (c) x = o; else x = new int[2]; return x instanceof int[] ? (int[]) x : new int[0]; }
  static String arms(boolean c, String a) { String s; if (c) s = a.trim(); else s = null; return s == null ? "-" : s; }
  static String arms2(boolean c, String a) { String s; if (c) s = null; else s = a.trim(); return s == null ? "-" : s; }
  static void use(Object o) { System.out.print(o); }
  static void armLocal(int k, Map<String, Object> m, java.util.List<String> l) { if (k == 1) { Object o = m.get("a"); use(o); } else if (k == 2) { java.util.List<String> c = l; use(c.size()); } }
  static Object joined(int k, Map<String, Object> m) { Object x; if (k == 1) { x = m.get("a"); } else { x = new ArrayList<>(); use(x); } return x; }
}
`

func TestDecompileBoundsAVariableTwoArmsAssign(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Bound", boundSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"java.lang.Object var2;", "var2 = arg1.get(\"k\");", "var2 = new java.util.ArrayList();",
		"var2 = new java.lang.StringBuilder(\"s\");", "var2 = new int[2];",
		"java.lang.String var2;", "var2 = arg1.trim();", "var2 = null;",
		// Two arm-local variables keep their slot apart, one joined variable does not.
		"java.lang.Object var3;", "java.util.List var3_2;", "var3 = arg1.get(\"a\");", "var3_2 = arg2;",
		"java.lang.Object var2;", "var2 = new java.util.ArrayList();", "use(var2);",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") || strings.Contains(source, "var2_2") {
		t.Errorf("expected one variable per method and no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Bound", source)
	driver := `public class BoundDriver {
  public static void main(String[] z) {
    java.util.Map<String, Object> m = new java.util.HashMap<>(); m.put("k", 5);
    System.out.println(Bound.first(true, m) + " " + Bound.first(false, m) + " " + Bound.second(true, m) + " " + Bound.second(false, m)
      + " " + Bound.arr(false, null).length + " " + Bound.arms(true, " x ") + Bound.arms(false, "y") + Bound.arms2(true, "x") + Bound.arms2(false, " y "));
    Bound.armLocal(1, m, null); Bound.armLocal(2, m, java.util.List.of("q"));
    System.out.println(" " + Bound.joined(1, m) + Bound.joined(2, m));
  }
}`
	compileWithJavacOn(t, dir, "BoundDriver", driver, dir)
	expected := runJava(t, dir, "BoundDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "BoundDriver")
	if actual != expected || actual != "5 [] s 5 2 x--y\nnull1[] null[]\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// Two arms that store values of different reference types into one variable
// - `new ArrayList<>()` and `new LinkedList<>()` - declared a bound this has no
// class hierarchy to compute. The variable's type stays open, an Object, until
// the first use asks for one: the class a method called on it belongs to (its
// static type, as javac wrote it), the field read from it, the parameter it is
// passed as, or the type it is returned as. One no use asks of is an Object.
// A checkcast on a value says what the value is, not the variable; a variable
// assigned an open one is typed by its own uses, and the two have to agree.
const openSource = `import java.util.*;
public class Open {
  static int size(boolean c) { List<String> l; if (c) l = new ArrayList<>(); else l = new LinkedList<>(); l.add("x"); return l.size(); }
  static Object ret(boolean c) { Collection<String> l; if (c) l = new ArrayList<>(); else l = new HashSet<>(); return l; }
  static int arg(boolean c) { Map<String, Integer> m; if (c) m = new HashMap<>(); else m = new TreeMap<>(); return count(m); }
  static int count(Map<String, Integer> m) { return m.size(); }
  static int field(boolean c) { java.awt.Point p; if (c) p = new java.awt.Point(1, 2); else p = new OpenPoint(); return p.x; }
  static String twoUses(boolean c) { CharSequence s; if (c) s = "abc"; else s = new StringBuilder("de"); return s.length() + "" + s.charAt(0); }
  // An argument takes a supertype too: addAll(Collection) before keep(Set) are two answers, the member call one.
  static int conflict(boolean c) { Set<String> s; if (c) s = Collections.emptySet(); else s = new HashSet<>(); Collections.addAll(s, "a"); return keep(s); }
  static int keep(Set<String> s) { return s.size(); }
  static int exact(boolean c) { Set<String> s; if (c) s = Collections.emptySet(); else s = new HashSet<>(); Collections.addAll(s, "a"); s.add("b"); return keep(s); }
  static Object arm(boolean c, Object o) { Collection<String> l; if (c) l = new ArrayList<>(); else l = new HashSet<>(); return c ? l : o; }
  static CharSequence armTyped(boolean c, CharSequence o) { CharSequence s; if (c) s = "a"; else s = new StringBuilder("b"); return c ? s : o; }
  static CharSequence cast(boolean c, Object o) { CharSequence s; if (c) s = (CharSequence) o; else s = new StringBuilder("x"); return s; }
  static int castLies(boolean c, Object o) { CharSequence s; if (c) s = (String) o; else s = new StringBuilder("x"); return s.length(); }
  static int unresolved(boolean c) { Collection<String> l; if (c) l = new ArrayList<>(); else l = new HashSet<>(); synchronized (l) { return 1; } }
  static Number linked(boolean c, int e) { Number d; if (c) d = Long.valueOf(e); else d = java.math.BigInteger.valueOf(e); Number n = d; if (d instanceof Long) { n = Long.valueOf(((Long) d).longValue() + 1); } return n; }
  static String useN(Object o) { return "O"; }
  static String useN(Number n) { return "N"; }
  static String linkedUses(boolean c, int e) { Number d; if (c) d = Long.valueOf(e); else d = java.math.BigInteger.valueOf(e); Object n = d; d.intValue(); return useN(n); }
  static String linkedOther(boolean c, int e) { Number d; if (c) d = Long.valueOf(e); else d = java.math.BigInteger.valueOf(e); Object n = d; n.hashCode(); return useN(d); }
  static int linkedNarrower(boolean c, int e) { Object d; if (c) d = Long.valueOf(e); else d = java.math.BigInteger.valueOf(e); Number n = (Number) d; d.hashCode(); return n.intValue(); }
  // Held only null, then a cast: the cast's type is tried first, and when a
  // use asks for a supertype instead, the type every value has is the one.
  static Exception merge(Exception a, Exception b) { return a == null ? b : a; }
  static String thrown(boolean c) throws java.io.IOException { java.io.IOException e = null; if (c) e = (java.io.IOException) merge(e, new java.io.IOException("io")); if (e != null) throw e; return "-"; }
  static String nulled(boolean c) { String s = null; if (c) s = "v"; StringBuilder b = new StringBuilder(); b.append((CharSequence) s); return b.append(s).toString(); }  // the upcast is lost: append(String) does the same
  // A variable that held a subclass first and widens later widens what was
  // typed by it: previous = ancestor took the narrower type as it was then.
  static CharSequence root(String s) { CharSequence ancestor = s; CharSequence previous; do { previous = ancestor; ancestor = ancestor.length() > 2 ? new StringBuilder(ancestor.subSequence(1, ancestor.length())) : null; } while (ancestor != null); return previous; }
  static String keyed(int id, int code, java.util.Hashtable<Object, Object> h) { String ks2 = null; String ks; if (id == 0) { ks = "k" + code; } else { if (code > 1) { ks2 = "k" + (code - 1); } ks = "k" + code; } Object o = null; if (ks2 != null) { o = h.get(ks2); if (o != null) ks = ks2; } if (o == null) o = h.get(ks); return o + ":" + ks; }
}
class OpenPoint extends java.awt.Point { OpenPoint() { super(3, 4); } }
`

func TestDecompileTypesAMergedVariableByItsFirstUse(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Open", openSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"java.util.List var1;", "var1 = new java.util.ArrayList();", "var1 = new java.util.LinkedList();", "var1.add(\"x\");",
		"java.lang.Object var1;", "return var1;",
		"java.util.Map var1;", "return count(var1);",
		"java.awt.Point var1;", "return var1.x;",
		"java.lang.CharSequence var1;",
		"java.util.Set var1;", "var1.add(\"b\");",
		"java.lang.Object var2;", "return arg0 ? var2 : arg1;",
		"java.lang.CharSequence var2;", "var2 = (java.lang.CharSequence) arg1;",
		"var2 = (java.lang.String) arg1;", "return var2.length();",
		"cappu: a variable whose uses ask for different types",
		"java.lang.Object var1;", "synchronized (var1) {",
		"java.lang.Number var2;", "java.lang.Number var3 = var2;",
		"java.lang.Object var3 = var2;", "return useN(var3);", "return useN(var2);",
		"java.lang.Number var3 = (java.lang.Number) var2;",
		"java.io.IOException var1 = null;", "throw var1;", "java.lang.String var1 = null;", "var2.append(var1);",
		"java.lang.CharSequence var2;\njava.lang.CharSequence var1 = arg0;", "java.lang.String var4;\njava.lang.String var3 = null;",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Count(source, "/* cappu:") != 1 || strings.Contains(source, "var1_2") {
		t.Errorf("expected one variable per method and one bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavacOn(t, again, "Open", source, dir)
	driver := `public class OpenDriver {
  public static void main(String[] z) throws Exception {
    System.out.println(Open.size(true) + " " + Open.size(false) + " " + Open.ret(false).getClass().getSimpleName() + " " + Open.arg(true)
      + " " + Open.field(false) + " " + Open.twoUses(true) + Open.twoUses(false) + " " + Open.exact(false)
      + " " + Open.arm(true, "o") + Open.arm(false, "o") + " " + Open.armTyped(true, "z") + Open.armTyped(false, "z") + " " + Open.cast(true, "q") + Open.cast(false, null)
      + " " + Open.linked(true, 4) + Open.linked(false, 4) + " " + Open.castLies(false, "q") + Open.unresolved(true)
      + " " + Open.linkedUses(true, 1) + Open.linkedOther(false, 1) + Open.linkedNarrower(true, 7) + " " + Open.thrown(false) + Open.nulled(true)
      + " " + Open.root("abcd") + " " + Open.keyed(1, 3, new java.util.Hashtable<>(java.util.Map.of("k2", "v"))));
    try { Open.thrown(true); } catch (java.io.IOException e) { System.out.println(e.getMessage()); }
  }
}`
	compileWithJavacOn(t, dir, "OpenDriver", driver, dir)
	expected := runJava(t, dir, "OpenDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "OpenDriver")
	if actual != expected || actual != "1 1 HashSet 0 3 3a2d 2 []o az qx 54 11 ON7 -vv cd v:k2\nio\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// `super.m()` names, in the bytecode, the class that declares `m` - which may
// be further up than the direct superclass (`super.clone()` is `Object`'s) - and
// an interface's default method is `Iface.super.m()`. The JVM allows an
// invokespecial on `this` nothing else, so the reference kind decides.
const superySource = `interface SuperyGreeter { default String greet() { return "hi"; } }
class SuperyBase { public String toString() { return "base"; } }
class SuperyMid extends SuperyBase {}
public class Supery extends SuperyMid implements SuperyGreeter, Cloneable {
  public String greet() { return SuperyGreeter.super.greet() + "!"; }
  public String toString() { return super.toString() + "-s"; }
  public Supery copy() { try { return (Supery) super.clone(); } catch (CloneNotSupportedException e) { throw new AssertionError(e); } }
}
`

func TestDecompileWritesSuperCallsToAnyAncestor(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Supery", superySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{"return SuperyGreeter.super.greet() + \"!\";", "return super.toString() + \"-s\";", "return (Supery) super.clone();"} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavacOn(t, again, "Supery", source, dir)
	driver := `public class SuperyDriver {
  public static void main(String[] z) { Supery s = new Supery(); System.out.println(s.greet() + " " + s + " " + (s.copy() != s)); }
}`
	compileWithJavacOn(t, dir, "SuperyDriver", driver, dir)
	expected := runJava(t, dir, "SuperyDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "SuperyDriver")
	if actual != expected || actual != "hi! base-s true\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// A switch whose every case leaves one value where they come back together - or
// throws - is a switch expression, and the code after the merge picks the value
// up. The value's type is the arms' common one, as for a conditional.
const switchExprSource = `public class SwitchExpr {
  static String name(int k) { return switch (k) { case 0 -> "zero"; case 1, 2 -> "small"; default -> throw new IllegalArgumentException("k=" + k); }; }
  static int val(int k) { int v = switch (k) { case 0 -> 10; case 1 -> 20; default -> k * 2; }; return v + 1; }
  static Object mixed(int k) { return switch (k) { case 0 -> "s"; case 1 -> Integer.valueOf(7); default -> null; }; }
  static long wide(int k) { return switch (k) { case 0 -> 1; default -> 5L; }; }
  static int stmt(int k) { int r = 0; switch (k) { case 0: r = 1; break; case 1: r = 2; default: r += 10; } return r; }
  static boolean t(int k) { return true; }
  static boolean mix(int k, boolean f) { return switch (k) { case 5 -> false; case 4 -> true; case 6 -> f; default -> t(k); }; }
  static int len(int k) { return (switch (k) { case 0 -> "a"; default -> "bb"; }).length(); }
  static int neg(int k) { return -switch (k) { case 0 -> 1; default -> 2; } + 5; }
}
`

func TestDecompileWritesASwitchExpression(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "SwitchExpr", switchExprSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"return switch (arg0) { case 0 -> \"zero\"; case 1, 2 -> \"small\"; default -> throw new java.lang.IllegalArgumentException(\"k=\" + arg0); };",
		"int var1 = switch (arg0) { case 0 -> 10; case 1 -> 20; default -> arg0 * 2; };",
		"return switch (arg0) { case 0 -> \"s\"; case 1 -> java.lang.Integer.valueOf(7); default -> null; };",
		"return switch (arg0) { case 0 -> 1L; default -> 5L; };",
		// The statement form stays one: its cases fall through and store.
		"case 1:", "var1 += 10;",
		// A boolean arm makes the others' 1/0 true/false.
		"return switch (arg0) { case 5 -> false; case 4 -> true; case 6 -> arg1; default -> t(arg0); };",
		// It binds like a cast: parenthesized under a member access, bare as an operand.
		"return (switch (arg0) { case 0 -> \"a\"; default -> \"bb\"; }).length();",
		"return -(switch (arg0) { case 0 -> 1; default -> 2; }) + 5;",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "SwitchExpr", source)
	driver := `public class SwitchExprDriver {
  public static void main(String[] z) {
    System.out.println(SwitchExpr.name(0) + SwitchExpr.name(2) + " " + SwitchExpr.val(1) + SwitchExpr.val(9) + " " + SwitchExpr.mixed(1) + SwitchExpr.mixed(5) + " " + SwitchExpr.wide(0) + SwitchExpr.wide(2) + " " + SwitchExpr.stmt(1) + SwitchExpr.stmt(0) + " " + SwitchExpr.mix(5, true) + SwitchExpr.mix(6, true) + " " + SwitchExpr.len(1) + SwitchExpr.neg(1));
    try { SwitchExpr.name(7); } catch (IllegalArgumentException e) { System.out.println(e.getMessage()); }
  }
}`
	compileWithJavacOn(t, dir, "SwitchExprDriver", driver, dir)
	expected := runJava(t, dir, "SwitchExprDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "SwitchExprDriver")
	if actual != expected || actual != "zerosmall 2119 7null 15 121 falsetrue 23\nk=7\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// A `return` inside a loop is a statement, not the loop's end - but where the
// test carries a call the header is not a pure test, so the follow has to come
// from somewhere else. A single unconditional latch says the test is still the
// header, and what it leaves to is the end.
const loopRetSource = `import java.util.*;
public class LoopRet {
  static int log;
  static int size(List<Object> l) { log++; return l.size(); }
  static int idx(List<Object> l, Object x) {
    for (int i = 0; i < size(l); i++) { if (l.get(i) == x) return i; } return -1; }
  static int two(List<Object> l, Object x, Object y) {
    for (int i = 0; i < size(l); i++) {
      if (l.get(i) == x) return i; if (l.get(i) == y) return -i - 1; } return -99; }
  static int doBrk(List<Object> l) {
    int i = 0, s = 0; do { if (size(l) == 0) break; s += i; i++; } while (i < 3); return s; }
  static int forever(List<Object> l) {
    int s = 0, i = 0;
    while (true) { int n = size(l); if (i >= n) break; s += i; i++; } return s; }
}`

// log counts the test, so a loop whose condition moved prints differently.
const loopRetDriverSource = `import java.util.*;
public class LoopRetDriver {
  public static void main(String[] args) {
    List<Object> l = new ArrayList<>(List.of("a", "b", "c"));
    for (Object t : new Object[] { "a", "c", "zz" }) {
      LoopRet.log = 0;
      System.out.println(LoopRet.idx(l, t) + " " + LoopRet.two(l, t, "b")
        + " " + LoopRet.doBrk(l) + " " + LoopRet.forever(l) + " " + LoopRet.log);
    }
  }
}`

func TestDecompileReconstructsALoopAReturnLeavesWhoseTestCarriesACall(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "LoopRet", loopRetSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "LoopRet", source)
	compileWithJavacOn(t, dir, "LoopRetDriver", loopRetDriverSource, dir)
	expected := runJava(t, dir, "LoopRetDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "LoopRetDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// An inner class assigns the enclosing instance to its synthetic field before
// the `super()`, which source cannot write. `Object`'s constructor does nothing,
// so the assignment stands where it is and the `super()` is dropped as usual.
func TestDecompileReconstructsAConstructorThatAssignsAFieldBeforeSuper(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	compileWithJavac(t, dir, "Ctory", `public class Ctory { int base = 3;
  class In { int k; In(int k) { this.k = k; } int get() { return k + base; } }
}`)
	source, err := Decompile(readFile(t, filepath.Join(dir, "Ctory$In.class")))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("the constructor bailed:\n%s", source)
	}
	for _, want := range []string{"this.this$0 = arg0;", "this.k = arg1;"} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	// The `super()` javac wrote is still implicit.
	if strings.Contains(source, "super(") {
		t.Errorf("the implicit super() came back:\n%s", source)
	}
}

// A local first stored inside a branch is declared at the top of the method -
// but in a constructor that chains, nothing may come before the
// `super(...)`/`this(...)` call, so the declarations follow it instead. Before
// Java 25 the other order does not compile.
func TestDecompileDeclaresAHoistedLocalAfterTheConstructorCall(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	compileWithJavac(t, dir, "HoistBase", `public class HoistBase { int b; HoistBase(int b) { this.b = b; } }`)
	classFile := compileWithJavacOn(t, dir, "Hoisty", `public class Hoisty extends HoistBase {
  int f;
  Hoisty(int a) { super(a); int x; if (a > 0) { x = 1; } else { x = 2; } f = x; }
  Hoisty(int a, int z) { this(a); int y; if (a > 0) { y = 1; } else { y = 2; } f += y + z; }
}`, dir)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a constructor bailed:\n%s", source)
	}
	for _, want := range []string{"super(arg0);\nint var2;", "this(arg0);\nint var3;"} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q in order:\n%s", want, source)
		}
	}
	// The proof is javac accepting it under --release 21.
	compileWithJavacOn(t, filepath.Join(dir, "again"), "Hoisty", source, dir)
	// A variable the call's own arguments assign is the exception: only Java 25
	// can write that, and it has to be written the way that source was.
	got := withHoisted([]string{"int x;", "int y;"}, []string{"super(x = a);", "y = 1;"}, "super(x = a);")
	want := []string{"int x;", "super(x = a);", "int y;", "y = 1;"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("withHoisted = %v, want %v", got, want)
	}
}

// A superclass that is not `Object` runs code the order is observable through,
// so the statements in front of its call still say so.
func TestDecompileSaysWhenAFieldIsAssignedBeforeASuperclassConstructorThatRuns(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	compileWithJavac(t, dir, "Ctors", `public class Ctors { int base = 3;
  static class Base { Base() { System.out.print(""); } }
  class In extends Base { int k; In(int k) { this.k = k; } int get() { return k + base; } }
}`)
	source, err := Decompile(readFile(t, filepath.Join(dir, "Ctors$In.class")))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: constructor call is not first") {
		t.Errorf("expected the bail:\n%s", source)
	}
}

// A boolean javac erased to `1`/`0` is still a boolean where it is written back,
// so an array index needs the ternary again - `a[b]` is not Java.
func TestDecompileWritesAMaterializedBooleanArrayIndexAsTheTernary(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "BoolIndex",
		`public class BoolIndex { int[][] t; int[] k;`+
			` void f(boolean d) { this.k = this.t[d ? 1 : 0]; } }`))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "this.t[arg0 ? 1 : 0]") {
		t.Errorf("expected the ternary index:\n%s", source)
	}
}

// A `char`, `byte` or `short` post-increment is an `iload`/`iadd`/`i2c`/`istore`,
// not an `iinc`: the store is a statement, so writing it in front of the value
// already on the stack would make that value read the incremented one.
func TestDecompileSaysWhenAnAssignmentHappensWhileTheVariableIsOnTheStack(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Narrow",
		`public class Narrow { static int f(char c) { return g(c++, c); }`+
			` static int g(int a, int b) { return a - b; } }`))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: an assignment to a variable that is already on the stack") {
		t.Errorf("expected the assignment bail:\n%s", source)
	}
}

// An increment is not a value that may be written twice or moved - and it is
// not: an array element or a field assigned while the stack still wants the
// value is written as the assignment it is, in place, and a receiver copied for
// a compound assignment is written once.
const incyBailsSource = `public class IncyBails {
  static int n;
  static int g(int a, int b) { n += a * 100 + b; return a - b; }
  static int nested(int[] a, int i) { a[a[i++]] += 1; return a[0]; }
  static int before(int[] a, int i) { return g(i++ + 1, a[i] = 5); }
  static int field(int i) { return g(i++ + 1, n = i); }
  static int local(int i) { int x = -1; int r = g(i++, x = i); return r + x; }
}`

func TestDecompileKeepsAnIncrementInPlaceBesideAnAssignment(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "IncyBails", incyBailsSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"arg0[arg0[arg1++]] += 1;", "return g(arg1++ + 1, arg0[arg1] = 5);",
		"return g(arg0++ + 1, n = arg0);", "int var2 = g(arg0++, var1 = arg0);",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "IncyBails", source)
	driver := `public class IncyBailsDriver {
  public static void main(String[] z) {
    int[] a = {1, 0, 2};
    System.out.println(IncyBails.nested(a, 0) + " " + a[1] + " " + IncyBails.before(a, 1) + " " + a[2] + " "
      + IncyBails.field(3) + " " + IncyBails.local(4) + " " + IncyBails.n);
  }
}`
	compileWithJavacOn(t, dir, "IncyBailsDriver", driver, dir)
	expected := runJava(t, dir, "IncyBailsDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "IncyBailsDriver")
	if actual != expected || actual != "1 1 -3 5 0 4 813\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// javac pushes the old value and increments behind it, so `i++` is the value on
// top of the stack at the increment. Every shape source can write it in has to
// come back running the same way.
const incySource = `public class Incy {
  static int n;
  static int g(int a, int b) { n += a * 10 + b; return a - b; }
  static int arg(int i) { return g(i++, i); }
  static int index(int[] a, int i) { a[i++] = 7; a[i++] = 8; return i; }
  static String concat(int i) { return "x" + i++ + i; }
  static int self(int i) { i = i++; return i; }
  static int second(int i) { return g(i, i++); }
  static int down(int i) { return g(i--, i); }
  static int twice(int[] a, int i) { return a[i++] + a[i++]; }
}`

const incyDriverSource = `public class IncyDriver {
  public static void main(String[] args) {
    int[] a = new int[6];
    for (int x = 0; x < 4; x++) {
      System.out.println(Incy.arg(x) + " " + Incy.index(a, x) + " " + Incy.concat(x)
        + " " + Incy.self(x) + " " + Incy.second(x) + " " + Incy.down(x)
        + " " + Incy.twice(a, 0) + " " + Incy.n + " " + java.util.Arrays.toString(a));
    }
  }
}`

func TestDecompileWritesTheIncrementTheWaySourceDid(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Incy", incySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	for _, want := range []string{
		"return g(arg0++, arg0);", "return g(arg0, arg0++);", "return g(arg0--, arg0);",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Incy", source)
	compileWithJavacOn(t, dir, "IncyDriver", incyDriverSource, dir)
	expected := runJava(t, dir, "IncyDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "IncyDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

func TestDecompileReconstructsControlFlowFixture(t *testing.T) {
	source := decompileBaseline(t, "ControlFlow")
	// Every shape comes back as the statement it was written as.
	for _, want := range []string{
		"if (arg0 < 0) {",
		"return arg0 >= arg1 && arg0 <= arg2;",
		"while (var2 < arg0) {", // the `for`, whose update is at the bottom
		"while (arg0 > 0) {",
		"} while (var1 < arg0);",
		"java.lang.System.out.println(sum(5));",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("a method still bails:\n%s", source)
	}
}

// The two arms store to the same slot with the same opcode but differently typed
// values - a String and an array, which no open type covers - and the read after
// them wants one variable: the store says so, rather than split it in two and
// leave the read on one arm's.
const ambiguousSlotSource = "class Amb { static java.lang.Object f(boolean c, java.lang.String s, int[] a) {" +
	" java.lang.Object o; if (c) { o = s; } else { o = a; } return o; } }"

func TestDecompileSaysWhenASlotComesFromEitherBranch(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Amb", ambiguousSlotSource))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: a variable that holds values of different types") {
		t.Errorf("expected the ambiguity to be reported in:\n%s", source)
	}
}

func TestDecompileReadsOneVariableWhenTheDebugTableScopesItPerBranch(t *testing.T) {
	// javac (and our emitter with -g) writes a LocalVariableTable row per scope
	// range, so one variable can appear once per arm; name and type say it is one.
	source, err := Decompile(emitClassBytes(t, "Amb", ambiguousSlotSource))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{"java.lang.Object o;", "o = s;", "o = a;", "return o;"} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
	if strings.Contains(source, "o_2") {
		t.Errorf("the variable was split in two:\n%s", source)
	}
	if diagnostics := diagnosticsOf("Amb", source); len(diagnostics) > 0 {
		t.Errorf("diagnostics: %v\n%s", diagnostics, source)
	}
}

// javac lays branches out its own way - our emitter is not the oracle here, the
// real compiler is: decompiled and recompiled, the bytecode has to come back
// identical, instruction for instruction.
const branchySource = `public class Branchy {
  static int clamp(int v, int lo, int hi) { if (v < lo) return lo; if (v > hi) return hi; return v; }
  static boolean between(int v, int lo, int hi) { return v >= lo && v <= hi; }
  static boolean either(int a, int b) { return a > 0 || b > 0; }
  static int max3(int a, int b, int c) { int m = a > b ? a : b; return m > c ? m : c; }
  static int sign(long v) { return v < 0L ? -1 : (v > 0L ? 1 : 0); }
  static int check(int a, java.lang.RuntimeException e) { if (a < 0) throw e; return a; }
  static int both(boolean c) { int x; if (c) { x = 1; } else { x = 2; } return x; }
  static boolean nested(int a, int b, int c) { return (a > 0 && b > 0) || c > 0; }
  static double pick(boolean c, int a, double b) { return c ? a : b; }
  static boolean isNull(java.lang.Object o) { return o == null; }
  static int index(boolean c, int[] a) { a[c ? 0 : 1] = 7; return a[0]; }
  static boolean staleCondition(int a) { boolean b = true; if (b) { return a > 0; } return b; }
  static int numeric(int a) { int x = a > 0 ? 1 : 0; return x + 1; }
  static int counted(boolean q, int[] xs) { return xs[q ? 2 : 0] + (q ? 1 : 0); }
  static int andOr(int a, int b, int c) { if ((a > 0 && b > 0) || c > 0) { return 11; } else { return 22; } }
  static int orAnd(int a, int b, int c) { if (a > 0 || (b > 0 && c > 0)) { return 11; } else { return 22; } }
  static int andGroup(int a, int b, int c) { if (a > 0 && (b > 0 || c > 0)) { return 11; } else { return 22; } }
}`

func TestDecompileRecompilesJavacBranchesToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Branchy", branchySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Branchy", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// Every loop shape javac writes. These reconstruct to source it compiles back to
// the same bytecode from, which is the only oracle that can see an inverted test
// or an arm on the wrong side - our own emitter lays branches out differently.
const loopySource = `public class Loopy {
  static int sum(int n) { int s = 0; for (int i = 0; i < n; i++) { s = s + i; } return s; }
  static int down(int n) { int c = 0; while (n > 0) { c = c + n; n = n - 1; } return c; }
  static int atLeastOnce(int n) { int i = 0; do { i = i + 3; } while (i < n); return i; }
  static int breaks(int[] xs, int stop) { int t = 0; for (int i = 0; i < xs.length; i++) { if (xs[i] == stop) { break; } t = t + xs[i]; } return t; }
  static int forever(int n) { int i = 0; while (true) { i = i + 2; if (i > n) { return i; } } }
  static int both(int a, int b) { int t = 0; while (a > 0 && b > 0) { t = t + 1; a = a - 1; b = b - 2; } return t; }
  static int either(int a, int b) { int t = 0; while (a > 0 || b > 0) { t = t + 1; a = a - 1; b = b - 1; } return t; }
  static int ifInside(int n) { int t = 0; for (int i = 0; i < n; i++) { if (i % 2 == 0) { t = t + i; } else { t = t - i; } } return t; }
  static int untilNull(java.lang.Object o, int n) { int i = 0; while (o == null && i < n) { i = i + 1; } return i; }
  static long longLoop(long n) { long s = 0L; while (s < n) { s = s + 3L; } return s; }
  static int arms(int a, int b) { int t = a; int i = 0; do { i = i + 1; if (i <= a) { t = t * i; if (a >= b) { continue; } } t = t * t; } while (i < b); return t; }
  static int tail(int a, int b) { int u = b; int i = 0; do { i = i + 1; if (i > a) { u = u * u; } else { u = u - a; } u = u * (u + 1); } while (i < a); return u; }
}`

// Every `try` shape javac writes, and the reconstruction has to recompile to the
// same bytecode: clause order, which arm is the body, and where the statement
// ends are all invisible to a text baseline.
const catchySource = `public class Catchy {
  static int one(int a) { try { return 10 / a; } catch (java.lang.ArithmeticException e) { return -1; } }
  static int two(int[] xs, int i) { int r = 0; try { r = xs[i]; } catch (java.lang.ArrayIndexOutOfBoundsException e) { r = -1; } catch (java.lang.NullPointerException e2) { r = -2; } return r; }
  static int multi(int[] xs, int i) { try { return xs[i] / i; } catch (java.lang.ArithmeticException | java.lang.ArrayIndexOutOfBoundsException e) { return e.hashCode(); } }
  static int loopy(int[] xs) { int s = 0; for (int i = 0; i < 10; i++) { try { s = s + xs[i]; } catch (java.lang.RuntimeException e) { break; } } return s; }
  static int continues(int[] xs) { int s = 0; int i = 0; while (i < xs.length) { try { s = s + 10 / xs[i]; } catch (java.lang.ArithmeticException e) { i = i + 1; continue; } i = i + 1; } return s; }
  static int nested(int[] xs) { try { try { return xs[0]; } catch (java.lang.NullPointerException e) { return 1; } } catch (java.lang.RuntimeException e) { return 2; } }
  static void unused(int a) { try { java.lang.System.out.println(a); } catch (java.lang.RuntimeException e) { } }
  static int rethrow(int[] xs) { try { return xs[0]; } catch (java.lang.RuntimeException e) { throw e; } }
  static int afterCatch(int[] xs) { int r = 0; try { return xs[0]; } catch (java.lang.RuntimeException e) { r = 5; } return r + 1; }
  static int twice(int[] xs) { int r = 0; try { r = xs[0]; } catch (java.lang.RuntimeException e) { r = 1; } try { r = r + xs[1]; } catch (java.lang.RuntimeException e2) { r = 2; } return r; }
  static int inIf(boolean c, int[] xs) { if (c) { try { return xs[0]; } catch (java.lang.RuntimeException e) { return -1; } } return 0; }
  static int throwsInside(int a) { try { if (a < 0) { throw new java.lang.IllegalStateException(); } return a; } catch (java.lang.IllegalStateException e) { return -1; } }
  static int alwaysThrows(int a) { try { throw new java.lang.IllegalStateException(); } catch (java.lang.IllegalStateException e) { return a; } }
  static int doWhile(int a) { int d = 0; do { try { check(a); } catch (java.lang.IllegalStateException e) { return -1; } d = d + 1; } while (d < 3); return d; }
  static int handlerBranch(int a) { try { check(a); return 1; } catch (java.lang.IllegalStateException e) { return a > 0 ? 5 : 6; } }
  static int slotAfter(int a) { int r = 0; try { r = 100 / a; } catch (java.lang.ArithmeticException e) { r = -1; } int q = r * 2; return q; }
  static int fallsBack(int n) { int s = 0; for (int i = 0; i < n; i++) { try { check(i); s = s + i; } catch (java.lang.IllegalStateException e) { s = s + 100; } } return s; }
  static int earlyReturn(int n) { try { if (n == 1) { return 1; } check(n); } catch (java.lang.IllegalStateException e) { return 8; } return 9; }
  static int breakInTry(int n) { int s = 0; for (int i = 0; i < n; i++) { try { if (i == 2) { break; } s = s + i; } catch (java.lang.IllegalStateException e) { s = -1; } } return s; }
  static int endExit(boolean c, int n) { try { if (c) { check(n); } else { return 0; } } catch (java.lang.IllegalStateException e) { return 2; } return 3; }
  static void check(int n) { if (n < 0) { throw new java.lang.IllegalStateException(); } }
}`

func TestDecompileRecompilesJavacTryCatchToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Catchy", catchySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Catchy", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

func TestDecompileRecompilesJavacLoopsToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Loopy", loopySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Loopy", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// A local first written inside a loop is declared at the top of the method, so
// the recompiled slots do not line up with javac's - the bytecode is not
// identical, but what it computes has to be. These run instead.
const loopyRunSource = `public class LoopyRun {
  static int nested(int n, int m) { int t = 0; for (int i = 0; i < n; i++) { for (int j = 0; j < m; j++) { t = t + i * j; } } return t; }
  static int continues(int[] xs) { int t = 0; int i = 0; while (i < xs.length) { int v = xs[i]; i = i + 1; if (v < 0) { continue; } t = t + v; } return t; }
  static int windows(int n) { int t = 0; int i = 0; while (i < n) { int step = i % 3 + 1; i = i + step; t = t + step * i; } return t; }
  static int triangle(int n) { int t = 0; int i = 0; do { int row = 0; for (int j = 0; j <= i; j++) { row = row + j; } t = t + row; i = i + 1; } while (i < n); return t; }
  static int breakOut(int a, int b) { int u = b; int i = 0; do { i = i + 1; u = a * 4; if (a == u) { break; } for (int j = 0; j < i; j++) { if (a != u) { u = a + b; } } } while (i < a); return u; }
}`

// The caller stays javac's, so only the class under test is swapped for the
// decompiled one - `main` itself is full of calls, which is a later phase.
const loopyDriverSource = `public class LoopyDriver {
  public static void main(String[] args) {
    int[] xs = { 3, -1, 4, -1, 5, 9, -2, 6 };
    for (int n = -1; n < 6; n++) {
      System.out.println(LoopyRun.nested(n, n + 1) + " " + LoopyRun.continues(xs)
        + " " + LoopyRun.windows(n) + " " + LoopyRun.triangle(n)
        + " " + LoopyRun.breakOut(n, n + 2));
    }
  }
}`

func TestDecompileRunsLikeJavacLoops(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "LoopyRun", loopyRunSource)
	compileWithJavacOn(t, dir, "LoopyDriver", loopyDriverSource, dir)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "LoopyRun", source)
	expected := runJava(t, dir, "LoopyDriver")
	// `again` first, so the decompiled class is the one that runs.
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "LoopyDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Error("the driver printed nothing")
	}
}

// runJava reports what the class's `main` printed.
func runJava(t *testing.T, classPath, name string) string {
	t.Helper()
	out, err := exec.Command("java", "-cp", classPath, name).Output()
	if err != nil {
		t.Fatalf("java: %v", err)
	}
	return string(out)
}

// Every call shape javac writes: static, virtual, interface, private and
// `super`, a `new`, a chain, a call whose value is dropped, and one inside a
// loop. Raw `java.util.List` on purpose - the decompiler works off descriptors,
// so a type argument would come back erased and only the signature would differ.
const callsySource = `public class Callsy {
  private int seed;
  public Callsy(int seed) { this.seed = seed; }
  private int twice(int v) { return v * 2; }
  static int stat(int v) { return v + 1; }
  int use(int v) { return this.twice(v) + stat(v); }
  int chain(String s) { return s.trim().length(); }
  static int iface(java.util.List xs) { return xs.size(); }
  static Object make(int v) { return new Callsy(v); }
  static int viaNew(int v) { return new Callsy(v).seed; }
  static void discard(java.util.List xs) { xs.remove(0); }
  static int nested(int v) { return stat(stat(stat(v))); }
  static String str(Object o) { return o.toString(); }
  static int cmp(String a, String b) { return a.compareTo(b); }
  int loopCall(int n) { int t = 0; for (int i = 0; i < n; i++) { t = t + this.twice(i); } return t; }
  static boolean eq(Object a, Object b) { return a.equals(b); }
  static int len(String s) { if (s == null) { return 0; } return s.length(); }
  int superHash() { return super.hashCode(); }
  static long widen(int v) { return java.lang.Math.abs((long) v); }
  static int both(int a, String s) { if (a > 0 && s.length() > 3) { return 1; } return 0; }
  static int either(String s, int a) { if (s == null || a > 0) { return 1; } return 0; }
  static int untilLen(String s) { int t = 0; while (t < s.length()) { t = t + 2; } return t; }
  static int pick(boolean c, int a) { return c ? stat(a) : stat(-a); }
  static String name(Object o) { return o == null ? "null" : o.toString(); }
  static int callTail(int n) { int t = 0; int i = 0; do { if (i > 1) { t = t + stat(i); } else { t = t - stat(i); } t = t + stat(t); i = i + 1; } while (stat(i) < n); return t; }
}`

func TestDecompileRecompilesJavacCallsToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Callsy", callsySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Callsy", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// Every array-initializer shape javac writes: the `new T[]{...}` form and the
// `{...}` shorthand, primitives of every width, a nested one, an element that is
// a call, a varargs pack, and the sized form that is *not* an initializer.
const arraylySource = `public class Arrayly {
  static int[] ints() { return new int[]{1, 2, 3}; }
  static int[] shorthand() { int[] a = {4, 5}; return a; }
  static int[] empty() { return new int[]{}; }
  static long[] longs() { return new long[]{1L, 2L}; }
  static double[] doubles() { return new double[]{1.5, 2.5}; }
  static boolean[] flags() { return new boolean[]{true, false}; }
  static char[] chars() { return new char[]{'a', 'b'}; }
  static byte[] bytes() { return new byte[]{1, 2}; }
  static short[] shorts() { return new short[]{1, 2}; }
  static float[] floats() { return new float[]{1.5f}; }
  static String[] strings() { return new String[]{"a", "b"}; }
  static Object[] objects() { return new Object[]{null, null}; }
  static int[][] nested() { return new int[][]{{1, 2}, {3}}; }
  static String[][] nestedRefs() { return new String[][]{{null}}; }
  static int sum(int[] a) { return a[0] + a[1]; }
  static int call() { return sum(new int[]{7, 8}); }
  static int[] fromCalls(int n) { return new int[]{sum(new int[]{n, n}), n}; }
  static int[] sized(int n) { int[] a = new int[n]; a[0] = 1; return a; }
  static int[] sizedConst() { int[] a = new int[2]; a[1] = 1; return a; }
  static int[] branchy(boolean c) { return new int[]{c ? 1 : 2, 3}; }
  static String fmt(int a) { return String.format("%d", new Object[]{Integer.valueOf(a)}); }
  static int[][] multi(int n) { return new int[n][2]; }
}`

func TestDecompileRecompilesJavacArrayInitializersToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Arrayly", arraylySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Arrayly", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// Every string concatenation javac writes. The recipe is what says where the
// literal parts sit, and only a recompile can see a misplaced one - so this runs
// the reconstruction back through javac and compares the bytecode.
const concattySource = `public class Concatty {
  static String si(String s, int i) { return s + i; }
  static String is(int i, String s) { return i + s; }
  static String around(String s, int i) { return "x=" + s + ", i=" + i + "!"; }
  static String plain(String s) { return s + ""; }
  static String twoInts(int i, int j) { return "" + i + j; }
  static String objects(Object a, Object b) { return "" + a + b; }
  static String tag(String s) { return "tag\u0001here" + s; }
  static String tags(String s) { return "a\u0002b" + s + "c\u0001d"; }
  static String charConst(String s) { return s + '\n' + "q"; }
  static String nullPart(String s) { return s + null; }
  static String escapes(String s) { return "\"q\"\\\t" + s + "\n"; }
  static String grouped(int i) { return "a" + (i + 1) + "b"; }
  static String append(String s, int n) { s += n; return s; }
  static String types(String s, long l, double d, float f, boolean b, char c, byte y, short h) { return s + l + d + f + b + c + y + h; }
  static String call(String s) { return s + s.length(); }
  static String objectNull(Object o) { return "v" + (Object) null + o; }
  static String stringNull(String s) { return "v" + (String) null + s; }
  static String nested(String a, String b, String c) { return a + b.trim() + c; }
  static int used(String a, int i) { return (a + i).length(); }
  static boolean cond(String a, String b) { return (a + b).isEmpty(); }
  static String loop(int n) { String s = ""; for (int i = 0; i < n; i++) { s = s + i; } return s; }
  static String ternary(boolean c, String a, int i) { return c ? a + i : a + "no"; }
  static String upper(String s) { return ("A" + s).toUpperCase(); }
  static void print(int i) { System.out.println("v" + i); }
}`

func TestDecompileRecompilesJavacStringConcatenationsToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Concatty", concattySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Concatty", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// Every switch shape javac lays out. Only a recompile can see a case written in
// the wrong place, so this runs the reconstruction back through javac and
// compares the bytecode.
const switchySource = `public class Switchy {
  static int dense(int x) { switch (x) { case 1: return 10; case 2: return 20; case 3: return 30; default: return -1; } }
  static int breaks(int x) { int r = 0; switch (x) { case 0: r = 1; break; case 1: r = 2; break; default: r = 9; } return r + 1; }
  static int fall(int x) { int r = 0; switch (x) { case 1: case 2: r += 1; case 3: r += 2; break; case 7: r += 4; break; } return r; }
  static int sparse(int x) { switch (x) { case 100: return 1; case 5000: return 2; case -7: return 3; } return 0; }
  static int noDefault(int x) { int r = 0; switch (x) { case 1: r = 5; break; case 2: r = 6; break; } return r; }
  static int emptyCase(int x) { int r = 3; switch (x) { case 1: break; case 2: r = 7; break; default: r = 8; } return r; }
  static int mixedExit(int x) { int r = 0; switch (x) { case 1: return 100; case 2: r = 2; break; default: r = 3; } return r; }
  static int defaultFirst(int x) { int r = 0; switch (x) { default: r += 1; case 5: r += 2; break; case 9: r += 4; } return r; }
  static int nested(int x, int y) { int r = 0; switch (x) { case 1: switch (y) { case 1: r = 11; break; default: r = 12; } break; case 2: r = 20; break; default: r = 99; } return r; }
  static int withIf(int x, boolean b) { int r = 0; switch (x) { case 1: if (b) { r = 1; } else { r = 2; } break; case 2: if (b) { return -1; } r = 3; break; } return r; }
  static int inWhile(int n) { int r = 0; int i = 0; while (i < n) { switch (i % 2) { case 0: r += 1; break; default: r += 2; } i = i + 1; } return r; }
  static int continues(int n) { int r = 0; int i = 0; while (i < n) { i = i + 1; switch (i % 3) { case 0: continue; case 1: r += 1; break; default: r += 2; } r *= 2; } return r; }
  static int charSwitch(char c) { switch (c) { case 'a': return 1; case 'z': return 26; default: return 0; } }
  static int inTry(int x) { int r = 0; try { switch (x) { case 1: r = 1; break; default: r = 2; } } catch (RuntimeException e) { r = -1; } return r; }
  static int doWhile(int n) { int r = 0; int i = 0; do { switch (i % 3) { case 0: r += 1; break; case 1: r += 2; break; default: r += 3; } r *= 2; i = i + 1; } while (i < n); return r; }
  static int doWhileTail(int n) { int r = 0; int i = 0; do { switch (i % 3) { case 0: r += 1; break; default: r += 3; } i = i + 1; } while (i < n); return r; }
  static int loopTail(int n, int x) { int r = 0; int i = 0; while (i < n) { i = i + 1; switch (x) { case 2: r += 1; break; case 3: r += 1; r += 4; break; default: r += 1; break; case 4: r += 1; } } return r; }
  static int sharedExit(int a, int b) { int r = 0; switch (a) { case 1: switch (b) { case 0: r += 2; case 1: r += 1; break; case 3: return r; } case 4: r += 9; return r; } return r; }
  static String str(String s) { switch (s) { case "a": return "A"; case "b": return "B"; default: return "?"; } }
}`

// javac compiles a lambda into a synthetic method plus an invokedynamic that
// `LambdaMetafactory` turns into the interface; a method reference points the
// same call site straight at the method. The body comes back inlined, so javac
// generates the same method from it again.
const lammySource = `import java.util.*;
import java.util.function.*;
public class Lammy {
  int base = 5;
  static int stat = 7;
  Runnable noCapture() { return () -> System.out.print("x"); }
  Runnable capture(int n) { return () -> System.out.print(n); }
  Supplier<Integer> field() { return () -> base; }
  IntUnaryOperator math(int k) { return x -> x * k + base; }
  Function<String, Integer> unboundRef() { return String::length; }
  Supplier<String> boundRef(String s) { return s::trim; }
  Supplier<Object> ctorRef() { return Object::new; }
  static Runnable staticRef() { return Lammy::helper; }
  static void helper() {}
  BiFunction<Integer, Integer, Integer> two(int k) { return (a, b) -> a + b + k; }
  int localCapture(int n) { int k = n * 2; Supplier<Integer> s = () -> k + base; return s.get(); }
  Supplier<Integer> block(int n) { return () -> { int t = n; t = t * 3; return t + base; }; }
  Function<Integer, Supplier<Integer>> nested(int n) { return a -> () -> a + n; }
  int stream(List<String> xs) { return xs.stream().map(String::length).reduce(0, Integer::sum); }
  Comparator<String> comparator() { return (a, b) -> a.length() - b.length(); }
  Runnable staticField() { return () -> stat++; }
  Runnable throwing() { return () -> { throw new RuntimeException("x"); }; }
  Runnable declaring() { return () -> { int q = 3; System.out.print(q); }; }
  String receiver() { return ((Supplier<String>) () -> "sup").get(); }
  Object arm(boolean c) { return c ? (Runnable) () -> System.out.print("1") : (Runnable) () -> System.out.print("2"); }
}`

const lammyDriverSource = `import java.util.*;
public class LammyDriver {
  public static void main(String[] args) {
    List<String> xs = Arrays.asList("a", "bb", "", "cccc");
    for (int n = 0; n < 4; n++) {
      Lammy l = new Lammy();
      l.noCapture().run();
      l.capture(n).run();
      l.staticRef().run();
      l.staticField().run();
      System.out.println(" " + l.field().get() + " " + l.math(n).applyAsInt(3)
        + " " + l.unboundRef().apply("abcd") + " " + l.boundRef(" q ").get()
        + " " + l.ctorRef().get().getClass() + " " + l.two(n).apply(1, 2)
        + " " + l.localCapture(n) + " " + l.block(n).get()
        + " " + l.nested(n).apply(3).get() + " " + l.stream(xs)
        + " " + l.comparator().compare("aa", "b"));
    }
  }
}`

func TestDecompileReconstructsJavacLambdas(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Lammy", lammySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	// A reference where source wrote one, a lambda where the body is inlined,
	// and nothing left of the synthetic method javac generated.
	if !strings.Contains(source, "java.lang.Object::new") || !strings.Contains(source, "Lammy::helper") ||
		!strings.Contains(source, "() -> java.lang.System.out.print(arg0)") ||
		strings.Contains(source, "lambda$") {
		t.Fatalf("the lambdas did not come back:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Lammy", source)
	compileWithJavacOn(t, dir, "LammyDriver", lammyDriverSource, dir)
	expected := runJava(t, dir, "LammyDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "LammyDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// A lambda that captures a variable this hoisted to the top of the method is not
// effectively final, and Java takes no other kind.
const hoistedCaptureSource = `import java.util.function.*;
public class Hoisted {
  static int f(int n) { int t = 0; for (int i = 0; i < n; i++) { int j = i; Supplier<Integer> s = () -> j * 2; t += s.get(); } return t; }
}`

func TestDecompileSaysWhenALambdaCaptureCannotBeFinal(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Hoisted", hoistedCaptureSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: a lambda that captures a variable that is not final") {
		t.Errorf("expected the bail, got:\n%s", source)
	}
}

// A `while (true)` opens with the block a `continue` jumps to, and a
// `synchronized` inside one keeps the `return` javac writes in it: neither is
// where the loop ends.
const foreverSource = `public class Forever {
  static final Object L = new Object();
  static int broke(int n) { int r = 0; while (true) { r += n; if (r > 100) { break; } n++; } return r; }
  static int held(int p) { int n = p; while (true) { synchronized (L) { if (n > 3) { return n; } n = n + 1; } } }
  static int leaves(int p) { int n = p; for (;;) { synchronized (L) { if (n > 3) { break; } } n = n + 1; } return n; }
  static int inside(int p) { int n = p; synchronized (L) { while (n < 4) { n = n + 1; } n = n * 2; } return n; }
}`

const foreverDriverSource = `public class ForeverDriver {
  public static void main(String[] args) {
    for (int p = 0; p < 7; p++) {
      System.out.println(Forever.broke(p) + " " + Forever.held(p) + " " + Forever.leaves(p)
        + " " + Forever.inside(p));
    }
  }
}`

func TestDecompileReconstructsForeverLoops(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Forever", foreverSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	if !strings.Contains(source, "while (true) {") {
		t.Errorf("the loop did not come back:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Forever", source)
	compileWithJavacOn(t, dir, "ForeverDriver", foreverDriverSource, dir)
	expected := runJava(t, dir, "ForeverDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "ForeverDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// The guards a reconstruction rests on, each of which said nothing when it was
// removed: a `Serializable` lambda is `altMetafactory` and carries flags this
// drops; one statement that is a *declaration* still needs the braces; a captured
// field is read again every time the lambda runs, where javac read it once; and a
// loop over a `try` over a `synchronized` needs the handler's edge to be
// reducible at all.
const guardSource = `import java.io.Serializable;
import java.util.function.*;
public class Guard {
  static final Object L = new Object();
  String name = "one";
  interface SRun extends Runnable, Serializable {}
  static SRun serial() { return (SRun) () -> System.out.print("s"); }
  static Runnable declaring() { return () -> { int q = 3; }; }
  Supplier<String> boundField() { return name::toUpperCase; }
  static int hooks(int n) { int r = 0; for (int i = 0; i < n; i++) { synchronized (L) { if (i == 2) { return r; } r += i; } } return r; }
  static final int[] taken = { 1, 0, 3 };
  static int runHooks() { int r = 0; for (int i = 0; i < taken.length; i++) { try { int hook; synchronized (L) { hook = taken[i]; } if (hook != 0) { r += 10 / hook; } } catch (RuntimeException t) { r -= 1; } } return r; }
}`

func TestDecompileKeepsTheLambdaAndMonitorGuards(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Guard", guardSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"cappu: an invokedynamic that is neither a lambda nor a concatenation",
		"cappu: a lambda that captures more than a variable",
		"() -> {",
		"synchronized (L) {",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
	if strings.Contains(source, "cappu: irreducible control flow") {
		t.Errorf("the monitor handler's edge is missing:\n%s", source)
	}
}

// javac writes `synchronized` as a monitor held in a synthetic local, guarded by
// a catch-all that releases it and rethrows - and splits the range around every
// `return`, `break` and `continue` that leaves the statement.
const syncySource = `public class Syncy {
  private final Object lock = new Object();
  int n;
  int simple() { synchronized (lock) { n = n + 1; } return n; }
  int early(int x) { synchronized (lock) { if (x > 0) { return 1; } n = n + 2; } return n; }
  int onThis() { synchronized (this) { n = n + 3; } return n; }
  int nested(Object other) { synchronized (lock) { synchronized (other) { n = n + 4; } } return n; }
  int allReturn() { synchronized (lock) { return n; } }
  int throwing() { synchronized (lock) { if (n == 0) { throw new IllegalStateException("x"); } return n; } }
  int withTry() { synchronized (lock) { try { return Integer.parseInt("7"); } catch (NumberFormatException e) { return -1; } } }
  int inTry() { try { synchronized (lock) { n = n + 1; } } catch (RuntimeException e) { return -1; } return n; }
  static int stat(Object o) { synchronized (o) { return o.hashCode(); } }
  int twice() { synchronized (lock) { n = n + 1; } synchronized (lock) { n = n + 2; } return n; }
  int reused(Object o) { synchronized (o) { n = n + 1; } String s = "hello"; return s.length() + n; }
  int shared(Object o) { String before = "a"; synchronized (o) { n = before.length(); } String after = "bb"; return n + after.length(); }
  int breakOut(int k) { int r = 0; for (int i = 0; i < k; i++) { synchronized (lock) { if (i == 2) { break; } r = r + i; } } return r; }
  int continueOut(int k) { int r = 0; for (int i = 0; i < k; i++) { synchronized (lock) { if (i == 2) { continue; } r = r + i; } r = r * 2; } return r; }
  synchronized int flagged() { return n; }
}`

func TestDecompileRecompilesJavacSynchronizedToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Syncy", syncySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	if !strings.Contains(source, "synchronized (this.lock) {") || strings.Contains(source, "monitorexit") {
		t.Errorf("the monitor is not written as a statement:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Syncy", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// A hand-written accessor or canonical constructor may be *smaller* than the one
// javac generates, so only their shape tells them apart: reading another
// component, or storing in another order, is the source's and stays.
const recordKeptSource = `public class Kept {
  public record Accessor(int x, int y) { public int x() { return y; } }
  public record Swapped(int x, int y) { public Swapped(int x, int y) { this.x = y; this.y = x; } }
  public record Negated(int x) { public Negated(int x) { this.x = -x; } }
  public record Wide(long a, String b, double c) { public int extra() { return 1; } }
}`

func TestDecompileKeepsARecordMemberThatOnlyLooksGenerated(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	compileWithJavac(t, dir, "Kept", recordKeptSource)
	for _, one := range []struct{ name, want string }{
		{"Kept$Accessor", "return this.y;"},
		{"Kept$Swapped", "this.x = y;"},
		{"Kept$Negated", "this.x = -x;"},
	} {
		source, err := Decompile(readFile(t, filepath.Join(dir, one.name+".class")))
		if err != nil {
			t.Fatalf("decompile %s: %v", one.name, err)
		}
		if !strings.Contains(source, one.want) {
			t.Errorf("%s dropped a member that is the source's:\n%s", one.name, source)
		}
	}
	// The generated members of a record whose components are wide are still
	// recognised - the slots they load from are two apart.
	wide, err := Decompile(readFile(t, filepath.Join(dir, "Kept$Wide.class")))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(wide, "record Kept$Wide(long a, java.lang.String b, double c)") ||
		strings.Contains(wide, "public long a()") {
		t.Errorf("the wide record did not come back as a header:\n%s", wide)
	}
}

// An assignment written out as a statement runs in front of everything the stack
// already holds, and a value that reads a field or an array element may be the
// one being written - under this name or another. So one the stack still wants
// is not written out: it stays the expression it is, where source put it.
const aliasSource = `public class Aliased {
  static int[] a = { 0, 0, 0 };
  int x;
  static int reads;
  static int rd() { reads++; return a[0]; }
  static int aliasArray() { int[] b = a; a[0] = 1; return a[0] + (b[0] = 5); }
  static int sameIndex() { int i = 0, j = 0; a[1] = 1; return a[i] + (a[j] = 7); }
  int sameObject(Aliased that) { this.x = 1; return this.x + (that.x = 9); }
  static int throughCall() { return rd() + (a[0] = 7); }
  static int chained() { int p, q; p = q = 5; return p + q; }
}`

func TestDecompileKeepsAnAssignmentWhereTheStackWantsIt(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Aliased", aliasSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"return a[0] + (var0[0] = 5);", "return a[var0] + (a[var1] = 7);",
		"return this.x + (arg0.x = 9);", "return rd() + (a[0] = 7);", "int var0 = var1 = 5;",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Aliased", source)
	driver := `public class AliasedDriver {
  public static void main(String[] z) {
    System.out.println(Aliased.aliasArray() + " " + Aliased.sameIndex() + " " + new Aliased().sameObject(new Aliased())
      + " " + Aliased.throughCall() + " " + Aliased.chained() + " " + Aliased.reads);
  }
}`
	compileWithJavacOn(t, dir, "AliasedDriver", driver, dir)
	expected := runJava(t, dir, "AliasedDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "AliasedDriver")
	if actual != expected || actual != "6 12 10 14 10 1\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// A record declares its state in the header, and javac writes the accessors, the
// canonical constructor and `equals`/`hashCode`/`toString` (through the
// `ObjectMethods` bootstrap) from it. Those come back as the header; anything the
// source added stays.
const recordSource = `public record Recordy(int x, String name, long[] data) {
  static int made;
  public Recordy {
    if (x < 0) { throw new IllegalArgumentException("x"); }
  }
  public int twice() { return x * 2; }
  public static Recordy of(int x) { made++; return new Recordy(x, "n", new long[] { 1L }); }
}`

const recordyDriverSource = `public class RecordyDriver {
  public static void main(String[] args) {
    for (int i = 0; i < 3; i++) {
      Recordy r = Recordy.of(i);
      System.out.println(r + " " + r.x() + " " + r.name() + " " + r.twice()
        + " " + r.hashCode() + " " + r.equals(Recordy.of(i)) + " " + Recordy.made);
    }
    try { new Recordy(-1, "n", null); } catch (RuntimeException e) {
      System.out.println("caught " + e.getMessage());
    }
  }
}`

func TestDecompileReconstructsARecord(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Recordy", recordSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	if !strings.Contains(source, "public record Recordy(int x, java.lang.String name, long[] data) {") {
		t.Fatalf("the header did not come back:\n%s", source)
	}
	// The three `ObjectMethods` members and the accessors are the header, and a
	// component may not be declared as a field on top of it.
	if strings.Contains(source, "hashCode()") || strings.Contains(source, "private final int x;") {
		t.Errorf("a generated member is still written:\n%s", source)
	}
	// The canonical constructor did more than store, so it stays - named after
	// the components, which Java checks.
	if !strings.Contains(source, "public Recordy(int x, java.lang.String name, long[] data) {") {
		t.Errorf("the canonical constructor is missing or unnamed:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Recordy", source)
	compileWithJavacOn(t, dir, "RecordyDriver", recordyDriverSource, dir)
	expected := runJava(t, dir, "RecordyDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "RecordyDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// javac writes a compound assignment by *copying* the target on the stack -
// `dup2` for an array element, `dup` for a field - and reads it back through the
// copy. The long form is what comes back, which is the same thing as long as the
// array and the index are read the same way twice.
const compoundSource = `public class Compound {
  static int[] a = { 1, 2, 3 };
  static long[] longs = { 1L };
  int n;
  static int s;
  static void arrPlus(int i, int x) { a[i] += x; }
  static void arrInc(int i) { a[i]++; }
  static void arrPre(int i) { ++a[i]; }
  static void arrShift(int i) { a[i] <<= 2; }
  static void longPlus(int i, long x) { longs[i] += x; }
  void fieldPlus(int x) { n += x; }
  void fieldInc() { n++; }
  static void statPlus(int x) { s += x; }
  static void statInc() { s++; }
}`

const compoundDriverSource = `public class CompoundDriver {
  public static void main(String[] args) {
    for (int i = 0; i < 3; i++) {
      Compound c = new Compound();
      Compound.arrPlus(i, 5); Compound.arrInc(i); Compound.arrPre(i);
      Compound.arrShift(i); Compound.longPlus(0, 7L);
      c.fieldPlus(3); c.fieldInc(); Compound.statPlus(2); Compound.statInc();
      System.out.println(Compound.a[0] + " " + Compound.a[1] + " " + Compound.a[2]
        + " " + c.n + " " + Compound.s + " " + Compound.longs[0]);
    }
  }
}`

// The *value* of a post-increment is the old one, and the long form reads the new
// one: a field's comes back as the `n++` it was; an array element's would need
// the assignment to stay an expression, which it does not.
const compoundBailsSource = `public class Both {
  static int[] a = { 1, 2, 3 };
  int n;
  static int arrValue(int i) { return a[i]++; }
  int fieldValue() { return n++; }
}`

func TestDecompileReconstructsCompoundAssignment(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Compound", compoundSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	if !strings.Contains(source, "a[arg0] = a[arg0] + arg1;") {
		t.Errorf("the compound assignment did not come back:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Compound", source)
	compileWithJavacOn(t, dir, "CompoundDriver", compoundDriverSource, dir)
	expected := runJava(t, dir, "CompoundDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "CompoundDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

func TestDecompileSaysWhenAPostIncrementValueIsUsed(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Both", compoundBailsSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if count := strings.Count(source, "an assignment with a value that could see it on the stack"); count != 1 {
		t.Errorf("expected one guarded method, got %d:\n%s", count, source)
	}
	if !strings.Contains(source, "return this.n++;") {
		t.Errorf("expected the field's post-increment:\n%s", source)
	}
}

// javac writes the body of a `finally` twice - once on the way out of the
// protected range, once in the catch-all that rethrows - and a `return` inside
// the body is one more copy. One way out is what this reads back; the rest say so.
const finallySource = `public class Finallies {
  static int n;
  static int simple(int x) { int r = 0; try { r = 10 / x; } finally { n += 1; } return r; }
  static int several(int x) { int r = 0; try { r = 10 / x; n += 2; } finally { System.out.print(""); } return r; }
  static int inLoop(int x) { int r = 0; for (int i = 0; i < x; i++) { try { r += 10 / (x - i); } finally { n += 5; } } return r; }
  static int returning(int a) { try { return a * 2; } finally { n += 6; } }
  static String returningRef(String s) { try { return s.trim(); } finally { n += 7; } }
}`

const finalliesDriverSource = `public class FinalliesDriver {
  public static void main(String[] args) {
    for (int x = -2; x < 4; x++) {
      String line;
      try {
        line = Finallies.simple(x) + " " + Finallies.several(x)
          + " " + Finallies.inLoop(x) + " " + Finallies.returning(x)
          + " " + Finallies.returningRef(" q ");
      } catch (RuntimeException e) { line = "ex"; }
      System.out.println(line + " " + Finallies.n);
    }
  }
}`

const finallyBailsSource = `public class Bails {
  static int n;
  static int caught(int x) { int r = 0; try { r = 10 / x; } catch (ArithmeticException e) { r = -1; } finally { n += 2; } return r; }
  static int nested(int x) { int r = 0; try { try { r = 10 / x; } finally { n += 3; } } finally { n += 4; } return r; }
  static int ifRet(int x) { try { if (x > 0) return 1; } finally { n += 5; } return 0; }
}`

func TestDecompileReconstructsAFinallyWithOneWayOut(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Finallies", finallySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	if !strings.Contains(source, "} finally {") {
		t.Fatalf("the statement did not come back:\n%s", source)
	}
	// The copy javac wrote on the way out is not a statement of its own.
	if strings.Count(source, "n = n + 1;") != 1 {
		t.Errorf("the copy on the way out is still there:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "Finallies", source)
	compileWithJavacOn(t, dir, "FinalliesDriver", finalliesDriverSource, dir)
	expected := runJava(t, dir, "FinalliesDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "FinalliesDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

func TestDecompileSaysWhenAFinallyIsWrittenMoreThanTwice(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Bails", finallyBailsSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	// A `catch` beside the `finally` and a `finally` inside one are each another
	// copy, and which one source wrote is not in the class file: both say so.
	if !strings.Contains(source, "cappu: a finally or synchronized block") {
		t.Errorf("expected the bail, got:\n%s", source)
	}
	// A second way out of the protected range is a copy of the body this cannot
	// tell from a statement source wrote.
	if !strings.Contains(source, "cappu: a finally with more than one way out") {
		t.Errorf("expected the second-way-out bail, got:\n%s", source)
	}
	if strings.Count(source, "cappu: ") != 6 {
		t.Errorf("expected three bailed methods, got:\n%s", source)
	}
}

// javac lays a `for` out with the test at the top and the update at the bottom,
// and a `continue` jumps to that update - which only the `for` form can say.
const forrySource = `public class Forry {
  static int simple(int n) { int r = 0; for (int i = 0; i < n; i++) { if (i % 3 == 1) { r += 5; continue; } r += 1; r *= 2; } return r; }
  static int inSwitch(int n) { int r = 0; for (int i = 0; i < n; i++) { switch (i % 3) { case 0: r += 1; break; case 1: continue; default: r += 3; } r *= 2; } return r; }
  static int inSwitchTry(int n) { int r = 0; for (int i = 0; i < n; i++) { switch (i % 2) { case 0: continue; default: r += 3; } } return r; }
  static int whileForm(int n) { int r = 0; int i = 0; while (i < n) { if (i % 2 == 0) { r += 1; } else { r += 2; } i++; } return r; }
  static int switchExits(int n, int x) { int r = 0; for (int i = 0; i < n; i++) { switch (x) { case 1: continue; case 2: r += 1; break; case 3: return -1; default: r += 3; } r *= 2; } return r; }
}`

func TestDecompileRecompilesJavacForLoopsToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Forry", forrySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	if !strings.Contains(source, "for (; var2 < arg0; var2++) {") {
		t.Errorf("expected the `for` form, got:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Forry", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// The update of a `for` is a list of expressions. When the block at the bottom of
// the body is not that - an allocation whose value is dropped, or an assignment a
// later retype still has to reach - it is not an update clause, and the
// statements belong at the end of the body, where the `while` form puts them.
const notAnUpdateSource = `public class NotAnUpdate {
  static int dropped(int n, StringBuilder out) { int r = 0; int i = 0; while (i < n) { if (i % 2 == 0) { r += 1; } else { r += 2; } new StringBuilder("x").append(i).toString(); out.append(i); i++; } return r; }
  static boolean retyped(int n) { boolean b = false; int i = 0; while (i < n) { if (i % 2 == 0) { i += 1; } else { i += 3; } b = true; i++; } return b; }
}`

func TestDecompileKeepsALoopTailThatIsNotAnUpdateClause(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "NotAnUpdate", notAnUpdateSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	// The dropped allocation is a statement of the body, not an update.
	if !strings.Contains(source, ".toString();") || !strings.Contains(source, "var1 = true;") {
		t.Errorf("the loop tail is missing:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "NotAnUpdate", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// A `continue` whose arm is the whole `if` comes back as the inverted test that
// runs the rest - the same thing, other bytecode - and a nested loop's variable
// is hoisted, so these can only be judged by running them.
const forryRunSource = `public class ForryRun {
  static int twoUpdates(int n) { int r = 0; for (int i = 0, j = n; i < j; i++, j--) { if (i == 2) { continue; } r += i * j; } return r; }
  static int twoContinues(int n) { int r = 0; for (int i = 0; i < n; i++) { if (i == 1) { continue; } if (i == 3) { r += 7; continue; } r += 1; } return r; }
  static int nested(int n) { int r = 0; for (int i = 0; i < n; i++) { for (int j = 0; j < n; j++) { if (j == 1) { continue; } r += i + j; } r += 1; } return r; }
  static int inWhile(int n) { int r = 0; int i = 0; while (i < n) { i = i + 1; if (i == 2) { continue; } r += i; } return r; }
  static int search(int[] a, int key) { int low = 0; int high = a.length - 1; while (low <= high) { int mid = (low + high) >>> 1; if (a[mid] < key) { low = mid + 1; } else if (a[mid] > key) { high = mid - 1; } else { return mid; } } return -(low + 1); }
}`

const forryDriverSource = `public class ForryDriver {
  public static void main(String[] args) {
    for (int n = 0; n < 8; n++) {
      System.out.println(n + " " + ForryRun.twoUpdates(n) + " " + ForryRun.twoContinues(n)
        + " " + ForryRun.nested(n) + " " + ForryRun.inWhile(n)
        + " " + ForryRun.search(new int[] { 0, 2, 4, 6, 8 }, n));
    }
  }
}`

func TestDecompileRunsLikeJavacForLoops(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "ForryRun", forryRunSource)
	compileWithJavacOn(t, dir, "ForryDriver", forryDriverSource, dir)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "ForryRun", source)
	expected := runJava(t, dir, "ForryDriver")
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "ForryDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// The same trap one level out from `i++`: `arr[idx++]` where `idx` is a *field*
// is a getstatic/dup/putstatic (a getfield under a dup_x1 for an instance
// field), and writing the assignment out first would make the read take the
// new value. The copy the dup left is the old value, so it is the `idx++`; a
// byte, short or char field's carries javac's narrowing, and one whose value
// is the *new* one stays the assignment it is. Anything else on the stack
// still keeps the store from becoming a statement.
const fieldPostIncrementSource = `public class FieldPost {
  static int[] arr = { 5, 6, 7 };
  static int idx = 0;
  int pos; byte b; char c = 'a'; long n; short sh;
  static int f() { int v = arr[idx++]; return v * 100 + idx; }
  int read() { return arr()[pos++] & 0xff; }
  int[] arr() { return arr; }
  int dec() { return arr[--pos + 1] + pos--; }
  int old() { return pos + pos++; }
  int fresh() { return pos + (pos += 1); }
  int both() { return pos++ + pos++; }
  byte bb() { return b++; }
  char cc() { return c++; }
  short ss() { return ss(sh--); }
  short ss(short v) { return v; }
  long ll() { return n++ + n--; }
  int other(FieldPost q) { return q.pos++ + q.arr()[q.pos % 3]; }
  int stays() { int k = pos; pos = pos + 2; return k + pos; }
  public static void main(String[] z) {
    FieldPost p = new FieldPost();
    System.out.println(f() + " " + p.read() + " " + p.read() + " " + p.dec() + " " + p.old() + " " + p.fresh() + " " + p.both()
      + " " + p.bb() + " " + p.cc() + " " + p.ss() + " " + p.ll() + " " + p.other(p) + " " + p.stays()
      + " " + idx + " " + p.pos + " " + p.b + " " + p.c + " " + p.sh + " " + p.n);
  }
}`

func TestDecompileWritesAFieldPostIncrementAsItsValue(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "FieldPost", fieldPostIncrementSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, want := range []string{
		"arr[idx++]", "this.arr()[this.pos++] & 255", "this.pos + this.pos++", "this.pos + (this.pos = this.pos + 1)",
		"this.pos++ + this.pos++", "return this.b++;", "return this.c++;", "this.ss(this.sh--)",
		"this.n++ + this.n--", "arg0.pos++ + arg0.arr()[arg0.pos % 3]", "this.pos = this.pos + 2;",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "/* cappu:") {
		t.Errorf("expected no bail:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "FieldPost", source)
	expected := runJava(t, dir, "FieldPost")
	actual := runJava(t, again, "FieldPost")
	if actual != expected || actual != "501 5 6 8 0 3 5 0 a 0 1 11 12 1 7 1 b -1 0\n" {
		t.Errorf("the decompiled class runs differently: %q vs %q", actual, expected)
	}
}

// javac puts the tail of a `do` body in the same block as the test, and the jump
// that leaves the inner `switch` lands there: `continue;` would skip the tail, so
// this says so instead of writing one.
const doTailSource = `public class DoTail {
  static int f(int n) { int r = 0; int i = 0; do { switch (i % 2) { case 0: switch (r % 3) { case 0: r += 1; break; case 1: return -1; default: r += 4; break; } break; default: r += 100; } i++; } while (i < n); return r; }
}`

func TestDecompileSaysWhenAJumpLandsInTheTailOfADoWhile(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "DoTail", doTailSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: a jump into the tail of a do-while") {
		t.Errorf("expected the bail, got:\n%s", source)
	}
}

// javac writes a `switch` over an enum from another file as a lookup through a
// synthetic `$SwitchMap$` array, held by an anonymous class no source can name.
const enumSource = "public enum Colour { RED, GREEN, BLUE }\n"

const enumSwitchSource = `public class Painter {
  static int f(Colour c) { switch (c) { case RED: return 1; case GREEN: return 2; default: return 0; } }
}`

func TestDecompileSaysWhenASwitchReadsTheEnumLookupTable(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	compileWithJavac(t, dir, "Colour", enumSource)
	classFile := compileWithJavacOn(t, dir, "Painter", enumSwitchSource, dir)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: an enum switch") {
		t.Errorf("expected the bail, got:\n%s", source)
	}
}

func TestDecompileRecompilesJavacSwitchesToTheSameBytecode(t *testing.T) {
	if !hasTool("javac") || !hasTool("javap") {
		t.Skip("no JDK (javac/javap)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Switchy", switchySource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Switchy", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
}

// A loop inside a `case` declares its variable there, which the reconstruction
// hoists to the top of the method - the slots shift, so only running it can say
// the two are the same.
const switchyRunSource = `public class SwitchyRun {
  static int loopInside(int x, int n) { int r = 0; switch (x) { case 1: for (int i = 0; i < n; i++) { if (i == 3) { break; } r += i; } break; default: r = -1; } return r; }
  static int doInside(int x, int n) { int r = 0; switch (x) { case 1: { int i = 0; do { r += i; i = i + 1; } while (i < n); break; } default: r = 7; } return r; }
  static int tryInside(int x) { switch (x) { case 1: try { return Integer.parseInt("nope"); } catch (NumberFormatException e) { return -1; } default: return 0; } }
}`

const switchyDriverSource = `public class SwitchyDriver {
  public static void main(String[] args) {
    for (int x = -1; x < 4; x++) {
      for (int n = 0; n < 5; n++) {
        System.out.println(SwitchyRun.loopInside(x, n) + " " + SwitchyRun.doInside(x, n)
          + " " + SwitchyRun.tryInside(x));
      }
    }
  }
}`

func TestDecompileRunsLikeJavacSwitches(t *testing.T) {
	if !hasTool("javac") || !hasTool("java") {
		t.Skip("no JDK (javac/java)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "SwitchyRun", switchyRunSource)
	compileWithJavacOn(t, dir, "SwitchyDriver", switchyDriverSource, dir)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if strings.Contains(source, "/* cappu:") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	again := filepath.Join(dir, "again")
	compileWithJavac(t, again, "SwitchyRun", source)
	expected := runJava(t, dir, "SwitchyDriver")
	// `again` first, so the decompiled class is the one that runs.
	actual := runJava(t, again+string(os.PathListSeparator)+dir, "SwitchyDriver")
	if actual != expected {
		t.Errorf("the decompiled class runs differently:\n%s\n--- from ---\n%s", actual, expected)
	}
	if expected == "" {
		t.Fatal("the driver printed nothing")
	}
}

// A static nested class is `new Outer.Inner(...)`; a true inner one is only
// writable as `outer.new Inner(...)`, which needs the enclosing file. The
// InnerClasses attribute of *this* file is what tells them apart - the first
// constructor parameter cannot, since a static one may take the outer type too.
func TestDecompileTellsAStaticNestedClassFromAnInnerOne(t *testing.T) {
	program := NewProgram()
	LoadJdkStub(program)
	uri := URI("file:///Nested.java")
	program.SetOpenDocument(uri, "public class Nested {"+
		" static class St { int v; St(Nested n, int v) { this.v = v; } }"+
		" class In { int v; In(int v) { this.v = v; } }"+
		" static int useStatic(Nested n) { return new St(n, 3).v; }"+
		" int useInner() { return new In(4).v; } }", 1)
	classes := EmitSourceFile(program.GetSourceFile(uri), program, NewChecker(program), false)
	for _, c := range classes {
		if c.Name != "Nested" {
			continue
		}
		source, err := Decompile(c.Bytes)
		if err != nil {
			t.Fatalf("decompile: %v", err)
		}
		// The emitter passes an inner class no enclosing instance, so its `new`
		// reads the same as a static one's, and the argument stays an argument.
		for _, want := range []string{"new Nested.St(arg0, 3)", "new Nested.In(4)"} {
			if !strings.Contains(source, want) {
				t.Errorf("missing %q in:\n%s", want, source)
			}
		}
		return
	}
	t.Fatal("class Nested was not emitted")
}

func TestDecompileWritesNestedTypeReferencesWithADot(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Outer",
		"class Outer { static class Inner {} static Inner get() { return null; } }"))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "Outer.Inner get()") {
		t.Errorf("nested reference kept its binary name:\n%s", source)
	}
}

func TestDecompileChainsToTheSuperConstructor(t *testing.T) {
	program := NewProgram()
	LoadJdkStub(program)
	uri := URI("file:///Base.java")
	program.SetOpenDocument(uri, "class Base { int v; Base(int v) { this.v = v; } }"+
		" class Sub extends Base { Sub(int x) { super(x); } }", 1)
	classes := EmitSourceFile(program.GetSourceFile(uri), program, NewChecker(program), false)
	for _, c := range classes {
		if !strings.HasSuffix(c.Name, "Sub") {
			continue
		}
		source, err := Decompile(c.Bytes)
		if err != nil {
			t.Fatalf("decompile: %v", err)
		}
		if !strings.Contains(source, "super(arg0);") {
			t.Errorf("missing the chain call:\n%s", source)
		}
		return
	}
	t.Fatal("class Sub was not emitted")
}

// --- the JDK as a corpus -------------------------------------------------------------

// A prologue statement that can throw is not movable across the `super()`:
// `Object`'s constructor is where an object is registered for finalization, so
// one that throws in front of it leaves an object that never was, and behind it
// one that is. Newer javacs than this one null-check the enclosing instance
// there, so the trigger has to come from the JDK on PATH.
func TestDecompileDropsTheNullCheckBeforeAnInnerClassSuper(t *testing.T) {
	jmod := jmodOf("java.desktop")
	if jmod == "" {
		t.Skip("no JDK with jmods/")
	}
	var bytes []byte
	for _, entry := range readJmodEntries(jmod) {
		if entry.Name == "classes/javax/swing/text/StringContent$StickyPosition.class" {
			bytes = entry.Read()
		}
	}
	if bytes == nil {
		t.Skip("the class is not in this image")
	}
	// Only this javac's layout is the point; one that does not null-check there
	// has nothing to say.
	text, err := Disassemble(bytes)
	if err != nil || !strings.Contains(text, "requireNonNull") {
		t.Skip("this javac does not null-check the enclosing instance")
	}
	source, err := Decompile(bytes)
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	// The `dup; requireNonNull; pop` is no statement of source's: dropped, the
	// prologue is movable across the `super()`.
	if strings.Contains(source, "/* cappu:") || strings.Contains(source, "requireNonNull") ||
		!strings.Contains(source, "this$0.marks.addElement(this.rec);") {
		t.Errorf("expected the null check dropped and the body reconstructed:\n%s", source)
	}
}

// A `ConstantValue` on an *instance* field is ignored by the JVM: javac assigns
// the value in the constructor instead, so writing both is the assignment twice
// - and on a `final` field the second one does not compile.
func TestDecompileDoesNotWriteAConstantValueOnAnInstanceField(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Cvi",
		`public class Cvi { final int M = 10; static final int S = 20; }`))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "static final int S = 20;") {
		t.Errorf("the static ConstantValue is missing:\n%s", source)
	}
	if strings.Contains(source, "int M = 10;") {
		t.Errorf("the instance ConstantValue came back:\n%s", source)
	}
}

// jmodOf is a module of the JDK on PATH (or JAVA_HOME), when it ships the jmods/
// a class corpus needs.
func jmodOf(module string) string {
	home := os.Getenv("JAVA_HOME")
	if home == "" {
		javac, err := exec.LookPath("javac")
		if err != nil {
			return ""
		}
		resolved, err := filepath.EvalSymlinks(javac)
		if err != nil {
			return ""
		}
		home = filepath.Dir(filepath.Dir(resolved))
	}
	jmod := filepath.Join(home, "jmods", module+".jmod")
	if _, err := os.Stat(jmod); err != nil {
		return ""
	}
	return jmod
}

// java.base is built with `-XDstringConcat=inline` - it holds StringConcatFactory
// itself - so it contains almost no concatenation invokedynamic. java.desktop is
// the module that covers that phase.
var corpusModules = []string{"java.base", "java.desktop"}

// Real classes from a real compiler: every shape javac emits, including the ones
// no fixture here covers. The bar is not a full reconstruction - most of these
// bail - but that what comes out is always Java the parser accepts.
func TestDecompileEveryClassInTheJdkCorpus(t *testing.T) {
	for _, module := range corpusModules {
		t.Run(module, func(t *testing.T) { decompileEveryClassIn(t, module) })
	}
}

func decompileEveryClassIn(t *testing.T, module string) {
	t.Helper()
	jmod := jmodOf(module)
	if jmod == "" {
		t.Skip("no JDK with jmods/")
	}
	entries := readJmodEntries(jmod)
	classes := 0
	var failures []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name, "classes/") || !strings.HasSuffix(entry.Name, ".class") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(entry.Name, "classes/"), ".class")
		if name == "module-info" {
			continue
		}
		classes++
		source, err := Decompile(entry.Read())
		if err != nil {
			failures = append(failures, name+": "+err.Error())
			continue
		}
		if diagnostics := ParseSourceFile(name+".java", source).AsSourceFile().ParseDiagnostics; len(diagnostics) > 0 {
			failures = append(failures, name+": "+diagnostics[0].MessageText)
		}
	}
	if classes < 1000 {
		t.Fatalf("only %d classes read from %s", classes, jmod)
	}
	if len(failures) > 0 {
		t.Errorf("%d of %d classes did not come back as parseable Java: %v",
			len(failures), classes, failures[:min(10, len(failures))])
	}
}
