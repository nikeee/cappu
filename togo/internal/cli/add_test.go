package cli

import "testing"

func TestParseAddCoordinate(t *testing.T) {
	cases := []struct {
		spec         string
		ok           bool
		key, version string
	}{
		{"com.google.code.gson:gson:2.14.0", true, "com.google.code.gson:gson", "2.14.0"},
		{"org.slf4j:slf4j-api", true, "org.slf4j:slf4j-api", ""},
		{"only-one", false, "", ""},
		{"a:b:c:d", false, "", ""},
		{"a::1", false, "", ""}, // empty segment
		{":b:1", false, "", ""},
	}
	for _, c := range cases {
		got, ok := parseAddCoordinate(c.spec)
		if ok != c.ok {
			t.Errorf("parseAddCoordinate(%q) ok = %v, want %v", c.spec, ok, c.ok)
			continue
		}
		if ok && (got.key != c.key || got.version != c.version) {
			t.Errorf("parseAddCoordinate(%q) = %+v, want key=%q version=%q", c.spec, got, c.key, c.version)
		}
	}
}

func TestResolveConfiguration(t *testing.T) {
	cases := map[string]string{
		"implementation": "implementation",
		"i":              "implementation",
		"a":              "api",
		"ap":             "annotationProcessor",
		"ti":             "testImplementation",
		"nope":           "",
		"":               "",
	}
	for arg, want := range cases {
		if got := resolveConfiguration(arg); got != want {
			t.Errorf("resolveConfiguration(%q) = %q, want %q", arg, got, want)
		}
	}
}

func TestLooksExact(t *testing.T) {
	exact := []string{"2.14.0", "1.0.0-SNAPSHOT", "3.0.0", "2-rc1"}
	for _, v := range exact {
		if !looksExact(v) {
			t.Errorf("looksExact(%q) = false, want true", v)
		}
	}
	prefix := []string{"", "2", "2.10"}
	for _, v := range prefix {
		if looksExact(v) {
			t.Errorf("looksExact(%q) = true, want false", v)
		}
	}
}
