// Package bspserver is cappu's Build Server Protocol server (nikeee/cappu#48):
// it lets a BSP client (IntelliJ, Metals, ...) import the project as two build
// targets, main and test, and drive cappu's compile and JUnit run. It reuses the
// LSP JSON-RPC connection (internal/lsp); BSP is the same framing and envelope.
// Spec: https://build-server-protocol.github.io/docs/specification
package bspserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/compile"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/lsp"
	"github.com/nikeee/cappu/internal/meta"
	"github.com/nikeee/cappu/internal/testing"
)

// BSPVersion is the protocol version the server (and connection file) speaks.
const BSPVersion = "2.2.0"

// ErrExitWithoutShutdown is returned by Run when the client sends build/exit
// before build/shutdown (BSP mirrors LSP: the process then exits 1).
var ErrExitWithoutShutdown = errors.New("exit without shutdown")

// BSP StatusCode values.
const (
	statusOK    = 1
	statusError = 2
)

type targetID struct {
	URI string `json:"uri"`
}

// Server is one BSP session over a project config.
type Server struct {
	cfg      *config.Config
	conn     *lsp.Conn
	shutdown bool
	// reported holds the files each target last published diagnostics for, so
	// a fixed file gets an empty reset instead of keeping stale errors.
	reported map[string]map[string]bool
}

// Serve runs a session on stdio.
func Serve(cfg *config.Config) error { return Run(cfg, os.Stdin, os.Stdout) }

// Run runs a session on the given stream until build/exit or EOF.
func Run(cfg *config.Config, r io.Reader, w io.Writer) error {
	return newServer(cfg).run(r, w)
}

func newServer(cfg *config.Config) *Server {
	return &Server{cfg: cfg, reported: map[string]map[string]bool{}}
}

func (s *Server) run(r io.Reader, w io.Writer) error {
	s.conn = lsp.NewConn(r, w)
	c := s.conn
	c.OnRequest("build/initialize", func(json.RawMessage) (any, *lsp.ResponseError) {
		return map[string]any{
			"displayName": "cappu",
			"version":     meta.Version,
			"bspVersion":  BSPVersion,
			"capabilities": map[string]any{
				"compileProvider": map[string]any{"languageIds": []string{"java"}},
				"testProvider":    map[string]any{"languageIds": []string{"java"}},
			},
		}, nil
	})
	c.OnNotification("build/initialized", func(json.RawMessage) {})
	c.OnRequest("workspace/buildTargets", func(json.RawMessage) (any, *lsp.ResponseError) {
		return map[string]any{"targets": s.buildTargets()}, nil
	})
	c.OnRequest("workspace/reload", func(json.RawMessage) (any, *lsp.ResponseError) { return nil, nil })
	c.OnRequest("buildTarget/sources", s.onSources)
	c.OnRequest("buildTarget/javacOptions", s.onJavacOptions)
	c.OnRequest("buildTarget/compile", s.onCompile)
	c.OnRequest("buildTarget/test", s.onTest)
	c.OnRequest("build/shutdown", func(json.RawMessage) (any, *lsp.ResponseError) {
		s.shutdown = true
		return nil, nil
	})
	c.OnNotification("build/exit", func(json.RawMessage) {
		if s.shutdown {
			c.Stop(nil)
		} else {
			c.Stop(ErrExitWithoutShutdown)
		}
	})
	return c.Run()
}

// The two targets are addressed by the project directory's URI plus ?id=.
func (s *Server) id(name string) targetID {
	return targetID{URI: pathToURI(s.cfg.BaseDir) + "?id=" + name}
}

func (s *Server) hasTests() bool {
	_, err := os.Stat(s.cfg.ResolvePath(config.DefaultTestSourcePath))
	return err == nil
}

