package compile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
)

func hasJDK() bool {
	return exec.Command("javac", "-version").Run() == nil && exec.Command("javap", "-version").Run() == nil
}

// End-to-end orchestrator test: RunCompile under the experimental compiler emits
// the same class bytes the emitter baseline records, written to the output tree.
func TestRunCompileExperimentalClasses(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Hello.java")
	if err := os.WriteFile(src, []byte(`public class Hello { public static void main(String[] args) { System.out.println("Hello, world"); } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"),
		[]byte(`{"compilerOptions":{"output":"classes","experimentalCompiler":{"enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "dist")
	result := RunCompile([]string{src}, Options{OutDir: outDir, Config: cfg})
	if !result.Success {
		t.Fatalf("compile failed: %+v", result.Diagnostics)
	}
	got, err := os.ReadFile(filepath.Join(outDir, "Hello.class"))
	if err != nil {
		t.Fatalf("Hello.class not written: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "..", "test-fixtures", "emitter", "emit-baselines", "Hello.class"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("written Hello.class (%d bytes) differs from the emitter baseline (%d bytes)", len(got), len(want))
	}
}

// The experimental compiler reports semantic errors and writes nothing.
func TestRunCompileReportsErrors(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Bad.java")
	if err := os.WriteFile(src, []byte("class Bad { void f(String s) {} void m() { f(1); } }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"),
		[]byte(`{"compilerOptions":{"output":"classes","experimentalCompiler":{"enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load("", dir)
	result := RunCompile([]string{src}, Options{OutDir: filepath.Join(dir, "dist"), Config: cfg})
	if result.Success {
		t.Error("expected a failing compile for an undefined symbol")
	}
	if len(result.Written) != 0 {
		t.Errorf("a failing compile must write nothing, wrote %v", result.Written)
	}
	if !hasError(result.Diagnostics) {
		t.Errorf("expected an error diagnostic, got %+v", result.Diagnostics)
	}
}

// TestValidateAgainstJavac ports validateJavac.test.ts (JDK-gated): our emitted
// bytecode's normalized disassembly must match javac's.
func TestValidateAgainstJavac(t *testing.T) {
	if !hasJDK() {
		t.Skip("javac/javap not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "V.java")
	if err := os.WriteFile(src, []byte("class V { int add(int a, int b) { return a + b; } }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cappu.json"),
		[]byte(`{"compilerOptions":{"output":"classes","experimentalCompiler":{"enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load("", dir)
	result := RunCompile([]string{src}, Options{OutDir: dir, Config: cfg})
	if !result.Success {
		t.Fatalf("compile failed: %+v", result.Diagnostics)
	}
	v := compiler.ValidateAgainstJavac([]string{src}, result.Written, "javac")
	if !v.OK || v.Compared != 1 {
		t.Errorf("validation = %+v, want OK with 1 class compared", v)
	}
}

func TestValidateUnavailableJavac(t *testing.T) {
	v := compiler.ValidateAgainstJavac([]string{"X.java"}, nil, "cappu-no-such-javac")
	if v.OK || v.Error == "" {
		t.Errorf("an unavailable javac should yield an error result, got %+v", v)
	}
}

// --- runCompile suite (port of src/compiler/compiler.test.ts) ---------------

func boolp(b bool) *bool { return &b }

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func loadCfg(t *testing.T, dir string) *config.Config {
	t.Helper()
	cfg, err := config.Load("", dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func zipNames(t *testing.T, jar string) []string {
	t.Helper()
	b, err := os.ReadFile(jar)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range compiler.ReadZipEntries(b) {
		names = append(names, e.Name)
	}
	return names
}

func TestRunCompileCleanReturnsWritten(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "A.java", "class A { int x = 1; }")
	r := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: dir, Config: loadCfg(t, dir)})
	if !r.Success || len(r.Written) != 1 || r.Written[0] != filepath.Join(dir, "A.class") {
		t.Errorf("result = %+v", r)
	}
	if len(r.Degraded) != 0 {
		t.Errorf("degraded = %v", r.Degraded)
	}
}

func TestRunCompileParseErrorLocated(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "Broken.java", "class Broken {")
	r := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: dir, Config: loadCfg(t, dir)})
	if r.Success || len(r.Written) != 0 || len(r.Diagnostics) == 0 {
		t.Fatalf("expected a failing located diagnostic, got %+v", r)
	}
	d := r.Diagnostics[0]
	if d.Severity != "error" || d.File != src || d.Line <= 0 || d.Column <= 0 {
		t.Errorf("diagnostic = %+v", d)
	}
}

func TestRunCompileCheckerDiagAndTypeCheckOff(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "C.java", `class C { int x = "s"; }`)
	checked := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: dir, Config: loadCfg(t, dir)})
	if checked.Success || !hasError(checked.Diagnostics) {
		t.Errorf("checked compile should fail, got %+v", checked)
	}
	unchecked := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: dir, TypeCheck: boolp(false), Config: loadCfg(t, dir)})
	if !unchecked.Success {
		t.Errorf("typeCheck:false should succeed, got %+v", unchecked)
	}
}

func TestRunCompileFailOnDegrade(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "D.java", "class D { D() { this(1); } D(int x) { } }")
	r := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: dir, FailOnDegrade: boolp(true), Config: loadCfg(t, dir)})
	if len(r.Degraded) == 0 {
		return // construct became supported; nothing to assert
	}
	if r.Success {
		t.Error("failOnDegrade should fail when a body degraded")
	}
}

