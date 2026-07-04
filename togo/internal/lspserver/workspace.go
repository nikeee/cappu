// Package lspserver is the Java language server: the JSON-RPC/LSP transport that
// wires this project's scanner/parser/binder/checker (via the Program) and the
// language-services layer to editor requests. Port of src/services/server.ts.
package lspserver

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
)

// FsPath is an absolute or cwd-relative filesystem path, distinct from a URI.
type FsPath string

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "target": true, "build": true, "out": true, "bin": true,
}

// pathToURI converts an absolute filesystem path to a file:// URI,
// percent-encoding like Node's pathToFileURL (spaces, non-ASCII).
func pathToURI(path FsPath) compiler.URI {
	abs, err := filepath.Abs(string(path))
	if err != nil {
		abs = string(path)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return compiler.URI(u.String())
}

// uriToPath converts a file:// URI back to a filesystem path,
// percent-decoding like Node's fileURLToPath.
func uriToPath(uri compiler.URI) FsPath {
	u, err := url.Parse(string(uri))
	if err != nil || u.Scheme != "file" {
		return FsPath(strings.TrimPrefix(string(uri), "file://"))
	}
	return FsPath(u.Path)
}

// findJavaFiles recursively collects .java file paths under dir, skipping build dirs.
func findJavaFiles(dir FsPath) []FsPath {
	var out []FsPath
	_ = filepath.WalkDir(string(dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != string(dir) && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".java") {
			out = append(out, FsPath(path))
		}
		return nil
	})
	return out
}

// loadJavaFiles loads every .java file under rootDir as (uri, text) pairs.
func loadJavaFiles(rootDir FsPath) [][2]string {
	var out [][2]string
	for _, path := range findJavaFiles(rootDir) {
		text, err := os.ReadFile(string(path))
		if err != nil {
			continue
		}
		out = append(out, [2]string{string(pathToURI(path)), string(text)})
	}
	return out
}

// missingConfiguredPaths returns configured classPath/sourcePaths entries that
// do not exist on disk (only when they come from an actual cappu.json, and
// excluding the best-effort external Maven/Gradle defaults). Port of
// src/compiler/compiler.ts.
func missingConfiguredPaths(cfg *config.Config) []string {
	if !cfg.FromFile {
		return nil
	}
	external := map[string]bool{}
	for _, p := range config.ExternalClassPaths {
		external[p] = true
	}
	var missing []string
	for _, p := range append(append([]string{}, cfg.CompilerOptions.ClassPath...), cfg.CompilerOptions.SourcePaths...) {
		if external[p] {
			continue
		}
		resolved := cfg.ResolvePath(p)
		if _, err := os.Stat(resolved); err != nil {
			missing = append(missing, resolved)
		}
	}
	return missing
}

// loadConfiguredSources registers the config's classPath (.class stubs) and
// sourcePaths (.java files, for resolution only) into a program. Port of
// loadConfiguredPaths in src/compiler/compiler.ts.
func loadConfiguredSources(program *compiler.Program, cfg *config.Config) {
	var classPaths []string
	for _, p := range cfg.CompilerOptions.ClassPath {
		classPaths = append(classPaths, cfg.ResolvePath(p))
	}
	compiler.LoadClassPath(program, classPaths)
	for _, p := range cfg.CompilerOptions.SourcePaths {
		for _, f := range loadJavaFiles(FsPath(cfg.ResolvePath(p))) {
			program.AddProjectFile(compiler.URI(f[0]), f[1])
		}
	}
	// .cappu/generated-sources/sources (annotation-processor output) is an
	// implicit extra source path; absent until the first processing compile.
	for _, f := range loadJavaFiles(FsPath(cfg.ResolvePath(filepath.Join(".cappu", "generated-sources", "sources")))) {
		program.AddProjectFile(compiler.URI(f[0]), f[1])
	}
}
