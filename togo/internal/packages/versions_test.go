package packages

import (
	"reflect"
	"testing"
)

// Port of src/packages/versions.test.ts.
func TestMatchesVersionSpec(t *testing.T) {
	cases := []struct {
		spec, version string
		want          bool
	}{
		{"2", "2", true},
		{"2", "2.10.1", true},
		{"2", "2-rc1", true},
		{"2.1", "2.1.3", true},
		{"2.1", "2.10.1", false}, // not a segment prefix
		{"2", "20.0", false},
	}
	for _, c := range cases {
		if got := MatchesVersionSpec(c.spec, c.version); got != c.want {
			t.Errorf("MatchesVersionSpec(%q, %q) = %v, want %v", c.spec, c.version, got, c.want)
		}
	}
}

func TestMatchingVersions(t *testing.T) {
	published := []string{"1.0", "2.0", "2.1", "2.10", "3.0"}
	if got := MatchingVersions(published, "2"); !reflect.DeepEqual(got, []string{"2.10", "2.1", "2.0"}) {
		t.Errorf(`MatchingVersions(_, "2") = %v`, got)
	}
	if got := MatchingVersions(published, ""); !reflect.DeepEqual(got, []string{"3.0", "2.10", "2.1", "2.0", "1.0"}) {
		t.Errorf("MatchingVersions(_, all) = %v", got)
	}
	if got := MatchingVersions(published, "9"); len(got) != 0 {
		t.Errorf(`MatchingVersions(_, "9") = %v, want empty`, got)
	}
}
