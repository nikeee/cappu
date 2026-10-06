// Package baselines carries the UPDATE_BASELINES=1 write mode that the Go test
// suites share. Every baseline family the deleted TypeScript tests could
// regenerate used the same rule - `shouldUpdate || !existsSync(path)` - so the
// rule lives here once instead of in each package's _test.go.
//
// Only test code imports this package; it deliberately does not depend on
// stdlib `testing`, so callers keep their own failure messages.
package baselines

import (
	"os"
	"path/filepath"
)

// Update is UPDATE_BASELINES=1: an intentional change is blessed by writing
// what the code now produces, instead of asserting the committed baseline.
var Update = os.Getenv("UPDATE_BASELINES") == "1"

// Write stores content at path when Update is set or the baseline does not
// exist yet, and reports whether it wrote - a caller that wrote has nothing
// left to assert. With Update unset and the baseline present it reports false
// and the committed bytes stand.
func Write(path string, content []byte) (bool, error) {
	if _, err := os.Stat(path); !Update && err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return false, err
	}
	return true, nil
}
