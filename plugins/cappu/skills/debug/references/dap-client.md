# Minimal DAP client for `cappu dap`

A dependency-free Node script (Node 20 or newer). Save it as `dap-client.mjs`
outside the project (a scratch directory is fine), then:

```sh
node dap-client.mjs <project-dir> </abs/path/to/File.java> <line> [<line> ...]
```

It spawns `cappu dap` in `<project-dir>`, launches the configured main class,
sets the breakpoints, prints the top frame and its locals at every stop, lets
the program continue, and exits when the program terminates. Program stdout is
echoed with a `[program]` prefix. Wrap it in `timeout 120 node ...` when running
unattended.

```js
// dap-client.mjs
import { spawn } from "node:child_process";

const [projectDir, file, ...lines] = process.argv.slice(2);
if (!projectDir || !file || lines.length === 0) {
  console.error("usage: node dap-client.mjs <project-dir> </abs/path/File.java> <line> [<line> ...]");
  process.exit(2);
}

const child = spawn("cappu", ["dap"], { cwd: projectDir, stdio: ["pipe", "pipe", "inherit"] });

let buf = Buffer.alloc(0);
let seq = 1;
const pending = new Map(); // request seq -> resolve
const eventQueue = []; // events that arrived before anyone waited for them
const eventWaiters = []; // { event, resolve }

child.stdout.on("data", chunk => {
  buf = Buffer.concat([buf, chunk]);
  for (;;) {
    const sep = buf.indexOf("\r\n\r\n");
    if (sep < 0) break;
    const len = Number(/Content-Length:\s*(\d+)/i.exec(buf.toString("ascii", 0, sep))[1]);
    if (buf.length < sep + 4 + len) break;
    const msg = JSON.parse(buf.toString("utf8", sep + 4, sep + 4 + len));
    buf = buf.subarray(sep + 4 + len);
    if (msg.type === "response") {
      pending.get(msg.request_seq)?.(msg);
      pending.delete(msg.request_seq);
    } else if (msg.type === "event") {
      if (msg.event === "output") process.stdout.write(`[program] ${msg.body.output}`);
      const i = eventWaiters.findIndex(w => w.event === msg.event);
      if (i >= 0) eventWaiters.splice(i, 1)[0].resolve(msg);
      else eventQueue.push(msg);
    }
  }
});

function request(command, args) {
  const s = seq++;
  const body = JSON.stringify({ seq: s, type: "request", command, arguments: args });
  child.stdin.write(`Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`);
  return new Promise(resolve => pending.set(s, resolve));
}

function waitEvent(event) {
  const i = eventQueue.findIndex(e => e.event === event);
  if (i >= 0) return Promise.resolve(eventQueue.splice(i, 1)[0]);
  return new Promise(resolve => eventWaiters.push({ event, resolve }));
}

async function topFrameLocals(threadId) {
  const stack = await request("stackTrace", { threadId });
  const top = stack.body.stackFrames[0];
  const scopes = await request("scopes", { frameId: top.id });
  const vars = await request("variables", { variablesReference: scopes.body.scopes[0].variablesReference });
  return { frame: `${top.name}:${top.line}`, locals: Object.fromEntries(vars.body.variables.map(v => [v.name, v.value])) };
}

await request("initialize", { adapterID: "cappu" });
await waitEvent("initialized");
const launch = await request("launch", {}); // add mainClass, args, vmArgs, stopOnEntry here
if (!launch.success) {
  console.error("launch failed:", launch.message);
  child.kill();
  process.exit(1);
}
await request("setBreakpoints", { source: { path: file }, breakpoints: lines.map(l => ({ line: Number(l) })) });
await request("configurationDone");

const terminated = waitEvent("terminated");
for (;;) {
  const ev = await Promise.race([waitEvent("stopped"), terminated]);
  if (ev.event === "terminated") break;
  const { frame, locals } = await topFrameLocals(ev.body.threadId);
  console.log(`stopped (${ev.body.reason}) at ${frame}`, locals);
  await request("continue", { threadId: ev.body.threadId }); // or "next" to step one line
}

await request("disconnect");
child.stdin.end();
```

## Expected output on the cappu repo's `examples/debug-app`

```sh
node dap-client.mjs ./debug-app "$PWD/debug-app/src/main/java/example/App.java" 8
```

```
stopped (breakpoint) at example.App.main:8 { args: 'java.lang.String[]@...', sum: '0', i: '1', squared: '1' }
stopped (breakpoint) at example.App.main:8 { args: 'java.lang.String[]@...', sum: '1', i: '2', squared: '4' }
stopped (breakpoint) at example.App.main:8 { args: 'java.lang.String[]@...', sum: '5', i: '3', squared: '9' }
[program] sum=14
```

Line 8 is `sum += squared;`, so each stop shows `sum` before the add. Replace
`continue` with `next` to step; the following `stopped` has reason `step`.
Pass `{ stopOnEntry: true }` to `launch` to stop on the first line of `main`
(reason `entry`) without any breakpoint.
