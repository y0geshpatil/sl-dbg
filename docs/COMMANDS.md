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
When the session has terminated, `location`, `thread`, and the pause `reason` are intentionally omitted — only the terminal facts survive:
```json
{"ok":true,"data":{"state":"exited","reason":"exited","exitCode":0}}
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

## Error Code Taxonomy

`error.code` values are stable identifiers tools can switch on. The set is intentionally small and additive — see [issues](https://github.com/y0geshpatil/sl-dbg/issues) tagged `area/proto` for proposed additions.

| Code | When | Hint |
|---|---|---|
| `USAGE_ERROR` | Bad CLI / JSON args (empty expression, missing file, line past EOF) | Validate input |
| `SESSION_NOT_FOUND` | Session id unknown / already stopped | Call `debug_sessions` |
| `READ_ONLY_MODE` | Mutating call against `--read-only` session | Start a new mutating session |
| `LAUNCH_FAILED` | Program died before any user command could run; **only when exit code ≠ 0**. Clean `exit 0` returns `state=exited` as success | Inspect `stderr`/`stdout` tails |
| `ADAPTER_FAILED` | Generic uncategorised adapter error | Last resort |
| `MISSING_DEBUG_INFO` | `AbsentInformationException` — class compiled without `-g` | `javac -g` |
| `CLASS_NOT_LOADED` | BP at a class the JVM hasn't loaded | Set BP earlier |
| `STALE_FRAME` | Frame invalidated after resume | Re-fetch stack |
| `VM_DISCONNECTED` | JVM terminated | New session |
| `EVAL_NO_THIS` | `this` in static/native frame | Use `Class.field` |
| `EVAL_NAME_UNKNOWN` | Identifier not in scope | Qualify with class |
| `EVAL_SYNTAX_ERROR` | Expression parse error | Fix syntax |
| `EVAL_RUNTIME_EXCEPTION` | Expression evaluated to a runtime exception (NPE, divide-by-zero, ClassCast) | Guard the receiver |
| `BREAKPOINT_UNVERIFIED` | `debug_inspect_at`: BP didn't bind | Pick an executable body line |
| `INSPECT_NOT_PAUSED` | `debug_inspect_at`: continue ended in exit/timeout, not the requested BP | Confirm reachability |
| `PAUSE_TIMEOUT` | Adapter accepted pause but did not stop within 10s | Set a line BP and continue instead |
| `TIMEOUT` | Operation exceeded its `--timeout` | Raise `--timeout` |
| `PROGRAM_NOT_ALLOWED` | `debug_start`: program path not in `SL_DBG_ALLOW_PROGRAM` allowlist | Add program to allowlist or unset env var |
| `SOURCE_PATH_DENIED` | `debug_source`: file outside `SL_DBG_ALLOW_SOURCE_ROOT` and not under the session's own roots | Add parent dir to allowlist or include in `sourceRoots` |
| `EVAL_DENIED` | `debug_eval`: expression matched `SL_DBG_DENY_EVAL_PATTERNS` | Rephrase, or set `SL_DBG_DENY_EVAL_PATTERNS=-` |
| `RESOURCE_EXHAUSTED` | `debug_start`/`debug_attach`: daemon at `SL_DBG_MAX_SESSIONS` cap | Stop another session or raise the cap |

### Security policy (env vars)

The daemon reads these at startup. All are optional; defaults preserve legacy behavior. See [docs/SECURITY.md](SECURITY.md) for the threat model.

| Env var | Purpose | Issue |
|---|---|---|
| `SL_DBG_ALLOW_PROGRAM` | Colon-separated glob allowlist for `start --program` paths. Globs match either the full path or basename. | #21 |
| `SL_DBG_ALLOW_SOURCE_ROOT` | Colon-separated absolute-path roots that `debug_source` may read. The session's own `sourceRoots`/`cwd`/program dir are always trusted. | #18 |
| `SL_DBG_MAX_SESSIONS` | Cap on concurrent sessions in the daemon. `0` (default) = unlimited. | #22 |
| `SL_DBG_AUDIT_LOG` | Path. When set, every `start`/`attach`/`eval`/`set` is appended as one NDJSON line (`ts`, `event`, `session`, `args`). | #23 |
| `SL_DBG_DENY_EVAL_PATTERNS` | Colon-separated substring deny list for `debug_eval` expressions. Default hardcoded list blocks the obvious Java side-effect classes (`FileOutputStream`, `Runtime.getRuntime`, …). Set to `-` to disable. | #19 |

## Schema / Versioning Policy

Every JSON response includes a `"schema": "1"` marker. The contract for `schema:"1"`:

1. **Additive only.** New optional fields may appear at any time; existing field names, JSON types, and `error.code` values will not change meaning.
2. **Removed fields are NOT re-introduced** with a different meaning. If a field is dropped (because the data is no longer accurate, e.g. `location` on an `exited` session), it stays dropped.
3. **`schema` will bump to `"2"`** only for an intentional breaking change announced ahead in [ROADMAP.md](ROADMAP.md). Tools should pin to a schema and warn on unknown values.

## Adapter Capability Notes

| Adapter | Restart supported | Notes |
|---|---|---|
| Java (java-debug) | ❌ no | JDI/JDWP cannot hot-restart a JVM. Use `sl-dbg stop` then `sl-dbg start` with the same args. `debug_restart` returns `UNSUPPORTED_FEATURE`. |
| Python (debugpy) | ✅ yes | |
| Go (delve) | ✅ yes | |
