package dapserver

// The DAP<->JDWP bridge: one debug session per `cappu dap` connection. It
// answers DAP requests by driving a jdwp.Client and turns JDWP events into DAP
// events. v1 scope: launch, breakpoints (with line->codeIndex mapping and
// ClassPrepare deferral), continue, stepping, and read-only locals (primitives
// + strings). The VM is driven all-threads-at-once (breakpoints suspend all).
//
// DAP requests run on the connection's read goroutine; JDWP events run on a
// dedicated goroutine fed by a buffered channel. A single mutex serializes both
// against the shared session state, matching the single-threaded TS reference.
// Port of src/services/dap/debugSession.ts.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/dap"
	"github.com/nikeee/cappu/internal/jdwp"
	jtest "github.com/nikeee/cappu/internal/testing"
)

type frameHandle struct {
	threadID uint64
	frameID  uint64
	location jdwp.Location
}

type requestedBp struct {
	line  int32
	id    int
	bound bool
}

type sourceBreakpoints struct {
	fqcn       string
	signature  string
	requested  []*requestedBp
	requestIDs []int32
}

// Session is a single DAP debug session.
type Session struct {
	conn *dap.Conn
	cfg  *config.Config
	mu   sync.Mutex

	client *jdwp.Client
	cmd    *exec.Cmd

	threadDap  map[uint64]int
	threadJdwp map[int]uint64
	nextThread int

	frames      map[int]frameHandle
	nextFrameID int
	varNodes    map[int]int
	nextVarRef  int

	breakpoints   map[string]*sourceBreakpoints
	classPrepared map[string]bool
	bpByRequest   map[int32]int
	nextBpID      int
	stepRequestID int32
	hasStep       bool

	// stopOnEntry: a one-shot breakpoint on main()'s first line; its requestID
	// is matched in the breakpoint event to report reason "entry".
	stopOnEntry    bool
	mainSignature  string
	entryRequestID int32
	hasEntry       bool

	sigCache     map[uint64]string
	methodsCache map[uint64][]jdwp.MethodInfo

	// JDWP events are queued (unbounded) and drained by processEvents under mu.
	// An unbounded queue is required: the JDWP read goroutine delivers events,
	// and if it could block on a full channel while a request handler holds mu
	// awaiting a JDWP reply, the reply (delivered by that same read goroutine)
	// would never arrive - a deadlock. Enqueue never blocks.
	evMu     sync.Mutex
	evCond   *sync.Cond
	evQueue  [][]byte
	evClosed bool
}

func NewSession(conn *dap.Conn, cfg *config.Config) *Session {
	s := &Session{
		conn:          conn,
		cfg:           cfg,
		threadDap:     map[uint64]int{},
		threadJdwp:    map[int]uint64{},
		nextThread:    1,
		frames:        map[int]frameHandle{},
		nextFrameID:   1,
		varNodes:      map[int]int{},
		nextVarRef:    1,
		breakpoints:   map[string]*sourceBreakpoints{},
		classPrepared: map[string]bool{},
		bpByRequest:   map[int32]int{},
		nextBpID:      1,
		sigCache:      map[uint64]string{},
		methodsCache:  map[uint64][]jdwp.MethodInfo{},
	}
	s.evCond = sync.NewCond(&s.evMu)
	conn.OnRequest("initialize", s.onInitialize)
	conn.OnRequest("launch", s.onLaunch)
	conn.OnRequest("setBreakpoints", s.onSetBreakpoints)
	conn.OnRequest("configurationDone", s.onConfigurationDone)
	conn.OnRequest("threads", s.onThreads)
	conn.OnRequest("stackTrace", s.onStackTrace)
	conn.OnRequest("scopes", s.onScopes)
	conn.OnRequest("variables", s.onVariables)
	conn.OnRequest("continue", s.onContinue)
	conn.OnRequest("next", func(r json.RawMessage) (any, error) { return s.onStep(r, jdwp.StepDepthOver) })
	conn.OnRequest("stepIn", func(r json.RawMessage) (any, error) { return s.onStep(r, jdwp.StepDepthIn) })
	conn.OnRequest("stepOut", func(r json.RawMessage) (any, error) { return s.onStep(r, jdwp.StepDepthOut) })
	conn.OnRequest("pause", s.onPause)
	conn.OnRequest("disconnect", s.onDisconnect)
	conn.OnRequest("terminate", s.onDisconnect)
	return s
}

