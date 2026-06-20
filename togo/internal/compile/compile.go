// Package compile is the javac-lite compile pipeline: it reads .java files,
// compiles them (with javac by default, or cappu's own emitter under the
// experimental compiler) and writes a class tree or a jar under the output
// root. Port of src/compiler/compiler.ts. RunCompile never prints; it returns
// what was written, what degraded and the diagnostics, and the caller renders.
//
// Not yet ported (treated as absent): annotation processors (#7) and the
// experimental-compiler --validate-against-javac check.
package compile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
)

// CompileDiagnostic is a source diagnostic located for display (1-based line/column).
type CompileDiagnostic struct {
	Severity string // "error" | "warning"
	File     string
	Line     int
	Column   int
	Code     int
	Message  string
}

// OutputKind is what to produce: "classes", "jar" or "fat-jar".
type OutputKind = string

// Options configures a compile run; explicit options take precedence over config.
type Options struct {
	OutDir        string // default "dist"
	Output        string // "" = use config
	ArtifactName  string // "" = <artifactId>-<version> / dir name
	Experimental  *bool  // nil = use config
	FailOnDegrade *bool  // nil = use config
	TypeCheck     *bool  // nil = true
	Config        *config.Config
}

// Result is the outcome of a compile run.
type Result struct {
	Success     bool
	Written     []string
	Degraded    []string
	Warnings    []string
	Diagnostics []CompileDiagnostic
}

var javacDiagRe = regexp.MustCompile(`^(.+?):(\d+): (error|warning): (.*)$`)
var leadingSpaceRe = regexp.MustCompile(`^\s`)
var summaryRe = regexp.MustCompile(`^\d+ (error|warning)s?$`)

// ParseJavacDiagnostics parses javac's stderr into located diagnostics.
func ParseJavacDiagnostics(stderr string) []CompileDiagnostic {
	var diagnostics []CompileDiagnostic
	var leftovers []string
	for _, line := range strings.Split(stderr, "\n") {
		if m := javacDiagRe.FindStringSubmatch(line); m != nil {
			sev := "error"
			if m[3] == "warning" {
				sev = "warning"
			}
			ln, _ := strconv.Atoi(m[2])
			diagnostics = append(diagnostics, CompileDiagnostic{Severity: sev, File: m[1], Line: ln, Message: m[4]})
		} else if strings.TrimSpace(line) != "" && !leadingSpaceRe.MatchString(line) && !summaryRe.MatchString(line) {
			leftovers = append(leftovers, line)
		}
	}
	if len(diagnostics) == 0 && len(leftovers) > 0 {
		diagnostics = append(diagnostics, CompileDiagnostic{Severity: "error", Message: strings.Join(leftovers, " ")})
	}
	return diagnostics
}

// MissingConfiguredPaths returns configured classPath/sourcePaths entries that do
// not exist on disk, for warning (only when they come from an actual cappu.json).
func MissingConfiguredPaths(cfg *config.Config) []string {
	if !cfg.FromFile {
		return nil
	}
	external := map[string]bool{}
	for _, e := range config.ExternalClassPaths {
		external[e] = true
	}
	var missing []string
	for _, p := range append(append([]string{}, cfg.CompilerOptions.ClassPath...), cfg.CompilerOptions.SourcePaths...) {
		if external[p] {
			continue
		}
		resolved := cfg.ResolvePath(p)
		if _, err := os.Stat(resolved); err != nil {
			missing = append(missing, resolved)
		}
	}
	return missing
}

// findFilesRelative returns every file under root, as forward-slash paths
// relative to root. A missing root yields nothing.
func findFilesRelative(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if rel, e := filepath.Rel(root, path); e == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

// resourceEntries collects every file under the configured resourcePaths.
func resourceEntries(cfg *config.Config) []compiler.ZipEntryInput {
	var entries []compiler.ZipEntryInput
	for _, configured := range cfg.CompilerOptions.ResourcePaths {
		root := cfg.ResolvePath(configured)
		for _, rel := range findFilesRelative(root) {
			if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				entries = append(entries, compiler.ZipEntryInput{Name: rel, Bytes: b})
			}
		}
	}
	return entries
}

