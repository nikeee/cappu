package config

import (
	"os"
	"path/filepath"
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
	if cfg.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", cfg.Version)
	}
	if cfg.CompilerOptions.Output != "classes" {
		t.Errorf("output default = %q, want classes", cfg.CompilerOptions.Output)
	}
	if cfg.CompilerOptions.Javac != "javac" {
		t.Errorf("javac default = %q, want javac", cfg.CompilerOptions.Javac)
	}
	if len(cfg.CompilerOptions.SourcePaths) != 1 || cfg.CompilerOptions.SourcePaths[0] != DefaultSourcePath {
		t.Errorf("sourcePaths default = %v", cfg.CompilerOptions.SourcePaths)
	}
	if len(cfg.PackageSources) != len(DefaultPackageSources) {
		t.Errorf("packageSources default = %v", cfg.PackageSources)
	}
	if cfg.Dependencies.API == nil || cfg.Dependencies.Implementation == nil {
		t.Error("dependency maps should default to non-nil empty maps")
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
