# sl-dbg — Command Reference

All commands accept `--json` (default), `--pretty`, `--quiet`, `--session <id>`, `--timeout <duration>`.

Default output is JSON. Errors go to stderr; structured payload on stdout.

## Global

### `sl-dbg version`
```json
{"version":"0.1.0","commit":"abc1234","date":"2026-06-26T13:00:00Z"}
```

### `sl-dbg help [command]`
Standard cobra help.

### `sl-dbg adapters`
List registered language adapters and detection status.
```json
{"ok":true,"data":{"adapters":[
  {"lang":"python","installed":true,"version":"1.8.0","path":"/usr/bin/python -m debugpy.adapter"},
  {"lang":"java","installed":false,"hint":"sl-dbg install-adapter java"}
]}}
```

### `sl-dbg install-adapter <lang>`
Bootstrap a missing adapter (pip install, go install, download jar, …).

### `sl-dbg daemon [start|stop|status|logs]`
Direct daemon control. Normally implicit.

### `sl-dbg config get|set <key> [value]`
Read/write user config.

### `sl-dbg mcp`
Run as an MCP server over stdio. Exposes every other command as an MCP tool. (Planned, Phase 5.)

---

## Session Lifecycle

### `sl-dbg start --lang <L> --program <path> [opts]`
Launch a new debug session by starting the program.

Flags:
- `--lang` — required (python, java, go, cpp, dotnet, node, rust)
- `--program` — path to entrypoint
- `--args "<args>"` — arguments passed to the program
- `--cwd <dir>` — working directory
- `--env KEY=val` (repeatable) — environment
- `--stop-on-entry` — pause at first line
- `--source-root <dir>` (repeatable)
- `--read-only` — forbid state mutation
- `--make-default` — set as default session (default true if first)

Response:
```json
{"ok":true,"data":{"session":"a1b2","state":"paused","reason":"entry",
  "location":{"file":"app.py","line":1}}}
```

### `sl-dbg attach --lang <L> [--host H --port P | --pid N] [opts]`
Attach to an already-running process.
```bash
sl-dbg attach --lang java --host localhost --port 5005
sl-dbg attach --lang python --pid 12345
```

### `sl-dbg listen --lang <L> --port <P>`
Listen for a target to connect (reverse attach). Useful for firewalled targets.

### `sl-dbg sessions`
List all active sessions.
```json
{"ok":true,"data":{"sessions":[
  {"id":"a1b2","lang":"python","state":"paused","program":"app.py","default":true},
  {"id":"c3d4","lang":"java","state":"running","attached":"svc.prod:5005"}
]}}
```

### `sl-dbg use <session-id>`
Set the default session for subsequent commands.

### `sl-dbg stop [<id>]`
Disconnect and terminate. With `--detach`, leaves target running (attach mode only).

### `sl-dbg restart [<id>]`
Restart the target.

### `sl-dbg state [<id>]`
Non-blocking: returns current session state.
```json
{"ok":true,"data":{"state":"paused","reason":"breakpoint",
  "location":{"file":"app.py","line":42,"function":"login"},
  "thread":1}}
```

---

## Breakpoints

### `sl-dbg break <location> [opts]`
Add a line breakpoint. Location: `file:line` or `Class:line` or `package.Class:line`.

Flags:
- `--if <expr>` — conditional
- `--hit <N>` — break on Nth hit
- `--log "<msg>"` — logpoint (no pause; prints msg with `{var}` interpolation)
- `--once` — auto-remove after first hit

```json
{"ok":true,"data":{"breakpoint":{"id":1,"verified":true,"file":"app.py","line":42}}}
```

### `sl-dbg break-fn <function>`
Function-entry breakpoint.
```bash
sl-dbg break-fn com.example.UserService.login
sl-dbg break-fn app.process_order
```

### `sl-dbg break-ex <ExceptionType> [--uncaught | --caught | --all]`
Exception breakpoint.
```bash
sl-dbg break-ex NullPointerException --uncaught
sl-dbg break-ex ValueError --all
```

### `sl-dbg watch <expression>`
Data breakpoint — break when expression value changes.
```bash
sl-dbg watch user.balance
```

### `sl-dbg breaks`
List all breakpoints.

### `sl-dbg unbreak <id> [...]`
Remove one or more. `--all` removes all.

