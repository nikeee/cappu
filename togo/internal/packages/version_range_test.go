package packages

import "testing"

// Port of src/packages/versionRange.test.ts.

func TestCompareVersions(t *testing.T) {
	lt := func(a, b string) {
		t.Helper()
		if CompareVersions(a, b) >= 0 {
			t.Errorf("want %s < %s", a, b)
		}
		if CompareVersions(b, a) <= 0 {
			t.Errorf("want %s > %s", b, a)
		}
	}
	eq := func(a, b string) {
		t.Helper()
		if CompareVersions(a, b) != 0 || CompareVersions(b, a) != 0 {
			t.Errorf("want %s == %s", a, b)
		}
	}

	eq("1.0", "1.0.0")
	eq("1", "1.0.0")
	lt("1.9", "1.10")
	lt("2.0", "10.0")
	lt("1.0-alpha", "1.0")
	lt("1.0-alpha", "1.0-beta")
	lt("1.0-beta", "1.0-milestone")
	lt("1.0-milestone", "1.0-rc")
	lt("1.0-rc", "1.0-snapshot")
	lt("1.0-snapshot", "1.0")
	lt("1.0", "1.0-sp")
	lt("1.1-alpha", "1.1")
	lt("1.0-rc1", "1.0.1")
	lt("1.0", "1.0-xyz")
}

func TestParseVersionSpec(t *testing.T) {
	if _, ok := ParseVersionSpec("1.2.3"); ok {
		t.Error("exact version should not parse as a spec")
	}
	if _, ok := ParseVersionSpec("2.0-SNAPSHOT"); ok {
		t.Error("exact version should not parse as a spec")
	}
	if spec, ok := ParseVersionSpec("RELEASE"); !ok || !spec.Newest {
		t.Error("RELEASE should parse as newest-wins")
	}
	if spec, ok := ParseVersionSpec("LATEST"); !ok || !spec.Newest {
		t.Error("LATEST should parse as newest-wins")
	}
	if _, ok := ParseVersionSpec("[1.0"); ok {
		t.Error("malformed range should not parse")
	}
	if _, ok := ParseVersionSpec("[]"); ok {
		t.Error("empty range should not parse")
	}
}

func TestSatisfies(t *testing.T) {
	sat := func(spec, version string) bool {
		s, ok := ParseVersionSpec(spec)
		if !ok {
			t.Fatalf("expected %s to parse", spec)
		}
		return Satisfies(s, version)
	}
	cases := []struct {
		spec, version string
		want          bool
	}{
		{"[1.0,2.0)", "1.0", true},
		{"[1.0,2.0)", "1.9.9", true},
		{"[1.0,2.0)", "2.0", false},
		{"[1.0,2.0)", "0.9", false},
		{"(,2.0]", "0.1", true},
		{"(,2.0]", "2.0", true},
		{"(,2.0]", "2.0.1", false},
		{"[1.0,)", "1.0", true},
		{"[1.0,)", "99", true},
		{"[1.0,)", "0.9", false},
		{"[1.5]", "1.5", true},
		{"[1.5]", "1.6", false},
		{"[1.5]", "1.4", false},
		{"[1.0,2.0),[3.0,)", "1.5", true},
		{"[1.0,2.0),[3.0,)", "2.5", false},
		{"[1.0,2.0),[3.0,)", "3.1", true},
		{"RELEASE", "0.0.1", true},
	}
	for _, c := range cases {
		if got := sat(c.spec, c.version); got != c.want {
			t.Errorf("satisfies(%s, %s) = %v, want %v", c.spec, c.version, got, c.want)
		}
	}
}

func TestSelectVersion(t *testing.T) {
	must := func(spec string) VersionSpec {
		s, ok := ParseVersionSpec(spec)
		if !ok {
			t.Fatalf("expected %s to parse", spec)
		}
		return s
	}
	published := []string{"1.0", "3.1", "1.5", "2.0", "1.9"}
	if got := SelectVersion(must("[1.0,2.0)"), published); got != "1.9" {
		t.Errorf("[1.0,2.0) = %q, want 1.9", got)
	}
	if got := SelectVersion(must("[1.0,)"), published); got != "3.1" {
		t.Errorf("[1.0,) = %q, want 3.1", got)
	}
	if got := SelectVersion(must("RELEASE"), published); got != "3.1" {
		t.Errorf("RELEASE = %q, want 3.1", got)
	}
	if got := SelectVersion(must("[5.0,6.0)"), []string{"1.0", "2.0"}); got != "" {
		t.Errorf("unsatisfiable = %q, want empty", got)
	}
}
