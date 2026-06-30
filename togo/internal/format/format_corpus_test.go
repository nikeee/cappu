package format

// Formatter compatibility ratchet over google-java-format's own source tree,
// the git submodule at test-fixtures/format/corpus/gjf. Mirrors the TypeScript
// src/format/format-corpus.test.ts: gjf dogfoods its own formatter, so its
// committed *.java files are gjf's canonical output and a perfect formatter is a
// fixpoint (formats each to itself). Regression ratchet, not a 100% gate -
// ratchet RATCHET UP only. Skipped when the submodule is absent (no network/JDK
// needed). The Go count must stay in lockstep with the TS count.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Current number of core sources formatted byte-identically. Ratchet UP only.
const corpusRatchet = 60

func TestCorpusFixpoint(t *testing.T) {
	root := filepath.Join("..", "..", "..", "test-fixtures", "format", "corpus", "gjf", "core")
	var files []string
	_ = filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() && strings.HasSuffix(path, ".java") {
			files = append(files, path)
		}
		return nil
	})
	if len(files) == 0 {
		t.Skip("gjf submodule not checked out")
	}

	matched := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		out, ferr := FormatSource(string(src), FormatOptions{Style: "google"}, f)
		if ferr != nil {
			continue // unsupported syntax counts as a non-match, not a crash
		}
		if out == string(src) {
			matched++
		}
	}
	t.Logf("gjf corpus fixpoint: %d/%d matched", matched, len(files))
	if matched < corpusRatchet {
		t.Fatalf("gjf corpus fixpoints regressed: %d < ratchet %d", matched, corpusRatchet)
	}
}
