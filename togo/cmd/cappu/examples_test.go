package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// End-to-end over the committed example projects (../../../examples): install
// from Maven Central (lockfiles pin versions), compile, run the fat jar and
// compare stdout. Port of src/examples.test.ts. Needs a JDK; the dependency-
// resolving cases additionally need network (gated on a Maven-reachability
// probe so the offline run skips cleanly).

var (
	buildOnce sync.Once
	cappuBin  string
	buildErr  error
)

func cappu(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cappu-bin-")
		if err != nil {
			buildErr = err
			return
		}
		cappuBin = filepath.Join(dir, "cappu")
		out, err := exec.Command("go", "build", "-o", cappuBin, ".").CombinedOutput()
		if err != nil {
			buildErr = err
			cappuBin = string(out)
		}
	})
	if buildErr != nil {
		t.Fatalf("build cappu: %v\n%s", buildErr, cappuBin)
	}
	return cappuBin
}

func hasJavac() bool { return exec.Command("javac", "-version").Run() == nil }

func javaBin() string {
	out, err := exec.Command("which", "javac").Output()
	if err != nil {
		return "java"
	}
	javac, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		return "java"
	}
	return filepath.Join(filepath.Dir(javac), "java")
}

var (
	netOnce sync.Once
	netOK   bool
)

func mavenReachable() bool {
	netOnce.Do(func() {
		client := http.Client{Timeout: 4 * time.Second}
		resp, err := client.Head("https://repo1.maven.org/maven2/")
		if err == nil {
			_ = resp.Body.Close()
			netOK = true
		}
	})
	return netOK
}

// e2eEnabled gates the dependency-resolving example tests: they hit Maven Central
// live (install/audit/licenses/update/run), so they are opt-in via CAPPU_E2E=1 -
// a dedicated networked CI leg, like the TS suite's gating - rather than part of
// the default `go test ./...` (which would flake on Maven rate-limiting).
func e2eEnabled() bool {
	return os.Getenv("CAPPU_E2E") == "1" && hasJavac() && mavenReachable()
}

func examplesDir() string { return filepath.Join("..", "..", "..", "examples") }

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	_ = filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		return os.WriteFile(target, b, 0o644)
	})
}

// runExample installs an example then runs the given command (default compile),
// returning the run's stdout (for compile, the fat jar's stdout under java).
func runExample(t *testing.T, name string, command ...string) string {
	t.Helper()
	if len(command) == 0 {
		command = []string{"compile"}
	}
	root := t.TempDir()
	store := t.TempDir()
	work := filepath.Join(root, name)
	for _, entry := range []string{"cappu.json", "cappu-lock.json", "src", ".gitignore"} {
		src := filepath.Join(examplesDir(), name, entry)
		if _, err := os.Stat(src); err == nil {
			copyTree(t, src, filepath.Join(work, entry))
		}
	}
	env := append(os.Environ(), "CAPPU_PACKAGE_STORE="+store)

	install := exec.Command(cappu(t), "install")
	install.Dir, install.Env = work, env
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install %s: %v\n%s", name, err, out)
	}
	run := exec.Command(cappu(t), command...)
	run.Dir, run.Env = work, env
	out, err := run.CombinedOutput()
	if command[0] != "compile" {
		// test/audit may exit non-zero by design; the caller inspects stdout.
		return string(out)
	}
	if err != nil {
		t.Fatalf("compile %s: %v\n%s", name, err, out)
	}
	jar := exec.Command(javaBin(), "-jar", filepath.Join(work, "dist", name+".jar"))
	jarOut, err := jar.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s.jar: %v\n%s", name, err, jarOut)
	}
	return string(jarOut)
}

func TestExampleGsonApp(t *testing.T) {
	if !e2eEnabled() {
		t.Skip("set CAPPU_E2E=1 (with javac + Maven Central) to run the dependency e2e suite")
	}
	if got := runExample(t, "gson-app"); got != "{\"x\":1,\"y\":2}\n" {
		t.Errorf("gson-app stdout = %q", got)
	}
}

