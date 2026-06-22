# debug-app

A one-file program (a loop with a couple of locals) debugged over the Debug
Adapter Protocol. `cappu dap` compiles the project with debug info, launches its
`mainClass` under JDWP, and bridges breakpoints, stepping, stack frames and
local variables to any DAP client:

```sh
cappu dap                     # speaks DAP over stdio (or --port <n> for TCP)
```

Point an editor at it with a launch config like:

```json
{
  "type": "cappu-dap",
  "request": "launch",
  "name": "Debug debug-app",
  "mainClass": "example.App"
}
```

A client then drives the session: `initialize` -> `launch` -> `setBreakpoints`
(e.g. line 8 of `App.java`) -> `configurationDone`; execution stops on the
breakpoint, where the stack, the locals (`i`, `squared`, `sum`) and stepping are
all available.
