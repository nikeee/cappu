package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	// A JSONC file with comments and a trailing comma; only "version" is set.
	path := writeConfig(t, `{
  // the project version
  "version": "1.2.3",
}`)
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.FromFile {
		t.Error("FromFile should be true when a file was read")
	}
	if cfg.ConfigPath != path {
		t.Errorf("ConfigPath = %q, want %q", cfg.ConfigPath, path)
	}
	if cfg.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", cfg.Version)
	}
	if cfg.CompilerOptions.Output != "classes" {
		t.Errorf("output default = %q, want classes", cfg.CompilerOptions.Output)
	}
	if cfg.CompilerOptions.Javac != "javac" {
		t.Errorf("javac default = %q, want javac", cfg.CompilerOptions.Javac)
	}
	if len(cfg.CompilerOptions.SourcePaths) != 2 ||
		cfg.CompilerOptions.SourcePaths[0] != DefaultSourcePath ||
		cfg.CompilerOptions.SourcePaths[1] != DefaultGeneratedSourcePath {
		t.Errorf("sourcePaths default = %v", cfg.CompilerOptions.SourcePaths)
	}
	wantSources := DefaultSources()
	if !slices.Equal(cfg.PackageSources, wantSources) {
		t.Errorf("packageSources default = %v, want %v", cfg.PackageSources, wantSources)
	}
	if cfg.Dependencies.API == nil || cfg.Dependencies.Implementation == nil ||
		cfg.Dependencies.CompileOnly == nil || cfg.Dependencies.RuntimeOnly == nil || cfg.Dependencies.Platform == nil {
		t.Error("dependency maps should default to non-nil empty maps")
	}
}

