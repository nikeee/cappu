// Port of src/compiler/checker.libcalls.test.ts
package compiler

import "testing"

var (
	badRegex  = int(Diagnostics.InvalidRegularExpression0.Code)
	badLetter = int(Diagnostics.InvalidDateTimePatternLetter0.Code)
	footgun   = int(Diagnostics.SuspiciousDateTimePatternLetter012.Code)
	badNumber = int(Diagnostics.String0IsNotAValid1.Code)
	badRadix  = int(Diagnostics.Radix0OutOfRange.Code)

	optionalNullCheck    = int(Diagnostics.OptionalOfNullableIfPresentCanBeReplacedWithANullCheck.Code)
	optionalGetUnguarded = int(Diagnostics.OptionalGet0CalledWithoutAnIsPresentGuard.Code)
	countCheck           = int(Diagnostics.CountCheck0CanBeReplacedWith1.Code)
	stringEq             = int(Diagnostics.StringsShouldBeComparedWithEqualsNot0.Code)
	boxingCtor           = int(Diagnostics.BoxingConstructorNew0IsDeprecated.Code)
	indexOfCheck         = int(Diagnostics.IndexOfCheck0CanBeReplacedWith1.Code)
	newString            = int(Diagnostics.NewString0CanBeReplacedWith1.Code)
	equalsEmpty          = int(Diagnostics.EqualsEmpty0CanBeReplacedWith1.Code)
	selfComparison       = int(Diagnostics.SuspiciousSelfComparison0.Code)

	boxedEq        = int(Diagnostics.BoxedTypesShouldBeComparedWithEqualsNot0.Code)
	emptyCatch     = int(Diagnostics.EmptyCatchBlockFor0.Code)
	optionalOfNull = int(Diagnostics.OptionalOfNullAlwaysThrows.Code)
	boolComparison = int(Diagnostics.RedundantBooleanComparison0CanBeReplacedWith1.Code)
	ifElseBool     = int(Diagnostics.IfElseReturningBooleans0CanBeReplacedWith1.Code)
	ternaryBool    = int(Diagnostics.TernaryWithBooleanLiterals0CanBeReplacedWith1.Code)
	collapsibleIf  = int(Diagnostics.NestedIfCanBeCollapsedToIf0.Code)
	optionalType   = int(Diagnostics.Type01ShouldNotBeOfTypeOptional.Code)
	indexedLoop    = int(Diagnostics.IndexedLoopOver0CanBeAForEachLoop.Code)
)

const libcallImports = "import java.util.regex.Pattern; import java.time.format.DateTimeFormatter;" +
	" import java.util.Optional; import java.util.List; import java.util.ArrayList;"

var libcallWanted = map[int]bool{
	badRegex: true, badLetter: true, footgun: true, badNumber: true, badRadix: true, tooFew: true,
	optionalNullCheck: true, optionalGetUnguarded: true, countCheck: true, stringEq: true,
	boxingCtor: true, indexOfCheck: true, newString: true, equalsEmpty: true, selfComparison: true,
	boxedEq: true, emptyCatch: true, optionalOfNull: true, boolComparison: true, ifElseBool: true,
	ternaryBool: true, collapsibleIf: true, optionalType: true, indexedLoop: true,
}

func libcallDiagnose(body string) []int {
	program := NewProgram()
	LoadJdkStub(program)
	src := libcallImports + " class C { void m() { " + body + " } }"
	program.SetOpenDocument("file:///T.java", src, 1)
	checker := NewChecker(program)
	var out []int
	for _, d := range checker.GetSemanticDiagnostics(program.GetSourceFile("file:///T.java")) {
		if code := int(d.Code); libcallWanted[code] {
			out = append(out, code)
		}
	}
	return out
}

// For rules that need custom method signatures/fields/params rather than the
// single fixed `void m()` body above.
func libcallDiagnoseClass(classBody string) []int {
	program := NewProgram()
	LoadJdkStub(program)
	src := libcallImports + " class C { " + classBody + " }"
	program.SetOpenDocument("file:///T.java", src, 1)
	checker := NewChecker(program)
	var out []int
	for _, d := range checker.GetSemanticDiagnostics(program.GetSourceFile("file:///T.java")) {
		if code := int(d.Code); libcallWanted[code] {
			out = append(out, code)
		}
	}
	return out
}

func TestRegexCallUnbalanced(t *testing.T) {
	for _, body := range []string{`Pattern.compile("(foo");`, `"x".split("[");`, `"x".replaceAll("a(", "b");`} {
		if !containsCode(libcallDiagnose(body), badRegex) {
			t.Errorf("want bad-regex for %q", body)
		}
	}
}