func TestMissingConfiguredPathsWarnsOnlyWithConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cappu.json", `{ "compilerOptions": { "classPath": ["./no-such-dir"] } }`)
	missing := MissingConfiguredPaths(loadCfg(t, dir))
	if !contains(missing, filepath.Join(dir, "no-such-dir")) || !contains(missing, filepath.Join(dir, "src/main/java")) {
		t.Errorf("missing = %v", missing)
	}
	bare := t.TempDir()
	if m := MissingConfiguredPaths(loadCfg(t, bare)); len(m) != 0 {
		t.Errorf("no cappu.json should warn nothing, got %v", m)
	}
}

func TestMissingConfiguredPathsSkipsExternalDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cappu.json", "{}")
	missing := MissingConfiguredPaths(loadCfg(t, dir))
	for _, ext := range []string{"target/dependency", "build/libs", "lib", "libs"} {
		if contains(missing, filepath.Join(dir, ext)) {
			t.Errorf("external default %s should not warn", ext)
		}
	}
}

func TestRunCompileAbsentDirsNoThrow(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "cappu.json", `{ "compilerOptions": { "classPath": ["./nope"], "sourcePaths": ["./nada"] } }`)
	src := writeFile(t, dir, "A.java", "class A { }")
	r := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: dir, Config: loadCfg(t, dir)})
	if !r.Success {
		t.Errorf("compile with absent dirs should succeed, got %+v", r)
	}
}

func TestRunCompileJarPacksClasses(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "A.java", "package app; class A { }")
	r := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: filepath.Join(dir, "dist"), Output: "jar", Config: loadCfg(t, dir)})
	if !r.Success || len(r.Written) == 0 || !strings.HasSuffix(r.Written[0], ".jar") {
		t.Fatalf("result = %+v", r)
	}
	got := zipNames(t, r.Written[0])
	want := []string{"META-INF/MANIFEST.MF", "app/A.class"}
	if !equalStrings(got, want) {
		t.Errorf("jar entries = %v, want %v", got, want)
	}
}

