// The subset of Debug Adapter Protocol request arguments, response bodies and
// event bodies cappu's adapter uses. Field names match the DAP spec exactly
// (they go on the wire as-is). Only what v1 needs is declared.
//
// Port reference for togo/internal/dap/protocol.go.

export interface Capabilities {
  supportsConfigurationDoneRequest?: boolean;
  supportsTerminateRequest?: boolean;
  supportTerminateDebuggee?: boolean;
}

export interface LaunchArguments {
  /** Fully-qualified main class to run (overrides cappu.json's mainClass). */
  mainClass?: string;
  /** Program arguments passed to the Java main(String[]). */
  args?: string[];
  /** JVM arguments for the debuggee (e.g. -Xmx512m, -Dkey=value). */
  vmArgs?: string[];
  /** Extra classpath entries appended to the project's runtime classpath. */
  classPath?: string[];
  /** Environment variables for the debuggee, merged over the inherited env. */
  env?: Record<string, string>;
  /** Working directory for the debuggee process. */
  cwd?: string;
  /** Stop on the first line of main before any user code runs. */
  stopOnEntry?: boolean;
  /** Launch without attaching the debugger (just run). */
  noDebug?: boolean;
}

export interface AttachArguments {
  hostName?: string;
  port: number;
}

export interface Source {
  name?: string;
  path?: string;
}

export interface SourceBreakpoint {
  line: number;
  condition?: string;
}

export interface SetBreakpointsArguments {
  source: Source;
  breakpoints?: SourceBreakpoint[];
  lines?: number[];
}

export interface Breakpoint {
  id?: number;
  verified: boolean;
  line?: number;
  message?: string;
}

export interface Thread {
  id: number;
  name: string;
}

export interface StackFrame {
  id: number;
  name: string;
  source?: Source;
  line: number;
  column: number;
}

export interface StackTraceArguments {
  threadId: number;
  startFrame?: number;
  levels?: number;
}

export interface Scope {
  name: string;
  variablesReference: number;
  expensive: boolean;
}

export interface ScopesArguments {
  frameId: number;
}

export interface Variable {
  name: string;
  value: string;
  type?: string;
  variablesReference: number;
}

export interface VariablesArguments {
  variablesReference: number;
}

export interface ThreadArgument {
  threadId: number;
}

export interface StepArguments {
  threadId: number;
}

// --- Event bodies -----------------------------------------------------------

export interface StoppedEventBody {
  reason: "entry" | "breakpoint" | "step" | "pause" | "exception";
  threadId?: number;
  allThreadsStopped?: boolean;
  description?: string;
}

export interface OutputEventBody {
  category: "stdout" | "stderr" | "console";
  output: string;
}

export interface ExitedEventBody {
  exitCode: number;
}

export interface ThreadEventBody {
  reason: "started" | "exited";
  threadId: number;
}

export interface ContinuedEventBody {
  threadId: number;
  allThreadsContinued?: boolean;
}
