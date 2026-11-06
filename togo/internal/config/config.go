// Package config loads cappu.json: project configuration for the compiler and
// the language server. JSONC (comments + trailing commas) is read by stripping
// comments before json.Unmarshal; defaults and validation mirror the single
// zod schema in src/config.ts. Programmatic edits live in edit.go.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"encoding/json"
	"github.com/tidwall/jsonc"
	"net/url"
	"path/filepath"
)

// InlayHints configures the language server's inlay hints. Absent in cappu.json
// means undefined (no hints object), so it is a pointer on LspOptions. Each field
// is a pointer so an absent key gets its zod default (true) in applyDefaults,
// distinct from an explicit false.
type InlayHints struct {
	ParameterNames *bool `json:"parameterNames"`
	VarTypes       *bool `json:"varTypes"`
}

// ExperimentalCompiler holds cappu's own (experimental) compiler knobs.
type ExperimentalCompiler struct {
	Enabled bool `json:"enabled"`
	// FailOnDegrade defaults to true (zod .default(true)); a pointer so an
	// explicit "failOnDegrade": false is distinguishable from an absent key.
	FailOnDegrade *bool `json:"failOnDegrade"`
	Validate      bool  `json:"validate"`
	DebugInfo     bool  `json:"debugInfo"`
}

// Nullness configures jspecify nullness checking (nikeee/cappu#25). Absent in
// cappu.json means undefined (disabled), so it is a pointer on CompilerOptions.
// The annotation lists are matched by simple name, so a project using JSR-305 can
// point them at javax.annotation.* instead of the jspecify default.
type Nullness struct {
	Enabled                 bool     `json:"enabled"`
	NullableAnnotations     []string `json:"nullableAnnotations"`
	NonNullAnnotations      []string `json:"nonNullAnnotations"`
	NullMarkedAnnotations   []string `json:"nullMarkedAnnotations"`
	NullUnmarkedAnnotations []string `json:"nullUnmarkedAnnotations"`
}

// CompilerOptions mirrors the "compilerOptions" section.
type CompilerOptions struct {
	ClassPath            []string             `json:"classPath"`
	SourcePaths          []string             `json:"sourcePaths"`
	ResourcePaths        []string             `json:"resourcePaths"`
	Output               string               `json:"output"`
	Javac                string               `json:"javac"`
	JavacOptions         []string             `json:"javacOptions"`
	Release              *int                 `json:"release,omitempty"`
	MainClass            string               `json:"mainClass,omitempty"`
	ExperimentalCompiler ExperimentalCompiler `json:"experimentalCompiler"`
	Nullness             *Nullness            `json:"nullness,omitempty"`
}

// LspOptions mirrors the "lspOptions" section.
type LspOptions struct {
	InlayHints *InlayHints `json:"inlayHints,omitempty"`
}

// DapOptions mirrors the "dapOptions" section.
type DapOptions struct {
	// EnableAssertions passes -ea to every debuggee launched by `cappu dap`.
	EnableAssertions bool `json:"enableAssertions"`
}

// Dependency is one entry of a dependencies map. In cappu.json it is the plain
// `"group:artifact": "version"` string, or an object when more than a version
// is needed: `{"version": "1.0", "classifier": "linux-x86_64", "exclude": ["g:a"]}`.
// An empty Version means managed: a `platform` entry supplies it.
type Dependency struct {
	Version    string `json:"version,omitempty"`
	Classifier string `json:"classifier,omitempty"`
	// Exclude lists "group:artifact" patterns (either side may be "*") pruned
	// from this dependency's transitive subtree.
	Exclude []string `json:"exclude,omitempty"`
}

// UnmarshalJSON accepts the string shorthand or the object form.
func (d *Dependency) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &d.Version)
	}
	if string(b) == "null" {
		return errors.New("a dependency must be a version string or an object, not null")
	}
	type plain Dependency // no methods, so this does not recurse
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*d = Dependency(p)
	return nil
}

// MarshalJSON writes the string shorthand when only a version is set, so a
// round trip leaves a plain entry plain.
func (d Dependency) MarshalJSON() ([]byte, error) {
	if d.Classifier == "" && len(d.Exclude) == 0 {
		return json.Marshal(d.Version)
	}
	type plain Dependency
	return json.Marshal(plain(d))
}

// Managed reports whether the version is left to a platform entry.
func (d Dependency) Managed() bool { return d.Version == "" }