func (s *Session) onInitialize(json.RawMessage) (any, error) {
	// The initialized event must follow the initialize response; emit it from a
	// goroutine so the synchronous response write goes first.
	go s.conn.SendEvent("initialized", nil)
	return dap.Capabilities{SupportsConfigurationDoneRequest: true, SupportsTerminateRequest: true}, nil
}

func (s *Session) onLaunch(raw json.RawMessage) (any, error) {
	var args dap.LaunchArguments
	_ = json.Unmarshal(raw, &args)
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, d := range CompileForDebug(s.cfg) {
		if d.Severity == "error" {
			return nil, fmt.Errorf("debug build failed: %s", d.Message)
		}
	}
	mainClass, err := ResolveMainClass(s.cfg, args)
	if err != nil {
		return nil, err
	}
	classPath := DebuggeeClassPath(s.cfg, args.ClassPath)
	java := jtest.ResolveJava(s.cfg)
	launched, err := LaunchUnderJdwp(java, classPath, mainClass, LaunchOptions{
		VMArgs:      DebuggeeVMArgs(s.cfg, args),
		ProgramArgs: args.Args,
		Env:         args.Env,
		Cwd:         args.Cwd,
	})
	if err != nil {
		return nil, err
	}
	s.cmd = launched.Cmd
	s.stopOnEntry = args.StopOnEntry
	s.mainSignature = "L" + strings.ReplaceAll(mainClass, ".", "/") + ";"

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.pumpOutput(launched.Stdout, "stdout") }()
	go func() { defer wg.Done(); s.pumpOutput(launched.Stderr, "stderr") }()
	go func() {
		wg.Wait()
		_ = launched.Cmd.Wait()
		s.conn.SendEvent("exited", dap.ExitedEventBody{ExitCode: launched.Cmd.ProcessState.ExitCode()})
		s.conn.SendEvent("terminated", nil)
	}()

	client, err := jdwp.Connect("127.0.0.1", launched.Port)
	if err != nil {
		// The JVM is running (and suspended); kill it so a failed launch does
		// not leak a frozen process.
		_ = launched.Cmd.Process.Kill()
		return nil, err
	}
	client.OnEvent(s.enqueueEvent)
	s.client = client
	go s.processEvents()

	// The VM started suspended; bind breakpoints set before the client connected.
	for _, entry := range s.breakpoints {
		s.bindSource(entry)
	}

	// stopOnEntry: arm a one-shot breakpoint on main(). The class may already be
	// loaded; if not, a ClassPrepare arms it when it loads.
	if s.stopOnEntry {
		if classes, _ := jdwp.ClassesBySignature(client, s.mainSignature); len(classes) > 0 {
			s.setEntryBreakpoint(s.mainSignature)
		} else {
			s.ensureClassPrepare(mainClass)
		}
	}
	return struct{}{}, nil
}

func (s *Session) onConfigurationDone(json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		_ = jdwp.VMResumeCmd(s.client)
	}
	return struct{}{}, nil
}

