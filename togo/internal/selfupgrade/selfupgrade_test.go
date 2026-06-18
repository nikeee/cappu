package selfupgrade

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformTarget(t *testing.T) {
	cases := []struct {
		goos, goarch string
		ok           bool
		artifact     string
	}{
		{"linux", "amd64", true, "cappu-linux-x64"},
		{"linux", "arm64", true, "cappu-linux-arm64"},
		{"windows", "amd64", true, "cappu-windows-x64"},
		{"darwin", "arm64", true, "cappu-darwin-arm64"},
		{"windows", "arm64", false, ""}, // windows is x64-only
		{"darwin", "amd64", false, ""},  // macOS is arm64-only
		{"plan9", "amd64", false, ""},
	}
	for _, c := range cases {
		got, ok := PlatformTarget(c.goos, c.goarch)
		if ok != c.ok {
			t.Errorf("PlatformTarget(%s,%s) ok=%v, want %v", c.goos, c.goarch, ok, c.ok)
			continue
		}
		if ok && got.Artifact != c.artifact {
			t.Errorf("PlatformTarget(%s,%s) = %q", c.goos, c.goarch, got.Artifact)
		}
	}
	if tgt, _ := PlatformTarget("windows", "amd64"); tgt.BinaryName != "cappu.exe" {
		t.Errorf("windows binary = %q, want cappu.exe", tgt.BinaryName)
	}
}

func zipWith(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSelfUpgradeRoundTrip(t *testing.T) {
	target, _ := PlatformTarget("linux", "amd64")
	fetchJSON := func(url string) ([]byte, error) {
		switch {
		case contains(url, "/runs?"):
			return []byte(`{"workflow_runs":[{"id":42,"head_sha":"abcdef1234567","created_at":"2026-06-18T00:00:00Z"}]}`), nil
		case contains(url, "/runs/42/artifacts"):
			return []byte(`{"artifacts":[{"id":7,"name":"cappu-linux-x64","expired":false}]}`), nil
		}
		t.Fatalf("unexpected json url %q", url)
		return nil, nil
	}
	fetchBytes := func(url string, _ DownloadProgress) ([]byte, error) {
		return zipWith(t, "cappu", []byte("NEW-BINARY")), nil
	}

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "cappu")
	if err := os.WriteFile(targetPath, []byte("OLD-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := SelfUpgrade(Options{
		TargetPath: targetPath,
		GOOS:       "linux",
		GOARCH:     "amd64",
		FetchJSON:  fetchJSON,
		FetchBytes: fetchBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Target.Artifact != target.Artifact || res.Artifact.RunSha != "abcdef1234567" {
		t.Errorf("result = %+v", res)
	}
	got, _ := os.ReadFile(targetPath)
	if string(got) != "NEW-BINARY" {
		t.Errorf("binary not replaced: %q", got)
	}
}

func TestLatestArtifactMissing(t *testing.T) {
	target, _ := PlatformTarget("linux", "amd64")
	fetchJSON := func(url string) ([]byte, error) {
		if contains(url, "/runs?") {
			return []byte(`{"workflow_runs":[{"id":1,"head_sha":"x","created_at":"t"}]}`), nil
		}
		return []byte(`{"artifacts":[{"id":9,"name":"cappu-windows-x64","expired":false}]}`), nil
	}
	if _, err := LatestArtifact(target, fetchJSON); err == nil {
		t.Error("expected an error when the artifact is absent")
	}
}

func TestResolveTokenFromEnv(t *testing.T) {
	t.Setenv("CAPPU_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "tok123")
	if token, ok := ResolveToken(); !ok || token != "tok123" {
		t.Errorf("ResolveToken = (%q, %v), want tok123", token, ok)
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
