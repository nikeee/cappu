package cli

import "testing"

// Port of src/cli/color.test.ts.
func TestColorEnabled(t *testing.T) {
	cases := []struct {
		isTTY   bool
		noColor string
		want    bool
	}{
		{true, "", true},
		{false, "", false},
		// NO_COLOR (https://no-color.org): set and non-empty disables colour...
		{true, "1", false},
		{true, "anything", false},
		// ...but an empty value does not count as set
		{true, "", true},
	}
	for _, c := range cases {
		if got := ColorEnabled(c.isTTY, c.noColor); got != c.want {
			t.Errorf("ColorEnabled(%v, %q) = %v, want %v", c.isTTY, c.noColor, got, c.want)
		}
	}
}
