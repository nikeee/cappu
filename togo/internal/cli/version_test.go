package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/config"
)

// Port of src/cli/version.test.ts. The TS test spawns the CLI binary; here we
// drive RunVersion directly (the dispatch wiring is covered by main).

func hasGit() bool {
	return exec.Command("git", "--version").Run() == nil
}

func loadDir(t *testing.T, dir string) *config.Config {
	t.Helper()
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func TestVersionBumpsAndTagsAtGitRoot(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	cfgPath := filepath.Join(dir, "cappu.json")
	if err := os.WriteFile(cfgPath, []byte("{\n  // my project\n  \"groupId\": \"com.example\",\n  \"artifactId\": \"lib\",\n  \"version\": \"1.2.3\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "init")

	if code := RunVersion("minor", "", loadDir(t, dir)); code != 0 {
		t.Fatalf("RunVersion exit = %d", code)
	}

	after, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(after), `"version": "1.3.0"`) {
		t.Errorf("version not bumped:\n%s", after)
	}
	if !strings.Contains(string(after), "// my project") {
		t.Errorf("comment not preserved:\n%s", after)
	}
	tags, _ := exec.Command("git", "-C", dir, "tag").Output()
	if !strings.Contains(string(tags), "v1.3.0") {
		t.Errorf("tag v1.3.0 missing, got %q", tags)
	}
	subject, _ := exec.Command("git", "-C", dir, "log", "-1", "--pretty=%s").Output()
	if strings.TrimSpace(string(subject)) != "v1.3.0" {
		t.Errorf("commit subject = %q, want v1.3.0", strings.TrimSpace(string(subject)))
	}
}

func TestVersionBumpsOutsideGitRepo(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cappu.json")
	if err := os.WriteFile(cfgPath, []byte("{\n  \"version\": \"0.9.0\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := RunVersion("patch", "", loadDir(t, dir)); code != 0 {
		t.Fatalf("RunVersion exit = %d", code)
	}
	after, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(after), `"version": "0.9.1"`) {
		t.Errorf("version not bumped:\n%s", after)
	}
}
