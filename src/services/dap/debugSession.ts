// The DAP<->JDWP bridge: one debug session per `cappu dap` connection. It
// answers DAP requests by driving a JdwpClient and turns JDWP events
// (breakpoint/step hits, class prepares, thread + VM lifecycle) into DAP events.
// v1 scope: launch, breakpoints, continue, stepping, and read-only locals
// (primitives + strings). The VM is driven all-threads-at-once (breakpoints use
// suspend-all), which keeps the single-threaded debug model simple.
//
// Port reference for togo/internal/dapserver/session.go.

import { existsSync, readFileSync } from "node:fs";
import { basename, join } from "node:path";

import { type CappuConfig, resolveConfigPath } from "../../config.ts";
import {
  allThreads,
  classesBySignature,
  eventRequestClear,
  eventRequestSet,
  type Location,
  methodLineTable,
  type MethodInfo,
  methodVariableTable,
  type JdwpValue,
  referenceTypeMethods,
  referenceTypeSignature,
  stackFrameGetValues,
  stringValue,
  threadFrames,
  threadName,
  vmExit,
  vmResume,
  vmSuspend,
} from "../../jdwp/commands.ts";
import { decodeComposite } from "../../jdwp/events.ts";
import { JdwpClient } from "../../jdwp/jdwpClient.ts";
import {
  EventKind,
  StepDepth,
  StepSize,
  SuspendPolicy,
  Tag,
  TypeTag,
} from "../../jdwp/protocol.ts";
import { compileForDebug, debuggeeClassPath, resolveMainClass } from "./debuggee.ts";
import { type DebuggeeProcess, launchUnderJdwp } from "./launch.ts";
import { resolveLine } from "./lineMapping.ts";
import type {
  Breakpoint,
  Capabilities,
  LaunchArguments,
  Scope,
  ScopesArguments,
  SetBreakpointsArguments,
  StackFrame,
  StackTraceArguments,
  StepArguments,
  ThreadArgument,
  Variable,
  VariablesArguments,
} from "./protocol.ts";
import { resolveJava } from "../../testing/index.ts";
import { signatureTagByte, signatureToType } from "./signatures.ts";
import type { DapConnection } from "./transport.ts";

interface FrameHandle {
  threadId: bigint;
  frameId: bigint;
  location: Location;
}

interface SourceBreakpoints {
  fqcn: string;
  signature: string;
  requested: { line: number; id: number; bound: boolean }[];
  requestIds: number[];
}

// Bidirectional map between DAP integer thread ids and JDWP threadID bigints.
class ThreadIds {
  private toDap = new Map<bigint, number>();
  private toJdwp = new Map<number, bigint>();
  private next = 1;
  dap(jdwp: bigint): number {
    let id = this.toDap.get(jdwp);
    if (id === undefined) {
      id = this.next++;
      this.toDap.set(jdwp, id);
      this.toJdwp.set(id, jdwp);
    }
    return id;
  }
  jdwp(dap: number): bigint {
    const j = this.toJdwp.get(dap);
    if (j === undefined) throw new Error(`unknown thread ${dap}`);
    return j;
  }
}

export class DebugSession {
  private client?: JdwpClient;
  private child?: DebuggeeProcess;
  private readonly threadIds = new ThreadIds();
  private readonly frames = new Map<number, FrameHandle>();
  private nextFrameId = 1;
  private readonly varNodes = new Map<number, { frameId: number }>();
  private nextVarRef = 1;
  private readonly breakpoints = new Map<string, SourceBreakpoints>();
  private readonly classPrepared = new Set<string>();
  private readonly bpByRequest = new Map<number, number>(); // JDWP requestId -> DAP bp id
  private nextBpId = 1;
  private stepRequestId?: number;
  // stopOnEntry: a one-shot breakpoint on main()'s first line; its requestId is
  // matched in the breakpoint event to report reason "entry" instead.
  private stopOnEntry = false;
  private mainSignature?: string;
  private entryRequestId?: number;
  // Per-class JDWP metadata caches (class structure is stable for the run).
  private readonly sigCache = new Map<bigint, string>();
  private readonly methodsCache = new Map<bigint, MethodInfo[]>();