// PackageSource is one packageSources entry: the plain URL string, or an
// object `{"url": ..., "auth": "NAME"}` for a repository that needs
// credentials. NAME selects the env vars CAPPU_SOURCE_NAME_USERNAME +
// CAPPU_SOURCE_NAME_PASSWORD (basic) or CAPPU_SOURCE_NAME_TOKEN (bearer);
// nothing secret lives in cappu.json. A `file://` URL or a filesystem path
// (`~/.m2/repository`, `../repo`, relative to cappu.json) is a local maven2
// repository.
type PackageSource struct {
	URL  string `json:"url"`
	Auth string `json:"auth,omitempty"`
}

// UnmarshalJSON accepts the string shorthand or the object form.
func (s *PackageSource) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &s.URL)
	}
	if string(b) == "null" {
		return errors.New("a package source must be a URL string or an object, not null")
	}
	type plain PackageSource
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*s = PackageSource(p)
	return nil
}

// MarshalJSON writes the string shorthand for an anonymous source.
func (s PackageSource) MarshalJSON() ([]byte, error) {
	if s.Auth == "" {
		return json.Marshal(s.URL)
	}
	type plain PackageSource
	return json.Marshal(plain(s))
}

// IsLocal reports a local repository: a file:// URL or a path written as one
// (absolute, ./, ../ or ~). A bare host name is neither: validate rejects it.
func (s PackageSource) IsLocal() bool {
	u := s.URL
	return strings.HasPrefix(u, "file://") || strings.HasPrefix(u, "/") || strings.HasPrefix(u, "./") ||
		strings.HasPrefix(u, "../") || u == "~" || strings.HasPrefix(u, "~/")
}

// LocalDir is the repository directory of a local source: `~` expanded,
// relative paths resolved against the config's directory.
func (s PackageSource) LocalDir(cfg *Config) string {
	dir := strings.TrimPrefix(s.URL, "file://")
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = home + dir[1:]
		}
	}
	return cfg.ResolvePath(dir)
}

// DefaultSources are DefaultPackageSources as entries.
func DefaultSources() []PackageSource {
	out := make([]PackageSource, 0, len(DefaultPackageSources))
	for _, url := range DefaultPackageSources {
		out = append(out, PackageSource{URL: url})
	}
	return out
}

// Dependencies are the "dependencies" section, keyed by configuration. Each
// classpath configuration maps "group:artifact" -> Dependency; Platform maps
// "group:artifact" -> version of a BOM whose <dependencyManagement> fills the
// managed entries and overrides transitive versions (Maven semantics).
type Dependencies struct {
	Platform map[string]string `json:"platform"`
	// PlatformOrder is the order the platform keys are written in cappu.json
	// (a Go map forgets it): when two platforms manage the same artifact the
	// first declared wins, like Maven's first import. Keys not listed here
	// (added programmatically) follow in sorted order; see PlatformKeys.
	PlatformOrder  []string              `json:"-"`
	API            map[string]Dependency `json:"api"`
	Implementation map[string]Dependency `json:"implementation"`
	// CompileOnly is on the compile classpath only (Maven provided): never in
	// a fat jar, a run or the published runtime scope.
	CompileOnly map[string]Dependency `json:"compileOnly"`
	// RuntimeOnly is never on the compile classpath (Maven runtime): drivers
	// and implementations loaded by name.
	RuntimeOnly         map[string]Dependency `json:"runtimeOnly"`
	AnnotationProcessor map[string]Dependency `json:"annotationProcessor"`
	TestImplementation  map[string]Dependency `json:"testImplementation"`
}

