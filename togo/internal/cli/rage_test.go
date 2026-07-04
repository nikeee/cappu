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
		// Node token parity: the platform line matches the TS build.
		nodePlatform(runtime.GOOS) + " " + nodeArch(runtime.GOARCH),
		meta.IssueTracker,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("rageReport() missing %q\ngot:\n%s", want, report)
		}
	}
}