  constructor(
    private readonly conn: DapConnection,
    private readonly config: CappuConfig,
  ) {
    conn.onRequest("initialize", () => this.onInitialize());
    conn.onRequest("launch", args => this.onLaunch(args as LaunchArguments));
    conn.onRequest("setBreakpoints", args =>
      this.onSetBreakpoints(args as SetBreakpointsArguments),
    );
    conn.onRequest("configurationDone", () => this.onConfigurationDone());
    conn.onRequest("threads", () => this.onThreads());
    conn.onRequest("stackTrace", args => this.onStackTrace(args as StackTraceArguments));
    conn.onRequest("scopes", args => this.onScopes(args as ScopesArguments));
    conn.onRequest("variables", args => this.onVariables(args as VariablesArguments));
    conn.onRequest("continue", () => this.onContinue());
    conn.onRequest("next", args => this.onStep(args as StepArguments, StepDepth.OVER));
    conn.onRequest("stepIn", args => this.onStep(args as StepArguments, StepDepth.INTO));
    conn.onRequest("stepOut", args => this.onStep(args as StepArguments, StepDepth.OUT));
    conn.onRequest("pause", args => this.onPause(args as ThreadArgument));
    conn.onRequest("disconnect", () => this.onDisconnect());
    conn.onRequest("terminate", () => this.onDisconnect());
  }

  private jdwp(): JdwpClient {
    if (!this.client) throw new Error("not launched");
    return this.client;
  }

  private onInitialize(): Capabilities {
    // The `initialized` event must follow the initialize response, so defer it.
    setImmediate(() => this.conn.sendEvent("initialized"));
    return { supportsConfigurationDoneRequest: true, supportsTerminateRequest: true };
  }

  private async onLaunch(args: LaunchArguments): Promise<void> {
    const diagnostics = compileForDebug(this.config);
    const errors = diagnostics.filter(d => d.severity === "error");
    if (errors.length > 0) throw new Error(`debug build failed: ${errors[0].message}`);

    const mainClass = resolveMainClass(this.config, args);
    const classPath = debuggeeClassPath(this.config, args.classPath ?? []);
    const java = resolveJava(this.config);
    const launched = await launchUnderJdwp(java, classPath, mainClass, {
      vmArgs: args.vmArgs,
      programArgs: args.args,
      env: args.env,
      cwd: args.cwd,
    });
    this.child = launched.process;
    this.stopOnEntry = args.stopOnEntry ?? false;
    this.mainSignature = `L${mainClass.replaceAll(".", "/")};`;

    launched.process.stdout.on("data", (c: Buffer) =>
      this.conn.sendEvent("output", { category: "stdout", output: c.toString("utf8") }),
    );
    launched.process.stderr.on("data", (c: Buffer) =>
      this.conn.sendEvent("output", { category: "stderr", output: c.toString("utf8") }),
    );
    launched.process.once("exit", code => {
      this.conn.sendEvent("exited", { exitCode: code ?? 0 });
      this.conn.sendEvent("terminated");
    });

    // If attaching the debugger fails, the JVM is already running (and
    // suspended); kill it so a failed launch does not leak a frozen process.
    let client: JdwpClient;
    try {
      client = await JdwpClient.connect("127.0.0.1", launched.port);
    } catch (e) {
      launched.process.kill();
      throw e;
    }
    client.onEvent(data => this.onJdwpEvent(data));
    this.client = client;

    // The VM started suspended (suspend=y); bind any breakpoints set before the
    // client connected, then wait for configurationDone to resume.
    for (const entry of this.breakpoints.values()) await this.bindSource(entry);

    // stopOnEntry: arm a one-shot breakpoint on main(). The class may already be
    // loaded at this point; if not, a ClassPrepare arms it when it loads.
    if (this.stopOnEntry && this.mainSignature) {
      const classes = await classesBySignature(client, this.mainSignature);
      if (classes.length > 0) await this.setEntryBreakpoint(this.mainSignature);
      else await this.ensureClassPrepare(mainClass);
    }
  }

  private async onConfigurationDone(): Promise<void> {
    if (this.client) await vmResume(this.client);
  }

