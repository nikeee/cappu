package build

import "testing"

// globToRegexp must accept what Node's path.matchesGlob accepts (the TS
// build's formatter ignore patterns): *, **, ?, {a,b} and [...] classes.
func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"src/**", "src/a/b/C.java", true},
		{"src/*.java", "src/C.java", true},
		{"src/*.java", "src/a/C.java", false},
		{"src/?.java", "src/A.java", true},
		{"src/{gen,vendor}/**", "src/gen/A.java", true},
		{"src/{gen,vendor}/**", "src/vendor/x/B.java", true},
		{"src/{gen,vendor}/**", "src/main/A.java", false},
		{"src/[A-C]*.java", "src/B1.java", true},
		{"src/[A-C]*.java", "src/D1.java", false},
		{"src/[!A-C]*.java", "src/D1.java", true},
		{"literal,comma", "literal,comma", true},
		{"lone[bracket", "lone[bracket", true},
	}
	for _, tc := range cases {
		if got := globToRegexp(tc.pattern).MatchString(tc.path); got != tc.want {
			t.Errorf("glob %q on %q = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}
