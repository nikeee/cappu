package mcp

// MCP server over stdio. Exposes the Java semantic engine to agents as tools.
// Mirrors the LSP server (internal/lspserver) but speaks the Model Context
// Protocol: newline-delimited JSON-RPC 2.0. Tool logic lives in tools.go /
// project.go (pure, tested); this module owns config-aware workspace loading,
// disk freshness and transport. Port of src/services/mcpServer.ts.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/nikeee/cappu/internal/compiler"
	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/processors"
)

// instructions are surfaced to the host in the initialize response.
const instructions = `cappu server read-only. Look at Java code and dependency tree. Never write file,
never compile, never run code.

Need write disk or run JVM? Use cappu CLI in shell:
  - Build (.class / jar / fat-jar in ./dist):  cappu compile
  - Run JUnit test:                            cappu test
rename_symbol give you edits. You apply edits. Server not write them.

Config file = cappu.json. Want schema? Run: cappu config-schema
All commands: cappu help`

const protocolVersion = "2024-11-05"

// supportedProtocolVersions are echoed back when the client asks for one of
// them (the TS SDK negotiates the same way).
var supportedProtocolVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18"}

// Server is the MCP stdio server.
type Server struct {
	program *compiler.Program
	checker *compiler.Checker
	tools   *Tools
	project *ProjectTools
	config  *config.Config

	w      io.Writer
	wmu    sync.Mutex
	mtimes map[string]int64
	// cpFingerprint/configMtime detect classpath and cappu.json changes
	// between tool calls (see refresh).
	cpFingerprint map[string]int64
	configMtime   int64

	registry []toolDef
}

type toolDef struct {
	name        string
	description string
	inputSchema map[string]any
	// usesProgram requires a workspace refresh before the call.
	usesProgram bool
	handler     func(args json.RawMessage) (any, error)
}

// NewServer builds the MCP server from the project config (may be nil).
func NewServer(cfg *config.Config) *Server {
	s := &Server{}
	s.rebuild(cfg)
	s.registerTools()
	return s
}

// rebuild replaces the whole semantic state from cfg. A classpath or config
// change cannot be patched incrementally (LoadClassPath never removes stubs
// for classes that disappeared), so program, checker and tools are rebuilt
// from scratch exactly like at startup. Tool handlers read s.tools/s.project
// at call time, so registerTools need not re-run.
func (s *Server) rebuild(cfg *config.Config) {
	s.config = cfg
	s.program = compiler.NewProgram()
	compiler.InstallJdkTypes(s.program, cfg)
	if cfg != nil {
		loadConfiguredSources(s.program, cfg)
	}
	s.checker = compiler.NewChecker(s.program)
	s.tools = NewTools(s.program, s.checker)
	if cfg != nil {
		s.project = NewProjectTools(cfg, ProjectToolDeps{})
	}
	s.mtimes = map[string]int64{}
	s.cpFingerprint = nil
	s.configMtime = 0
	if cfg != nil {
		s.cpFingerprint = classpathFingerprint(cfg)
		if cfg.ConfigPath != "" {
			if info, err := os.Stat(cfg.ConfigPath); err == nil {
				s.configMtime = info.ModTime().UnixNano()
			}
		}
	}
}

// Serve runs the MCP server over stdio.
func Serve(cfg *config.Config) error {
	return NewServer(cfg).Run(os.Stdin, os.Stdout)
}

func sj(args json.RawMessage, v any) {
	if len(args) > 0 {
		_ = json.Unmarshal(args, v)
	}
}