func (s *Server) buildTargets() []map[string]any {
	target := func(name string, tags []string, deps []targetID, canTest bool) map[string]any {
		return map[string]any{
			"id":            s.id(name),
			"displayName":   name,
			"baseDirectory": pathToURI(s.cfg.BaseDir),
			"tags":          tags,
			"languageIds":   []string{"java"},
			"dependencies":  deps,
			"capabilities":  map[string]bool{"canCompile": true, "canTest": canTest, "canRun": false, "canDebug": false},
		}
	}
	targets := []map[string]any{target("main", []string{}, []targetID{}, false)}
	if s.hasTests() {
		targets = append(targets, target("test", []string{"test"}, []targetID{s.id("main")}, true))
	}
	return targets
}

type targetsParams struct {
	Targets  []targetID `json:"targets"`
	OriginID string     `json:"originId,omitempty"`
}

func decode(params json.RawMessage) targetsParams {
	var p targetsParams
	_ = json.Unmarshal(params, &p)
	return p
}

// targetName maps a target id back to "main" / "test" ("" if not ours).
func (s *Server) targetName(id targetID) string {
	for _, name := range []string{"main", "test"} {
		if id == s.id(name) {
			return name
		}
	}
	return ""
}

func (s *Server) onSources(params json.RawMessage) (any, *lsp.ResponseError) {
	items := []map[string]any{}
	for _, id := range decode(params).Targets {
		var dirs []string
		switch s.targetName(id) {
		case "main":
			dirs = s.cfg.CompilerOptions.SourcePaths
		case "test":
			dirs = []string{config.DefaultTestSourcePath}
		default:
			continue
		}
		sources := []map[string]any{}
		for _, d := range dirs {
			// SourceItemKind 2 = directory
			sources = append(sources, map[string]any{"uri": pathToURI(s.cfg.ResolvePath(d)) + "/", "kind": 2, "generated": false})
		}
		items = append(items, map[string]any{"target": id, "sources": sources})
	}
	return map[string]any{"items": items}, nil
}

func (s *Server) onJavacOptions(params json.RawMessage) (any, *lsp.ResponseError) {
	items := []map[string]any{}
	for _, id := range decode(params).Targets {
		var classpath []string
		var classDir string
		switch s.targetName(id) {
		case "main":
			classpath, classDir = build.ClassPath(s.cfg), testing.MainClassesDir(s.cfg)
		case "test":
			classpath, classDir = testing.TestRuntimeClassPath(s.cfg), testing.TestClassesDir(s.cfg)
		default:
			continue
		}
		uris := make([]string, len(classpath))
		for i, p := range classpath {
			uris[i] = pathToURI(p)
		}
		items = append(items, map[string]any{"target": id, "options": []string{}, "classpath": uris, "classDirectory": pathToURI(classDir)})
	}
	return map[string]any{"items": items}, nil
}

// compileTargets builds the requested targets in dependency order (main before
// test, main at most once) and publishes their diagnostics. Main compiles into
// the tree `cappu test` compiles tests against, so a test build reuses it.
func (s *Server) compileTargets(p targetsParams) bool {
	want := map[string]bool{}
	for _, id := range p.Targets {
		want[s.targetName(id)] = true
	}
	if want["main"] || want["test"] {
		var diags []compile.CompileDiagnostic
		ok := true
		if sources := build.SourceJavaFiles(s.cfg); len(sources) > 0 {
			result := compile.RunCompile(sources, compile.Options{OutDir: testing.MainClassesDir(s.cfg), Output: "classes", Config: s.cfg})
			for _, w := range result.Warnings {
				s.log(p.OriginID, 2, w)
			}
			diags, ok = result.Diagnostics, result.Success
		}
		s.publish("main", p.OriginID, diags)
		if !ok {
			return false
		}
	}
	if want["test"] {
		diags := testing.CompileTests(s.cfg, testing.FindTestSources(s.cfg))
		s.publish("test", p.OriginID, diags)
		for _, d := range diags {
			if d.Severity == "error" {
				return false
			}
		}
	}
	return true
}

func status(ok bool) int {
	if ok {
		return statusOK
	}
	return statusError
}