func (s *Session) onSetBreakpoints(raw json.RawMessage) (any, error) {
	var args dap.SetBreakpointsArguments
	_ = json.Unmarshal(raw, &args)
	s.mu.Lock()
	defer s.mu.Unlock()

	if args.Source.Path == "" {
		return dap.SetBreakpointsResponseBody{Breakpoints: []dap.Breakpoint{}}, nil
	}
	var lines []int32
	if len(args.Breakpoints) > 0 {
		for _, b := range args.Breakpoints {
			lines = append(lines, int32(b.Line))
		}
	} else {
		for _, l := range args.Lines {
			lines = append(lines, int32(l))
		}
	}

	if prev, ok := s.breakpoints[args.Source.Path]; ok && s.client != nil {
		for _, requestID := range prev.requestIDs {
			_ = jdwp.EventRequestClear(s.client, jdwp.EKBreakpoint, requestID)
		}
	}

	fqcn, signature := classSignatureForSource(args.Source.Path)
	entry := &sourceBreakpoints{fqcn: fqcn, signature: signature}
	for _, line := range lines {
		entry.requested = append(entry.requested, &requestedBp{line: line, id: s.nextBpID})
		s.nextBpID++
	}
	s.breakpoints[args.Source.Path] = entry

	if s.client != nil {
		s.bindSource(entry)
	}
	out := make([]dap.Breakpoint, len(entry.requested))
	for i, r := range entry.requested {
		out[i] = dap.Breakpoint{ID: r.id, Verified: r.bound, Line: int(r.line)}
	}
	return dap.SetBreakpointsResponseBody{Breakpoints: out}, nil
}

// ensureClassPrepare registers a ClassPrepare request for a fully-qualified
// class name once, so we get notified (and can bind/arm) when it loads.
func (s *Session) ensureClassPrepare(fqcn string) {
	if s.classPrepared[fqcn] {
		return
	}
	s.classPrepared[fqcn] = true
	_, _ = jdwp.EventRequestSet(s.client, jdwp.EKClassPrepare, jdwp.SuspendAll,
		[]jdwp.Modifier{{Kind: jdwp.ModClassMatch, Pattern: fqcn}})
}

// bindSource resolves each requested line to a JDWP breakpoint, registering a
// ClassPrepare request when the class is not loaded yet. Caller holds s.mu.
func (s *Session) bindSource(entry *sourceBreakpoints) {
	classes, err := jdwp.ClassesBySignature(s.client, entry.signature)
	if err != nil {
		return
	}
	if len(classes) == 0 {
		s.ensureClassPrepare(entry.fqcn)
		return
	}
	classID := classes[0].TypeID
	methods := s.classMethods(classID)
	var methodLines []MethodLines
	for _, m := range methods {
		lt, err := jdwp.MethodLineTableCmd(s.client, classID, m.MethodID)
		if err != nil {
			continue
		}
		methodLines = append(methodLines, MethodLines{MethodID: m.MethodID, Lines: lt.Lines})
	}
	for _, bp := range entry.requested {
		if bp.bound {
			continue
		}
		loc, ok := ResolveLine(methodLines, bp.line)
		if !ok {
			continue
		}
		requestID, err := jdwp.EventRequestSet(s.client, jdwp.EKBreakpoint, jdwp.SuspendAll,
			[]jdwp.Modifier{{Kind: jdwp.ModLocationOnly, Location: jdwp.Location{
				TypeTag: jdwp.TypeTagClass, ClassID: classID, MethodID: loc.MethodID, Index: loc.Index,
			}}})
		if err != nil {
			continue
		}
		entry.requestIDs = append(entry.requestIDs, requestID)
		s.bpByRequest[requestID] = bp.id
		bp.bound = true
		bp.line = loc.Line
	}
}

