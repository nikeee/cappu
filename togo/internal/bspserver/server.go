// Package bspserver is cappu's Build Server Protocol server (nikeee/cappu#48):
// it lets a BSP client (IntelliJ, Metals, ...) import the project as two build
// targets, main and test, and drive cappu's compile and JUnit run. It reuses the
// LSP JSON-RPC connection (internal/lsp); BSP is the same framing and envelope.
// Spec: https://build-server-protocol.github.io/docs/specification
package bspserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nikeee/cappu/internal/build"
	"github.com/nikeee/cappu/internal/cli"
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
	statusOK        = 1
	statusError     = 2
	statusCancelled = 3
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
	// mainWritten is what the last main compile wrote, for main-class detection.
	mainWritten []string
	// buildMu serializes compile / test / run: they run off the read loop (so
	// `$/cancelRequest` and run/readStdin get through) but share the output
	// tree, reported and mainWritten.
	buildMu sync.Mutex
	// stdin holds the running programs' stdin by originId, for run/readStdin.
	stdinMu sync.Mutex
	stdin   map[string]io.Writer
}

// Serve runs a session on stdio.
func Serve(cfg *config.Config) error { return Run(cfg, os.Stdin, os.Stdout) }

// Run runs a session on the given stream until build/exit or EOF.
func Run(cfg *config.Config, r io.Reader, w io.Writer) error {
	return newServer(cfg).run(r, w)
}

