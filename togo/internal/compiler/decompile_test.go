package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
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
	"QualifiedAnon$Inner",
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

// Classes kept for the bail-out rendering: an inner class's constructor and the
// members javac generates for an enum are not this phase's job, and must say so.
var notDecompiled = []string{
	"EnumAbstract",
	"EnumMixed",
	"QualifiedAnon$1",
	"QualifiedAnon",
	"QualifiedNew",
}

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
//     does.
var noRoundtrip = map[string]bool{
	"ClassLit": true, "Nest$Counter": true,
	"EnumAbstract$1": true, "EnumAbstract$2": true, "EnumMixed$1": true, "EnumMixed$2": true,
	"ICast": true, "BoundErasure": true, "EnumUnqualified": true, "Boxing": true,
	"QualifiedAnon$Inner": true, "QualifiedNew$Inner": true,
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	javaFile := filepath.Join(dir, name+".java")
	if err := os.WriteFile(javaFile, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	args := []string{"--release", "21", "-d", dir}
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
		// around it needs a label - which this phase does not write.
		source: "class SwitchLabeled { static int f(int n, int x) { int r = 0;" +
			" outer: while (r < n) { switch (x) { case 1: r += 1; break; case 2: break outer;" +
			" default: r += 3; } r += 1; } return r; } }",
		want:          []string{"cappu: a labeled break or continue"},
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
	if !strings.Contains(source, "(var2 = poll(arg0)) != null") {
		t.Errorf("expected the assignment as a value:\n%s", source)
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

// An increment is not a value that may be written twice or moved: `Effects` says
// so for a call, but an operand keeps none of it once it is nested, so the text
// is what the `dup` and the assignment guards have to read.
const incyBailsSource = `public class IncyBails {
  static int n;
  static int g(int a, int b) { n += a * 100 + b; return a - b; }
  static int nested(int[] a, int i) { a[a[i++]] += 1; return a[0]; }
  static int before(int[] a, int i) { return g(i++ + 1, a[i] = 5); }
  static int field(int i) { return g(i++ + 1, n = i); }
  static int local(int i) { int x = -1; int r = g(i++, x = i); return r + x; }
}`

func TestDecompileSaysWhenAnIncrementWouldBeWrittenTwiceOrMoved(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "IncyBails", incyBailsSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: dup of an increment") {
		t.Errorf("expected the dup bail:\n%s", source)
	}
	if !strings.Contains(source, "cappu: an assignment with a value that could see it on the stack") {
		t.Errorf("expected the assignment bail:\n%s", source)
	}
	// A store into a local is the one that comes back: written as the value it
	// is, it stays behind the increment instead of moving in front of it.
	if !strings.Contains(source, "g(arg0++, var1 = arg0)") {
		t.Errorf("expected the local store as a value:\n%s", source)
	}
	// `g` and `local` are the bodies that come back.
	if strings.Count(source, "cappu: ") != 6 {
		t.Errorf("expected three bailed methods, got:\n%s", source)
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
// values, so without a debug table there is nothing left to say whether that is
// one variable or two - and guessing would produce code that lies.
const ambiguousSlotSource = "class Amb { static java.lang.Object f(boolean c, java.lang.String s, int[] a) {" +
	" java.lang.Object o; if (c) { o = s; } else { o = a; } return o; } }"

func TestDecompileSaysWhenASlotComesFromEitherBranch(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Amb", ambiguousSlotSource))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: local 3 is written in more than one branch") {
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
// one being written - under this name or another. Only locals and literals are
// safe, so the rest say so.
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

func TestDecompileSaysWhenAnAssignmentWouldMoveInFrontOfAValue(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "Aliased", aliasSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if count := strings.Count(source, "an assignment with a value that could see it on the stack"); count != 4 {
		t.Errorf("expected four guarded methods, got %d:\n%s", count, source)
	}
	// A chained assignment is the one shape here that comes back: the store is
	// the value, so it stays where source put it.
	if !strings.Contains(source, "int var0 = var1 = 5;") {
		t.Errorf("the chained assignment did not come back:\n%s", source)
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
// one: those need the assignment to stay an expression, which it does not.
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
	// Both of them, and both for the same reason: the store would have to run in
	// front of the value the post-increment yields.
	if count := strings.Count(source, "an assignment with a value that could see it on the stack"); count != 2 {
		t.Errorf("expected two guarded methods, got %d:\n%s", count, source)
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
// is a getstatic/dup/putstatic, and writing the assignment out first would make
// the read take the new value.
const fieldPostIncrementSource = `public class FieldPost {
  static int[] arr = { 5, 6, 7 };
  static int idx = 0;
  static int f() { int v = arr[idx++]; return v * 100 + idx; }
}`

func TestDecompileSaysWhenAFieldIsAssignedWhileItIsOnTheStack(t *testing.T) {
	if !hasTool("javac") {
		t.Skip("no JDK (javac)")
	}
	dir := t.TempDir()
	classFile := compileWithJavac(t, dir, "FieldPost", fieldPostIncrementSource)
	source, err := Decompile(readFile(t, classFile))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: an assignment with a value that could see it on the stack") {
		t.Errorf("expected the bail, got:\n%s", source)
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
		for _, want := range []string{"new Nested.St(arg0, 3)", "cappu: an inner class constructor"} {
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
func TestDecompileSaysWhenAStatementThatCanThrowComesBeforeSuper(t *testing.T) {
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
	if !strings.Contains(source, "cappu: constructor call is not first") {
		t.Errorf("expected the bail:\n%s", source)
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