// setEntryBreakpoint arms a one-shot breakpoint at main()'s first line (a
// Count:1 modifier makes it self-clearing). The breakpoint event reports
// reason "entry". Caller holds s.mu.
func (s *Session) setEntryBreakpoint(signature string) {
	if s.hasEntry {
		return
	}
	classes, err := jdwp.ClassesBySignature(s.client, signature)
	if err != nil || len(classes) == 0 {
		return
	}
	classID := classes[0].TypeID
	var mainID uint64
	found := false
	for _, m := range s.classMethods(classID) {
		if m.Name == "main" {
			mainID = m.MethodID
			found = true
			break
		}
	}
	if !found {
		return
	}
	var index uint64
	if lt, err := jdwp.MethodLineTableCmd(s.client, classID, mainID); err == nil && len(lt.Lines) > 0 {
		index = lt.Lines[0].LineCodeIndex
		for _, e := range lt.Lines {
			if e.LineCodeIndex < index {
				index = e.LineCodeIndex
			}
		}
	}
	rid, err := jdwp.EventRequestSet(s.client, jdwp.EKBreakpoint, jdwp.SuspendAll, []jdwp.Modifier{
		{Kind: jdwp.ModLocationOnly, Location: jdwp.Location{TypeTag: jdwp.TypeTagClass, ClassID: classID, MethodID: mainID, Index: index}},
		{Kind: jdwp.ModCount, Count: 1}, // one-shot
	})
	if err != nil {
		return
	}
	s.entryRequestID = rid
	s.hasEntry = true
}

func (s *Session) onThreads(json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return dap.ThreadsResponseBody{Threads: []dap.Thread{}}, nil
	}
	ids, err := jdwp.AllThreads(s.client)
	if err != nil {
		return nil, err
	}
	threads := make([]dap.Thread, 0, len(ids))
	for _, jid := range ids {
		name, _ := jdwp.ThreadName(s.client, jid)
		threads = append(threads, dap.Thread{ID: s.threadDapID(jid), Name: name})
	}
	return dap.ThreadsResponseBody{Threads: threads}, nil
}

func (s *Session) onStackTrace(raw json.RawMessage) (any, error) {
	var args dap.StackTraceArguments
	_ = json.Unmarshal(raw, &args)
	s.mu.Lock()
	defer s.mu.Unlock()
	jid, ok := s.threadJdwp[args.ThreadID]
	if !ok || s.client == nil {
		return nil, fmt.Errorf("unknown thread %d", args.ThreadID)
	}
	frames, err := jdwp.ThreadFrames(s.client, jid, 0, -1)
	if err != nil {
		return nil, err
	}
	out := make([]dap.StackFrame, 0, len(frames))
	for _, f := range frames {
		id := s.nextFrameID
		s.nextFrameID++
		s.frames[id] = frameHandle{threadID: jid, frameID: f.FrameID, location: f.Location}
		fqcn := SignatureToType(s.classSignature(f.Location.ClassID))
		methodName := s.methodName(f.Location.ClassID, f.Location.MethodID)
		var src *dap.Source
		if path := s.sourcePathForClass(fqcn); path != "" {
			src = &dap.Source{Name: filepath.Base(path), Path: path}
		}
		out = append(out, dap.StackFrame{
			ID:     id,
			Name:   fqcn + "." + methodName,
			Source: src,
			Line:   int(s.lineForLocation(f.Location)),
			Column: 0,
		})
	}
	return dap.StackTraceResponseBody{StackFrames: out, TotalFrames: len(out)}, nil
}

func (s *Session) onScopes(raw json.RawMessage) (any, error) {
	var args dap.ScopesArguments
	_ = json.Unmarshal(raw, &args)
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := s.nextVarRef
	s.nextVarRef++
	s.varNodes[ref] = args.FrameID
	return dap.ScopesResponseBody{Scopes: []dap.Scope{{Name: "Locals", VariablesReference: ref, Expensive: false}}}, nil
}

