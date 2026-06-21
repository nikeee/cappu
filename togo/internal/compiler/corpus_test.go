package compiler

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Robustness tests over a local Java corpus (OpenJDK sources + the javac
// langtools test suite) under test-fixtures/parser/corpus, or JAVA_CORPUS_DIR.
// Port of src/compiler/corpus.test.ts. Every file must parse + bind without
// crashing; the semantic layer must resolve/type without crashing and resolve a
// healthy fraction of names. Skipped when the corpus is absent.

func parserCorpusDir() string {
	if d := os.Getenv("JAVA_CORPUS_DIR"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "..", "test-fixtures", "parser", "corpus")
}

func parserCorpusFiles() []string {
	var out []string
	_ = filepath.WalkDir(parserCorpusDir(), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".java") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

func TestJavaCorpusParsesWithoutCrashing(t *testing.T) {
	files := parserCorpusFiles()
	if len(files) == 0 {
		t.Skip("no parser corpus present")
	}
	total, clean := 0, 0
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		sf := ParseSourceFile(file, string(source))
		if sf.Kind != SourceFile {
			t.Errorf("%s: not a SourceFile", file)
			continue
		}
		BindSourceFile(sf) // must not throw
		n := len(sf.AsSourceFile().ParseDiagnostics)
		total += n
		if n == 0 {
			clean++
		}
	}
	t.Logf("corpus: %d files, %d clean, %d parse diagnostics total", len(files), clean, total)
}

func TestJavaCorpusResolvesWithoutCrashing(t *testing.T) {
	files := parserCorpusFiles()
	if len(files) == 0 {
		t.Skip("no parser corpus present")
	}
	program := NewProgram()
	LoadJdkStub(program)
	for _, file := range files {
		if source, err := os.ReadFile(file); err == nil {
			program.SetOpenDocument(corpusURI(file), string(source), 1)
		}
	}
	checker := NewChecker(program)

	identifiers, resolved := 0, 0
	var walk func(node *Node)
	walk = func(node *Node) {
		if node.Kind == Identifier {
			identifiers++
			if checker.ResolveName(node) != nil {
				resolved++
			}
		}
		checker.GetTypeOfExpression(node) // must not throw
		node.ForEachChild(func(child *Node) bool {
			walk(child)
			return false
		})
	}
	for _, file := range files {
		if sf := program.GetSourceFile(corpusURI(file)); sf != nil {
			walk(sf)
		}
	}

	rate := 1.0
	if identifiers > 0 {
		rate = float64(resolved) / float64(identifiers)
	}
	t.Logf("corpus semantics: %d identifiers, %.1f%% resolved", identifiers, rate*100)
	if identifiers == 0 {
		t.Error("expected at least one identifier")
	}
	if rate <= 0.5 {
		t.Errorf("resolution rate %.1f%% below the 50%% regression floor", rate*100)
	}
}
