// Port of src/compiler/checker.libcalls.test.ts
package compiler

import "testing"

var (
	badRegex  = int(Diagnostics.InvalidRegularExpression0.Code)
	badLetter = int(Diagnostics.InvalidDateTimePatternLetter0.Code)
	footgun   = int(Diagnostics.SuspiciousDateTimePatternLetter012.Code)
	badNumber = int(Diagnostics.String0IsNotAValid1.Code)
	badRadix  = int(Diagnostics.Radix0OutOfRange.Code)
)

func libcallDiagnose(body string) []int {
	program := NewProgram()
	LoadJdkStub(program)
	src := "import java.util.regex.Pattern; import java.time.format.DateTimeFormatter;" +
		" class C { void m() { " + body + " } }"
	program.SetOpenDocument("file:///T.java", src, 1)
	checker := NewChecker(program)
	wanted := map[int]bool{badRegex: true, badLetter: true, footgun: true, badNumber: true, badRadix: true, tooFew: true}
	var out []int
	for _, d := range checker.GetSemanticDiagnostics(program.GetSourceFile("file:///T.java")) {
		if code := int(d.Code); wanted[code] {
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