func TestRegexCallValidSilent(t *testing.T) {
	if got := libcallDiagnose(`Pattern.compile("(foo)+");`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
	if got := libcallDiagnose(`String r = "(foo"; Pattern.compile(r);`); len(got) != 0 {
		t.Errorf("want silent for non-literal, got %v", got)
	}
}

func TestDateTimeCallUnknownLetter(t *testing.T) {
	if !containsCode(libcallDiagnose(`DateTimeFormatter.ofPattern("yyyy-jj");`), badLetter) {
		t.Error("want bad-letter")
	}
}

func TestDateTimeCallFootgun(t *testing.T) {
	if !containsCode(libcallDiagnose(`DateTimeFormatter.ofPattern("YYYY-MM-dd");`), footgun) {
		t.Error("want footgun")
	}
}

func TestDateTimeCallValidSilent(t *testing.T) {
	if got := libcallDiagnose(`DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestNumberParseInvalid(t *testing.T) {
	if !containsCode(libcallDiagnose(`Integer.parseInt("12a");`), badNumber) {
		t.Error("want bad-number")
	}
	if !containsCode(libcallDiagnose(`Long.parseLong("x");`), badNumber) {
		t.Error("want bad-number for long")
	}
}

func TestNumberParseRadix(t *testing.T) {
	if !containsCode(libcallDiagnose(`Integer.parseInt("FF");`), badNumber) {
		t.Error("want bad-number base 10")
	}
	if got := libcallDiagnose(`Integer.parseInt("FF", 16);`); len(got) != 0 {
		t.Errorf("want silent base 16, got %v", got)
	}
	if !containsCode(libcallDiagnose(`Integer.parseInt("1", 99);`), badRadix) {
		t.Error("want bad-radix")
	}
}

func TestNumberParseValidSilent(t *testing.T) {
	if got := libcallDiagnose(`Integer.parseInt("-42");`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestConsoleFormat(t *testing.T) {
	if !containsCode(libcallDiagnose(`System.console().printf("%s %s", "a");`), tooFew) {
		t.Error("want too-few for Console.printf")
	}
}

// --- Optional.ofNullable(x).ifPresent(...) (nikeee/cappu#42) -----------------

func TestOptionalIfPresentLambdaFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = "x"; Optional.ofNullable(s).ifPresent(v -> System.out.println(v));`), optionalNullCheck) {
		t.Error("want optional-null-check for lambda")
	}
}

func TestOptionalIfPresentNonVariableFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = "x"; Optional.ofNullable(s.trim()).ifPresent(v -> System.out.println(v));`), optionalNullCheck) {
		t.Error("want optional-null-check for non-variable argument")
	}
}

func TestOptionalIfPresentMethodRefFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = "x"; Optional.ofNullable(s).ifPresent(System.out::println);`), optionalNullCheck) {
		t.Error("want optional-null-check for method reference")
	}
}

func TestOptionalOfIfPresentSilent(t *testing.T) {
	if got := libcallDiagnose(`String s = "x"; Optional.of(s).ifPresent(v -> System.out.println(v));`); len(got) != 0 {
		t.Errorf("want silent for Optional.of, got %v", got)
	}
}

func TestOptionalOfNullableWithoutIfPresentSilent(t *testing.T) {
	if got := libcallDiagnose(`String s = "x"; Optional.ofNullable(s).map(v -> v);`); len(got) != 0 {
		t.Errorf("want silent for map, got %v", got)
	}
}

// --- Optional#get() without an isPresent()/isEmpty() guard (nikeee/cappu#42) ---

func TestOptionalGetUnguardedFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`Optional<String> o = Optional.empty(); o.get();`), optionalGetUnguarded) {
		t.Error("want optional-get-unguarded")
	}
}

