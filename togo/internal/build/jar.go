// Package build produces a project jar by delegating to javac (the Node build's
// default, non-experimental compile path) and packaging the result. Milestone:
// this is the minimal javac-delegation jar build that `cappu publish` needs;
// the full `cappu compile` command (output kinds, resources, fat-jar, the
// experimental compiler) will build on it later.
package build

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/jdks"
)

// Javac is the compiler binary to use: the provisioned JDK's javac when a "jdk"
// is configured and unpacked, else compilerOptions.javac (default "javac").
func Javac(cfg *config.Config) string {
	if provisioned := jdks.ProvisionedJavac(cfg); provisioned != "" {
		return provisioned
	}
	if cfg.CompilerOptions.Javac != "" {
		return cfg.CompilerOptions.Javac
	}
	return "javac"
}

// skipDirs are build/VCS directories never scanned for sources.
var skipDirs = map[string]struct{}{
	"node_modules": {}, ".git": {}, "target": {}, "build": {}, "out": {}, "bin": {},
}

// JavaFilesIn collects every .java file under dir, skipping build/VCS
// directories. A missing/unreadable dir is empty.
func JavaFilesIn(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // a missing/unreadable dir is simply empty
		}
		if d.IsDir() {
			if _, skip := skipDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".java") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// SourceJavaFiles collects every .java file under the configured sourcePaths.
// Port of findSourceJavaFiles.
func SourceJavaFiles(cfg *config.Config) []string {
	var files []string
	for _, sp := range cfg.CompilerOptions.SourcePaths {
		files = append(files, JavaFilesIn(cfg.ResolvePath(sp))...)
	}
	return files
}

// FormattableFiles are the .java files `cappu format` operates on: every source
// file under the configured sourcePaths, minus any matching a
// formatterOptions.ignore glob (matched against the path relative to the config
// directory). Port of findFormattableFiles.
func FormattableFiles(cfg *config.Config) []string {
	files := SourceJavaFiles(cfg)
	ignore := cfg.FormatterOptions.Ignore
	if len(ignore) == 0 {
		return files
	}
	var matchers []*regexp.Regexp
	for _, pat := range ignore {
		matchers = append(matchers, globToRegexp(pat))
	}
	var out []string
	for _, f := range files {
		rel, err := filepath.Rel(cfg.BaseDir, f)
		if err != nil {
			rel = f
		}
		rel = filepath.ToSlash(rel)
		ignored := false
		for _, m := range matchers {
			if m.MatchString(rel) {
				ignored = true
				break
			}
		}
		if !ignored {
			out = append(out, f)
		}
	}
	return out
}

// globToRegexp converts a glob pattern to an anchored regexp.
// ponytail: handles *, ** and ? only (not brace/bracket classes); add those if
// an ignore pattern ever needs them.
func globToRegexp(pat string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch c {
		case '*':
			if i+1 < len(pat) && pat[i+1] == '*' {
				b.WriteString(".*") // ** matches across path separators
				i++
			} else {
				b.WriteString("[^/]*") // * stays within a path segment
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// ExpandJarDirs returns each existing root plus the jars directly inside it
// (jars pass through). javac's -cp treats a directory as a .class tree only, so
// dependency jars must be listed individually. Port of expandedJarDirs.
func ExpandJarDirs(roots []string) []string {
	var out []string
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		out = append(out, root)
		if strings.HasSuffix(root, ".jar") {
			continue
		}
		jars, _ := filepath.Glob(filepath.Join(root, "*.jar"))
		sort.Strings(jars)
		out = append(out, jars...)
	}
	return out
}

// ClassPath is the configured classPath, resolved and jar-expanded.
func ClassPath(cfg *config.Config) []string {
	resolved := make([]string, len(cfg.CompilerOptions.ClassPath))
	for i, cp := range cfg.CompilerOptions.ClassPath {
		resolved[i] = cfg.ResolvePath(cp)
	}
	return ExpandJarDirs(resolved)
}

// Compile runs javac over sources into outDir with the given classpath entries.
// Returns javac's stderr as the error on failure.
func Compile(cfg *config.Config, sources []string, outDir string, classpath []string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	args := []string{"-d", outDir, "-encoding", "UTF-8"}
	if cfg.CompilerOptions.Release != nil {
		args = append(args, "--release", strconv.Itoa(*cfg.CompilerOptions.Release))
	}
	if len(classpath) > 0 {
		args = append(args, "-cp", strings.Join(classpath, string(os.PathListSeparator)))
	}
	args = append(args, sources...)
	cmd := exec.Command(Javac(cfg), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("javac failed: %s", msg)
	}
	return nil
}

// BuildJar compiles the configured sources with javac and packages the .class
// files into dist/<base>.jar. Returns the jar path. Requires javac on PATH (or
// the configured "javac" binary).
func BuildJar(cfg *config.Config) (string, error) {
	sources := SourceJavaFiles(cfg)
	if len(sources) == 0 {
		return "", fmt.Errorf("no sources to compile (configured sourcePaths are empty)")
	}

	classesDir, err := os.MkdirTemp("", "cappu-classes-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(classesDir) }()

	if err := Compile(cfg, sources, classesDir, ClassPath(cfg)); err != nil {
		return "", err
	}

	distDir := cfg.ResolvePath("./dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return "", err
	}
	jarPath := filepath.Join(distDir, cfg.ArtifactBaseName()+".jar")
	if err := writeJar(jarPath, classesDir, cfg.CompilerOptions.MainClass); err != nil {
		return "", err
	}
	return jarPath, nil
}

// writeJar zips every file under classesDir into a jar, with a minimal manifest
// (Main-Class when configured).
func writeJar(jarPath, classesDir, mainClass string) (err error) {
	out, err := os.Create(jarPath)
	if err != nil {
		return err
	}
	// Closing a written file can surface a flush error, so report it (unless an
	// earlier error already won).
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	zw := zip.NewWriter(out)

	manifest := "Manifest-Version: 1.0\r\n"
	if mainClass != "" {
		manifest += "Main-Class: " + mainClass + "\r\n"
	}
	manifest += "\r\n"
	if err := addZipEntry(zw, "META-INF/MANIFEST.MF", []byte(manifest)); err != nil {
		return err
	}

	walkErr := filepath.WalkDir(classesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(classesDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// jar paths always use forward slashes
		return addZipEntry(zw, filepath.ToSlash(rel), data)
	})
	if walkErr != nil {
		_ = zw.Close()
		return walkErr
	}
	return zw.Close()
}

func addZipEntry(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, bytes.NewReader(data))
	return err
}