func newServer(cfg *config.Config) *Server {
	return &Server{cfg: cfg, reported: map[string]map[string]bool{}, stdin: map[string]io.Writer{}}
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
				"runProvider":     map[string]any{"languageIds": []string{"java"}},
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
	c.OnRequestAsync("buildTarget/compile", s.serialized(s.onCompile))
	c.OnRequestAsync("buildTarget/test", s.serialized(s.onTest))
	c.OnRequestAsync("buildTarget/run", s.serialized(s.onRun))
	c.OnNotification("run/readStdin", s.onReadStdin)
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

// serialized runs a build request under buildMu.
func (s *Server) serialized(h lsp.AsyncRequestHandler) lsp.AsyncRequestHandler {
	return func(ctx context.Context, params json.RawMessage) (any, *lsp.ResponseError) {
		s.buildMu.Lock()
		defer s.buildMu.Unlock()
		return h(ctx, params)
	}
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
	target := func(name string, tags []string, deps []targetID, isTest bool) map[string]any {
		return map[string]any{
			"id":            s.id(name),
			"displayName":   name,
			"baseDirectory": pathToURI(s.cfg.BaseDir),
			"tags":          tags,
			"languageIds":   []string{"java"},
			"dependencies":  deps,
			"capabilities":  map[string]bool{"canCompile": true, "canTest": isTest, "canRun": !isTest, "canDebug": false},
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
		s.mainWritten = nil
		if sources := build.SourceJavaFiles(s.cfg); len(sources) > 0 {
			result := compile.RunCompile(sources, compile.Options{OutDir: testing.MainClassesDir(s.cfg), Output: "classes", Config: s.cfg})
			for _, w := range result.Warnings {
				s.log(p.OriginID, 2, w)
			}
			diags, ok, s.mainWritten = result.Diagnostics, result.Success, result.Written
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

// status is a build request's StatusCode; cancelled wins over the outcome.
func status(ctx context.Context, ok bool) int {
	switch {
	case ctx.Err() != nil:
		return statusCancelled
	case ok:
		return statusOK
	}
	return statusError
}

// ponytail: a cancelled compile still finishes its javac run (the compile
// pipeline takes no context); the cancellation shows in the status code, and
// test / run skip starting their child.
func (s *Server) onCompile(ctx context.Context, params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode(params)
	return map[string]any{"originId": p.OriginID, "statusCode": status(ctx, s.compileTargets(p))}, nil
}

// onTest compiles main + test and runs the JUnit launcher over the whole test
// tree (BSP's per-class TestParams data is ignored), its output streamed as
// build/logMessage. With testOptions.coverage on, the JaCoCo agent is attached
// like `cappu test` does.
func (s *Server) onTest(ctx context.Context, params json.RawMessage) (any, *lsp.ResponseError) {
	p := decode(params)
	result := func(ok bool) (any, *lsp.ResponseError) {
		return map[string]any{"originId": p.OriginID, "statusCode": status(ctx, ok)}, nil
	}
	isTest := false
	for _, id := range p.Targets {
		isTest = isTest || s.targetName(id) == "test"
	}
	if !isTest {
		return result(true) // main has no tests
	}
	if !s.compileTargets(targetsParams{Targets: []targetID{s.id("test")}, OriginID: p.OriginID}) || ctx.Err() != nil {
		return result(false)
	}
	launcher, err := testing.ConsoleLauncherJar(s.cfg)
	agent := ""
	if err == nil && s.cfg.TestOptions.Coverage {
		if agent, err = testing.JacocoAgentJar(s.cfg); err == nil {
			// the JaCoCo agent opens destfile but does not create parent dirs
			err = os.MkdirAll(s.cfg.ResolvePath(s.cfg.TestOptions.ReportsDir), 0o755)
		}
	}
	if err != nil {
		s.log(p.OriginID, 1, err.Error())
		return result(false)
	}
	out := newLineWriter(func(line string) { s.log(p.OriginID, 4, line) })
	cmd := exec.CommandContext(ctx, testing.ResolveJava(s.cfg), testing.TestRunArgs(s.cfg, launcher, agent)...)
	cmd.Stdout, cmd.Stderr = out, out
	err = cmd.Run()
	out.flush()
	return result(s.exited(p.OriginID, err))
}

type runParams struct {
	Target    targetID `json:"target"`
	OriginID  string   `json:"originId,omitempty"`
	Arguments []string `json:"arguments"`
}

// onRun compiles main and runs its main class (compilerOptions.mainClass, or
// the single class declaring main) with the request's arguments, streaming the
// program's output as run/printStdout / run/printStderr and feeding it
// run/readStdin. Cancelling the request kills the program.
func (s *Server) onRun(ctx context.Context, params json.RawMessage) (any, *lsp.ResponseError) {
	var p runParams
	_ = json.Unmarshal(params, &p)
	if s.targetName(p.Target) != "main" {
		return nil, &lsp.ResponseError{Code: lsp.ErrInvalidParams, Message: "only the main target can run"}
	}
	result := func(ok bool) (any, *lsp.ResponseError) {
		return map[string]any{"originId": p.OriginID, "statusCode": status(ctx, ok)}, nil
	}
	if !s.compileTargets(targetsParams{Targets: []targetID{p.Target}, OriginID: p.OriginID}) || ctx.Err() != nil {
		return result(false)
	}
	outDir := testing.MainClassesDir(s.cfg)
	mainClass, reason := cli.SelectMainClass(cli.DetectMainClasses(s.mainWritten, outDir), s.cfg.CompilerOptions.MainClass)
	if reason != "" {
		s.log(p.OriginID, 1, reason)
		return result(false)
	}
	classPath := append([]string{outDir}, build.ClassPath(s.cfg)...)
	args := append([]string{"-cp", strings.Join(classPath, string(os.PathListSeparator)), mainClass}, p.Arguments...)
	printer := func(method string) *lineWriter {
		return newLineWriter(func(line string) {
			_ = s.conn.Notify(method, map[string]any{"originId": p.OriginID, "message": line + "\n"})
		})
	}
	stdout, stderr := printer("run/printStdout"), printer("run/printStderr")
	cmd := exec.CommandContext(ctx, testing.ResolveJava(s.cfg), args...)
	cmd.Dir = s.cfg.BaseDir
	cmd.Stdout, cmd.Stderr = stdout, stderr
	stdin, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err == nil {
		s.stdinMu.Lock()
		s.stdin[p.OriginID] = stdin
		s.stdinMu.Unlock()
		err = cmd.Wait()
		s.stdinMu.Lock()
		delete(s.stdin, p.OriginID)
		s.stdinMu.Unlock()
	}
	stdout.flush()
	stderr.flush()
	return result(s.exited(p.OriginID, err))
}

// onReadStdin writes run/readStdin's value to the stdin of the program its
// originId started (dropped if that program is not running).
func (s *Server) onReadStdin(params json.RawMessage) {
	var p struct {
		OriginID string `json:"originId"`
		Value    string `json:"value"`
	}
	_ = json.Unmarshal(params, &p)
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	if w, ok := s.stdin[p.OriginID]; ok {
		_, _ = io.WriteString(w, p.Value)
	}
}

// exited reports a child's outcome: success, a non-zero exit, or a failure to
// start it at all (logged).
func (s *Server) exited(originID string, err error) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		s.log(originID, 1, "could not run java: "+err.Error())
	}
	return false
}

// lineWriter hands each complete line written to it to emit; flush sends a
// trailing partial line. exec calls Write from one goroutine per writer.
type lineWriter struct {
	emit func(string)
	buf  []byte
}

func newLineWriter(emit func(string)) *lineWriter { return &lineWriter{emit: emit} }

func (w *lineWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		w.emit(strings.TrimSuffix(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
	}
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

// WriteConnectionFile writes .bsp/cappu.json beside cappu.json, pointing at
// this binary, and returns its path (`cappu bsp --install`).
func WriteConnectionFile(cfg *config.Config) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	data, err := ConnectionFile(exe)
	if err != nil {
		return "", err
	}
	path := cfg.ResolvePath(filepath.Join(".bsp", "cappu.json"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(data, '\n'), 0o644)
}

// pathToURI converts an absolute filesystem path to a file:// URI (same
// encoding as the language server's).
func pathToURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return strings.TrimSuffix(u.String(), "/")
}
