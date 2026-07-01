// Port of src/compiler/formatString.test.ts
package compiler

import "testing"

func mustParse(t *testing.T, s string) FormatParse {
	t.Helper()
	p, ok := ParseFormatString(s)
	if !ok {
		t.Fatalf("ParseFormatString(%q) = not ok, want ok", s)
	}
	return p
}

func TestParseFormatCounts(t *testing.T) {
	if got := mustParse(t, "%s %s %d").MaxIndex; got != 3 {
		t.Errorf("maxIndex = %d, want 3", got)
	}
	if got := mustParse(t, "hello").MaxIndex; got != 0 {
		t.Errorf("maxIndex = %d, want 0", got)
	}
}

func TestParseFormatPercentAndNewline(t *testing.T) {
	if got := mustParse(t, "100%% done%n%s").MaxIndex; got != 1 {
		t.Errorf("maxIndex = %d, want 1", got)
	}
}

func TestParseFormatFlagsWidthPrecision(t *testing.T) {
	if got := mustParse(t, "%-10.2f [%03d] %+,d").MaxIndex; got != 3 {
		t.Errorf("maxIndex = %d, want 3", got)
	}
}

func TestParseFormatExplicitIndex(t *testing.T) {
	p := mustParse(t, "%2$s %1$s")
	if p.MaxIndex != 2 {
		t.Errorf("maxIndex = %d, want 2", p.MaxIndex)
	}
	if p.Consumers[0].ArgIndex != 2 || p.Consumers[1].ArgIndex != 1 {
		t.Errorf("indices = %v, want [2 1]", p.Consumers)
	}
}

func TestParseFormatRelative(t *testing.T) {
	p := mustParse(t, "%s %<S")
	if p.MaxIndex != 1 {
		t.Errorf("maxIndex = %d, want 1", p.MaxIndex)
	}
	if p.Consumers[0].ArgIndex != 1 || p.Consumers[1].ArgIndex != 1 {
		t.Errorf("indices = %v, want [1 1]", p.Consumers)
	}
}

func TestParseFormatDateTime(t *testing.T) {
	p := mustParse(t, "%tY-%tm")
	if p.MaxIndex != 2 {
		t.Errorf("maxIndex = %d, want 2", p.MaxIndex)
	}
	if p.Consumers[0].Conversion != "t" || p.Consumers[1].Conversion != "t" {
		t.Errorf("conversions = %v, want [t t]", p.Consumers)
	}
}

func TestParseFormatMalformed(t *testing.T) {
	for _, s := range []string{"trailing %", "%z bad", "%t", "%<s", "%0$s"} {
		if _, ok := ParseFormatString(s); ok {
			t.Errorf("ParseFormatString(%q) = ok, want not ok", s)
		}
	}
}

func TestConversionAcceptsGeneral(t *testing.T) {
	if got := ConversionAccepts("s", ArgTypeDescriptor{Fqn: "java.lang.Object"}); got != AcceptsUnknown {
		t.Errorf("s(Object) = %v, want Unknown", got)
	}
	if got := ConversionAccepts("b", ArgTypeDescriptor{Primitive: "int"}); got != AcceptsUnknown {
		t.Errorf("b(int) = %v, want Unknown", got)
	}
}

func TestConversionAcceptsPrimitives(t *testing.T) {
	cases := []struct {
		conv string
		prim string
		want Accepts
	}{
		{"d", "int", AcceptsYes},
		{"d", "double", AcceptsNo},
		{"f", "double", AcceptsYes},
		{"f", "int", AcceptsNo},
		{"c", "char", AcceptsYes},
		{"c", "boolean", AcceptsNo},
	}
	for _, tc := range cases {
		if got := ConversionAccepts(tc.conv, ArgTypeDescriptor{Primitive: tc.prim}); got != tc.want {
			t.Errorf("%s(%s) = %v, want %v", tc.conv, tc.prim, got, tc.want)
		}
	}
}

func TestConversionAcceptsReferences(t *testing.T) {
	cases := []struct {
		conv string
		fqn  string
		want Accepts
	}{
		{"d", "java.lang.Integer", AcceptsYes},
		{"d", "java.lang.String", AcceptsNo},
		{"d", "java.lang.Double", AcceptsNo},
		{"f", "java.lang.Integer", AcceptsNo},
		{"d", "java.lang.Object", AcceptsUnknown},
		{"d", "com.example.Money", AcceptsUnknown},
	}
	for _, tc := range cases {
		if got := ConversionAccepts(tc.conv, ArgTypeDescriptor{Fqn: tc.fqn}); got != tc.want {
			t.Errorf("%s(%s) = %v, want %v", tc.conv, tc.fqn, got, tc.want)
		}
	}
}
