package cli

import (
	"runtime"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/meta"
)

func TestRageReport(t *testing.T) {
	report := rageReport()
	for _, want := range []string{
		"cappu " + meta.Version,
		"go " + runtime.Version(),
		runtime.GOOS + " " + runtime.GOARCH,
		meta.IssueTracker,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("rageReport() missing %q\ngot:\n%s", want, report)
		}
	}
}
