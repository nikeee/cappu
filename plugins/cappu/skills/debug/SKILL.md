---
name: debug
description: "Use when you need to debug a running Java program in a cappu project with breakpoints, stepping and local variable inspection by driving cappu dap over the Debug Adapter Protocol, or when configuring debug launches (mainClass, args, vmArgs, env, cwd, stopOnEntry, assertions). Not for compile errors or failing tests without a debugger - see build-test."
---

# Debug a cappu project over DAP

`cappu dap` is a Debug Adapter Protocol server: it compiles the project with
debug info, launches the main class under JDWP itself, and bridges breakpoints,
stepping, stack frames and locals. Launch only, no attach. Advertised
capabilities are just `configurationDone` and `terminate`: no conditional
breakpoints, no `evaluate` or watch expressions, no `setVariable`, no hit
counts. You read state through `variables`. No editor ships a `cappu-dap`
debugger type yet, so from an agent you drive the protocol yourself; a runnable
Node client is in `references/dap-client.md`.

## When to use it

Reach for DAP when the question is "what are the values at line N" and a
`System.out.println` round-trip through `cappu run` would be slower or would
touch code you must not edit. For a crash with a stack trace, `cappu run` and
the trace are usually enough.

## Procedure

1. **Know the main class.** `compilerOptions.mainClass`, or exactly one `main`
   in the sources; otherwise pass `mainClass` in the launch request.
2. **Start the adapter** from the project root: `cappu dap` (stdio,
   `Content-Length`-framed JSON) or `cappu dap -p 4711` (TCP).
3. **Drive the session** in this order (the order cappu's own tests use):

   | # | Request | Notes |
   |---|---|---|
   | 1 | `initialize` `{ adapterID: "cappu" }` | then wait for the `initialized` event |
   | 2 | `launch` `{ mainClass?, args?, vmArgs?, classPath?, env?, cwd?, stopOnEntry? }` | compiles and starts the JVM suspended |
   | 3 | `setBreakpoints` `{ source: { path: "/abs/App.java" }, breakpoints: [{ line: 8 }] }` | absolute path; one request per file, it replaces that file's breakpoints |
   | 4 | `configurationDone` | the program runs |
   | 5 | wait for `stopped` | `body.reason` is `breakpoint`, `entry` or `step`; `body.threadId` |
   | 6 | `threads`, `stackTrace { threadId }`, `scopes { frameId }`, `variables { variablesReference }` | frame `name` is `pkg.Class.method`, `line` is 1-based; locals are `name`/`value` strings, objects render as `java.lang.String[]@<id>` |
   | 7 | `next { threadId }` or `continue { threadId }` | `next` yields another `stopped` with reason `step` |
   | 8 | wait for `terminated`, then `disconnect`, then close stdin | program stdout arrives as `output` events during the run |

4. **Stop cleanly.** The JVM exits when the program ends; after `disconnect`
   close the adapter's stdin so the `cappu dap` process exits.

## Launch request fields

| Field | Default | Meaning |
|---|---|---|
| `mainClass` | `compilerOptions.mainClass` or the single `main` found | class to launch |
| `args` | `[]` | program arguments |
| `vmArgs` | `[]` | JVM flags, e.g. `["-Xmx256m", "-Dkey=value"]`; a `-da` here overrides `enableAssertions` |
| `classPath` | none | extra classpath entries on top of the project's |
| `env` | inherited | extra environment for the debuggee |
| `cwd` | project root | working directory of the debuggee |
| `stopOnEntry` | `false` | stop on the first line of `main` before any user code |

Project-wide assertions: `{ "dapOptions": { "enableAssertions": true } }` in
`cappu.json`.

## Troubleshooting

- **No `stopped` event**: the breakpoint path was relative, or the line has no
  executable code (declaration-only lines, blank lines, closing braces). Use the
  first statement line inside the block.
- **`launch` fails**: compile error (fix with `cappu check` or `cappu compile`)
  or no unambiguous main class.
- **Values look wrong**: a breakpoint stops before the line executes; `sum` at a
  `sum += x` line is the value before the add.

## Gotchas checklist

- [ ] Breakpoints after `launch`, before `configurationDone`.
- [ ] Absolute `source.path`, executable line.
- [ ] No `evaluate`; inspect via `scopes` and `variables` only.
- [ ] Launch only; attaching to a running JVM is not possible.
- [ ] `disconnect`, then close stdin, so the adapter process exits.