func TestUserClassPathKeepsManagedDir(t *testing.T) {
	// A user-supplied classPath replaces the conventional defaults, but the
	// cappu-managed dependency dir must stay searchable or installed jars never
	// resolve. It is prepended when absent, and not duplicated when present.
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			"prepended when absent",
			`{ "compilerOptions": { "classPath": ["../core/dist/app.jar"] } }`,
			[]string{DefaultClassPath, "../core/dist/app.jar"},
		},
		{
			"not duplicated when present",
			`{ "compilerOptions": { "classPath": ["` + DefaultClassPath + `", "libs"] } }`,
			[]string{DefaultClassPath, "libs"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.body), "")
			if err != nil {
				t.Fatal(err)
			}
			got := cfg.CompilerOptions.ClassPath
			if len(got) != len(tc.want) {
				t.Fatalf("classPath = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("classPath = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestLoadDiscoversConfigFromSubdirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DefaultConfigName),
		[]byte(`{ "version": "1.2.3" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "src", "main", "java")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// No explicit path, cwd is the nested dir: Load walks up to the project root.
	cfg, err := Load("", nested)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.FromFile {
		t.Error("FromFile should be true: the parent config was found")
	}
	if cfg.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3 (parent config not found)", cfg.Version)
	}
	// BaseDir is the project root, not the cwd.
	if cfg.BaseDir != dir {
		t.Errorf("BaseDir = %q, want %q", cfg.BaseDir, dir)
	}
}

func TestExperimentalCompilerFailOnDegradeDefault(t *testing.T) {
	// Absent and present-without-the-key both default to true (zod .default(true));
	// only an explicit false stays false.
	ptr := func(cfg *Config) *bool { return cfg.CompilerOptions.ExperimentalCompiler.FailOnDegrade }
	deref := func(b *bool) bool { return b != nil && *b }

	absent, _ := Load(writeConfig(t, `{}`), "")
	if !deref(ptr(absent)) {
		t.Error("failOnDegrade should default to true when experimentalCompiler is absent")
	}
	noKey, _ := Load(writeConfig(t, `{ "compilerOptions": { "experimentalCompiler": { "enabled": true } } }`), "")
	if !deref(ptr(noKey)) {
		t.Error("failOnDegrade should default to true when the key is absent")
	}
	explicit, _ := Load(writeConfig(t, `{ "compilerOptions": { "experimentalCompiler": { "failOnDegrade": false } } }`), "")
	if deref(ptr(explicit)) {
		t.Error(`an explicit "failOnDegrade": false must be honoured`)
	}
}

func TestNullnessDefaults(t *testing.T) {
	// Absent means disabled (nil pointer).
	absent, _ := Load(writeConfig(t, `{}`), "")
	if absent.CompilerOptions.Nullness != nil {
		t.Error("nullness should be nil (disabled) when absent")
	}
	// Present but with empty annotation lists: the jspecify defaults fill in.
	cfg, _ := Load(writeConfig(t, `{ "compilerOptions": { "nullness": { "enabled": true } } }`), "")
	n := cfg.CompilerOptions.Nullness
	if n == nil || !n.Enabled {
		t.Fatal("nullness should be present and enabled")
	}
	if len(n.NullableAnnotations) != 1 || n.NullableAnnotations[0] != "org.jspecify.annotations.Nullable" {
		t.Errorf("nullableAnnotations default = %v", n.NullableAnnotations)
	}
	if len(n.NonNullAnnotations) != 1 || n.NonNullAnnotations[0] != "org.jspecify.annotations.NonNull" {
		t.Errorf("nonNullAnnotations default = %v", n.NonNullAnnotations)
	}
}

func TestInlayHintsDefaults(t *testing.T) {
	// Absent means undefined (nil pointer): the server uses its own defaults.
	absent, _ := Load(writeConfig(t, `{}`), "")
	if absent.LspOptions.InlayHints != nil {
		t.Error("inlayHints should be nil when absent")
	}
	// Present with one key set false: the other gets its zod default (true),
	// mirroring src/config.test.ts ({ varTypes: false } -> parameterNames true).
	cfg, _ := Load(writeConfig(t, `{ "lspOptions": { "inlayHints": { "varTypes": false } } }`), "")
	h := cfg.LspOptions.InlayHints
	if h == nil {
		t.Fatal("inlayHints should be present")
	}
	if h.ParameterNames == nil || !*h.ParameterNames {
		t.Errorf("parameterNames default = %v, want true", h.ParameterNames)
	}
	if h.VarTypes == nil || *h.VarTypes {
		t.Errorf("varTypes = %v, want false", h.VarTypes)
	}
}

func TestLoadMissingDefaultFileIsEmptyConfig(t *testing.T) {
	cfg, err := Load("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FromFile {
		t.Error("FromFile should be false when no file exists")
	}
	if cfg.CompilerOptions.Output != "classes" {
		t.Error("defaults should still apply to the empty config")
	}
}

func TestLoadMissingExplicitFileErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json"), ""); err == nil {
		t.Error("expected an error for a missing explicit config path")
	}
}

func TestLoadRejectsInvalidVersion(t *testing.T) {
	path := writeConfig(t, `{ "version": "1.0" }`)
	if _, err := Load(path, ""); err == nil {
		t.Error("expected a validation error for a non-semver version")
	}
}

func TestLoadRejectsFreeTextLicense(t *testing.T) {
	path := writeConfig(t, `{ "license": "The Apache Software License, Version 2.0" }`)
	if _, err := Load(path, ""); err == nil {
		t.Error("expected a validation error for a non-SPDX license")
	}
}

func TestLoadRejectsInvalidOutput(t *testing.T) {
	path := writeConfig(t, `{ "compilerOptions": { "output": "exe" } }`)
	if _, err := Load(path, ""); err == nil {
		t.Error("expected a validation error for an output outside the enum")
	}
}

func TestLoadRejectsInvalidStyle(t *testing.T) {
	path := writeConfig(t, `{ "formatterOptions": { "style": "k&r" } }`)
	if _, err := Load(path, ""); err == nil {
		t.Error("expected a validation error for a style outside the enum")
	}
}

// auditOptions.ignore entries are objects with an id and a reason; both are
// required so every accepted advisory says why. Go build only.
func TestLoadAuditIgnore(t *testing.T) {
	path := writeConfig(t, `{ "auditOptions": { "ignore": [
		{ "id": "CVE-2021-44228", "reason": "JNDI lookups disabled" }
	] } }`)
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []AuditIgnore{{ID: "CVE-2021-44228", Reason: "JNDI lookups disabled"}}
	if !slices.Equal(cfg.AuditOptions.Ignore, want) {
		t.Errorf("ignore = %v, want %v", cfg.AuditOptions.Ignore, want)
	}

	defaults, err := Load(writeConfig(t, `{}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if defaults.AuditOptions.Ignore == nil || len(defaults.AuditOptions.Ignore) != 0 {
		t.Errorf("ignore default = %v, want empty non-nil", defaults.AuditOptions.Ignore)
	}

	for _, bad := range []string{
		`[{ "id": "CVE-2021-44228" }]`,
		`[{ "id": "CVE-2021-44228", "reason": "  " }]`,
		`[{ "reason": "no id" }]`,
		`["CVE-2021-44228"]`,
		`[{ "id": " CVE-2021-44228", "reason": "r" }]`,
		`[{ "id": "CVE-2021-44228", "reason": "a" }, { "id": "cve-2021-44228", "reason": "b" }]`,
	} {
		path := writeConfig(t, `{ "auditOptions": { "ignore": `+bad+` } }`)
		if _, err := Load(path, ""); err == nil {
			t.Errorf("expected a validation error for ignore %s", bad)
		}
	}
}

// An importOrder entry is a package prefix ending in "*", never a glob:
// matching is a plain prefix test, so anything else is reported rather than
// silently matching nothing. Mirrors the TS config test.
func TestLoadImportOrder(t *testing.T) {
	path := writeConfig(t, `{ "formatterOptions": { "importOrder": ["android.*", "", "java.*", "*"] } }`)
	c, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"android.*", "", "java.*", "*"}
	if got := c.FormatterOptions.ImportOrder; !slices.Equal(got, want) {
		t.Errorf("ImportOrder = %v, want %v", got, want)
	}
	if c2, err := Load(writeConfig(t, `{}`), ""); err != nil {
		t.Fatalf("Load: %v", err)
	} else if c2.FormatterOptions.ImportOrder != nil {
		t.Errorf("ImportOrder defaults to %v, want nil (the style's own order)", c2.FormatterOptions.ImportOrder)
	}
	// Every accepted shape: the catch-all, a lone blank line, a class-name prefix.
	for _, good := range [][]string{{"*"}, {""}, {"com.acme.Widget*"}} {
		raw, _ := json.Marshal(good)
		c, err := Load(writeConfig(t, `{ "formatterOptions": { "importOrder": `+string(raw)+` } }`), "")
		if err != nil {
			t.Errorf("importOrder %s: %v", raw, err)
		} else if !slices.Equal(c.FormatterOptions.ImportOrder, good) {
			t.Errorf("ImportOrder = %v, want %v", c.FormatterOptions.ImportOrder, good)
		}
	}
	// An empty list is not the same as an unset one: it configures "no groups",
	// so every import falls into the unmatched block. Keeping it non-nil is what
	// carries that distinction into OrderImports.
	if c3, err := Load(writeConfig(t, `{ "formatterOptions": { "importOrder": [] } }`), ""); err != nil {
		t.Fatalf("Load: %v", err)
	} else if c3.FormatterOptions.ImportOrder == nil || len(c3.FormatterOptions.ImportOrder) != 0 {
		t.Errorf("an empty importOrder loaded as %#v, want an empty non-nil slice", c3.FormatterOptions.ImportOrder)
	}
	for _, bad := range []string{
		`["com.*.internal"]`, `["com.acme"]`, `["**"]`, `["java.*extra"]`, `["*.internal.*"]`, `[" "]`,
	} {
		if _, err := Load(writeConfig(t, `{ "formatterOptions": { "importOrder": `+bad+` } }`), ""); err == nil {
			t.Errorf("expected a validation error for importOrder %s", bad)
		}
	}
}