func TestRunCompileResourcesCopied(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "A.java", "package app; class A { }")
	writeFile(t, dir, "src/main/resources/conf/app.properties", "k=v\n")
	writeFile(t, dir, "src/main/resources/top.txt", "hi")
	out := filepath.Join(dir, "dist")
	r := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: out, Config: loadCfg(t, dir)})
	if !r.Success {
		t.Fatalf("compile failed: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "conf", "app.properties")); string(b) != "k=v\n" {
		t.Errorf("app.properties = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "top.txt")); string(b) != "hi" {
		t.Errorf("top.txt = %q", b)
	}
	jarred := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: out, Output: "jar", Config: loadCfg(t, dir)})
	got := append([]string{}, zipNames(t, jarred.Written[0])...)
	sortStrings(got)
	want := []string{"META-INF/MANIFEST.MF", "app/A.class", "conf/app.properties", "top.txt"}
	if !equalStrings(got, want) {
		t.Errorf("jar entries = %v, want %v", got, want)
	}
}

func TestRunCompileMainClass(t *testing.T) {
	decodeManifest := func(t *testing.T, jar string) string {
		b, _ := os.ReadFile(jar)
		return string(compiler.ReadZipEntries(b)[0].Read())
	}
	// unique main -> detected
	dir := t.TempDir()
	src := writeFile(t, dir, "M.java", "package app; public class M { public static void main(String[] a) {} }")
	r := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: dir, Output: "jar", Config: loadCfg(t, dir)})
	if !r.Success || decodeManifest(t, r.Written[0]) != "Manifest-Version: 1.0\r\nMain-Class: app.M\r\n\r\n" {
		t.Errorf("unique main manifest = %q", decodeManifest(t, r.Written[0]))
	}
	// two mains, nothing configured -> no Main-Class
	dir2 := t.TempDir()
	a := writeFile(t, dir2, "A.java", "public class A { public static void main(String[] a) {} }")
	b := writeFile(t, dir2, "B.java", "public class B { public static void main(String... a) {} }")
	r2 := RunCompile([]string{a, b}, Options{Experimental: boolp(true), OutDir: dir2, Output: "jar", Config: loadCfg(t, dir2)})
	if !r2.Success || decodeManifest(t, r2.Written[0]) != "Manifest-Version: 1.0\r\n\r\n" {
		t.Errorf("two-mains manifest = %q", decodeManifest(t, r2.Written[0]))
	}
	// configured mainClass wins
	dir3 := t.TempDir()
	writeFile(t, dir3, "cappu.json", `{ "compilerOptions": { "mainClass": "B" } }`)
	a3 := writeFile(t, dir3, "A.java", "public class A { public static void main(String[] a) {} }")
	b3 := writeFile(t, dir3, "B.java", "public class B { public static void main(String[] a) {} }")
	r3 := RunCompile([]string{a3, b3}, Options{Experimental: boolp(true), OutDir: dir3, Output: "jar", Config: loadCfg(t, dir3)})
	if !r3.Success || decodeManifest(t, r3.Written[0]) != "Manifest-Version: 1.0\r\nMain-Class: B\r\n\r\n" {
		t.Errorf("configured-main manifest = %q", decodeManifest(t, r3.Written[0]))
	}
}

func TestRunCompileMultipleMainsWarn(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "A.java", "public class A { public static void main(String[] a) {} }")
	b := writeFile(t, dir, "B.java", "public class B { public static void main(String[] a) {} }")
	r := RunCompile([]string{a, b}, Options{Experimental: boolp(true), OutDir: filepath.Join(dir, "dist"), Output: "jar", Config: loadCfg(t, dir)})
	if !r.Success {
		t.Fatalf("compile failed: %+v", r)
	}
	warned := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "several classes declare main") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a multiple-mains warning, got %v", r.Warnings)
	}
}

