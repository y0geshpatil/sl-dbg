# sl-dbg — AI Agent Integration Guide

This document explains how an AI coding agent should drive `sl-dbg` to debug software autonomously.

## Why sl-dbg Fits AI Agents

| Traditional debugger | sl-dbg |
|---|---|
| Interactive REPL | One-shot commands |
| Plain text output | Structured JSON |
| Hold-the-connection protocol | Stateless invocations |
| GUI-bound | Pure CLI |
| Requires protocol code in client | Just shell + JSON parsing |

The agent's "thoughts" map directly to shell calls.

## The Universal Agent Loop

```
WHILE session.state != "exited":
    1.  observe = `sl-dbg snapshot` || `sl-dbg state`
    2.  reasoning = LLM("Given this state, what next?", observe, goal)
    3.  action = reasoning.tool_call
    4.  result = sl-dbg <action>...
    5.  store (observe, action, result) in scratchpad
```

Every iteration is one tool call. The LLM decides; sl-dbg executes; the JSON response feeds the next decision.

## Recommended Tool Schemas

If you're wiring sl-dbg into an LLM via function calling, expose these tools:

```jsonc
{
  "name": "debug_start",
  "description": "Start a debug session for a program.",
  "input_schema": {
    "type": "object",
    "properties": {
      "lang": {"type":"string","enum":["python","java","go","node","cpp","dotnet","rust"]},
      "program": {"type":"string"},
      "args": {"type":"array","items":{"type":"string"}},
      "stop_on_entry": {"type":"boolean"}
    },
    "required": ["lang","program"]
  }
}
```

Map the rest similarly:

| Tool | sl-dbg command |
|---|---|
| `debug_attach` | `sl-dbg attach` |
| `debug_break` | `sl-dbg break <loc>` |
| `debug_unbreak` | `sl-dbg unbreak <id>` |
| `debug_continue` | `sl-dbg continue` |
| `debug_step_over` | `sl-dbg next` |
| `debug_step_into` | `sl-dbg step` |
| `debug_step_out` | `sl-dbg finish` |
| `debug_snapshot` | `sl-dbg snapshot` |
| `debug_eval` | `sl-dbg eval <expr>` |
| `debug_set_var` | `sl-dbg set <name> <value>` |
| `debug_stop` | `sl-dbg stop` |

When using MCP, `sl-dbg mcp` exposes these as native JSON-RPC 2.0 tools — zero glue. Every tool accepts an optional `session` string; omit it and the daemon's default (newest started) is used.

### MCP composites (highly recommended for agents)

The MCP server ships pre-baked composites that collapse common multi-step flows into a single tool call. Prefer these over raw `debug_start → debug_break → debug_continue → debug_locals` chains; they're cheaper for the LLM and avoid race conditions.

| Tool | What it does in one call |
|---|---|
| `debug_run_until_break` | start (or attach) → set breakpoint → continue → return pause snapshot |
| `debug_inspect_at` | set BP → run to it → snapshot locals + evaluate a list of expressions → continue. Short-circuits with `BREAKPOINT_UNVERIFIED` (line has no executable code / class not loaded) or `INSPECT_NOT_PAUSED` (program exited / timed out before hitting the BP) — in either case `locals`/`evaluations` are omitted rather than returning misleading "no frames in current stack" errors. |
| `debug_explain_pause` | return a one-paragraph English summary of where the program paused, what changed, which watches moved. When the session has exited/terminated/is still running, returns the matching one-liner instead of fabricating a pause — agents won't try follow-up ops on a dead session. |
| `debug_snapshot_compact` | snapshot capped at N vars per scope; token-budget aware |

## Patterns

### Pattern: Bisect a Bug

```
1. Run program → it crashes at line 88 with NullPointerException.
2. break-ex NullPointerException
3. continue → paused at line 88 with NPE
4. snapshot → see which variable is null
5. stack → find where the variable was last assigned
6. break <upstream_line> --if "var == null"
7. unbreak-ex
8. restart
9. continue → paused at the assignment when null
10. snapshot → root cause identified
```

### Pattern: Hypothesis-Driven Probing

```
hypothesis = "the cache is being invalidated too aggressively"

1. break-fn Cache.invalidate
2. continue
3. ON each hit:
     snapshot → check timestamp, key, caller
     evaluate "self.invalidation_count" → see frequency
     if "interesting":  break-fn Cache.invalidate --if <refined>
     continue
4. After enough samples, LLM concludes / refines hypothesis
```