func (s *Session) onVariables(raw json.RawMessage) (any, error) {
	var args dap.VariablesArguments
	_ = json.Unmarshal(raw, &args)
	s.mu.Lock()
	defer s.mu.Unlock()
	frameID, ok := s.varNodes[args.VariablesReference]
	if !ok {
		return dap.VariablesResponseBody{Variables: []dap.Variable{}}, nil
	}
	frame, ok := s.frames[frameID]
	if !ok || s.client == nil {
		return dap.VariablesResponseBody{Variables: []dap.Variable{}}, nil
	}
	slots, err := jdwp.MethodVariableTable(s.client, frame.location.ClassID, frame.location.MethodID)
	if err != nil {
		return dap.VariablesResponseBody{Variables: []dap.Variable{}}, nil
	}
	var visible []jdwp.VariableSlot
	for _, sl := range slots {
		if frame.location.Index >= sl.CodeIndex && frame.location.Index < sl.CodeIndex+uint64(sl.Length) {
			visible = append(visible, sl)
		}
	}
	if len(visible) == 0 {
		return dap.VariablesResponseBody{Variables: []dap.Variable{}}, nil
	}
	reqSlots := make([]jdwp.Slot, len(visible))
	for i, sl := range visible {
		reqSlots[i] = jdwp.Slot{Slot: sl.Slot, SigByte: SignatureTagByte(sl.Signature)}
	}
	values, err := jdwp.StackFrameGetValues(s.client, frame.threadID, frame.frameID, reqSlots)
	if err != nil {
		return nil, err
	}
	vars := make([]dap.Variable, len(visible))
	for i, sl := range visible {
		vars[i] = dap.Variable{
			Name:               sl.Name,
			Type:               SignatureToType(sl.Signature),
			Value:              s.renderValue(values[i], sl.Signature),
			VariablesReference: 0,
		}
	}
	return dap.VariablesResponseBody{Variables: vars}, nil
}

// errNotLaunched is returned when a request that drives the VM arrives before a
// successful launch (mirrors the TS jdwp() guard that throws "not launched").
var errNotLaunched = errors.New("not launched")

// Resume is whole-VM (breakpoints suspend all threads).
func (s *Session) onContinue(json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, errNotLaunched
	}
	s.clearStopState()
	if err := jdwp.VMResumeCmd(s.client); err != nil {
		return nil, err
	}
	return dap.ContinueResponseBody{AllThreadsContinued: true}, nil
}

func (s *Session) onStep(raw json.RawMessage, depth int32) (any, error) {
	var args dap.ThreadArgument
	_ = json.Unmarshal(raw, &args)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, errNotLaunched
	}
	jid, ok := s.threadJdwp[args.ThreadID]
	if !ok {
		return nil, fmt.Errorf("unknown thread %d", args.ThreadID)
	}
	requestID, err := jdwp.EventRequestSet(s.client, jdwp.EKSingleStep, jdwp.SuspendAll,
		[]jdwp.Modifier{{Kind: jdwp.ModStep, ThreadID: jid, StepSize: jdwp.StepSizeLine, StepDepth: depth}})
	if err != nil {
		return nil, err
	}
	s.stepRequestID = requestID
	s.hasStep = true
	s.clearStopState()
	if err := jdwp.VMResumeCmd(s.client); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

func (s *Session) onPause(raw json.RawMessage) (any, error) {
	var args dap.ThreadArgument
	_ = json.Unmarshal(raw, &args)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, errNotLaunched
	}
	if err := jdwp.VMSuspendCmd(s.client); err != nil {
		return nil, err
	}
	s.conn.SendEvent("stopped", dap.StoppedEventBody{Reason: "pause", ThreadID: args.ThreadID, AllThreadsStopped: true})
	return struct{}{}, nil
}

func (s *Session) onDisconnect(json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		_ = jdwp.VMExitCmd(s.client, 0)
		s.client.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	s.stopEvents() // let the processEvents goroutine exit instead of leaking
	return struct{}{}, nil
}

// enqueueEvent is the JDWP event callback; it runs on the JDWP read goroutine
// and must never block (see the evQueue comment), so it only appends.
func (s *Session) enqueueEvent(data []byte) {
	s.evMu.Lock()
	s.evQueue = append(s.evQueue, data)
	s.evMu.Unlock()
	s.evCond.Signal()
}