func TestRunCompileFatJarMergesDeps(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "B.java", "package app; class B { }")
	depDir := filepath.Join(dir, ".cappu", "lib", "classes")
	_ = os.MkdirAll(depDir, 0o755)
	depJar := compiler.WriteZip([]compiler.ZipEntryInput{
		{Name: "META-INF/MANIFEST.MF", Bytes: []byte{1}}, // must not leak
		{Name: "org/dep/D.class", Bytes: []byte{7}},
		{Name: "app/B.class", Bytes: []byte{9}}, // loses to ours
	})
	if err := os.WriteFile(filepath.Join(depDir, "dep.jar"), depJar, 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "cappu.json", "{}")
	r := RunCompile([]string{src}, Options{Experimental: boolp(true), OutDir: filepath.Join(dir, "dist"), Output: "fat-jar", Config: loadCfg(t, dir)})
	if !r.Success {
		t.Fatalf("compile failed: %+v", r)
	}
	jb, _ := os.ReadFile(r.Written[0])
	entries := compiler.ReadZipEntries(jb)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if !equalStrings(names, []string{"META-INF/MANIFEST.MF", "app/B.class", "org/dep/D.class"}) {
		t.Errorf("fat-jar entries = %v", names)
	}
	if len(entries[1].Read()) <= 9 {
		t.Errorf("our app/B.class should win over the 1-byte dependency fake")
	}
}

func TestDefaultCompileDelegatesToJavac(t *testing.T) {
	if !hasJDK() {
		t.Skip("javac not available")
	}
	dir := t.TempDir()
	src := writeFile(t, dir, "M.java", "package app; public class M { public static void main(String[] a) {} }")
	r := RunCompile([]string{src}, Options{OutDir: filepath.Join(dir, "dist"), Config: loadCfg(t, dir)})
	if !r.Success || len(r.Written) != 1 || r.Written[0] != filepath.Join(dir, "dist", "app", "M.class") {
		t.Fatalf("result = %+v", r)
	}
	bs, _ := os.ReadFile(r.Written[0])
	if len(bs) < 4 || bs[0] != 0xca || bs[1] != 0xfe || bs[2] != 0xba || bs[3] != 0xbe {
		t.Errorf("not a class file: % x", bs[:4])
	}
	jar := RunCompile([]string{src}, Options{OutDir: filepath.Join(dir, "dist2"), Output: "jar", Config: loadCfg(t, dir)})
	if !jar.Success {
		t.Fatalf("jar compile failed: %+v", jar)
	}
	jb, _ := os.ReadFile(jar.Written[0])
	if !strings.Contains(string(compiler.ReadZipEntries(jb)[0].Read()), "Main-Class: app.M") {
		t.Error("javac jar should carry Main-Class app.M")
	}
}

func TestDefaultCompileReleaseTargetsOlderVersion(t *testing.T) {
	if !hasJDK() {
		t.Skip("javac not available")
	}
	dir := t.TempDir()
	writeFile(t, dir, "cappu.json", `{ "compilerOptions": { "release": 17 } }`)
	src := writeFile(t, dir, "R.java", "class R {}")
	r := RunCompile([]string{src}, Options{OutDir: dir, Config: loadCfg(t, dir)})
	if !r.Success {
		t.Fatalf("compile failed: %+v", r)
	}
	bs, _ := os.ReadFile(r.Written[0])
	if major := int(bs[6])<<8 | int(bs[7]); major != 61 { // Java 17 -> 61
		t.Errorf("major version = %d, want 61", major)
	}
}

func TestDefaultCompileSurfacesJavacDiagnostics(t *testing.T) {
	if !hasJDK() {
		t.Skip("javac not available")
	}
	dir := t.TempDir()
	src := writeFile(t, dir, "B.java", `class B { void m() { int x = "s"; } }`)
	r := RunCompile([]string{src}, Options{OutDir: dir, Config: loadCfg(t, dir)})
	if r.Success || len(r.Diagnostics) == 0 {
		t.Fatalf("expected a javac diagnostic, got %+v", r)
	}
	d := r.Diagnostics[0]
	if d.File != src || d.Line != 1 || !strings.Contains(d.Message, "incompatible types") {
		t.Errorf("diagnostic = %+v", d)
	}
}

func contains(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

func TestParseJavacDiagnostics(t *testing.T) {
	out := ParseJavacDiagnostics("A.java:3: error: cannot find symbol\n  int x = y;\n          ^\n1 error\n")
	if len(out) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(out), out)
	}
	if out[0].File != "A.java" || out[0].Line != 3 || out[0].Severity != "error" || out[0].Message != "cannot find symbol" {
		t.Errorf("parsed = %+v", out[0])
	}
}