### Pattern: Data-Flow Tracing

```
1. break <line where bad value first observed>
2. continue → paused
3. snapshot
4. watch <expr that holds the value>
5. step back (or restart with watch) and step through forward
6. snapshot at each step → trace the value's origin
```

## Best Practices for Agents

1. **Always start with `snapshot`** at a pause. It returns the maximum useful state in one call (stack + all locals across all frames + watches + exception).

2. **Use conditional breakpoints aggressively.** Don't single-step through 1000 iterations. `break L --if "i > 950"` skips ahead.

3. **Logpoints over print debugging.** `break L --log "x={x} y={y}"` doesn't pause — emits structured output the agent can drain via `sl-dbg output`.

4. **Set `--timeout` on blocking commands.** `sl-dbg continue --timeout 10s` won't hang the agent if the target loops. On timeout the agent can decide to `pause`.

5. **Use `--read-only` for production attach.** Forbids accidentally calling functions with side effects via `eval`.

6. **Cache reasoning between calls.** Sessions are stateful on the sl-dbg side; the agent only needs to track its hypotheses.

7. **Drop breakpoints when done.** Stale BPs slow execution.

8. **Use `sl-dbg events --follow` in a parallel channel** if the agent wants to watch async events (log output, child threads).

## Sample Agent Prompt (System Message Excerpt)

```
You are a debugging agent with access to `sl-dbg`, a CLI debugger.
- Each call MUST be a single shell command starting with `sl-dbg`.
- Output is JSON; parse `data` for results, `error` for failures.
- Start with `sl-dbg start --lang <L> --program <P>` or `sl-dbg attach`.
- At every pause, call `sl-dbg snapshot` before reasoning.
- Set conditional breakpoints when iterating; avoid single-step in loops.
- When done, call `sl-dbg stop`.
- If the user asked you to investigate a bug, end by summarizing root cause
  with file:line citations and a proposed fix.
```

## Example Session (Python)

```bash
$ sl-dbg start --lang python --program ./buggy.py --stop-on-entry
{"ok":true,"data":{"session":"a1b2","state":"paused","reason":"entry",
  "location":{"file":"buggy.py","line":1}}}

$ sl-dbg break buggy.py:42 --if "user is None"
{"ok":true,"data":{"breakpoint":{"id":1,"verified":true}}}

$ sl-dbg continue
{"ok":true,"data":{"state":"paused","reason":"breakpoint",
  "location":{"file":"buggy.py","line":42,"function":"login"},"hitBreakpoint":1}}

$ sl-dbg snapshot
{"ok":true,"data":{
  "state":"paused",
  "location":{"file":"buggy.py","line":42,"function":"login"},
  "stack":[
    {"id":1000,"name":"login","file":"buggy.py","line":42},
    {"id":1001,"name":"handle_request","file":"server.py","line":88}
  ],
  "locals":{
    "username":{"value":"'guest'","type":"str"},
    "user":{"value":"None","type":"NoneType"},
    "session_id":{"value":"'xyz'","type":"str"}
  },
  ...
}}

# LLM reasons: "user is None because get_user('guest') returned None.
#  Let me check why."

$ sl-dbg eval "User.objects.filter(username='guest').first()"
{"ok":true,"data":{"result":"None","type":"NoneType"}}

# LLM: "No user named 'guest'. The bug is missing handling for unknown users."

$ sl-dbg stop
{"ok":true,"data":{"session":"a1b2","state":"terminated"}}
```

## Anti-Patterns to Avoid

- **Don't single-step through long loops.** Use conditional breakpoints.
- **Don't ignore the `state` field.** If `state=="running"` you can't inspect.
- **Don't keep dozens of sessions open.** Stop ones you're done with.
- **Don't run `eval` with side-effecting code on production targets.** Use `--read-only` to enforce.

## Roadmap for Agent-Specific Features

Planned in Phase 5+:
- `sl-dbg trace --on-call <fn> --record state.jsonl` — record every call to a function with full state, for offline LLM analysis.
- `sl-dbg find-bug --hypothesis "..."` — guided exploration mode where sl-dbg suggests next probes.
- `sl-dbg replay state.jsonl` — replay a recorded trace.
