package selfupgrade

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Port of src/selfupgrade/selfupgrade.test.ts. Platform names are Go-native
// (GOOS/GOARCH) since PlatformTarget takes those; the matrix mirrors the TS.

func TestPlatformTarget(t *testing.T) {
	cases := []struct {
		goos, goarch string
		ok           bool
		artifact     string
	}{
		{"linux", "amd64", true, "cappu-linux-x64"},
		{"linux", "arm64", true, "cappu-linux-arm64"},
		{"darwin", "arm64", true, "cappu-darwin-arm64"},
		{"windows", "amd64", true, "cappu-windows-x64"},
		{"windows", "arm64", false, ""}, // no windows-arm64
		{"darwin", "amd64", false, ""},  // no macOS x64
		{"freebsd", "amd64", false, ""}, // unsupported OS
	}
	for _, c := range cases {
		got, ok := PlatformTarget(c.goos, c.goarch)
		if ok != c.ok || (ok && got.Artifact != c.artifact) {
			t.Errorf("PlatformTarget(%s,%s) = (%+v,%v)", c.goos, c.goarch, got, ok)
		}
	}
	if tgt, _ := PlatformTarget("windows", "amd64"); tgt.BinaryName != "cappu.exe" {
		t.Errorf("windows binary = %q", tgt.BinaryName)
	}
	if tgt, _ := PlatformTarget("linux", "amd64"); tgt.BinaryName != "cappu" {
		t.Errorf("linux binary = %q", tgt.BinaryName)
	}
}

var linuxTarget = Target{Artifact: "cappu-linux-x64", BinaryName: "cappu"}

// fakeJSON serves the runs document, or the artifacts document for /artifacts.
func fakeJSON(runs, artifacts string) FetchJSON {
	return func(url string) ([]byte, error) {
		if strings.HasSuffix(url, "/artifacts") {
			return []byte(artifacts), nil
		}
		return []byte(runs), nil
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

func TestLatestArtifactSelectsMatching(t *testing.T) {
	ref, err := LatestArtifact(linuxTarget, fakeJSON(
		`{"workflow_runs":[{"id":42,"head_sha":"abc1234def","created_at":"2026-06-13T00:00:00Z"}]}`,
		`{"artifacts":[{"id":7,"name":"cappu-darwin-arm64","expired":false},{"id":9,"name":"cappu-linux-x64","expired":false}]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	want := ArtifactRef{ID: 9, Name: "cappu-linux-x64", RunSha: "abc1234def", RunCreatedAt: "2026-06-13T00:00:00Z"}
	if ref != want {
		t.Errorf("ref = %+v, want %+v", ref, want)
	}
}

func TestLatestArtifactErrors(t *testing.T) {
	cases := []struct {
		runs, artifacts, wantErr string
	}{
		{`{"workflow_runs":[]}`, `{}`, "no successful CD run"},
		{`{"workflow_runs":[{"id":1,"head_sha":"a","created_at":"t"}]}`, `{"artifacts":[]}`, "has no artifact 'cappu-linux-x64'"},
		{`{"workflow_runs":[{"id":1,"head_sha":"a","created_at":"t"}]}`, `{"artifacts":[{"id":9,"name":"cappu-linux-x64","expired":true}]}`, "has expired"},
	}
	for _, c := range cases {
		_, err := LatestArtifact(linuxTarget, fakeJSON(c.runs, c.artifacts))
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("err = %v, want containing %q", err, c.wantErr)
		}
	}
}

func TestDownloadBinaryExtractsZip(t *testing.T) {
	zipBytes := zipWith(t, "cappu", []byte("ELF-ish bytes"))
	got, err := DownloadBinary(9, "cappu", func(string, DownloadProgress) ([]byte, error) { return zipBytes, nil }, nil)
	if err != nil || string(got) != "ELF-ish bytes" {
		t.Fatalf("extract = (%q, %v)", got, err)
	}
	// non-zip input is a clear error
	if _, err := DownloadBinary(9, "cappu", func(string, DownloadProgress) ([]byte, error) { return []byte{1, 2}, nil }, nil); err == nil || !strings.Contains(err.Error(), "not a valid zip") {
		t.Errorf("non-zip err = %v", err)
	}
	// a single non-directory entry is accepted even under a different name
	wrong := zipWith(t, "readme.txt", []byte{1})
	if b, err := DownloadBinary(9, "cappu", func(string, DownloadProgress) ([]byte, error) { return wrong, nil }, nil); err != nil || len(b) != 1 {
		t.Errorf("wrong-name = (%v, %v)", b, err)
	}
}

func TestDownloadBinaryForwardsProgress(t *testing.T) {
	zipBytes := zipWith(t, "cappu", []byte("x"))
	var calls [][2]int64
	_, err := DownloadBinary(9, "cappu", func(_ string, onProgress DownloadProgress) ([]byte, error) {
		onProgress(50, 100)
		onProgress(100, 100)
		return zipBytes, nil
	}, func(received, total int64) { calls = append(calls, [2]int64{received, total}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != [2]int64{50, 100} || calls[1] != [2]int64{100, 100} {
		t.Errorf("progress calls = %v", calls)
	}
}

func TestReplaceBinaryKeepsExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cappu")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceBinary(target, []byte("new binary")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "new binary" {
		t.Errorf("contents = %q", data)
	}
	info, _ := os.Stat(target)
	if info.Mode()&0o111 == 0 {
		t.Error("binary should still be executable")
	}
}

func TestSelfUpgradeEndToEnd(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cappu")
	if err := os.WriteFile(target, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	zipBytes := zipWith(t, "cappu", []byte("v2"))
	result, err := SelfUpgrade(Options{
		TargetPath: target,
		GOOS:       "linux",
		GOARCH:     "amd64",
		FetchJSON: fakeJSON(
			`{"workflow_runs":[{"id":5,"head_sha":"deadbee","created_at":"2026-06-13T12:00:00Z"}]}`,
			`{"artifacts":[{"id":3,"name":"cappu-linux-x64","expired":false}]}`,
		),
		FetchBytes: func(string, DownloadProgress) ([]byte, error) { return zipBytes, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "v2" || result.Artifact.RunSha != "deadbee" || result.TargetPath != target {
		t.Errorf("result = %+v, contents=%q", result, data)
	}
}

func TestSelfUpgradeUnbuiltPlatformFailsBeforeFetch(t *testing.T) {
	_, err := SelfUpgrade(Options{GOOS: "windows", GOARCH: "arm64", Token: "x"})
	if err == nil || !strings.Contains(err.Error(), "no cappu build for windows/arm64") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveTokenPrecedence(t *testing.T) {
	cases := []struct {
		cappu, github, gh, want string
	}{
		{"a", "b", "c", "a"}, // CAPPU_GITHUB_TOKEN wins
		{"", "b", "c", "b"},  // then GITHUB_TOKEN
		{"", "", "c", "c"},   // then GH_TOKEN
	}
	for _, c := range cases {
		t.Setenv("CAPPU_GITHUB_TOKEN", c.cappu)
		t.Setenv("GITHUB_TOKEN", c.github)
		t.Setenv("GH_TOKEN", c.gh)
		if got, ok := ResolveToken(); !ok || got != c.want {
			t.Errorf("ResolveToken(%+v) = (%q,%v), want %q", c, got, ok, c.want)
		}
	}
}