func TestLoadRejectsInvalidCoordinates(t *testing.T) {
	if _, err := Load(writeConfig(t, `{ "groupId": "com example" }`), ""); err == nil {
		t.Error("expected a validation error for a groupId with a space")
	}
	if _, err := Load(writeConfig(t, `{ "artifactId": "my lib" }`), ""); err == nil {
		t.Error("expected a validation error for an artifactId with a space")
	}
}

func TestLoadRejectsReleaseBelow8(t *testing.T) {
	path := writeConfig(t, `{ "compilerOptions": { "release": 5 } }`)
	if _, err := Load(path, ""); err == nil {
		t.Error("expected a validation error for release < 8")
	}
}

func TestLoadAcceptsRelease21(t *testing.T) {
	path := writeConfig(t, `{ "compilerOptions": { "release": 21 } }`)
	if _, err := Load(path, ""); err != nil {
		t.Errorf("release 21 should validate: %v", err)
	}
}

func TestLoadRejectsNonURLPublishRepository(t *testing.T) {
	path := writeConfig(t, `{ "publishRepository": "not a url" }`)
	if _, err := Load(path, ""); err == nil {
		t.Error("expected a validation error for a non-URL publishRepository")
	}
}

func TestLoadAcceptsURLPublishRepository(t *testing.T) {
	path := writeConfig(t, `{ "publishRepository": "https://repo.example.com/maven2" }`)
	if _, err := Load(path, ""); err != nil {
		t.Errorf("a valid publishRepository URL should validate: %v", err)
	}
}

