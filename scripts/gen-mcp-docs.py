#!/usr/bin/env python3
"""Generate sl-dbg-site/mcp.md from a live `sl-dbg mcp` tools/list dump.

Usage:
    scripts/gen-mcp-docs.py [/path/to/sl-dbg-binary] > sl-dbg-site/mcp.md
"""
from __future__ import annotations

import json
import shutil
import subprocess
import sys
from datetime import datetime, timezone


def fetch_tools(binary: str) -> dict:
    init = {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2024-11-05", "capabilities": {},
                   "clientInfo": {"name": "gen-mcp-docs", "version": "1"}},
    }
    initd = {"jsonrpc": "2.0", "method": "notifications/initialized"}
    list_tools = {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}
    list_res = {"jsonrpc": "2.0", "id": 3, "method": "resources/list", "params": {}}
    list_prm = {"jsonrpc": "2.0", "id": 4, "method": "prompts/list", "params": {}}
    payload = "\n".join(json.dumps(m) for m in (init, initd, list_tools, list_res, list_prm)) + "\n"
    proc = subprocess.run([binary, "mcp", "--safe", "--allow-program", "*", "--allow-eval"],
                          input=payload, text=True,
                          capture_output=True, timeout=10)
    out = {"tools": [], "resources": [], "prompts": [], "server": {}}
    for line in proc.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue
        if "result" not in msg:
            continue
        r = msg["result"]
        if msg.get("id") == 1:
            out["server"] = r.get("serverInfo", {})
        elif msg.get("id") == 2:
            out["tools"] = r.get("tools", [])
        elif msg.get("id") == 3:
            out["resources"] = r.get("resources", [])
        elif msg.get("id") == 4:
            out["prompts"] = r.get("prompts", [])
    return out


def md_table_args(schema: dict) -> str:
    if not schema or schema.get("type") != "object":
        return "_no arguments_"
    props = schema.get("properties") or {}
    if not props:
        return "_no arguments_"
    required = set(schema.get("required") or [])
    rows = ["| Name | Type | Required | Description |", "|---|---|---|---|"]
    for name in sorted(props):
        spec = props[name]
        t = spec.get("type", "")
        if spec.get("enum"):
            sep = "\\|"
            t = "enum(" + sep.join(map(str, spec["enum"])) + ")"
        elif spec.get("items"):
            inner = spec["items"].get("type", "any")
            t = f"array&lt;{inner}&gt;"
        req = "✅" if name in required else ""
        desc = (spec.get("description") or "").replace("|", "\\|").replace("\n", " ")
        rows.append(f"| `{name}` | `{t}` | {req} | {desc} |")
    return "\n".join(rows)


def categorise(name: str) -> str:
    if name in {"debug_start", "debug_attach", "debug_restart", "debug_stop", "debug_sessions"}:
        return "Lifecycle"
    if name.startswith("debug_break") or name == "debug_unbreak" or name == "debug_breaks":
        return "Breakpoints"
    if name in {"debug_continue", "debug_step", "debug_next", "debug_finish", "debug_pause", "debug_until"}:
        return "Execution control"
    if name in {"debug_stack", "debug_threads", "debug_locals", "debug_globals", "debug_fields",
                "debug_source", "debug_output", "debug_events", "debug_state", "debug_snapshot",
                "debug_snapshot_compact", "debug_explain_pause", "debug_print"}:
        return "Inspection"
    if name in {"debug_eval", "debug_set", "debug_watch"}:
        return "Evaluation & state"
    if name in {"debug_listen", "debug_adapters"}:
        return "Infra"
    return "Composite recipes"