func TestOptionalGetAfterIsPresentSilent(t *testing.T) {
	if got := libcallDiagnose(`Optional<String> o = Optional.empty(); if (o.isPresent()) { o.get(); }`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestOptionalGetAfterIsEmptySilent(t *testing.T) {
	if got := libcallDiagnose(`Optional<String> o = Optional.empty(); if (!o.isEmpty()) { o.get(); }`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestOptionalGetOnCallReceiverSilent(t *testing.T) {
	if got := libcallDiagnose(`Optional.ofNullable("x").get();`); len(got) != 0 {
		t.Errorf("want silent for unprovable receiver, got %v", got)
	}
}

func TestOptionalGetDifferentVariableDoesNotGuard(t *testing.T) {
	got := libcallDiagnose(`Optional<String> a = Optional.empty(); Optional<String> b = Optional.empty(); if (b.isPresent()) { a.get(); }`)
	if !containsCode(got, optionalGetUnguarded) {
		t.Error("want optional-get-unguarded when the guard is on a different variable")
	}
}

// --- size()/length() compared to 0/1 -> isEmpty()/!isEmpty() (nikeee/cappu#42) ---

func TestCountCheckSizeEqualsZeroFlagged(t *testing.T) {
	got := libcallDiagnose(`List<String> xs = new ArrayList<>(); if (xs.size() == 0) {}`)
	if !containsCode(got, countCheck) {
		t.Error("want count-check")
	}
}

func TestCountCheckAllCombosFlagged(t *testing.T) {
	for _, cmp := range []string{"!= 0", "> 0", "< 1", ">= 1", "<= 0"} {
		got := libcallDiagnose(`List<String> xs = new ArrayList<>(); if (xs.size() ` + cmp + `) {}`)
		if !containsCode(got, countCheck) {
			t.Errorf("want count-check for %q", cmp)
		}
	}
}

func TestCountCheckLiteralOnLeftFlagged(t *testing.T) {
	got := libcallDiagnose(`List<String> xs = new ArrayList<>(); if (0 == xs.size()) {}`)
	if !containsCode(got, countCheck) {
		t.Error("want count-check")
	}
}

func TestCountCheckStringLengthFlagged(t *testing.T) {
	got := libcallDiagnose(`String s = "x"; if (s.length() == 0) {}`)
	if !containsCode(got, countCheck) {
		t.Error("want count-check")
	}
}

func TestCountCheckNonZeroLiteralSilent(t *testing.T) {
	if got := libcallDiagnose(`List<String> xs = new ArrayList<>(); if (xs.size() == 2) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestCountCheckUnrecognizedComboSilent(t *testing.T) {
	if got := libcallDiagnose(`List<String> xs = new ArrayList<>(); if (xs.size() > 1) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

// --- == / != on Strings -> equals() (nikeee/cappu#42) ---------------------------

func TestStringEqualsFlagged(t *testing.T) {
	got := libcallDiagnose(`String a = "x"; String b = "y"; if (a == b) {}`)
	if !containsCode(got, stringEq) {
		t.Error("want string-eq")
	}
}

func TestStringNotEqualsFlagged(t *testing.T) {
	got := libcallDiagnose(`String a = "x"; String b = "y"; if (a != b) {}`)
	if !containsCode(got, stringEq) {
		t.Error("want string-eq")
	}
}

func TestStringEqualsAgainstNullSilent(t *testing.T) {
	if got := libcallDiagnose(`String a = "x"; if (a == null) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestStringEqualsNonStringSilent(t *testing.T) {
	if got := libcallDiagnose(`int a = 1; int b = 2; if (a == b) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestStringEqualsOneSideOnlySilent(t *testing.T) {
	if got := libcallDiagnose(`String a = "x"; Object b = new Object(); if (a == b) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

// --- boxing constructors -> valueOf() (nikeee/cappu#42) --------------------------

func TestBoxingConstructorIntegerFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`Integer i = new Integer(5);`), boxingCtor) {
		t.Error("want boxing-ctor")
	}
}

func TestBoxingConstructorBooleanCharacterFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`Boolean b = new Boolean(true);`), boxingCtor) {
		t.Error("want boxing-ctor for Boolean")
	}
	if !containsCode(libcallDiagnose(`Character c = new Character('x');`), boxingCtor) {
		t.Error("want boxing-ctor for Character")
	}
}

func TestBoxingConstructorValueOfSilent(t *testing.T) {
	if got := libcallDiagnose(`Integer i = Integer.valueOf(5);`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestBoxingConstructorNonBoxingTypeSilent(t *testing.T) {
	if got := libcallDiagnose(`List<String> xs = new ArrayList<>();`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

// --- indexOf(...) != -1 -> contains(...) (nikeee/cappu#42) -----------------------

func TestIndexOfNotEqualsNegativeOneFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = "x"; if (s.indexOf("a") != -1) {}`), indexOfCheck) {
		t.Error("want indexOf-check")
	}
}

func TestIndexOfEqualsNegativeOneFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = "x"; if (s.indexOf("a") == -1) {}`), indexOfCheck) {
		t.Error("want indexOf-check")
	}
}

func TestIndexOfListFlagged(t *testing.T) {
	got := libcallDiagnose(`List<String> xs = new ArrayList<>(); if (xs.indexOf("a") != -1) {}`)
	if !containsCode(got, indexOfCheck) {
		t.Error("want indexOf-check")
	}
}

func TestIndexOfLiteralOnLeftFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = "x"; if (-1 != s.indexOf("a")) {}`), indexOfCheck) {
		t.Error("want indexOf-check")
	}
}

func TestIndexOfNonNegativeOneSilent(t *testing.T) {
	if got := libcallDiagnose(`String s = "x"; if (s.indexOf("a") != 0) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

// --- redundant new String(...) (nikeee/cappu#42) ---------------------------------

func TestNewStringEmptyFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = new String();`), newString) {
		t.Error("want new-string")
	}
}

func TestNewStringWrappingStringFlagged(t *testing.T) {
	got := libcallDiagnose(`String a = "x"; String b = new String(a);`)
	if !containsCode(got, newString) {
		t.Error("want new-string")
	}
}

func TestNewStringFromBytesSilent(t *testing.T) {
	if got := libcallDiagnose(`byte[] bs = null; String s = new String(bs);`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

// --- equals("") -> isEmpty() (nikeee/cappu#42) ------------------------------------

func TestEqualsEmptyReceiverFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = "x"; if (s.equals("")) {}`), equalsEmpty) {
		t.Error("want equals-empty")
	}
}

func TestEqualsEmptyArgFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String s = "x"; if ("".equals(s)) {}`), equalsEmpty) {
		t.Error("want equals-empty")
	}
}

func TestEqualsNonEmptyLiteralSilent(t *testing.T) {
	if got := libcallDiagnose(`String s = "x"; if (s.equals("x")) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestEqualsEmptyNonStringReceiverSilent(t *testing.T) {
	if got := libcallDiagnose(`Object o = new Object(); if (o.equals("")) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

// --- self-comparison (nikeee/cappu#42) --------------------------------------------

func TestSelfComparisonBinaryFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`int x = 1; if (x == x) {}`), selfComparison) {
		t.Error("want self-comparison")
	}
}

func TestSelfComparisonEqualsCallFlagged(t *testing.T) {
	if !containsCode(libcallDiagnose(`String x = "a"; if (x.equals(x)) {}`), selfComparison) {
		t.Error("want self-comparison")
	}
}

func TestSelfComparisonCompareToCallFlagged(t *testing.T) {
	got := libcallDiagnose(`String x = "a"; if (x.compareTo(x) == 0) {}`)
	if !containsCode(got, selfComparison) {
		t.Error("want self-comparison")
	}
}

func TestSelfComparisonFieldAccessFlagged(t *testing.T) {
	got := libcallDiagnoseClass(`int a; void m() { if (this.a == this.a) {} }`)
	if !containsCode(got, selfComparison) {
		t.Error("want self-comparison")
	}
}

func TestSelfComparisonDifferentVariablesSilent(t *testing.T) {
	if got := libcallDiagnose(`int x = 1; int y = 2; if (x == y) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestSelfComparisonCallsNotFlagged(t *testing.T) {
	got := libcallDiagnoseClass(`int next() { return 1; } void m() { if (next() == next()) {} }`)
	if len(got) != 0 {
		t.Errorf("want silent (unprovable), got %v", got)
	}
}

// --- boxed reference == comparison -> equals() (nikeee/cappu#42 follow-up) -------

func TestBoxedEqualsFlagged(t *testing.T) {
	got := libcallDiagnose(`Integer a = 1000; Integer b = 1000; if (a == b) {}`)
	if !containsCode(got, boxedEq) {
		t.Error("want boxed-eq")
	}
}

func TestBoxedNotEqualsFlagged(t *testing.T) {
	got := libcallDiagnose(`Integer a = 1000; Integer b = 1000; if (a != b) {}`)
	if !containsCode(got, boxedEq) {
		t.Error("want boxed-eq")
	}
}

func TestBoxedEqualsWithPrimitiveSilent(t *testing.T) {
	if got := libcallDiagnose(`Integer a = 1000; int b = 1000; if (a == b) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}

func TestBoxedEqualsAgainstNullSilent(t *testing.T) {
	if got := libcallDiagnose(`Integer a = 1000; if (a == null) {}`); len(got) != 0 {
		t.Errorf("want silent, got %v", got)
	}
}