// stopEvents wakes processEvents so it can exit (called on disconnect).
func (s *Session) stopEvents() {
	s.evMu.Lock()
	s.evClosed = true
	s.evMu.Unlock()
	s.evCond.Signal()
}

func (s *Session) processEvents() {
	for {
		s.evMu.Lock()
		for len(s.evQueue) == 0 && !s.evClosed {
			s.evCond.Wait()
		}
		if len(s.evQueue) == 0 && s.evClosed {
			s.evMu.Unlock()
			return
		}
		data := s.evQueue[0]
		s.evQueue = s.evQueue[1:]
		s.evMu.Unlock()

		s.mu.Lock()
		// A malformed event must not panic this goroutine and kill event
		// handling; the byte-reader codec panics on a truncated packet. Only
		// the decode is guarded (like the TS build) so a real bug in the
		// dispatch below still surfaces as a panic.
		if comp, ok := decodeEvent(data, s.client.IDSizes); ok {
			s.handleJdwpEvent(comp)
		}
		s.mu.Unlock()
	}
}

// decodeEvent decodes a composite, absorbing the codec's truncation panic.
func decodeEvent(data []byte, sizes jdwp.IDSizes) (comp jdwp.Composite, ok bool) {
	defer func() { ok = recover() == nil }()
	return jdwp.DecodeComposite(data, sizes), true
}

// handleJdwpEvent dispatches a decoded composite. Caller holds s.mu.
func (s *Session) handleJdwpEvent(comp jdwp.Composite) {
	for _, ev := range comp.Events {
		switch ev.Kind {
		case jdwp.EKBreakpoint:
			entry := s.hasEntry && ev.RequestID == s.entryRequestID
			if entry {
				s.hasEntry = false // the Count:1 request is spent
			}
			reason := "breakpoint"
			if entry {
				reason = "entry"
			}
			s.clearStopState()
			s.conn.SendEvent("stopped", dap.StoppedEventBody{Reason: reason, ThreadID: s.threadDapID(ev.Thread), AllThreadsStopped: true})
		case jdwp.EKSingleStep:
			if s.hasStep {
				_ = jdwp.EventRequestClear(s.client, jdwp.EKSingleStep, s.stepRequestID)
				s.hasStep = false
			}
			s.clearStopState()
			s.conn.SendEvent("stopped", dap.StoppedEventBody{Reason: "step", ThreadID: s.threadDapID(ev.Thread), AllThreadsStopped: true})
		case jdwp.EKClassPrepare:
			s.onClassPrepare(ev.Signature)
		case jdwp.EKThreadStart:
			s.conn.SendEvent("thread", dap.ThreadEventBody{Reason: "started", ThreadID: s.threadDapID(ev.Thread)})
		case jdwp.EKThreadDeath:
			s.conn.SendEvent("thread", dap.ThreadEventBody{Reason: "exited", ThreadID: s.threadDapID(ev.Thread)})
		case jdwp.EKVMDeath:
			s.conn.SendEvent("terminated", nil)
		}
	}
}

// onClassPrepare binds deferred breakpoints for a freshly-loaded class and
// resumes (the ClassPrepare request suspended all threads). Caller holds s.mu.
func (s *Session) onClassPrepare(signature string) {
	for _, entry := range s.breakpoints {
		if entry.signature != signature {
			continue
		}
		s.bindSource(entry)
		for _, bp := range entry.requested {
			if bp.bound {
				s.conn.SendEvent("breakpoint", dap.BreakpointEventBody{
					Reason:     "changed",
					Breakpoint: dap.Breakpoint{ID: bp.id, Verified: true, Line: int(bp.line)},
				})
			}
		}
	}
	if s.stopOnEntry && signature == s.mainSignature {
		s.setEntryBreakpoint(signature)
	}
	_ = jdwp.VMResumeCmd(s.client)
}

