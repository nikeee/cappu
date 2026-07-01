// Port of src/compiler/checker.format.test.ts
package compiler

import "testing"

var (
	tooFew  = int(Diagnostics.FormatNotEnoughArguments01.Code)
	tooMany = int(Diagnostics.FormatTooManyArguments01.Code)
	badType = int(Diagnostics.FormatConversionIncompatible01.Code)
)

// formatDiagnose returns only the format-related diagnostic codes for a snippet
// wrapped in a method body (so the call resolves against the stub).
func formatDiagnose(body string) []int {
	program := NewProgram()
	LoadJdkStub(program)
	src := "import java.io.PrintWriter; import java.util.Locale; class C { void m(PrintWriter pw) { " + body + " } }"
	program.SetOpenDocument("file:///T.java", src, 1)
	checker := NewChecker(program)
	var out []int
	for _, d := range checker.GetSemanticDiagnostics(program.GetSourceFile("file:///T.java")) {
		code := int(d.Code)
		if code == tooFew || code == tooMany || code == badType {
			out = append(out, code)
		}
	}
	return out
}

func TestFormatTooFew(t *testing.T) {
	if !containsCode(formatDiagnose(`String.format("%s %s", "a");`), tooFew) {
		t.Error("want too-few warning")
	}
}

func TestFormatTooMany(t *testing.T) {
	if !containsCode(formatDiagnose(`String.format("%s", "a", "b");`), tooMany) {
		t.Error("want too-many warning")
	}
}

func TestFormatRightCountSilent(t *testing.T) {
	if got := formatDiagnose(`String.format("%s %s", "a", "b");`); len(got) != 0 {
		t.Errorf("want no warnings, got %v", got)
	}
}

func TestFormatPercentNewlineSilent(t *testing.T) {
	if got := formatDiagnose(`String.format("%s 100%%%n", "a");`); len(got) != 0 {
		t.Errorf("want no warnings, got %v", got)
	}
}

func TestFormatPositional(t *testing.T) {
	if !containsCode(formatDiagnose(`String.format("%2$s %1$s", "a");`), tooFew) {
		t.Error("want too-few warning")
	}
	if got := formatDiagnose(`String.format("%2$s %1$s", "a", "b");`); len(got) != 0 {
		t.Errorf("want no warnings, got %v", got)
	}
}

func TestFormatTypeMismatch(t *testing.T) {
	if !containsCode(formatDiagnose(`String.format("%d", "a");`), badType) {
		t.Error("want type-mismatch warning")
	}
	if got := formatDiagnose(`String.format("%d", 1);`); len(got) != 0 {
		t.Errorf("want no warnings, got %v", got)
	}
	if got := formatDiagnose(`String.format("%s", 1);`); len(got) != 0 {
		t.Errorf("want no warnings for %%s, got %v", got)
	}
}

func TestFormatPrintf(t *testing.T) {
	if !containsCode(formatDiagnose(`System.out.printf("%s %s", "a");`), tooFew) {
		t.Error("want too-few warning for printf")
	}
}

func TestFormatPrintWriter(t *testing.T) {
	if !containsCode(formatDiagnose(`pw.format("%d", "a");`), badType) {
		t.Error("want type-mismatch warning for PrintWriter.format")
	}
}

func TestFormatFormatted(t *testing.T) {
	if !containsCode(formatDiagnose(`"%s %s".formatted("a");`), tooFew) {
		t.Error("want too-few warning for formatted")
	}
	if !containsCode(formatDiagnose(`"%s".formatted("a", "b");`), tooMany) {
		t.Error("want too-many warning for formatted")
	}
}

func TestFormatLocaleOverload(t *testing.T) {
	if !containsCode(formatDiagnose(`String.format(Locale.US, "%s %s", "a");`), tooFew) {
		t.Error("want too-few warning for Locale overload")
	}
	if got := formatDiagnose(`String.format(Locale.US, "%s", "a");`); len(got) != 0 {
		t.Errorf("want no warnings, got %v", got)
	}
}

func TestFormatNonLiteralSilent(t *testing.T) {
	if got := formatDiagnose(`String fmt = "%s %s"; String.format(fmt, "a");`); len(got) != 0 {
		t.Errorf("want no warnings for non-literal, got %v", got)
	}
}

func TestFormatMalformedSilent(t *testing.T) {
	if got := formatDiagnose(`String.format("%z", "a");`); len(got) != 0 {
		t.Errorf("want no warnings for malformed, got %v", got)
	}
}