// classPathEntries collects the dependency class/jar contents for a fat jar.
func classPathEntries(cfg *config.Config) []compiler.ZipEntryInput {
	var entries []compiler.ZipEntryInput
	addJar := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		for _, entry := range compiler.ReadZipEntries(data) {
			if strings.HasPrefix(entry.Name, "META-INF/") || strings.HasSuffix(entry.Name, "/") {
				continue
			}
			entries = append(entries, compiler.ZipEntryInput{Name: entry.Name, Bytes: entry.Read()})
		}
	}
	for _, configured := range cfg.CompilerOptions.ClassPath {
		root := cfg.ResolvePath(configured)
		if strings.HasSuffix(root, ".jar") {
			addJar(root)
			continue
		}
		for _, rel := range findFilesRelative(root) {
			full := filepath.Join(root, filepath.FromSlash(rel))
			if strings.HasSuffix(rel, ".jar") {
				addJar(full)
			} else if strings.HasSuffix(rel, ".class") {
				if b, err := os.ReadFile(full); err == nil {
					entries = append(entries, compiler.ZipEntryInput{Name: rel, Bytes: b})
				}
			}
		}
	}
	return entries
}

// mainClassWarning advises when a jar has several main methods and no configured mainClass.
func mainClassWarning(mainClasses []string, configured string) []string {
	if configured == "" && len(mainClasses) > 1 {
		return []string{"several classes declare main(String[]) (" + strings.Join(mainClasses, ", ") +
			"); the jar has no Main-Class - set compilerOptions.mainClass to pick one"}
	}
	return nil
}

func pathToURI(file string) compiler.URI {
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	return compiler.URI("file://" + filepath.ToSlash(abs))
}

func manifestBytes(mainClass string) []byte {
	m := "Manifest-Version: 1.0\r\n"
	if mainClass != "" {
		m += "Main-Class: " + mainClass + "\r\n"
	}
	return []byte(m + "\r\n")
}