func TestTestOptionsCoverageDefault(t *testing.T) {
	cfg, err := Load(writeConfig(t, `{ "version": "1.0.0" }`), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TestOptions.Coverage {
		t.Error("coverage should default to false")
	}
	cfg, err = Load(writeConfig(t, `{ "testOptions": { "coverage": true } }`), "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TestOptions.Coverage {
		t.Error("coverage: true should parse")
	}
}

func TestDependencyStringOrObject(t *testing.T) {
	path := writeConfig(t, `{
  "dependencies": {
    "platform": { "org.springframework.boot:spring-boot-dependencies": "4.1.0" },
    "implementation": {
      "org.springframework.boot:spring-boot-starter-web": "",
      "com.google.code.gson:gson": "2.13.1",
      "org.apache.commons:commons-lang3": { "version": "3.17.0", "exclude": ["commons-logging:commons-logging", "org.slf4j:*"] },
      "io.netty:netty-transport-native-epoll": { "classifier": "linux-x86_64" }
    }
  }
}`)
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Dependency{
		"org.springframework.boot:spring-boot-starter-web": {},
		"com.google.code.gson:gson":                        {Version: "2.13.1"},
		"org.apache.commons:commons-lang3":                 {Version: "3.17.0", Exclude: []string{"commons-logging:commons-logging", "org.slf4j:*"}},
		"io.netty:netty-transport-native-epoll":            {Classifier: "linux-x86_64"},
	}
	if !reflect.DeepEqual(cfg.Dependencies.Implementation, want) {
		t.Errorf("implementation = %+v", cfg.Dependencies.Implementation)
	}
	if !cfg.Dependencies.Implementation["org.springframework.boot:spring-boot-starter-web"].Managed() {
		t.Error("an empty version is a managed entry")
	}
	if cfg.Dependencies.Platform["org.springframework.boot:spring-boot-dependencies"] != "4.1.0" {
		t.Errorf("platform = %v", cfg.Dependencies.Platform)
	}
	if !reflect.DeepEqual(cfg.Dependencies.Configuration("implementation"), want) || cfg.Dependencies.Configuration("platform") != nil {
		t.Error("Configuration() should name the classpath configurations only")
	}
}

func TestDependencyMarshalRoundTrip(t *testing.T) {
	deps := map[string]Dependency{
		"g:a": {Version: "1"},
		"g:b": {Version: "1", Classifier: "c"},
		"g:m": {},
	}
	b, err := json.Marshal(deps)
	if err != nil {
		t.Fatal(err)
	}
	// a plain entry stays the string shorthand; a managed one is ""
	if want := `{"g:a":"1","g:b":{"version":"1","classifier":"c"},"g:m":""}`; string(b) != want {
		t.Errorf("marshal = %s, want %s", b, want)
	}
	var back map[string]Dependency
	if err := json.Unmarshal(b, &back); err != nil || !reflect.DeepEqual(back, deps) {
		t.Errorf("round trip = %+v (err %v)", back, err)
	}
}

func TestLoadRejectsBadDependencies(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"dependencies":{"platform":{"org.x:bom":""}}}`, "needs a version"},
		{`{"dependencies":{"implementation":{"org.x:y":""}}}`, "no platform manages it"},
		{`{"dependencies":{"implementation":{"org.x:y":{"version":"1","exclude":["nocolon"]}}}}`, "exclude"},
		{`{"dependencies":{"api":{"justartifact":"1"}}}`, "group:artifact"},
	} {
		if _, err := Load(writeConfig(t, tc.body), ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.body, err, tc.want)
		}
	}
	// "" is fine once a platform exists: whether it manages the key is only
	// known at resolve time.
	if _, err := Load(writeConfig(t, `{"dependencies":{"platform":{"org.x:bom":"1"},"implementation":{"org.x:y":""}}}`), ""); err != nil {
		t.Error(err)
	}
}

func TestLoadKeepsPlatformDeclarationOrder(t *testing.T) {
	path := writeConfig(t, `{
  "dependencies": {
    "platform": {
      "org.z:last-bom": "1", // declared first: highest precedence
      "org.a:first-bom": "2",
    },
  },
}`)
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dependencies.PlatformKeys(); !reflect.DeepEqual(got, []string{"org.z:last-bom", "org.a:first-bom"}) {
		t.Errorf("PlatformKeys = %v, want declaration order", got)
	}
	// keys the order does not know (added programmatically) follow, sorted
	cfg.Dependencies.Platform["org.m:mid"] = "3"
	cfg.Dependencies.Platform["org.b:b"] = "3"
	if got := cfg.Dependencies.PlatformKeys(); !reflect.DeepEqual(got, []string{"org.z:last-bom", "org.a:first-bom", "org.b:b", "org.m:mid"}) {
		t.Errorf("PlatformKeys = %v", got)
	}
}

func TestParseRefusesManagedEntryWithoutPlatform(t *testing.T) {
	// what `cappu remove platform` would write after dropping the only BOM
	_, err := Parse([]byte(`{"dependencies":{"platform":{},"testImplementation":{"org.junit.jupiter:junit-jupiter":""}}}`), "/p/cappu.json")
	if err == nil || !strings.Contains(err.Error(), "no platform manages it") {
		t.Errorf("err = %v", err)
	}
}

func TestPackageSourcesStringOrObject(t *testing.T) {
	path := writeConfig(t, `{
  "packageSources": [
    "https://repo.maven.apache.org/maven2",
    { "url": "https://nexus.corp.example/repository/maven-public", "auth": "NEXUS" },
    "file:///opt/repo",
    "~/.m2/repository",
    "../sibling/repo"
  ]
}`)
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []PackageSource{
		{URL: "https://repo.maven.apache.org/maven2"},
		{URL: "https://nexus.corp.example/repository/maven-public", Auth: "NEXUS"},
		{URL: "file:///opt/repo"},
		{URL: "~/.m2/repository"},
		{URL: "../sibling/repo"},
	}
	if !reflect.DeepEqual(cfg.PackageSources, want) {
		t.Errorf("packageSources = %+v", cfg.PackageSources)
	}
	for i, local := range []bool{false, false, true, true, true} {
		if cfg.PackageSources[i].IsLocal() != local {
			t.Errorf("%s IsLocal = %v, want %v", cfg.PackageSources[i].URL, !local, local)
		}
	}
	if dir := cfg.PackageSources[2].LocalDir(cfg); dir != "/opt/repo" {
		t.Errorf("file:// dir = %q", dir)
	}
	if dir := cfg.PackageSources[4].LocalDir(cfg); dir != filepath.Join(cfg.BaseDir, "../sibling/repo") {
		t.Errorf("relative dir = %q", dir)
	}
	if home, _ := os.UserHomeDir(); cfg.PackageSources[3].LocalDir(cfg) != filepath.Join(home, ".m2/repository") {
		t.Errorf("~ dir = %q", cfg.PackageSources[3].LocalDir(cfg))
	}
	b, _ := json.Marshal(cfg.PackageSources[:2])
	if want := `["https://repo.maven.apache.org/maven2",{"url":"https://nexus.corp.example/repository/maven-public","auth":"NEXUS"}]`; string(b) != want {
		t.Errorf("marshal = %s", b)
	}
}

func TestLoadRejectsBadPackageSources(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"packageSources":[{"url":"https://x","auth":"nexus"}]}`, "env var name"},
		{`{"packageSources":[{"url":"../repo","auth":"NEXUS"}]}`, "takes no auth"},
		{`{"packageSources":[{"auth":"NEXUS"}]}`, "url must not be empty"},
	} {
		if _, err := Load(writeConfig(t, tc.body), ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.body, err, tc.want)
		}
	}
}

func TestLoadRejectsSchemelessSourceAndNull(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"packageSources":["nexus.corp.example/repository/maven-public"]}`, "neither an http(s) URL nor a path"},
		{`{"packageSources":[null]}`, "not null"},
		{`{"dependencies":{"platform":{"org.p:bom":"1"},"implementation":{"org.x:y":null}}}`, "not null"},
	} {
		if _, err := Load(writeConfig(t, tc.body), ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.body, err, tc.want)
		}
	}
}
