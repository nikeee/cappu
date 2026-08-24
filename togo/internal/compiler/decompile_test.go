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
	"AnnAll", "Arithmetic", "ArrayLoad", "ArrayStore", "CastInstance", "Cl",
	"ClassLit", "Constants", "Empty", "EnumAbstract$1", "EnumAbstract$2",
	"EnumMixed$1", "EnumMixed$2", "Fields", "FloatArith", "FloatConst", "FloatConv",
	"Fold", "ICast$A", "ICast$B", "ISA", "ISB", "ImplicitSealed", "IntConv",
	"IntLiterals", "Locals", "LongArith", "Methods", "ModifiedFields", "Nest$Counter",
	"Nest$Point", "Nest", "NewArray", "Pt", "ReturnLiterals", "Returns", "Rt",
	"Sealed", "SealedI", "StaticFields", "SubA", "SubB", "SubC", "VarargsAndAbstract",
}

// Classes kept for the bail-out rendering: control flow (phase 1.4+) and method
// calls (phase 1.6) are not this phase's job, and must say so.
var notDecompiled = []string{
	"BoundErasure", "Boxing", "Compute", "Concat", "ControlFlow", "EnumAbstract",
	"EnumMixed", "EnumUnqualified", "Hello", "ICast", "Invoke", "PrivateCall",
	"QualifiedAnon$1", "QualifiedAnon$Inner", "QualifiedAnon", "QualifiedNew$Inner",
	"QualifiedNew", "VarargsPack",
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
var noRoundtrip = map[string]bool{
	"ClassLit": true, "Nest$Counter": true,
	"EnumAbstract$1": true, "EnumAbstract$2": true, "EnumMixed$1": true, "EnumMixed$2": true,
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
	for _, name := range fullyDecompiled {
		if strings.Contains(decompileBaseline(t, name), "not decompiled") {
			t.Errorf("%s: expected a full reconstruction", name)
		}
	}
	for _, name := range notDecompiled {
		if !strings.Contains(decompileBaseline(t, name), "not decompiled") {
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	javaFile := filepath.Join(dir, name+".java")
	if err := os.WriteFile(javaFile, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if out, err := exec.Command("javac", "--release", "21", "-d", dir, javaFile).CombinedOutput(); err != nil {
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
		// The condition is a boolean, so an index has to ask for the int back.
		want:          []string{"return (arg0 > arg1 ? arg0 : arg1) + 1;", "var1[!arg0 ? 0 : 1] = 7;"},
		selfContained: true,
	},
	{
		// Only the branch says the local is a boolean: `istore` is what an int uses.
		name: "BoolVar",
		source: "class BoolVar { static int f(int a) { boolean big = a > 10;" +
			" if (big) { return 1; } return 0; } }",
		want:          []string{"boolean var1 = arg0 > 10;", "if (var1) {"},
		reject:        []string{"var1 != 0"},
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
		// Phase 1.5; until then it has to say so rather than produce something wrong.
		name: "Loop",
		source: "class Loop { static int f(int n) { int s = 0; for (int i = 0; i < n; i++) s += i;" +
			" return s; } }",
		want:          []string{"loops are not decompiled yet", "not decompiled"},
		selfContained: true,
	},
	{
		name: "Blank",
		source: "class Blank { static int v() { return 1; } static final int N;" +
			" static { N = v(); } }",
		// The initializer could not be reconstructed, so `final` cannot stand,
		// and a static initializer may not throw.
		want:          []string{"static int N;"},
		reject:        []string{"static final int N", "UnsupportedOperationException"},
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

func TestDecompileReconstructsControlFlowFixture(t *testing.T) {
	source := decompileBaseline(t, "ControlFlow")
	// Two branches, no loop: both come back as the expression they were written as.
	for _, want := range []string{
		"if (arg0 < 0) {",
		"return arg0 >= arg1 && arg0 <= arg2;",
		// The loops in the same class still say they are a later phase.
		"cappu: loops are not decompiled yet",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
}

// The two arms store to the same slot with the same opcode but differently typed
// values, so without a debug table there is nothing left to say whether that is
// one variable or two - and guessing would produce code that lies.
const ambiguousSlotSource = "class Amb { static boolean f(boolean c, int x) { boolean b;" +
	" if (c) { b = true; } else { b = x > 1; } return b; } }"

func TestDecompileSaysWhenASlotComesFromEitherBranch(t *testing.T) {
	source, err := Decompile(emitClassBytesNoDebug(t, "Amb", ambiguousSlotSource))
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	if !strings.Contains(source, "cappu: local 2 is written in more than one branch") {
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
	for _, want := range []string{"boolean b;", "b = true;", "b = x > 1;", "return b;"} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
	if strings.Contains(source, "b_2") {
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
	if strings.Contains(source, "not decompiled") {
		t.Fatalf("a method bailed:\n%s", source)
	}
	roundTripped := compileWithJavac(t, filepath.Join(dir, "again"), "Branchy", source)
	if javapText(t, roundTripped) != javapText(t, classFile) {
		t.Errorf("recompiled bytecode differs:\n%s\n--- from ---\n%s",
			javapText(t, roundTripped), javapText(t, classFile))
	}
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

// javaBaseJmod is the JDK on PATH (or JAVA_HOME), when it ships the jmods/ a
// class corpus needs.
func javaBaseJmod() string {
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
	jmod := filepath.Join(home, "jmods", "java.base.jmod")
	if _, err := os.Stat(jmod); err != nil {
		return ""
	}
	return jmod
}

// Real classes from a real compiler: every shape javac emits, including the ones
// no fixture here covers. The bar is not a full reconstruction - most of these
// bail - but that what comes out is always Java the parser accepts.
func TestDecompileEveryClassInJavaBase(t *testing.T) {
	jmod := javaBaseJmod()
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
