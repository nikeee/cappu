// Package jdks provisions a JDK declared by a cappu.json "jdk" entry (e.g.
// "temurin-21"): downloaded once into the per-user cache and unpacked into the
// project-local .cappu/jdks/<spec>. Print-free; the CLI renders progress. Port
// of src/jdks/jdks.ts.
package jdks

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/nikeee/cappu/internal/cache"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/httpx"
)

var distributions = map[string]struct{}{"temurin": {}, "corretto": {}}

// Spec is a parsed "<distribution>-<version>".
type Spec struct {
	Distribution string
	Version      string // major feature version ("21", "17")
}

var majorVersion = regexp.MustCompile(`^\d+$`)

// ParseSpec parses "temurin-21" / "corretto-17"; ok is false otherwise.
func ParseSpec(spec string) (Spec, bool) {
	dash := strings.LastIndex(spec, "-")
	if dash < 0 {
		return Spec{}, false
	}
	dist, version := spec[:dash], spec[dash+1:]
	if _, known := distributions[dist]; !known || !majorVersion.MatchString(version) {
		return Spec{}, false
	}
	return Spec{Distribution: dist, Version: version}, true
}

// DownloadURL is the redirecting download url of the latest GA build for a spec
// on this platform, or ok=false when the distribution does not publish for it.
func DownloadURL(spec Spec, goos, goarch string) (string, bool) {
	if spec.Distribution == "temurin" {
		os := map[string]string{"linux": "linux", "darwin": "mac", "windows": "windows"}[goos]
		cpu := map[string]string{"amd64": "x64", "arm64": "aarch64"}[goarch]
		if os == "" || cpu == "" {
			return "", false
		}
		return fmt.Sprintf("https://api.adoptium.net/v3/binary/latest/%s/ga/%s/%s/jdk/hotspot/normal/eclipse", spec.Version, os, cpu), true
	}
	// corretto: stable "latest" urls per os/arch
	os := map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}[goos]
	cpu := map[string]string{"amd64": "x64", "arm64": "aarch64"}[goarch]
	if os == "" || cpu == "" {
		return "", false
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("https://corretto.aws/downloads/latest/amazon-corretto-%s-%s-%s-jdk.%s", spec.Version, cpu, os, ext), true
}

func storeDir() string {
	return cache.Dir("jdks", os.Getenv("CAPPU_JDK_STORE"))
}

// ProjectDir is where a provisioned spec lives: .cappu/jdks/<spec>.
func ProjectDir(cfg *config.Config, spec string) string {
	return cfg.ResolvePath(filepath.Join(".cappu", "jdks", spec))
}

func provisionedBin(cfg *config.Config, name string) string {
	if cfg.JDK == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(ProjectDir(cfg, cfg.JDK), "bin", name)
	if _, err := os.Stat(bin); err == nil {
		return bin
	}
	return ""
}

// ProvisionedJavac is the provisioned JDK's javac, or "" (callers fall back to
// compilerOptions.javac).
func ProvisionedJavac(cfg *config.Config) string { return provisionedBin(cfg, "javac") }

// ProvisionedJava is the provisioned JDK's java launcher, or "".
func ProvisionedJava(cfg *config.Config) string { return provisionedBin(cfg, "java") }

// ProvisionedJdkHome is the provisioned JDK's home directory (holding bin/,
// lib/, jmods/), or "" when no jdk is configured or it has not been unpacked.
// The type checker reads real JDK classes from its jmods/ (jdk_image.go).
func ProvisionedJdkHome(cfg *config.Config) string {
	if cfg.JDK == "" {
		return ""
	}
	dir := ProjectDir(cfg, cfg.JDK)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	return ""
}

// Result reports the outcome of a provision.
type Result struct {
	JdkDir             string
	AlreadyProvisioned bool
	FromCache          bool
}

// Provision ensures cfg's "jdk" spec is unpacked under .cappu/jdks/<spec>. The
// archive is downloaded into the per-user cache at most once; an already
// unpacked project JDK short-circuits.
func Provision(cfg *config.Config, specText string, onProgress func(received, total int64)) (Result, error) {
	spec, ok := ParseSpec(specText)
	if !ok {
		return Result{}, fmt.Errorf("unknown jdk '%s' (expected <distribution>-<version>, e.g. temurin-21)", specText)
	}
	url, ok := DownloadURL(spec, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return Result{}, fmt.Errorf("%s has no download for %s/%s", specText, runtime.GOOS, runtime.GOARCH)
	}

	jdkDir := ProjectDir(cfg, specText)
	// The java launcher doubles as the "fully unpacked" marker (a torn unpack
	// leaves no bin/java because unpack removes the target on failure).
	launcher := filepath.Join(jdkDir, "bin", "java")
	if runtime.GOOS == "windows" {
		launcher += ".exe"
	}
	if _, err := os.Stat(launcher); err == nil {
		return Result{JdkDir: jdkDir, AlreadyProvisioned: true}, nil
	}

	ext := ".tar.gz"
	if strings.HasSuffix(url, ".zip") {
		ext = ".zip"
	}
	archive := filepath.Join(storeDir(), fmt.Sprintf("%s-%s-%s%s", specText, runtime.GOOS, runtime.GOARCH, ext))
	fromCache := false
	if _, err := os.Stat(archive); err == nil {
		fromCache = true
	} else if err := downloadTo(url, archive, onProgress); err != nil {
		return Result{}, err
	}
	if err := unpack(archive, jdkDir); err != nil {
		return Result{}, err
	}
	return Result{JdkDir: jdkDir, FromCache: fromCache}, nil
}

// downloadTo streams url (following redirects) to file, reporting byte progress.
func downloadTo(url, file string, onProgress func(received, total int64)) error {
	resp, err := httpx.Client.Get(url) //nolint:gosec,noctx // a fixed vendor download url
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed: HTTP %d for %s", resp.StatusCode, url)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	part := file + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	total := resp.ContentLength
	_, copyErr := io.Copy(out, &httpx.ProgressReader{R: resp.Body, Total: total, OnProgress: onProgress})
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(part, file)
}

// unpack uses the system tar (bsdtar on win/mac, GNU tar on linux - both handle
// .tar.gz and .zip), stripping one top-level directory so target directly
// contains bin/, lib/, ...
func unpack(archive, target string) error {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("tar", "-xf", archive, "--strip-components=1", "-C", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(target)
		return fmt.Errorf("unpacking %s failed: %s", archive, strings.TrimSpace(string(out)))
	}
	return nil
}