### `sl-dbg enable <id>` / `sl-dbg disable <id>`
Toggle without removing.

---

## Execution Control

All execution commands block until the target pauses again (or `--timeout` fires).

### `sl-dbg run`
Run from start (after `start --stop-on-entry`).

### `sl-dbg continue` (alias: `c`)
Resume until next pause.

### `sl-dbg step` (alias: `si`)
Step into.

### `sl-dbg next` (alias: `n`)
Step over.

### `sl-dbg finish` (alias: `out`)
Step out of current frame.

### `sl-dbg until <line>`
Continue until reaching line (auto-removes after).

### `sl-dbg goto <line>`
Jump to line without executing intervening code (where supported).

### `sl-dbg pause`
Pause a running target.

### `sl-dbg back` / `sl-dbg reverse`
Step backward / reverse-run (where adapter supports it).

Common response:
```json
{"ok":true,"data":{"state":"paused","reason":"breakpoint",
  "location":{"file":"app.py","line":42,"function":"login"},
  "thread":1,"hitBreakpoint":1}}
```

---

## Inspection

### `sl-dbg stack [--thread <id>] [--limit N]`
Call stack.
```json
{"ok":true,"data":{"frames":[
  {"id":1000,"name":"login","file":"app.py","line":42},
  {"id":1001,"name":"main","file":"app.py","line":88}
]}}
```

### `sl-dbg threads`
List all threads.

### `sl-dbg locals [--frame N]`
Local variables. Returns recursive structure or shallow with `objectId` refs.
```json
{"ok":true,"data":{"locals":{
  "user":{"type":"User","value":"<User id=5>","ref":1002,"expandable":true},
  "items":{"type":"list","value":"[1, 2, 3]","ref":1003}
}}}
```

### `sl-dbg globals [--frame N]`
Global / module-level variables.

### `sl-dbg fields <ref>`
Expand a previously returned object reference.

### `sl-dbg eval <expression> [--frame N]`
Evaluate. May have side effects unless `--read-only` is set on the session.
```json
{"ok":true,"data":{"result":"7","type":"int"}}
```

### `sl-dbg set <name> <value> [--frame N]`
Modify a variable.

### `sl-dbg watch-expr <expression>` / `sl-dbg watches` / `sl-dbg unwatch-expr <id>`
Persistent watch expressions, evaluated and returned with every pause.

### `sl-dbg exception`
Details about the current exception (only valid when paused on exception).

### `sl-dbg source [--frame N] [--around L]`
Source code around current line.

### `sl-dbg modules`
Loaded modules / shared libraries / JARs.

### `sl-dbg snapshot`
Full state dump: stack + locals for every frame + globals + watches. Best command for an agent to "see everything" at a pause point.
```json
{"ok":true,"data":{
  "state":"paused","location":{...},
  "threads":[...],"frames":[...],
  "locals":{...},"globals":{...},
  "watches":[...],"exception":null
}}
```

---

## Output & Events

### `sl-dbg output [--stdout | --stderr | --all] [--follow]`
Drain target's captured stdout/stderr.

### `sl-dbg events [--follow] [--since <ts>]`
Stream raw debug events (stopped, output, module, breakpoint, …) as JSON Lines.

### `sl-dbg logs`
Daemon log tail (for debugging sl-dbg itself).

---

## Memory & Disassembly (Phase 4+)

### `sl-dbg mem read <addr> <size>`
### `sl-dbg mem write <addr> <hex>`
### `sl-dbg disasm <addr> [--count N]`

---

## Output Envelope (Standard)

Every JSON response has this shape:

```json
{
  "ok": true,
  "data": { /* command-specific */ },
  "session": "a1b2",
  "state": "paused|running|exited|terminated",
  "ts": "2026-06-26T13:00:00Z"
}
```

Or on error:

```json
{
  "ok": false,
  "error": {
    "code": "STABLE_ERROR_CODE",
    "message": "human readable",
    "details": {},
    "hint": "what to try next"
  },
  "session": "a1b2",
  "ts": "..."
}
```

## Exit Codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Debugger error (BP not verified, target crashed) |
| 2 | Usage error |
| 3 | IPC error (daemon down) |
| 4 | Adapter error |
| 130 | Interrupted |