def main() -> int:
    binary = sys.argv[1] if len(sys.argv) > 1 else shutil.which("sl-dbg") or "sl-dbg"
    data = fetch_tools(binary)
    tools = data["tools"]
    server = data["server"]
    resources = data["resources"]
    prompts = data["prompts"]
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%d")

    groups: dict[str, list[dict]] = {}
    for t in tools:
        groups.setdefault(categorise(t["name"]), []).append(t)
    order = ["Lifecycle", "Breakpoints", "Execution control", "Inspection",
             "Evaluation & state", "Composite recipes", "Infra"]

    p = print
    p("# sl-dbg MCP server reference")
    p("")
    p(f"Auto-generated from a live `sl-dbg mcp` introspection on **{ts}** "
      f"(server `{server.get('name','sl-dbg')}` v`{server.get('version','dev')}`).")
    p("")
    p("> This file is the canonical contract between sl-dbg and any "
      "MCP-aware agent. Every entry below is what the agent receives "
      "from `tools/list` / `resources/list` / `prompts/list`, verbatim "
      "from the binary.")
    p("")
    p("## At a glance")
    p("")
    p(f"- **Tools:** {len(tools)}  ")
    p(f"- **Resources:** {len(resources)}  ")
    p(f"- **Prompts:** {len(prompts)}  ")
    p("- **Transport:** stdio (JSON-RPC 2.0)")
    p("- **Protocol version:** `2024-11-05`")
    p("")
    p("## Wire it up")
    p("")
    p("```bash")
    p("# One command per agent — safe read-merge-write with .bak backup")
    p("sl-dbg mcp install claude   # or: cursor | vscode | codex | copilot | all")
    p("```")
    p("")

    if resources:
        p("## Resources")
        p("")
        p("Read-only views into live daemon state. An agent fetches them with")
        p("`resources/read` instead of paying for a tool call.")
        p("")
        p("| URI | Name | Description | MIME |")
        p("|---|---|---|---|")
        for r in resources:
            p(f"| `{r.get('uri','')}` | {r.get('name','')} | {r.get('description','')} | `{r.get('mimeType','application/json')}` |")
        p("")

    if prompts:
        p("## Prompts")
        p("")
        p("Prebuilt prompt templates the user can pick from the agent UI.")
        p("")
        for pr in prompts:
            p(f"### `{pr.get('name','')}`")
            p("")
            p(pr.get("description", ""))
            p("")
            for a in pr.get("arguments", []) or []:
                req = " *(required)*" if a.get("required") else ""
                p(f"- `{a['name']}`{req} — {a.get('description','')}")
            p("")

    p("## Tools")
    p("")
    p("Every tool returns a JSON object. Errors come back as a top-level "
      "`{ \"ok\": false, \"err\": { \"code\": \"...\", \"msg\": \"...\" } }` envelope.")
    p("")

    for g in order:
        items = groups.get(g, [])
        if not items:
            continue
        p(f"### {g}")
        p("")
        for t in items:
            p(f"#### `{t['name']}`")
            p("")
            p(t.get("description", "_(no description)_"))
            p("")
            p(md_table_args(t.get("inputSchema") or {}))
            p("")

    p("---")
    p("")
    p("## Security knobs the agent does NOT see")
    p("")
    p("The daemon enforces these *before* a tool call ever reaches the "
      "adapter — independent of what the LLM decides to call:")
    p("")
    p("| Env var | Effect |")
    p("|---|---|")
    p("| `SL_DBG_DENY_EVAL_PATTERNS` | Comma-separated substrings; `debug_eval` expressions matching any are rejected with `EVAL_DENIED`. Defaults to known Java side-effect classes (`FileOutputStream`, `Runtime`, etc.). |")
    p("| `SL_DBG_ALLOW_SOURCE_ROOT`  | Colon-separated dir list; `debug_source` paths outside any of them return `SOURCE_PATH_DENIED`. |")
    p("| `--read-only`               | Hides every state-mutating tool from `tools/list`. The agent literally cannot see them. |")
    p("| `--allow-cwd <dir>`         | `debug_start`/`debug_attach` reject programs whose cwd or path falls outside the listed roots (`POLICY_DENIED`). |")
    p("| `--deny-program <substr>`   | Case-insensitive substring deny-list applied to the program path. |")
    p("")
    p("See [SECURITY.md](SECURITY.md) for the full threat model and the "
      "policy decision flow.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