  private async onSetBreakpoints(
    args: SetBreakpointsArguments,
  ): Promise<{ breakpoints: Breakpoint[] }> {
    const path = args.source.path;
    if (!path) return { breakpoints: [] };
    const lines = args.breakpoints?.map(b => b.line) ?? args.lines ?? [];

    // Replace any previous breakpoints for this source.
    const prev = this.breakpoints.get(path);
    if (prev && this.client) {
      for (const requestId of prev.requestIds) {
        await eventRequestClear(this.client, EventKind.BREAKPOINT, requestId).catch(() => {});
      }
    }

    const { fqcn, signature } = classSignatureForSource(path);
    const entry: SourceBreakpoints = {
      fqcn,
      signature,
      requested: lines.map(line => ({ line, id: this.nextBpId++, bound: false })),
      requestIds: [],
    };
    this.breakpoints.set(path, entry);

    if (this.client) await this.bindSource(entry);
    return {
      breakpoints: entry.requested.map(r => ({ id: r.id, verified: r.bound, line: r.line })),
    };
  }

  // Resolve each requested line in a source to a JDWP breakpoint. If the class
  // is not loaded yet, register a ClassPrepare request so binding happens when
  // it loads (onJdwpEvent handles the event).
  // Register a ClassPrepare request for a fully-qualified class name once, so
  // we get notified (and can bind/arm) when that class loads.
  private async ensureClassPrepare(fqcn: string): Promise<void> {
    if (this.classPrepared.has(fqcn)) return;
    this.classPrepared.add(fqcn);
    await eventRequestSet(this.jdwp(), EventKind.CLASS_PREPARE, SuspendPolicy.ALL, [
      { kind: 5, pattern: fqcn },
    ]);
  }

  private async bindSource(entry: SourceBreakpoints): Promise<void> {
    const client = this.jdwp();
    const classes = await classesBySignature(client, entry.signature);
    if (classes.length === 0) {
      await this.ensureClassPrepare(entry.fqcn);
      return;
    }
    const classId = classes[0].typeId;
    const methods = await this.classMethods(classId);
    const methodLines = await Promise.all(
      methods.map(async m => ({
        methodId: m.methodId,
        lines: await methodLineTable(client, classId, m.methodId)
          .then(lt => lt.lines)
          .catch(() => []),
      })),
    );
    for (const bp of entry.requested) {
      if (bp.bound) continue;
      const loc = resolveLine(methodLines, bp.line);
      if (!loc) continue;
      const requestId = await eventRequestSet(client, EventKind.BREAKPOINT, SuspendPolicy.ALL, [
        {
          kind: 7,
          location: { typeTag: TypeTag.CLASS, classId, methodId: loc.methodId, index: loc.index },
        },
      ]);
      entry.requestIds.push(requestId);
      this.bpByRequest.set(requestId, bp.id);
      bp.bound = true;
      bp.line = loc.line;
    }
  }

  // Arm a one-shot breakpoint at main()'s first line (a Count:1 modifier makes
  // it self-clearing). The breakpoint event reports reason "entry".
  private async setEntryBreakpoint(signature: string): Promise<void> {
    if (this.entryRequestId !== undefined) return;
    const client = this.jdwp();
    const classes = await classesBySignature(client, signature);
    if (classes.length === 0) return;
    const classId = classes[0].typeId;
    const main = (await this.classMethods(classId)).find(m => m.name === "main");
    if (!main) return;
    const lt = await methodLineTable(client, classId, main.methodId).catch(() => null);
    const index =
      lt && lt.lines.length > 0
        ? lt.lines.reduce((a, b) => (b.lineCodeIndex < a.lineCodeIndex ? b : a)).lineCodeIndex
        : 0n;
    this.entryRequestId = await eventRequestSet(client, EventKind.BREAKPOINT, SuspendPolicy.ALL, [
      { kind: 7, location: { typeTag: TypeTag.CLASS, classId, methodId: main.methodId, index } },
      { kind: 1, count: 1 }, // one-shot
    ]);
  }

  private async onThreads(): Promise<{ threads: { id: number; name: string }[] }> {
    const client = this.jdwp();
    const ids = await allThreads(client);
    const threads = await Promise.all(
      ids.map(async jid => ({ id: this.threadIds.dap(jid), name: await threadName(client, jid) })),
    );
    return { threads };
  }

  private async onStackTrace(
    args: StackTraceArguments,
  ): Promise<{ stackFrames: StackFrame[]; totalFrames: number }> {
    const client = this.jdwp();
    const jid = this.threadIds.jdwp(args.threadId);
    const frames = await threadFrames(client, jid);
    const stackFrames: StackFrame[] = [];
    for (const f of frames) {
      const id = this.nextFrameId++;
      this.frames.set(id, { threadId: jid, frameId: f.frameId, location: f.location });
      const fqcn = signatureToType(await this.classSignature(f.location.classId));
      const methodName = await this.methodName(f.location.classId, f.location.methodId);
      const line = await this.lineForLocation(f.location);
      const path = sourcePathForClass(this.config, fqcn);
      stackFrames.push({
        id,
        name: `${fqcn}.${methodName}`,
        source: path ? { name: basename(path), path } : undefined,
        line,
        column: 0,
      });
    }
    return { stackFrames, totalFrames: stackFrames.length };
  }