func (s *Server) registerTools() {
	str := map[string]any{"type": "string"}
	strArray := map[string]any{"type": "array", "items": str}
	objSchema := func(props map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	refTool := func(name, desc string, h func(args json.RawMessage) (any, error)) {
		s.registry = append(s.registry, toolDef{name: name, description: desc, inputSchema: objSchema(map[string]any{"ref": str}, "ref"), usesProgram: true, handler: h})
	}

	s.registry = append(s.registry, toolDef{
		name:        "diagnostics",
		description: "Java syntax, binding and type diagnostics. Omit `files` to check the whole workspace.",
		inputSchema: objSchema(map[string]any{"files": strArray}),
		usesProgram: true,
		handler: func(args json.RawMessage) (any, error) {
			var a struct {
				Files []string `json:"files"`
			}
			sj(args, &a)
			return map[string]any{"diagnostics": s.tools.Diagnostics(a.Files)}, nil
		},
	})
	s.registry = append(s.registry, toolDef{
		name:        "deprecated_uses",
		description: "Find uses of @Deprecated methods and types, with each declaration's since/forRemoval. Omit `files` to scan the whole workspace.",
		inputSchema: objSchema(map[string]any{"files": strArray}),
		usesProgram: true,
		handler: func(args json.RawMessage) (any, error) {
			var a struct {
				Files []string `json:"files"`
			}
			sj(args, &a)
			return map[string]any{"deprecatedUses": s.tools.DeprecatedUses(a.Files)}, nil
		},
	})
	s.registry = append(s.registry, toolDef{
		name:        "outline",
		description: "Top-level type/member outline of one Java file.",
		inputSchema: objSchema(map[string]any{"file": str}, "file"),
		usesProgram: true,
		handler: func(args json.RawMessage) (any, error) {
			var a struct {
				File string `json:"file"`
			}
			sj(args, &a)
			return map[string]any{"symbols": s.tools.Outline(a.File)}, nil
		},
	})
	s.registry = append(s.registry, toolDef{
		name:        "search_symbols",
		description: "Find indexed Java types whose fully-qualified name contains `query`.",
		inputSchema: objSchema(map[string]any{"query": str}, "query"),
		usesProgram: true,
		handler: func(args json.RawMessage) (any, error) {
			var a struct {
				Query string `json:"query"`
			}
			sj(args, &a)
			return map[string]any{"matches": s.tools.SearchSymbols(a.Query)}, nil
		},
	})
	refTool("describe_symbol", "Describe a symbol (kind, signature, Javadoc, definition). `ref` is a type FQN or simple name, or `Type#member` (e.g. `java.util.List#add`).", func(args json.RawMessage) (any, error) {
		return map[string]any{"matches": s.tools.DescribeSymbol(refArg(args))}, nil
	})
	refTool("find_definition", "Locate where a symbol is declared. `ref` as in describe_symbol.", func(args json.RawMessage) (any, error) {
		return map[string]any{"definitions": s.tools.FindDefinition(refArg(args))}, nil
	})
	refTool("find_references", "Find every use of a symbol across the workspace. `ref` as in describe_symbol.", func(args json.RawMessage) (any, error) {
		return s.tools.FindReferences(refArg(args)), nil
	})
	refTool("find_implementations", "For an interface/class: its subtypes. For a method: the overrides in those subtypes. `ref` as in describe_symbol.", func(args json.RawMessage) (any, error) {
		return s.tools.FindImplementations(refArg(args)), nil
	})
	refTool("list_members", "List a type's members (fields/methods/...), declared and inherited, each flagged. `ref` is a type FQN or simple name.", func(args json.RawMessage) (any, error) {
		return s.tools.ListMembers(refArg(args)), nil
	})
	refTool("find_callers", "Find the call sites of a method (call hierarchy). `ref` as in describe_symbol.", func(args json.RawMessage) (any, error) {
		return s.tools.FindCallers(refArg(args)), nil
	})
	refTool("type_hierarchy", "Supertypes (extends/implements, walked up) and subtypes of a type. `ref` as in describe_symbol.", func(args json.RawMessage) (any, error) {
		return s.tools.TypeHierarchy(refArg(args)), nil
	})
	s.registry = append(s.registry, toolDef{
		name:        "resolve_import",
		description: `Fully-qualified import candidates for an unqualified type name (e.g. "List").`,
		inputSchema: objSchema(map[string]any{"name": str}, "name"),
		usesProgram: true,
		handler: func(args json.RawMessage) (any, error) {
			var a struct {
				Name string `json:"name"`
			}
			sj(args, &a)
			return map[string]any{"imports": s.tools.ResolveImport(a.Name)}, nil
		},
	})
	s.registry = append(s.registry, toolDef{
		name:        "rename_symbol",
		description: "The workspace edits a rename would make (returned for you to apply; nothing is written). `ref` as in describe_symbol.",
		inputSchema: objSchema(map[string]any{"ref": str, "newName": str}, "ref", "newName"),
		usesProgram: true,
		handler: func(args json.RawMessage) (any, error) {
			var a struct {
				Ref     string `json:"ref"`
				NewName string `json:"newName"`
			}
			sj(args, &a)
			return s.tools.RenameSymbol(a.Ref, a.NewName), nil
		},
	})

	if s.project == nil {
		return
	}
	emptySchema := map[string]any{"type": "object", "properties": map[string]any{}}
	s.registry = append(s.registry,
		toolDef{name: "audit", description: "Scan the project's resolved dependencies (transitive) for known vulnerabilities (OSV).", inputSchema: emptySchema, handler: func(json.RawMessage) (any, error) { return s.project.Audit() }},
		toolDef{name: "licenses", description: "List every resolved dependency and the license it ships under (best-effort SPDX).", inputSchema: emptySchema, handler: func(json.RawMessage) (any, error) {
			rows, err := s.project.Licenses()
			return map[string]any{"licenses": rows}, err
		}},
		toolDef{name: "search_packages", description: "Search the configured package sources; returns group:artifact:version coords.", inputSchema: objSchema(map[string]any{"query": str}, "query"), handler: func(args json.RawMessage) (any, error) {
			var a struct {
				Query string `json:"query"`
			}
			sj(args, &a)
			matches, err := s.project.SearchPackages(a.Query)
			return map[string]any{"matches": matches}, err
		}},
		toolDef{name: "outdated", description: "Declared dependencies with a newer conflict-free stable version available (preview of `cappu update`; writes nothing).", inputSchema: emptySchema, handler: func(json.RawMessage) (any, error) {
			outdated, err := s.project.Outdated()
			return map[string]any{"outdated": outdated}, err
		}},
		toolDef{name: "latest_version", description: "The newest published version of a `group:artifact` across the sources.", inputSchema: objSchema(map[string]any{"coord": str}, "coord"), handler: func(args json.RawMessage) (any, error) {
			var a struct {
				Coord string `json:"coord"`
			}
			sj(args, &a)
			return s.project.LatestVersion(a.Coord)
		}},
		toolDef{name: "dependency_tree", description: "The resolved transitive dependency graph, or - with `coord` (group:artifact:version) - the path that pulls it onto the classpath.", inputSchema: objSchema(map[string]any{"coord": str}), handler: func(args json.RawMessage) (any, error) {
			var a struct {
				Coord string `json:"coord"`
			}
			sj(args, &a)
			return s.project.DependencyTree(a.Coord)
		}},
	)
}

func refArg(args json.RawMessage) string {
	var a struct {
		Ref string `json:"ref"`
	}
	sj(args, &a)
	return a.Ref
}

// refresh keeps tool results current with on-disk changes between calls.
// A cappu.json edit reloads the config and rebuilds everything (malformed
// edits keep the last good config, logged once); a classpath change (jar
// added/removed/replaced, e.g. by `cappu install`) rebuilds too. Source
// .java files are re-read individually by mtime. A config file appearing
// (server started without one) or disappearing is deliberately not handled:
// the tool list is a startup snapshot.
func (s *Server) refresh() {
	if s.config == nil {
		return
	}
	if s.config.ConfigPath != "" {
		if info, err := os.Stat(s.config.ConfigPath); err == nil {
			if mtime := info.ModTime().UnixNano(); mtime != s.configMtime {
				// Record first: a broken file logs once, not per call, and is
				// retried only when it changes again.
				s.configMtime = mtime
				next, lerr := config.Load(s.config.ConfigPath, s.config.BaseDir)
				if lerr != nil {
					fmt.Fprintf(os.Stderr, "cappu: %s (keeping previous config)\n", lerr)
				} else {
					s.rebuild(next)
				}
			}
		}
	}
	if fp := classpathFingerprint(s.config); !maps.Equal(fp, s.cpFingerprint) {
		s.rebuild(s.config)
	}
	for _, p := range s.config.CompilerOptions.SourcePaths {
		base := s.config.ResolvePath(p)
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".java") {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			mtime := info.ModTime().UnixNano()
			if s.mtimes[path] == mtime {
				return nil
			}
			text, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			s.program.AddProjectFile(pathToURI(filepath.ToSlash(path)), string(text))
			s.mtimes[path] = mtime
			return nil
		})
	}
}

