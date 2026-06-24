package cli

import (
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/install"
)

func TestFormatOutdated(t *testing.T) {
	if got := FormatOutdated(nil); got != "" {
		t.Errorf("empty rows should format to empty string, got %q", got)
	}
	out := FormatOutdated([]install.OutdatedDependency{
		{Configuration: "implementation", Key: "org.x:lib", Current: "1.0", Wanted: "1.2", Latest: "2.0"},
	})
	for _, want := range []string{"dependency", "org.x:lib", "1.0", "1.2", "2.0", "implementation"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}
