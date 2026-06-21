// Package config loads cappu.json: project configuration for the compiler and
// the language server. JSONC (comments + trailing commas) is read by stripping
// comments before json.Unmarshal; defaults and validation mirror the single
// zod schema in src/config.ts. Programmatic edits live in edit.go.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/tidwall/jsonc"
)

// InlayHints configures the language server's inlay hints. Absent in cappu.json
// means undefined (no hints object), so it is a pointer on LspOptions.
type InlayHints struct {
	ParameterNames bool `json:"parameterNames"`
	VarTypes       bool `json:"varTypes"`
}

// ExperimentalCompiler holds cappu's own (experimental) compiler knobs.
type ExperimentalCompiler struct {
	Enabled       bool `json:"enabled"`
	FailOnDegrade bool `json:"failOnDegrade"`
	Validate      bool `json:"validate"`
}

// CompilerOptions mirrors the "compilerOptions" section.
type CompilerOptions struct {
	ClassPath            []string             `json:"classPath"`
	SourcePaths          []string             `json:"sourcePaths"`
	ResourcePaths        []string             `json:"resourcePaths"`
	Output               string               `json:"output"`
	Quiet                *bool                `json:"quiet,omitempty"`
	Javac                string               `json:"javac"`
	Release              *int                 `json:"release,omitempty"`
	MainClass            string               `json:"mainClass,omitempty"`
	ExperimentalCompiler ExperimentalCompiler `json:"experimentalCompiler"`
}

// LspOptions mirrors the "lspOptions" section.
type LspOptions struct {
	InlayHints *InlayHints `json:"inlayHints,omitempty"`
}

// Dependencies are the "dependencies" section, keyed by configuration. Each
// map is "group:artifact" -> version.
type Dependencies struct {
	API                 map[string]string `json:"api"`
	Implementation      map[string]string `json:"implementation"`
	AnnotationProcessor map[string]string `json:"annotationProcessor"`
	TestImplementation  map[string]string `json:"testImplementation"`
}

// Config is the parsed cappu.json plus where it came from. Mirrors CappuConfig.
type Config struct {
	CompilerOptions   CompilerOptions `json:"compilerOptions"`
	LspOptions        LspOptions      `json:"lspOptions"`
	PackageSources    []string        `json:"packageSources"`
	Dependencies      Dependencies    `json:"dependencies"`
	JDK               string          `json:"jdk,omitempty"`
	License           string          `json:"license,omitempty"`
	GroupID           string          `json:"groupId,omitempty"`
	ArtifactID        string          `json:"artifactId,omitempty"`
	Version           string          `json:"version,omitempty"`
	PublishRepository string          `json:"publishRepository,omitempty"`

	// BaseDir is the directory the config file lives in; relative paths resolve
	// against it. FromFile reports whether an actual cappu.json was read.
	BaseDir  string `json:"-"`
	FromFile bool   `json:"-"`
}

// Load reads the config from explicitPath, or from cwd/cappu.json. A missing
// default file yields the empty (all-defaults) config; a missing explicit path,
// a JSONC parse error or a shape violation returns an error naming the path.
func Load(explicitPath, cwd string) (*Config, error) {
	var path string
	if explicitPath != "" {
		if filepath.IsAbs(explicitPath) {
			path = explicitPath
		} else {
			path = filepath.Join(cwd, explicitPath)
		}
	} else {
		path = filepath.Join(cwd, DefaultConfigName)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if explicitPath != "" {
				return nil, fmt.Errorf("config file not found: %s", path)
			}
			return empty(cwd), nil
		}
		return nil, err
	}

	cfg := &Config{}
	// jsonc.ToJSON strips comments and trailing commas in place, yielding plain
	// JSON for the standard decoder. DisallowUnknownFields is intentionally NOT
	// set: an unknown key (e.g. "$schema") is ignored, matching zod.
	if err := json.Unmarshal(jsonc.ToJSON(raw), cfg); err != nil {
		return nil, fmt.Errorf("invalid %s:\n%w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid %s:\n%w", path, err)
	}
	cfg.BaseDir = filepath.Dir(path)
	cfg.FromFile = true
	return cfg, nil
}

