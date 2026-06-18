package jdks

import "testing"

func TestParseSpec(t *testing.T) {
	cases := []struct {
		spec       string
		ok         bool
		dist, vers string
	}{
		{"temurin-21", true, "temurin", "21"},
		{"corretto-17", true, "corretto", "17"},
		{"temurin", false, "", ""},
		{"unknown-21", false, "", ""},
		{"temurin-lts", false, "", ""},
	}
	for _, c := range cases {
		got, ok := ParseSpec(c.spec)
		if ok != c.ok {
			t.Errorf("ParseSpec(%q) ok=%v, want %v", c.spec, ok, c.ok)
			continue
		}
		if ok && (got.Distribution != c.dist || got.Version != c.vers) {
			t.Errorf("ParseSpec(%q) = %+v", c.spec, got)
		}
	}
}

func TestDownloadURL(t *testing.T) {
	temurin, _ := ParseSpec("temurin-21")
	if got, _ := DownloadURL(temurin, "linux", "amd64"); got != "https://api.adoptium.net/v3/binary/latest/21/ga/linux/x64/jdk/hotspot/normal/eclipse" {
		t.Errorf("temurin linux/amd64 = %q", got)
	}
	if got, _ := DownloadURL(temurin, "darwin", "arm64"); got != "https://api.adoptium.net/v3/binary/latest/21/ga/mac/aarch64/jdk/hotspot/normal/eclipse" {
		t.Errorf("temurin darwin/arm64 = %q", got)
	}
	corretto, _ := ParseSpec("corretto-17")
	if got, _ := DownloadURL(corretto, "windows", "amd64"); got != "https://corretto.aws/downloads/latest/amazon-corretto-17-x64-windows-jdk.zip" {
		t.Errorf("corretto windows/amd64 = %q", got)
	}
	if got, _ := DownloadURL(corretto, "linux", "amd64"); got != "https://corretto.aws/downloads/latest/amazon-corretto-17-x64-linux-jdk.tar.gz" {
		t.Errorf("corretto linux/amd64 = %q", got)
	}
	if _, ok := DownloadURL(temurin, "plan9", "mips"); ok {
		t.Error("unsupported platform should be ok=false")
	}
}