  private onScopes(args: ScopesArguments): { scopes: Scope[] } {
    const ref = this.nextVarRef++;
    this.varNodes.set(ref, { frameId: args.frameId });
    return { scopes: [{ name: "Locals", variablesReference: ref, expensive: false }] };
  }

  private async onVariables(args: VariablesArguments): Promise<{ variables: Variable[] }> {
    const node = this.varNodes.get(args.variablesReference);
    const frame = node && this.frames.get(node.frameId);
    if (!frame) return { variables: [] };
    const client = this.jdwp();
    const slots = await methodVariableTable(
      client,
      frame.location.classId,
      frame.location.methodId,
    ).catch(() => []);
    const visible = slots.filter(
      s =>
        frame.location.index >= s.codeIndex &&
        frame.location.index < s.codeIndex + BigInt(s.length),
    );
    if (visible.length === 0) return { variables: [] };
    const values = await stackFrameGetValues(
      client,
      frame.threadId,
      frame.frameId,
      visible.map(s => ({ slot: s.slot, sigByte: signatureTagByte(s.signature) })),
    );
    const variables: Variable[] = [];
    for (let i = 0; i < visible.length; i++) {
      variables.push({
        name: visible[i].name,
        type: signatureToType(visible[i].signature),
        value: await this.renderValue(values[i], visible[i].signature),
        variablesReference: 0,
      });
    }
    return { variables };
  }

  // Resume is whole-VM (breakpoints suspend all threads), so the request's
  // threadId is not needed.
  private async onContinue(): Promise<{ allThreadsContinued: boolean }> {
    this.clearStopState();
    await vmResume(this.jdwp());
    return { allThreadsContinued: true };
  }

  private async onStep(args: StepArguments, depth: number): Promise<void> {
    const client = this.jdwp();
    const jid = this.threadIds.jdwp(args.threadId);
    this.stepRequestId = await eventRequestSet(client, EventKind.SINGLE_STEP, SuspendPolicy.ALL, [
      { kind: 10, threadId: jid, size: StepSize.LINE, depth },
    ]);
    this.clearStopState();
    await vmResume(client);
  }

  private async onPause(args: ThreadArgument): Promise<void> {
    await vmSuspend(this.jdwp());
    this.conn.sendEvent("stopped", {
      reason: "pause",
      threadId: args.threadId,
      allThreadsStopped: true,
    });
  }

  private async onDisconnect(): Promise<void> {
    if (this.client) await vmExit(this.client, 0).catch(() => {});
    this.child?.kill();
    this.client?.close();
  }

  private onJdwpEvent(data: Buffer): void {
    // Runs on the JDWP stream's data callback: guard against a teardown race
    // (client gone) and a malformed composite, neither of which should throw
    // out of the event emitter.
    const client = this.client;
    if (!client) return;
    let composite: ReturnType<typeof decodeComposite>;
    try {
      composite = decodeComposite(data, client.idSizes);
    } catch {
      return; // an undecodable event is not fatal
    }
    for (const ev of composite.events) {
      switch (ev.kind) {
        case EventKind.BREAKPOINT: {
          const entry = ev.requestId === this.entryRequestId;
          if (entry) this.entryRequestId = undefined; // the Count:1 request is spent
          this.clearStopState();
          this.conn.sendEvent("stopped", {
            reason: entry ? "entry" : "breakpoint",
            threadId: this.threadIds.dap(ev.thread),
            allThreadsStopped: true,
          });
          break;
        }
        case EventKind.SINGLE_STEP:
          if (this.stepRequestId !== undefined) {
            void eventRequestClear(client, EventKind.SINGLE_STEP, this.stepRequestId).catch(
              () => {},
            );
            this.stepRequestId = undefined;
          }
          this.clearStopState();
          this.conn.sendEvent("stopped", {
            reason: "step",
            threadId: this.threadIds.dap(ev.thread),
            allThreadsStopped: true,
          });
          break;
        case EventKind.CLASS_PREPARE:
          void this.onClassPrepare(ev.signature);
          break;
        case EventKind.THREAD_START:
          this.conn.sendEvent("thread", {
            reason: "started",
            threadId: this.threadIds.dap(ev.thread),
          });
          break;
        case EventKind.THREAD_DEATH:
          this.conn.sendEvent("thread", {
            reason: "exited",
            threadId: this.threadIds.dap(ev.thread),
          });
          break;
        case EventKind.VM_DEATH:
          this.conn.sendEvent("terminated");
          break;
      }
    }
  }