// classpathFingerprint maps every .jar/.class file reachable from the
// config's classPath entries to its mtime - exactly the set LoadClassPath
// reads. Map inequality means the classpath changed (add, remove, replace);
// directory mtimes are deliberately not used (unreliable for nested changes).
// Port of src/workspace.ts classpathFingerprint.
func classpathFingerprint(cfg *config.Config) map[string]int64 {
	fp := map[string]int64{}
	stat := func(path string) {
		if info, err := os.Stat(path); err == nil {
			fp[path] = info.ModTime().UnixNano()
		}
	}
	for _, p := range cfg.CompilerOptions.ClassPath {
		entry := cfg.ResolvePath(p)
		if strings.HasSuffix(entry, ".jar") {
			stat(entry)
			continue
		}
		_ = filepath.WalkDir(entry, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".jar") || strings.HasSuffix(path, ".class") {
				stat(path)
			}
			return nil
		})
	}
	return fp
}

// loadConfiguredSources registers the config's classPath (.class stubs) and
// sourcePaths (.java sources, plus the generated-sources tree) into a program
// at startup. Port of loadConfiguredPaths in src/compiler/compiler.ts (as used
// by src/services/mcpServer.ts).
func loadConfiguredSources(program *compiler.Program, cfg *config.Config) {
	var classPaths []string
	for _, p := range cfg.CompilerOptions.ClassPath {
		classPaths = append(classPaths, cfg.ResolvePath(p))
	}
	compiler.LoadClassPath(program, classPaths)
	// .cappu/generated-sources/sources (annotation-processor output) is an
	// implicit extra source path; absent until the first processing compile.
	sourceDirs := make([]string, 0, len(cfg.CompilerOptions.SourcePaths)+1)
	for _, p := range cfg.CompilerOptions.SourcePaths {
		sourceDirs = append(sourceDirs, cfg.ResolvePath(p))
	}
	sourceDirs = append(sourceDirs, processors.GeneratedSourcesDir(cfg))
	for _, base := range sourceDirs {
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".java") {
				return nil
			}
			text, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			program.AddProjectFile(pathToURI(filepath.ToSlash(path)), string(text))
			return nil
		})
	}
}

