package format

// Formatter compatibility ratchet over google-java-format's own source tree,
// the git submodule at test-fixtures/format/corpus/gjf. Mirrors the TypeScript
// src/format/format-corpus.test.ts: gjf dogfoods its own formatter, so its
// committed *.java files are gjf's canonical output and a perfect formatter is a
// fixpoint (formats each to itself). Regression ratchet, not a 100% gate -
// ratchet RATCHET UP only. Skipped when the submodule is absent (no network/JDK
// needed). The Go count must stay in lockstep with the TS count.
//
// The idempotence check below holds over this tree. gjf itself is not a fixpoint
// everywhere - wrapping a trailing comment turns the continuation into an
// own-line comment, which the next pass re-indents - and we wrap where gjf
// wraps; over 5114 real-world sources gjf is unstable on 14 files and we on 22.
//
// 62 is the CEILING, not a gap: the 9 files that are not fixpoints were
// committed by an older google-java-format, so gjf 1.25.2 does not reproduce
// them either, and our output equals gjf 1.25.2's on every one of them.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Current number of core sources formatted byte-identically. Ratchet UP only.
const corpusRatchet = 62

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

	// Formatting is a normalization, so it must reach a fixpoint in ONE pass:
	// format(format(x)) == format(x) for EVERY file, matched or not. Mirrors the
	// TS test "formatting the gjf corpus is idempotent".
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		once, ferr := FormatSource(string(src), FormatOptions{Style: "google"}, f)
		if ferr != nil {
			continue
		}
		twice, ferr := FormatSource(once, FormatOptions{Style: "google"}, f)
		if ferr != nil {
			t.Errorf("%s: second pass failed: %v", filepath.Base(f), ferr)
			continue
		}
		if twice != once {
			t.Errorf("%s: formatting is not idempotent", filepath.Base(f))
		}
	}
}