  // A class we deferred breakpoints on has loaded: bind them and resume (the
  // ClassPrepare request suspended all threads so binding races nothing).
  private async onClassPrepare(signature: string): Promise<void> {
    const client = this.jdwp();
    for (const entry of this.breakpoints.values()) {
      if (entry.signature !== signature) continue;
      await this.bindSource(entry);
      for (const bp of entry.requested) {
        if (bp.bound) {
          this.conn.sendEvent("breakpoint", {
            reason: "changed",
            breakpoint: { id: bp.id, verified: true, line: bp.line },
          });
        }
      }
    }
    if (this.stopOnEntry && signature === this.mainSignature) {
      await this.setEntryBreakpoint(signature);
    }
    await vmResume(client);
  }

  private clearStopState(): void {
    this.frames.clear();
    this.varNodes.clear();
    this.nextFrameId = 1;
    this.nextVarRef = 1;
  }

  private async classSignature(classId: bigint): Promise<string> {
    let sig = this.sigCache.get(classId);
    if (sig === undefined) {
      sig = await referenceTypeSignature(this.jdwp(), classId);
      this.sigCache.set(classId, sig);
    }
    return sig;
  }

  private async classMethods(classId: bigint): Promise<MethodInfo[]> {
    let methods = this.methodsCache.get(classId);
    if (methods === undefined) {
      methods = await referenceTypeMethods(this.jdwp(), classId);
      this.methodsCache.set(classId, methods);
    }
    return methods;
  }

  private async methodName(classId: bigint, methodId: bigint): Promise<string> {
    const methods = await this.classMethods(classId);
    return methods.find(m => m.methodId === methodId)?.name ?? "<unknown>";
  }

  private async lineForLocation(loc: Location): Promise<number> {
    const lt = await methodLineTable(this.jdwp(), loc.classId, loc.methodId).catch(() => null);
    if (!lt) return 0;
    let line = 0;
    let bestIndex = -1n;
    for (const e of lt.lines) {
      if (e.lineCodeIndex <= loc.index && e.lineCodeIndex > bestIndex) {
        bestIndex = e.lineCodeIndex;
        line = e.lineNumber;
      }
    }
    return line;
  }

  private async renderValue(value: JdwpValue, signature: string): Promise<string> {
    if (value.kind === "primitive") {
      if (signature === "C") return `'${String.fromCharCode(Number(value.value))}'`;
      return String(value.value);
    }
    if (value.objectId === 0n) return "null";
    if (value.tag === Tag.STRING) {
      return JSON.stringify(await stringValue(this.jdwp(), value.objectId));
    }
    return `${signatureToType(signature)}@${value.objectId.toString(16)}`;
  }
}

// Derive the JDWP class signature for a source file from its `package`
// declaration and file name (the public type matches the file name).
export function classSignatureForSource(path: string): { fqcn: string; signature: string } {
  let pkg = "";
  try {
    const m = /^\s*package\s+([\w.]+)\s*;/m.exec(readFileSync(path, "utf8"));
    if (m) pkg = m[1];
  } catch {
    // unreadable source: treat as the default package
  }
  const typeName = basename(path).replace(/\.java$/, "");
  const fqcn = pkg ? `${pkg}.${typeName}` : typeName;
  return { fqcn, signature: `L${fqcn.replaceAll(".", "/")};` };
}

// Find the .java for a class under the configured source paths (top-level type
// name; inner classes share the enclosing file).
export function sourcePathForClass(config: CappuConfig, fqcn: string): string | undefined {
  const top = fqcn.split("$")[0];
  const rel = `${top.replaceAll(".", "/")}.java`;
  for (const sp of config.compilerOptions.sourcePaths) {
    const p = resolveConfigPath(config, join(sp, rel));
    if (existsSync(p)) return p;
  }
  return undefined;
}
