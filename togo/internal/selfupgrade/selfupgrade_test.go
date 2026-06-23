package selfupgrade

import (
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
		asset        string
	}{
		{"linux", "amd64", true, "cappu-linux-x64"},
		{"linux", "arm64", true, "cappu-linux-arm64"},
		{"darwin", "arm64", true, "cappu-darwin-arm64"},
		{"windows", "amd64", true, "cappu-win-x64.exe"},
		{"windows", "arm64", false, ""}, // no windows-arm64
		{"darwin", "amd64", false, ""},  // no macOS x64
		{"freebsd", "amd64", false, ""}, // unsupported OS
	}
	for _, c := range cases {
		got, ok := PlatformTarget(c.goos, c.goarch)
		if ok != c.ok || (ok && got != c.asset) {
			t.Errorf("PlatformTarget(%s,%s) = (%q,%v)", c.goos, c.goarch, got, ok)
		}
	}
}

const linuxAsset = "cappu-linux-x64"

// fakeJSON serves the same release document for any url.
func fakeJSON(release string) FetchJSON {
	return func(string) ([]byte, error) { return []byte(release), nil }
}

func TestLatestReleaseSelectsMatching(t *testing.T) {
	ref, err := LatestRelease(linuxAsset, fakeJSON(
		`{"tag_name":"v1.2.3","published_at":"2026-06-13T00:00:00Z","assets":[`+
			`{"name":"cappu-darwin-arm64","browser_download_url":"https://example/darwin"},`+
			`{"name":"cappu-linux-x64","browser_download_url":"https://example/linux"}]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	want := ReleaseRef{AssetName: "cappu-linux-x64", AssetURL: "https://example/linux", Tag: "v1.2.3", PublishedAt: "2026-06-13T00:00:00Z"}
	if ref != want {
		t.Errorf("ref = %+v, want %+v", ref, want)
	}
}

func TestLatestReleaseErrors(t *testing.T) {
	cases := []struct{ release, wantErr string }{
		{`{}`, "no published release"},
		{`{"tag_name":"v1.0.0","assets":[]}`, "has no asset 'cappu-linux-x64'"},
	}
	for _, c := range cases {
		_, err := LatestRelease(linuxAsset, fakeJSON(c.release))
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("err = %v, want containing %q", err, c.wantErr)
		}
	}
}

func TestSameVersion(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.3", true},
		{"v1.2.4", "1.2.3", false},
		{"v1.2.3", "", false}, // unknown current version never counts as up to date
	}
	for _, c := range cases {
		if got := SameVersion(c.tag, c.current); got != c.want {
			t.Errorf("SameVersion(%q,%q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}

func TestDownloadBinaryFromAsset(t *testing.T) {
	got, err := DownloadBinary("https://example/linux", func(string, DownloadProgress) ([]byte, error) {
		return []byte("ELF-ish bytes"), nil
	}, nil)
	if err != nil || string(got) != "ELF-ish bytes" {
		t.Fatalf("download = (%q, %v)", got, err)
	}
}

func TestDownloadBinaryForwardsProgress(t *testing.T) {
	var calls [][2]int64
	_, err := DownloadBinary("https://example/linux", func(_ string, onProgress DownloadProgress) ([]byte, error) {
		onProgress(50, 100)
		onProgress(100, 100)
		return []byte("x"), nil
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
	result, err := SelfUpgrade(Options{
		TargetPath: target,
		GOOS:       "linux",
		GOARCH:     "amd64",
		FetchJSON: fakeJSON(
			`{"tag_name":"v2.0.0","published_at":"2026-06-13T12:00:00Z","assets":[` +
				`{"name":"cappu-linux-x64","browser_download_url":"https://example/linux"}]}`,
		),
		FetchBytes: func(string, DownloadProgress) ([]byte, error) { return []byte("v2"), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "v2" || result.Release.Tag != "v2.0.0" || result.TargetPath != target || result.UpToDate {
		t.Errorf("result = %+v, contents=%q", result, data)
	}
}

func TestSelfUpgradeSkipsWhenUpToDate(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cappu")
	if err := os.WriteFile(target, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := SelfUpgrade(Options{
		TargetPath:     target,
		CurrentVersion: "2.0.0",
		GOOS:           "linux",
		GOARCH:         "amd64",
		FetchJSON: fakeJSON(
			`{"tag_name":"v2.0.0","published_at":"2026-06-13T12:00:00Z","assets":[` +
				`{"name":"cappu-linux-x64","browser_download_url":"https://example/linux"}]}`,
		),
		FetchBytes: func(string, DownloadProgress) ([]byte, error) {
			t.Fatal("should not download when already up to date")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if !result.UpToDate || string(data) != "v1" {
		t.Errorf("result = %+v, contents=%q (binary should be untouched)", result, data)
	}
}

func TestSelfUpgradeUnbuiltPlatformFailsBeforeFetch(t *testing.T) {
	_, err := SelfUpgrade(Options{GOOS: "windows", GOARCH: "arm64"})
	if err == nil || !strings.Contains(err.Error(), "no cappu build for windows/arm64") {
		t.Errorf("err = %v", err)
	}
}
