package dap

// The subset of Debug Adapter Protocol request arguments, response bodies and
// event bodies cappu's adapter uses. JSON field names match the DAP spec (they
// go on the wire as-is). Port of src/services/dap/protocol.ts.

type Capabilities struct {
	SupportsConfigurationDoneRequest bool `json:"supportsConfigurationDoneRequest,omitempty"`
	SupportsTerminateRequest         bool `json:"supportsTerminateRequest,omitempty"`
}

type LaunchArguments struct {
	MainClass   string   `json:"mainClass,omitempty"`
	Args        []string `json:"args,omitempty"`
	ClassPath   []string `json:"classPath,omitempty"`
	StopOnEntry bool     `json:"stopOnEntry,omitempty"`
	NoDebug     bool     `json:"noDebug,omitempty"`
}

type Source struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

type SourceBreakpoint struct {
	Line      int    `json:"line"`
	Condition string `json:"condition,omitempty"`
}

type SetBreakpointsArguments struct {
	Source      Source             `json:"source"`
	Breakpoints []SourceBreakpoint `json:"breakpoints,omitempty"`
	Lines       []int              `json:"lines,omitempty"`
}

type Breakpoint struct {
	ID       int    `json:"id,omitempty"`
	Verified bool   `json:"verified"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message,omitempty"`
}

type Thread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type StackFrame struct {
	ID     int     `json:"id"`
	Name   string  `json:"name"`
	Source *Source `json:"source,omitempty"`
	Line   int     `json:"line"`
	Column int     `json:"column"`
}

type StackTraceArguments struct {
	ThreadID   int `json:"threadId"`
	StartFrame int `json:"startFrame,omitempty"`
	Levels     int `json:"levels,omitempty"`
}

type Scope struct {
	Name               string `json:"name"`
	VariablesReference int    `json:"variablesReference"`
	Expensive          bool   `json:"expensive"`
}

type ScopesArguments struct {
	FrameID int `json:"frameId"`
}

type Variable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variablesReference"`
}

type VariablesArguments struct {
	VariablesReference int `json:"variablesReference"`
}

type ThreadArgument struct {
	ThreadID int `json:"threadId"`
}

// --- Event bodies -----------------------------------------------------------

type StoppedEventBody struct {
	Reason            string `json:"reason"`
	ThreadID          int    `json:"threadId,omitempty"`
	AllThreadsStopped bool   `json:"allThreadsStopped,omitempty"`
	Description       string `json:"description,omitempty"`
}

type OutputEventBody struct {
	Category string `json:"category"`
	Output   string `json:"output"`
}

type ExitedEventBody struct {
	ExitCode int `json:"exitCode"`
}

type ThreadEventBody struct {
	Reason   string `json:"reason"`
	ThreadID int    `json:"threadId"`
}

type ContinueResponseBody struct {
	AllThreadsContinued bool `json:"allThreadsContinued"`
}

type StackTraceResponseBody struct {
	StackFrames []StackFrame `json:"stackFrames"`
	TotalFrames int          `json:"totalFrames"`
}

type ThreadsResponseBody struct {
	Threads []Thread `json:"threads"`
}

type ScopesResponseBody struct {
	Scopes []Scope `json:"scopes"`
}

type VariablesResponseBody struct {
	Variables []Variable `json:"variables"`
}

type SetBreakpointsResponseBody struct {
	Breakpoints []Breakpoint `json:"breakpoints"`
}

type BreakpointEventBody struct {
	Reason     string     `json:"reason"`
	Breakpoint Breakpoint `json:"breakpoint"`
}