func (s *Server) onCompile(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode(params)
	return map[string]any{"originId": p.OriginID, "statusCode": status(s.compileTargets(p))}, nil
}

// onTest compiles main + test and runs the JUnit launcher over the whole test
// tree (BSP's per-class TestParams data is ignored).
func (s *Server) onTest(params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode(params)
	result := func(ok bool) (any, *lsp.ResponseError) {
		return map[string]any{"originId": p.OriginID, "statusCode": status(ok)}, nil
	}
	isTest := false
	for _, id := range p.Targets {
		isTest = isTest || s.targetName(id) == "test"
	}
	if !isTest {
		return result(true) // main has no tests
	}
	if !s.compileTargets(targetsParams{Targets: []targetID{s.id("test")}, OriginID: p.OriginID}) {
		return result(false)
	}
	launcher, err := testing.ConsoleLauncherJar(s.cfg)
	if err != nil {
		s.log(p.OriginID, 1, err.Error())
		return result(false)
	}
	// ponytail: output is sent once the run ends, stream it line by line if
	// long suites need live progress. Coverage (testOptions.coverage) is not
	// attached here; `cappu test` does that.
	out, err := exec.Command(testing.ResolveJava(s.cfg), testing.TestRunArgs(s.cfg, launcher, "")...).CombinedOutput()
	if len(out) > 0 {
		s.log(p.OriginID, 4, string(out))
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			s.log(p.OriginID, 1, "could not run java: "+err.Error())
		}
		return result(false)
	}
	return result(true)
}

// log sends build/logMessage (type 1 error, 2 warning, 3 info, 4 log).
func (s *Server) log(originID string, typ int, message string) {
	_ = s.conn.Notify("build/logMessage", map[string]any{"type": typ, "message": message, "originId": originID})
}

// publish sends one build/publishDiagnostics per file with diagnostics (reset:
// true replaces the previous set), plus an empty reset for every file the
// target reported last time but not now. Located-less diagnostics go to the log.
func (s *Server) publish(target, originID string, diags []compile.CompileDiagnostic) {
	byFile := map[string][]lsp.Diagnostic{}
	for _, d := range diags {
		severity := 1
		if d.Severity == "warning" {
			severity = 2
		}
		if d.File == "" {
			s.log(originID, severity, d.Message)
			continue
		}
		file, err := filepath.Abs(d.File)
		if err != nil {
			file = d.File
		}
		line, col := max(d.Line-1, 0), max(d.Column-1, 0)
		pos := lsp.Position{Line: line, Character: col}
		byFile[file] = append(byFile[file], lsp.Diagnostic{
			Range: lsp.Range{Start: pos, End: pos}, Severity: severity, Code: d.Code, Source: "cappu", Message: d.Message,
		})
	}
	previous := s.reported[target]
	s.reported[target] = map[string]bool{}
	for file := range previous {
		if _, ok := byFile[file]; !ok {
			byFile[file] = []lsp.Diagnostic{}
		}
	}
	for file, ds := range byFile {
		if len(ds) > 0 {
			s.reported[target][file] = true
		}
		_ = s.conn.Notify("build/publishDiagnostics", map[string]any{
			"textDocument": map[string]string{"uri": pathToURI(file)},
			"buildTarget":  s.id(target),
			"originId":     originID,
			"diagnostics":  ds,
			"reset":        true,
		})
	}
}

// ConnectionFile is the BSP discovery file (.bsp/cappu.json) clients scan the
// workspace for; argv starts this binary's `bsp` subcommand.
func ConnectionFile(executable string) ([]byte, error) {
	return json.MarshalIndent(map[string]any{
		"name":       "cappu",
		"version":    meta.Version,
		"bspVersion": BSPVersion,
		"languages":  []string{"java"},
		"argv":       []string{executable, "bsp"},
	}, "", "  ")
}

// pathToURI converts an absolute filesystem path to a file:// URI (same
// encoding as the language server's).
func pathToURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return strings.TrimSuffix(u.String(), "/")
}