// RunCompile compiles files (the caller has ensured the list is non-empty).
func RunCompile(files []string, options Options) Result {
	cfg := options.Config
	outDir := options.OutDir
	if outDir == "" {
		outDir = "dist"
	}
	output := options.Output
	if output == "" {
		output = cfg.CompilerOptions.Output
	}
	jarName := options.ArtifactName
	if jarName == "" {
		jarName = cfg.ArtifactBaseName()
	}
	experimental := cfg.CompilerOptions.ExperimentalCompiler.Enabled
	if options.Experimental != nil {
		experimental = *options.Experimental
	}
	if !experimental {
		return runJavacCompile(files, outDir, output, cfg, jarName)
	}
	failOnDegrade := cfg.CompilerOptions.ExperimentalCompiler.FailOnDegrade
	if options.FailOnDegrade != nil {
		failOnDegrade = *options.FailOnDegrade
	}
	typeCheck := options.TypeCheck == nil || *options.TypeCheck

	program := compiler.NewProgram()
	compiler.LoadJdkStub(program)
	loadConfiguredPaths(program, cfg)
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return Result{Diagnostics: []CompileDiagnostic{{Severity: "error", File: file, Message: err.Error()}}}
		}
		program.AddProjectFile(pathToURI(file), string(b))
	}
	checker := compiler.NewChecker(program)

	var diagnostics []CompileDiagnostic
	for _, file := range files {
		sf := program.GetSourceFile(pathToURI(file))
		sfd := sf.AsSourceFile()
		lineStarts := compiler.ComputeLineStarts(sfd.Text)
		all := append(append([]compiler.Diagnostic{}, sfd.ParseDiagnostics...), sfd.BindDiagnostics...)
		if typeCheck {
			all = append(all, checker.GetSemanticDiagnostics(sf)...)
		}
		for _, d := range all {
			diagnostics = append(diagnostics, toCompileDiagnostic(d, file, lineStarts))
		}
	}
	if hasError(diagnostics) {
		return Result{Success: false, Diagnostics: diagnostics}
	}

	var degraded []string
	compiler.SetDegradeListener(func(className, member string) {
		degraded = append(degraded, strings.ReplaceAll(className, "/", ".")+"."+member)
	})
	defer compiler.SetDegradeListener(nil)

	var written, warnings []string
	var classes []compiler.ZipEntryInput
	var mainClasses []string
	for _, file := range files {
		sf := program.GetSourceFile(pathToURI(file))
		for _, cls := range compiler.EmitSourceFile(sf, program, checker, false) {
			classes = append(classes, compiler.ZipEntryInput{Name: cls.Name + ".class", Bytes: cls.Bytes})
			if cls.HasMainMethod {
				mainClasses = append(mainClasses, strings.ReplaceAll(cls.Name, "/", "."))
			}
		}
	}
	have := map[string]bool{}
	for _, c := range classes {
		have[c.Name] = true
	}
	var resources []compiler.ZipEntryInput
	for _, r := range resourceEntries(cfg) {
		if !have[r.Name] {
			have[r.Name] = true
			resources = append(resources, r)
		}
	}

	if output == "classes" {
		for _, entry := range append(append([]compiler.ZipEntryInput{}, classes...), resources...) {
			out := filepath.Join(outDir, filepath.FromSlash(entry.Name))
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err == nil {
				if os.WriteFile(out, entry.Bytes, 0o644) == nil {
					written = append(written, out)
				}
			}
		}
	} else {
		mainClass := cfg.CompilerOptions.MainClass
		if mainClass == "" && len(mainClasses) == 1 {
			mainClass = mainClasses[0]
		}
		warnings = append(warnings, mainClassWarning(mainClasses, cfg.CompilerOptions.MainClass)...)
		entries := []compiler.ZipEntryInput{{Name: "META-INF/MANIFEST.MF", Bytes: manifestBytes(mainClass)}}
		entries = append(entries, classes...)
		entries = append(entries, resources...)
		if output == "fat-jar" {
			seen := map[string]bool{}
			for _, e := range entries {
				seen[e.Name] = true
			}
			for _, e := range classPathEntries(cfg) {
				if !seen[e.Name] {
					seen[e.Name] = true
					entries = append(entries, e)
				}
			}
		}
		jar := filepath.Join(outDir, jarName+".jar")
		_ = os.MkdirAll(outDir, 0o755)
		if err := os.WriteFile(jar, compiler.WriteZip(entries), 0o644); err == nil {
			written = append(written, jar)
		}
	}

	if len(degraded) > 0 && failOnDegrade {
		diagnostics = append(diagnostics, CompileDiagnostic{Severity: "error",
			Message: strconv.Itoa(len(degraded)) + " method(s) degraded to a placeholder body (--fail-on-degrade)"})
		return Result{Success: false, Diagnostics: diagnostics, Written: written, Degraded: degraded}
	}
	return Result{Success: true, Written: written, Degraded: degraded, Warnings: warnings}
}

