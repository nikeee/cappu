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

// SourceJavaFiles collects every .java file under the configured sourcePaths,
// skipping build/VCS directories. Port of findSourceJavaFiles.
func SourceJavaFiles(cfg *config.Config) []string {
	var files []string
	for _, sp := range cfg.CompilerOptions.SourcePaths {
		root := cfg.ResolvePath(sp)
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a missing/unreadable source dir is simply empty
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
	}
	return files
}

// classpath joins the existing entries of the configured classPath (dirs and
// jars) with the OS path separator; missing entries are ignored.
func classpath(cfg *config.Config) string {
	var entries []string
	for _, cp := range cfg.CompilerOptions.ClassPath {
		resolved := cfg.ResolvePath(cp)
		if _, err := os.Stat(resolved); err == nil {
			entries = append(entries, resolved)
		}
	}
	return strings.Join(entries, string(os.PathListSeparator))
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

	args := []string{"-d", classesDir}
	if cfg.CompilerOptions.Release != nil {
		args = append(args, "--release", strconv.Itoa(*cfg.CompilerOptions.Release))
	}
	if cp := classpath(cfg); cp != "" {
		args = append(args, "-cp", cp)
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
		return "", fmt.Errorf("javac failed: %s", msg)
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