func TestExampleJunitApp(t *testing.T) {
	if !e2eEnabled() {
		t.Skip("set CAPPU_E2E=1 (with javac + Maven Central) to run the dependency e2e suite")
	}
	out := runExample(t, "junit-app", "test")
	if !strings.Contains(out, "2 tests successful") || !strings.Contains(out, "0 tests failed") {
		t.Errorf("junit-app test output = %q", out)
	}
}

func TestExampleAuditApp(t *testing.T) {
	if !e2eEnabled() {
		t.Skip("set CAPPU_E2E=1 (with javac + Maven Central) to run the dependency e2e suite")
	}
	root := t.TempDir()
	store := t.TempDir()
	work := filepath.Join(root, "audit-app")
	_ = os.MkdirAll(work, 0o755)
	src, _ := os.ReadFile(filepath.Join(examplesDir(), "audit-app", "cappu.json"))
	_ = os.WriteFile(filepath.Join(work, "cappu.json"), src, 0o644)
	env := append(os.Environ(), "CAPPU_PACKAGE_STORE="+store)

	audit := exec.Command(cappu(t), "audit")
	audit.Dir, audit.Env = work, env
	out, err := audit.CombinedOutput()
	if err == nil {
		t.Error("audit with a known vulnerable dep should exit non-zero")
	}
	s := string(out)
	if !strings.Contains(s, "CVE-2021-44228") || !strings.Contains(s, "org.apache.logging.log4j:log4j-core:2.14.1") {
		t.Errorf("audit output missing Log4Shell finding:\n%s", s)
	}

	jsonCmd := exec.Command(cappu(t), "audit", "--json")
	jsonCmd.Dir, jsonCmd.Env = work, env
	jsonOut, _ := jsonCmd.CombinedOutput()
	var report struct {
		Vulnerable []struct {
			Coordinate string   `json:"coordinate"`
			Path       []string `json:"path"`
			Advisories []struct {
				Aliases []string `json:"aliases"`
			} `json:"advisories"`
		} `json:"vulnerable"`
	}
	if err := json.Unmarshal(jsonOut, &report); err != nil {
		t.Fatalf("audit --json: %v\n%s", err, jsonOut)
	}
	found := false
	for _, v := range report.Vulnerable {
		if strings.HasPrefix(v.Coordinate, "org.apache.logging.log4j:log4j-core:") {
			found = true
			if len(v.Path) == 0 || v.Path[len(v.Path)-1] != v.Coordinate {
				t.Errorf("path should end at the vulnerable pkg: %v", v.Path)
			}
		}
	}
	if !found {
		t.Error("audit --json did not report log4j-core")
	}
}

func TestExampleGsonLicenses(t *testing.T) {
	if !e2eEnabled() {
		t.Skip("set CAPPU_E2E=1 (with javac + Maven Central) to run the dependency e2e suite")
	}
	root := t.TempDir()
	store := t.TempDir()
	work := filepath.Join(root, "gson-app")
	_ = os.MkdirAll(work, 0o755)
	src, _ := os.ReadFile(filepath.Join(examplesDir(), "gson-app", "cappu.json"))
	_ = os.WriteFile(filepath.Join(work, "cappu.json"), src, 0o644)
	env := append(os.Environ(), "CAPPU_PACKAGE_STORE="+store)

	human := exec.Command(cappu(t), "licenses")
	human.Dir, human.Env = work, env
	hout, err := human.CombinedOutput()
	if err != nil {
		t.Fatalf("licenses: %v\n%s", err, hout)
	}
	if !strings.Contains(string(hout), "com.google.code.gson:gson:2.13.1") || !strings.Contains(string(hout), "Apache-2.0") {
		t.Errorf("licenses output = %q", hout)
	}

	jsonCmd := exec.Command(cappu(t), "licenses", "--json")
	jsonCmd.Dir, jsonCmd.Env = work, env
	jout, _ := jsonCmd.CombinedOutput()
	var rows []struct {
		Coordinate string   `json:"coordinate"`
		Spdx       []string `json:"spdx"`
	}
	if err := json.Unmarshal(jout, &rows); err != nil {
		t.Fatalf("licenses --json: %v\n%s", err, jout)
	}
	ok := false
	for _, r := range rows {
		if r.Coordinate == "com.google.code.gson:gson:2.13.1" {
			for _, s := range r.Spdx {
				if s == "Apache-2.0" {
					ok = true
				}
			}
		}
	}
	if !ok {
		t.Errorf("licenses --json missing gson Apache-2.0: %s", jout)
	}
}