func (s *Session) clearStopState() {
	s.frames = map[int]frameHandle{}
	s.varNodes = map[int]int{}
	s.nextFrameID = 1
	s.nextVarRef = 1
}

func (s *Session) threadDapID(jdwpID uint64) int {
	if id, ok := s.threadDap[jdwpID]; ok {
		return id
	}
	id := s.nextThread
	s.nextThread++
	s.threadDap[jdwpID] = id
	s.threadJdwp[id] = jdwpID
	return id
}

func (s *Session) classSignature(classID uint64) string {
	if sig, ok := s.sigCache[classID]; ok {
		return sig
	}
	sig, _ := jdwp.ReferenceTypeSignature(s.client, classID)
	s.sigCache[classID] = sig
	return sig
}

func (s *Session) classMethods(classID uint64) []jdwp.MethodInfo {
	if m, ok := s.methodsCache[classID]; ok {
		return m
	}
	methods, _ := jdwp.ReferenceTypeMethods(s.client, classID)
	s.methodsCache[classID] = methods
	return methods
}

func (s *Session) methodName(classID, methodID uint64) string {
	for _, m := range s.classMethods(classID) {
		if m.MethodID == methodID {
			return m.Name
		}
	}
	return "<unknown>"
}

func (s *Session) lineForLocation(loc jdwp.Location) int32 {
	lt, err := jdwp.MethodLineTableCmd(s.client, loc.ClassID, loc.MethodID)
	if err != nil {
		return 0
	}
	var line int32
	var bestIndex uint64
	found := false
	for _, e := range lt.Lines {
		if e.LineCodeIndex <= loc.Index && (!found || e.LineCodeIndex > bestIndex) {
			bestIndex = e.LineCodeIndex
			line = e.LineNumber
			found = true
		}
	}
	return line
}

func (s *Session) renderValue(v jdwp.Value, signature string) string {
	if v.Object {
		if v.ObjectID == 0 {
			return "null"
		}
		if v.Tag == jdwp.TagString {
			str, _ := jdwp.StringValue(s.client, v.ObjectID)
			return strconv.Quote(str)
		}
		return fmt.Sprintf("%s@%x", SignatureToType(signature), v.ObjectID)
	}
	switch v.Tag {
	case jdwp.TagBoolean:
		return strconv.FormatBool(v.Bool)
	case jdwp.TagChar:
		return fmt.Sprintf("'%c'", rune(v.Int))
	case jdwp.TagFloat, jdwp.TagDouble:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	default:
		return strconv.FormatInt(v.Int, 10)
	}
}

func (s *Session) pumpOutput(r io.Reader, category string) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.conn.SendEvent("output", dap.OutputEventBody{Category: category, Output: string(buf[:n])})
		}
		if err != nil {
			return
		}
	}
}

var packageRe = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*;`)

// classSignatureForSource derives the JDWP class signature for a source file
// from its package declaration and file name.
func classSignatureForSource(path string) (fqcn, signature string) {
	pkg := ""
	if data, err := os.ReadFile(path); err == nil {
		if m := packageRe.FindSubmatch(data); m != nil {
			pkg = string(m[1])
		}
	}
	typeName := strings.TrimSuffix(filepath.Base(path), ".java")
	if pkg != "" {
		fqcn = pkg + "." + typeName
	} else {
		fqcn = typeName
	}
	return fqcn, "L" + strings.ReplaceAll(fqcn, ".", "/") + ";"
}

func (s *Session) sourcePathForClass(fqcn string) string {
	top := strings.SplitN(fqcn, "$", 2)[0]
	rel := strings.ReplaceAll(top, ".", "/") + ".java"
	for _, sp := range s.cfg.CompilerOptions.SourcePaths {
		p := s.cfg.ResolvePath(filepath.Join(sp, rel))
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