// PlatformKeys are the platform keys in precedence order: declaration order
// first, then any key PlatformOrder does not list, sorted.
func (d Dependencies) PlatformKeys() []string {
	if len(d.Platform) == 0 {
		return nil
	}
	keys := make([]string, 0, len(d.Platform))
	seen := map[string]bool{}
	for _, key := range d.PlatformOrder {
		if _, ok := d.Platform[key]; ok && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	for _, key := range slices.Sorted(maps.Keys(d.Platform)) {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	return keys
}

// SetConfiguration replaces the entries of one classpath configuration; an
// unknown name is ignored.
func (d *Dependencies) SetConfiguration(name string, entries map[string]Dependency) {
	switch name {
	case "api":
		d.API = entries
	case "implementation":
		d.Implementation = entries
	case "compileOnly":
		d.CompileOnly = entries
	case "runtimeOnly":
		d.RuntimeOnly = entries
	case "annotationProcessor":
		d.AnnotationProcessor = entries
	case "testImplementation":
		d.TestImplementation = entries
	}
}

// Configuration returns the entries of one classpath configuration by its
// cappu.json name, or nil for an unknown name (platform is not one).
func (d Dependencies) Configuration(name string) map[string]Dependency {
	switch name {
	case "api":
		return d.API
	case "implementation":
		return d.Implementation
	case "compileOnly":
		return d.CompileOnly
	case "runtimeOnly":
		return d.RuntimeOnly
	case "annotationProcessor":
		return d.AnnotationProcessor
	case "testImplementation":
		return d.TestImplementation
	}
	return nil
}

// FormatterOptions mirrors the "formatterOptions" section. Deliberately few
// knobs - the only choice is the indent style.
type FormatterOptions struct {
	// Style is "google" (2-space) or "aosp" (4-space).
	Style string `json:"style"`
	// Ignore is a list of glob patterns excluded from formatting.
	Ignore []string `json:"ignore"`
	// ImportOrder lays out the import block: package prefixes ending in "*", in
	// the order their groups appear, with "" inserting a blank line. An import
	// joins the group whose prefix matches it longest. Statics always come first
	// in their own group; imports matching nothing form a last group. Nil means
	// the style's own google-java-format order.
	ImportOrder []string `json:"importOrder"`
}

// TestOptions mirrors the "testOptions" section. The JUnit launcher always
// streams its summary to stdout; OutputFormat is additive - only "junit" also
// writes junit-XML reports into ReportsDir. "tap" is not native to the launcher.
type TestOptions struct {
	// OutputFormat is "text" (stdout only) or "junit" (also write junit-XML).
	OutputFormat string `json:"outputFormat"`
	// ReportsDir is where junit-XML and jacoco.exec land.
	ReportsDir string `json:"reportsDir"`
	// Coverage attaches the JaCoCo agent and writes jacoco.exec into ReportsDir.
	Coverage bool `json:"coverage"`
}

// AuditIgnore is one "auditOptions.ignore" entry: an advisory `cappu audit`
// still reports but no longer fails on. ID matches an advisory's primary id
// (GHSA-...) or one of its CVE aliases, ignoring case; Reason records why it
// is accepted.
type AuditIgnore struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// AuditOptions mirrors the "auditOptions" section. Go build only (not yet in
// src/config.ts).
type AuditOptions struct {
	// Ignore lists the accepted advisories. An entry that matches no finding is
	// an error unless `cappu audit --allow-stale-ignores` is given.
	Ignore []AuditIgnore `json:"ignore"`
}

// Config is the parsed cappu.json plus where it came from. Mirrors CappuConfig.
type Config struct {
	CompilerOptions   CompilerOptions  `json:"compilerOptions"`
	LspOptions        LspOptions       `json:"lspOptions"`
	DapOptions        DapOptions       `json:"dapOptions"`
	FormatterOptions  FormatterOptions `json:"formatterOptions"`
	TestOptions       TestOptions      `json:"testOptions"`
	AuditOptions      AuditOptions     `json:"auditOptions"`
	PackageSources    []PackageSource  `json:"packageSources"`
	Dependencies      Dependencies     `json:"dependencies"`
	JDK               string           `json:"jdk,omitempty"`
	License           string           `json:"license,omitempty"`
	GroupID           string           `json:"groupId,omitempty"`
	ArtifactID        string           `json:"artifactId,omitempty"`
	Version           string           `json:"version,omitempty"`
	PublishRepository string           `json:"publishRepository,omitempty"`

	// BaseDir is the directory the config file lives in; relative paths resolve
	// against it. FromFile reports whether an actual cappu.json was read.
	BaseDir  string `json:"-"`
	FromFile bool   `json:"-"`
	// ConfigPath is the absolute path of the loaded cappu.json; empty when
	// pure defaults. Mirrors CappuConfig.configPath.
	ConfigPath string `json:"-"`
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
		found := findConfigUpward(cwd)
		if found == "" {
			return empty(cwd), nil // nothing found walking up
		}
		path = found
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
	return Parse(raw, path)
}

// Parse loads a config from the JSONC text of the file at path (which only
// names it: for error messages and BaseDir). The editing commands parse the
// text they are about to write, so an edit can never leave cappu.json
// unloadable.
func Parse(raw []byte, path string) (*Config, error) {
	cfg := &Config{}
	// jsonc.ToJSON strips comments and trailing commas in place, yielding plain
	// JSON for the standard decoder. DisallowUnknownFields is intentionally NOT
	// set: an unknown key (e.g. "$schema") is ignored, matching zod.
	text := jsonc.ToJSON(slices.Clone(raw))
	if err := json.Unmarshal(text, cfg); err != nil {
		return nil, fmt.Errorf("invalid %s:\n%w", path, err)
	}
	cfg.Dependencies.PlatformOrder = platformKeyOrder(string(text))
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid %s:\n%w", path, err)
	}
	cfg.BaseDir = filepath.Dir(path)
	cfg.FromFile = true
	cfg.ConfigPath = path
	return cfg, nil
}

// findConfigUpward returns the nearest cappu.json at or above dir, or "" if none
// exists up to the filesystem root. Mirrors how npm/cargo/uv/git locate the
// project root, so a command run from a subdirectory still sees the project
// config (its dir becomes BaseDir) instead of silently falling back to defaults.
func findConfigUpward(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	dir = abs
	for {
		candidate := filepath.Join(dir, DefaultConfigName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached the filesystem root
		}
		dir = parent
	}
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
	} else if !slices.Contains(co.ClassPath, DefaultClassPath) {
		// A user-supplied classPath replaces the defaults, but the cappu-managed
		// dependency dir must stay searchable or installed jars never resolve.
		co.ClassPath = append([]string{DefaultClassPath}, co.ClassPath...)
	}
	if co.SourcePaths == nil {
		co.SourcePaths = append([]string{}, DefaultSourcePaths...)
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
	if co.JavacOptions == nil {
		co.JavacOptions = []string{}
	}
	if co.ExperimentalCompiler.FailOnDegrade == nil {
		t := true
		co.ExperimentalCompiler.FailOnDegrade = &t
	}
	if n := co.Nullness; n != nil {
		if n.NullableAnnotations == nil {
			n.NullableAnnotations = []string{"org.jspecify.annotations.Nullable"}
		}
		if n.NonNullAnnotations == nil {
			n.NonNullAnnotations = []string{"org.jspecify.annotations.NonNull"}
		}
		if n.NullMarkedAnnotations == nil {
			n.NullMarkedAnnotations = []string{"org.jspecify.annotations.NullMarked"}
		}
		if n.NullUnmarkedAnnotations == nil {
			n.NullUnmarkedAnnotations = []string{"org.jspecify.annotations.NullUnmarked"}
		}
	}
	if h := c.LspOptions.InlayHints; h != nil {
		if h.ParameterNames == nil {
			t := true
			h.ParameterNames = &t
		}
		if h.VarTypes == nil {
			t := true
			h.VarTypes = &t
		}
	}
	if c.FormatterOptions.Style == "" {
		c.FormatterOptions.Style = "google"
	}
	if c.FormatterOptions.Ignore == nil {
		c.FormatterOptions.Ignore = []string{}
	}
	if c.TestOptions.OutputFormat == "" {
		c.TestOptions.OutputFormat = "text"
	}
	if c.TestOptions.ReportsDir == "" {
		c.TestOptions.ReportsDir = DefaultTestReportsDir
	}
	if c.AuditOptions.Ignore == nil {
		c.AuditOptions.Ignore = []AuditIgnore{}
	}
	if c.PackageSources == nil {
		c.PackageSources = DefaultSources()
	}
	d := &c.Dependencies
	if d.Platform == nil {
		d.Platform = map[string]string{}
	}
	if d.API == nil {
		d.API = map[string]Dependency{}
	}
	if d.Implementation == nil {
		d.Implementation = map[string]Dependency{}
	}
	if d.CompileOnly == nil {
		d.CompileOnly = map[string]Dependency{}
	}
	if d.RuntimeOnly == nil {
		d.RuntimeOnly = map[string]Dependency{}
	}
	if d.AnnotationProcessor == nil {
		d.AnnotationProcessor = map[string]Dependency{}
	}
	if d.TestImplementation == nil {
		d.TestImplementation = map[string]Dependency{}
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
	switch c.FormatterOptions.Style {
	case "google", "aosp":
	default:
		return fmt.Errorf(`formatterOptions.style: must be one of "google", "aosp"`)
	}
	// An importOrder entry is a package prefix ending in "*" (or ""), never a
	// glob: matching is a plain prefix test, so a `*` anywhere else is a mistake
	// worth reporting rather than a rule that silently matches nothing.
	for _, entry := range c.FormatterOptions.ImportOrder {
		if entry == "" {
			continue
		}
		if !strings.HasSuffix(entry, "*") || strings.Contains(strings.TrimSuffix(entry, "*"), "*") {
			return fmt.Errorf(
				`formatterOptions.importOrder: %q must be "" or a package prefix ending in "*", e.g. "java.*"`,
				entry)
		}
	}
	switch c.TestOptions.OutputFormat {
	case "text", "junit":
	default:
		return fmt.Errorf(`testOptions.outputFormat: must be one of "text", "junit"`)
	}
	firstIgnore := map[string]int{}
	for i, ignore := range c.AuditOptions.Ignore {
		if ignore.ID == "" || strings.TrimSpace(ignore.ID) != ignore.ID {
			return fmt.Errorf("auditOptions.ignore[%d].id: must be an advisory id or CVE without surrounding whitespace, e.g. \"CVE-2021-44228\"", i)
		}
		// ids match case-insensitively, so "cve-1" duplicates "CVE-1"
		key := strings.ToUpper(ignore.ID)
		if j, ok := firstIgnore[key]; ok {
			return fmt.Errorf("auditOptions.ignore[%d].id: %s duplicates auditOptions.ignore[%d]", i, ignore.ID, j)
		}
		firstIgnore[key] = i
		if strings.TrimSpace(ignore.Reason) == "" {
			return fmt.Errorf("auditOptions.ignore[%d].reason: must say why %s is accepted", i, ignore.ID)
		}
	}
	if err := c.Dependencies.validate(); err != nil {
		return err
	}
	for i, source := range c.PackageSources {
		if source.URL == "" {
			return fmt.Errorf("packageSources[%d]: url must not be empty", i)
		}
		if source.Auth != "" && !AuthName.MatchString(source.Auth) {
			return fmt.Errorf("packageSources[%d]: auth %q must be an env var name part, e.g. \"NEXUS\" (CAPPU_SOURCE_NEXUS_TOKEN)", i, source.Auth)
		}
		if source.Auth != "" && source.IsLocal() {
			return fmt.Errorf("packageSources[%d]: a local repository takes no auth", i)
		}
		if !source.IsLocal() {
			if u, err := url.Parse(source.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("packageSources[%d]: %q is neither an http(s) URL nor a path (write a path as /..., ./..., ../... or ~/...)", i, source.URL)
			}
		}
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

// validate checks the coordinate keys, that every platform is pinned, that a
// managed entry has a platform to be managed by, and the exclude patterns.
func (d Dependencies) validate() error {
	for key, version := range d.Platform {
		if !isCoordinateKey(key) {
			return fmt.Errorf("dependencies.platform: %q must be \"group:artifact\"", key)
		}
		if version == "" {
			return fmt.Errorf("dependencies.platform: %s needs a version", key)
		}
	}
	for _, name := range Configurations {
		for key, dep := range d.Configuration(name) {
			if !isCoordinateKey(key) {
				return fmt.Errorf("dependencies.%s: %q must be \"group:artifact\"", name, key)
			}
			if dep.Managed() && len(d.Platform) == 0 {
				return fmt.Errorf("dependencies.%s: %s has no version and no platform manages it", name, key)
			}
			for _, pattern := range dep.Exclude {
				if !IsExcludePattern(pattern) {
					return fmt.Errorf("dependencies.%s: %s: exclude %q must be \"group:artifact\" (either side may be *)", name, key, pattern)
				}
			}
		}
	}
	return nil
}

// isCoordinateKey reports whether key is "group:artifact".
func isCoordinateKey(key string) bool {
	group, artifact, ok := strings.Cut(key, ":")
	return ok && MavenID.MatchString(group) && MavenID.MatchString(artifact)
}

// IsExcludePattern reports whether pattern is "group:artifact" with "*" allowed
// on either side.
func IsExcludePattern(pattern string) bool {
	group, artifact, ok := strings.Cut(pattern, ":")
	side := func(s string) bool { return s == "*" || MavenID.MatchString(s) }
	return ok && side(group) && side(artifact)
}

// CompileClassPath are the classPath entries javac and the language server
// compile against: the configured ones plus the compileOnly jars.
func (c *Config) CompileClassPath() []string {
	return append(slices.Clone(c.CompilerOptions.ClassPath), DefaultCompileOnlyPath)
}

// RuntimeClassPath are the entries a run, a debuggee or a fat jar needs: the
// configured ones plus the runtimeOnly jars, never the compileOnly ones.
func (c *Config) RuntimeClassPath() []string {
	return append(slices.Clone(c.CompilerOptions.ClassPath), DefaultRuntimeOnlyPath)
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