func TestExampleUpdateBumpsDependency(t *testing.T) {
	if !e2eEnabled() {
		t.Skip("set CAPPU_E2E=1 (with javac + Maven Central) to run the dependency e2e suite")
	}
	work := t.TempDir()
	store := t.TempDir()
	_ = os.WriteFile(filepath.Join(work, "cappu.json"),
		[]byte("{\n  \"dependencies\": {\n    \"implementation\": {\n      // pinned old on purpose\n      \"com.google.code.gson:gson\": \"2.8.9\"\n    }\n  }\n}\n"), 0o644)
	upd := exec.Command(cappu(t), "update")
	upd.Dir = work
	upd.Env = append(os.Environ(), "CAPPU_PACKAGE_STORE="+store)
	if out, err := upd.CombinedOutput(); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	after, _ := os.ReadFile(filepath.Join(work, "cappu.json"))
	s := string(after)
	if strings.Contains(s, "2.8.9") || !strings.Contains(s, "com.google.code.gson:gson") || !strings.Contains(s, "// pinned old on purpose") {
		t.Errorf("update result cappu.json:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join(work, "cappu-lock.json")); err != nil {
		t.Error("update should refresh the lockfile")
	}
}

func TestExampleCompileArtifactName(t *testing.T) {
	if !hasJavac() {
		t.Skip("needs javac")
	}
	work := t.TempDir()
	_ = os.WriteFile(filepath.Join(work, "cappu.json"), []byte(`{ "compilerOptions": { "mainClass": "x.M", "quiet": true } }`), 0o644)
	srcDir := filepath.Join(work, "src", "main", "java", "x")
	_ = os.MkdirAll(srcDir, 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, "M.java"), []byte("package x; public class M { public static void main(String[] a) {} }"), 0o644)
	c := exec.Command(cappu(t), "compile", "-o", "jar", "--artifact", "app")
	c.Dir = work
	c.Env = append(os.Environ(), "CAPPU_PACKAGE_STORE="+t.TempDir())
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(work, "dist", "app.jar")); err != nil {
		t.Errorf("--artifact should produce dist/app.jar: %v", err)
	}
}

func TestExampleCompilePublishableJarAndPom(t *testing.T) {
	if !hasJavac() {
		t.Skip("needs javac")
	}
	work := t.TempDir()
	cfg := map[string]any{
		"groupId": "com.example", "artifactId": "demo-lib", "version": "1.0.0", "license": "MIT",
		"dependencies": map[string]any{"implementation": map[string]string{"com.google.code.gson:gson": "2.13.1"}},
	}
	b, _ := json.Marshal(cfg)
	_ = os.WriteFile(filepath.Join(work, "cappu.json"), b, 0o644)
	srcDir := filepath.Join(work, "src", "main", "java", "com", "example")
	_ = os.MkdirAll(srcDir, 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, "Hello.java"), []byte("package com.example; public class Hello {}"), 0o644)
	c := exec.Command(cappu(t), "compile", "-o", "jar")
	c.Dir = work
	c.Env = append(os.Environ(), "CAPPU_PACKAGE_STORE="+t.TempDir())
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(work, "dist", "demo-lib-1.0.0.jar")); err != nil {
		t.Errorf("expected dist/demo-lib-1.0.0.jar: %v", err)
	}
	pom, err := os.ReadFile(filepath.Join(work, "dist", "demo-lib-1.0.0.pom"))
	if err != nil {
		t.Fatalf("no POM: %v", err)
	}
	ps := string(pom)
	if !strings.Contains(ps, "<artifactId>demo-lib</artifactId>") || !strings.Contains(ps, "<version>1.0.0</version>") || !strings.Contains(ps, "<artifactId>gson</artifactId>") {
		t.Errorf("POM missing expected coordinates:\n%s", ps)
	}
}