// empty is the all-defaults config (no file present).
func empty(baseDir string) *Config {
	cfg := &Config{}
	cfg.applyDefaults()
	cfg.BaseDir = baseDir
	cfg.FromFile = false
	return cfg
}

// applyDefaults fills the zod-schema defaults for any section/field left absent
// (a nil slice/map means the JSON key was missing, matching zod's `.default()`
// which only fires on undefined).
func (c *Config) applyDefaults() {
	co := &c.CompilerOptions
	if co.ClassPath == nil {
		co.ClassPath = append([]string{DefaultClassPath}, ExternalClassPaths...)
	}
	if co.SourcePaths == nil {
		co.SourcePaths = []string{DefaultSourcePath}
	}
	if co.ResourcePaths == nil {
		co.ResourcePaths = []string{DefaultResourcePath}
	}
	if co.Output == "" {
		co.Output = "classes"
	}
	if co.Javac == "" {
		co.Javac = "javac"
	}
	if c.PackageSources == nil {
		c.PackageSources = append([]string(nil), DefaultPackageSources...)
	}
	d := &c.Dependencies
	if d.API == nil {
		d.API = map[string]string{}
	}
	if d.Implementation == nil {
		d.Implementation = map[string]string{}
	}
	if d.AnnotationProcessor == nil {
		d.AnnotationProcessor = map[string]string{}
	}
	if d.TestImplementation == nil {
		d.TestImplementation = map[string]string{}
	}
}

// validate enforces the enum/range/regex/SPDX/URL refinements zod applies to
// the compiler, coordinate, license and publish fields.
func (c *Config) validate() error {
	switch c.CompilerOptions.Output {
	case "classes", "jar", "fat-jar":
	default:
		return fmt.Errorf(`compilerOptions.output: must be one of "classes", "jar", "fat-jar"`)
	}
	if r := c.CompilerOptions.Release; r != nil && *r < 8 {
		return fmt.Errorf("compilerOptions.release: must be >= 8")
	}
	if c.GroupID != "" && !MavenID.MatchString(c.GroupID) {
		return fmt.Errorf("groupId: must be a Maven id (letters, digits, . _ -)")
	}
	if c.ArtifactID != "" && !MavenID.MatchString(c.ArtifactID) {
		return fmt.Errorf("artifactId: must be a Maven id (letters, digits, . _ -)")
	}
	if c.Version != "" && !Semver.MatchString(c.Version) {
		return fmt.Errorf("version: must be a semver version, e.g. 1.0.0 or 2.1.0-SNAPSHOT")
	}
	if c.License != "" && !IsValidSpdxExpression(c.License) {
		return fmt.Errorf(`license: not a valid SPDX license expression (e.g. "MIT", "Apache-2.0", "(MIT OR Apache-2.0)")`)
	}
	if c.PublishRepository != "" {
		if u, err := url.Parse(c.PublishRepository); err != nil || u.Scheme == "" {
			return fmt.Errorf("publishRepository: must be a valid URL")
		}
	}
	return nil
}

// ResolvePath resolves a (possibly relative) path entry against the config's
// directory.
func (c *Config) ResolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.BaseDir, path)
}

// ArtifactBaseName is the base name (no extension) of the build artifacts:
// "<artifactId>-<version>" when both coordinates are set (the publishable name
// a Maven registry expects), otherwise the project directory name.
func (c *Config) ArtifactBaseName() string {
	if c.ArtifactID != "" && c.Version != "" {
		return c.ArtifactID + "-" + c.Version
	}
	abs, err := filepath.Abs(c.BaseDir)
	if err != nil {
		abs = c.BaseDir
	}
	return filepath.Base(abs)
}
