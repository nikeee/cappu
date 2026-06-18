package semver

import "testing"

// Port of src/version.test.ts (bumpSemver).
func TestBump(t *testing.T) {
	cases := []struct {
		version string
		release ReleaseType
		want    string
	}{
		{"1.2.3", Major, "2.0.0"},
		{"1.2.3", Minor, "1.3.0"},
		{"1.2.3", Patch, "1.2.4"},
		{"0.0.0", Patch, "0.0.1"},
		// pre-release / build metadata is dropped (a release is a clean version)
		{"1.2.3-SNAPSHOT", Patch, "1.2.4"},
		{"2.0.0-rc.1+build.7", Minor, "2.1.0"},
	}
	for _, c := range cases {
		got, err := Bump(c.version, c.release)
		if err != nil {
			t.Fatalf("Bump(%q, %q) errored: %v", c.version, c.release, err)
		}
		if got != c.want {
			t.Errorf("Bump(%q, %q) = %q, want %q", c.version, c.release, got, c.want)
		}
	}
}

func TestBumpRejectsNonSemver(t *testing.T) {
	// TS rejects a partial version ("1.2", fewer than 3 components).
	for _, v := range []string{"1.2", "RELEASE"} {
		if _, err := Bump(v, Patch); err == nil {
			t.Errorf("expected an error for non-semver version %q", v)
		}
	}
}