func hasError(diags []CompileDiagnostic) bool {
	for _, d := range diags {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

func toCompileDiagnostic(d compiler.Diagnostic, file string, lineStarts []int) CompileDiagnostic {
	lc := compiler.GetLineAndCharacterOfPosition(lineStarts, d.Pos)
	sev := "warning"
	if d.Category == compiler.CategoryError {
		sev = "error"
	}
	return CompileDiagnostic{Severity: sev, File: file, Line: lc.Line + 1, Column: lc.Character + 1, Code: d.Code, Message: d.MessageText}
}

// loadConfiguredPaths registers the config's classPath (.class stubs) and
// sourcePaths (.java sources, for resolution only) into a program.
func loadConfiguredPaths(program *compiler.Program, cfg *config.Config) {
	var classPaths []string
	for _, p := range cfg.CompilerOptions.ClassPath {
		classPaths = append(classPaths, cfg.ResolvePath(p))
	}
	compiler.LoadClassPath(program, classPaths)
	for _, sp := range cfg.CompilerOptions.SourcePaths {
		dir := cfg.ResolvePath(sp)
		for _, file := range build.JavaFilesIn(dir) {
			if b, err := os.ReadFile(file); err == nil {
				program.AddProjectFile(pathToURI(file), string(b))
			}
		}
	}
}

// runJavacCompile is the default path: javac compiles into a temp dir, then the
// outputs are packaged (Main-Class is read from javac's class bytes).
func runJavacCompile(files []string, outDir, output string, cfg *config.Config, jarName string) Result {
	tmp, err := os.MkdirTemp("", "cappu-javac-")
	if err != nil {
		return Result{Diagnostics: []CompileDiagnostic{{Severity: "error", Message: err.Error()}}}
	}
	defer os.RemoveAll(tmp)

	args := []string{"-d", tmp, "-encoding", "UTF-8"}
	if cfg.CompilerOptions.Release != nil {
		args = append(args, "--release", strconv.Itoa(*cfg.CompilerOptions.Release))
	}
	classPath := build.ClassPath(cfg)
	if len(classPath) > 0 {
		args = append(args, "-cp", strings.Join(classPath, string(os.PathListSeparator)))
	}
	var sourcePaths []string
	for _, p := range cfg.CompilerOptions.SourcePaths {
		resolved := cfg.ResolvePath(p)
		if _, err := os.Stat(resolved); err == nil {
			sourcePaths = append(sourcePaths, resolved)
		}
	}
	if len(sourcePaths) > 0 {
		args = append(args, "-sourcepath", strings.Join(sourcePaths, string(os.PathListSeparator)))
	}
	args = append(args, files...)

	cmd := exec.Command(build.Javac(cfg), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		diags := ParseJavacDiagnostics(stderr.String())
		if len(diags) == 0 {
			diags = []CompileDiagnostic{{Severity: "error", Message: build.Javac(cfg) + " failed: " + err.Error()}}
		}
		return Result{Success: false, Diagnostics: diags}
	}

	outputFiles := findFilesRelative(tmp)
	have := map[string]bool{}
	for _, f := range outputFiles {
		have[f] = true
	}
	var resources []compiler.ZipEntryInput
	for _, r := range resourceEntries(cfg) {
		if !have[r.Name] {
			resources = append(resources, r)
		}
	}
	var written, warnings []string
	if output == "classes" {
		for _, rel := range outputFiles {
			b, err := os.ReadFile(filepath.Join(tmp, filepath.FromSlash(rel)))
			if err != nil {
				continue
			}
			target := filepath.Join(outDir, filepath.FromSlash(rel))
			if os.MkdirAll(filepath.Dir(target), 0o755) == nil && os.WriteFile(target, b, 0o644) == nil {
				written = append(written, target)
			}
		}
		for _, entry := range resources {
			target := filepath.Join(outDir, filepath.FromSlash(entry.Name))
			if os.MkdirAll(filepath.Dir(target), 0o755) == nil && os.WriteFile(target, entry.Bytes, 0o644) == nil {
				written = append(written, target)
			}
		}
	} else {
		var classes []compiler.ZipEntryInput
		var mainClasses []string
		for _, rel := range outputFiles {
			b, err := os.ReadFile(filepath.Join(tmp, filepath.FromSlash(rel)))
			if err != nil {
				continue
			}
			classes = append(classes, compiler.ZipEntryInput{Name: rel, Bytes: b})
			if strings.HasSuffix(rel, ".class") && compiler.ClassDeclaresMain(b) {
				mainClasses = append(mainClasses, strings.ReplaceAll(strings.TrimSuffix(rel, ".class"), "/", "."))
			}
		}
		mainClass := cfg.CompilerOptions.MainClass
		if mainClass == "" && len(mainClasses) == 1 {
			mainClass = mainClasses[0]
		}
		warnings = append(warnings, mainClassWarning(mainClasses, cfg.CompilerOptions.MainClass)...)
		entries := []compiler.ZipEntryInput{{Name: "META-INF/MANIFEST.MF", Bytes: manifestBytes(mainClass)}}
		entries = append(entries, classes...)
		entries = append(entries, resources...)
		if output == "fat-jar" {
			seen := map[string]bool{}
			for _, e := range entries {
				seen[e.Name] = true
			}
			for _, e := range classPathEntries(cfg) {
				if !seen[e.Name] {
					seen[e.Name] = true
					entries = append(entries, e)
				}
			}
		}
		jar := filepath.Join(outDir, jarName+".jar")
		_ = os.MkdirAll(outDir, 0o755)
		if os.WriteFile(jar, compiler.WriteZip(entries), 0o644) == nil {
			written = append(written, jar)
		}
	}
	return Result{Success: true, Written: written, Warnings: warnings}
}