// --- transport (newline-delimited JSON-RPC 2.0) ------------------------------

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Run reads newline-delimited JSON-RPC requests and serves them until EOF.
func (s *Server) Run(reader io.Reader, writer io.Writer) error {
	s.w = writer
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		s.dispatch(req)
	}
	return scanner.Err()
}

func (s *Server) dispatch(req mcpRequest) {
	switch req.Method {
	case "initialize":
		// Echo a supported client protocolVersion (the TS SDK's negotiation);
		// anything else falls back to our default.
		version := protocolVersion
		var init struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		sj(req.Params, &init)
		if slices.Contains(supportedProtocolVersions, init.ProtocolVersion) {
			version = init.ProtocolVersion
		}
		s.reply(req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cappu", "version": "1.0.0"},
			"instructions":    instructions,
		})
	case "notifications/initialized", "initialized":
		// notification: no response
	case "ping":
		s.reply(req.ID, map[string]any{})
	case "tools/list":
		var tools []map[string]any
		for _, t := range s.registry {
			tools = append(tools, map[string]any{"name": t.name, "description": t.description, "inputSchema": t.inputSchema})
		}
		s.reply(req.ID, map[string]any{"tools": tools})
	case "tools/call":
		s.handleToolCall(req)
	default:
		if len(req.ID) > 0 {
			s.replyError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func (s *Server) handleToolCall(req mcpRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	sj(req.Params, &params)
	for _, t := range s.registry {
		if t.name != params.Name {
			continue
		}
		if err := validateArgs(t, params.Arguments); err != nil {
			s.replyError(req.ID, -32602, err.Error())
			return
		}
		if t.usesProgram {
			s.refresh()
		}
		data, err := t.handler(params.Arguments)
		if err != nil {
			s.reply(req.ID, toolText(map[string]any{"error": err.Error()}, true))
			return
		}
		s.reply(req.ID, toolText(data, false))
		return
	}
	s.replyError(req.ID, -32602, "unknown tool: "+params.Name)
}

// validateArgs enforces the tool's declared required fields; the TS SDK
// zod-validates the same schemas, so a missing arg errors instead of silently
// becoming "".
func validateArgs(t toolDef, args json.RawMessage) error {
	var m map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &m); err != nil {
			return fmt.Errorf("invalid arguments for %s: %v", t.name, err)
		}
	}
	required, _ := t.inputSchema["required"].([]string)
	for _, key := range required {
		if _, ok := m[key]; !ok {
			return fmt.Errorf("invalid arguments for %s: missing %s", t.name, key)
		}
	}
	return nil
}

func toolText(data any, isError bool) map[string]any {
	// Like the TS JSON.stringify: no HTML escaping (List<String>, not
	// List\u003cString\u003e).
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(data)
	text := strings.TrimRight(buf.String(), "\n")
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isError {
		result["isError"] = true
	}
	return result
}

func (s *Server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // notification
	}
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) replyError(id json.RawMessage, code int, message string) {
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (s *Server) write(msg any) {
	body, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.w.Write(append(body, '\n'))
}
