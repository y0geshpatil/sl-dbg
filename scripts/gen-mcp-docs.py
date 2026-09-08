#!/usr/bin/env python3
"""Generate sl-dbg-site/mcp.md from a live `sl-dbg mcp` tools/list dump.

Usage:
    scripts/gen-mcp-docs.py [/path/to/sl-dbg-binary] > sl-dbg-site/mcp.md
"""
from __future__ import annotations

import json
import io
import os
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from contextlib import redirect_stdout
from pathlib import Path


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
    binary = str(Path(shutil.which(binary) or binary).resolve())
    # Introspection persists safe-mode policy; never inherit a user's state or socket.
    with tempfile.TemporaryDirectory(prefix=".mcp-docs-", dir=Path.cwd()) as scratch:
        root = Path(scratch).resolve()
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(("SL_DBG_", "XDG_"))}
        for key, subdir in {
            "HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_STATE_HOME": "state",
            "XDG_CACHE_HOME": "cache", "XDG_DATA_HOME": "data",
            "XDG_RUNTIME_DIR": "run", "TMPDIR": "temp", "TMP": "temp", "TEMP": "temp",
        }.items():
            path = root / subdir
            path.mkdir(mode=0o700, exist_ok=True)
            env[key] = str(path)
        # Relative socket names avoid macOS's short sockaddr_un path limit.
        env["SL_DBG_SOCKET"] = "daemon.sock"
        try:
            proc = subprocess.run(
                [binary, "mcp", "--safe", "--allow-program", "*", "--allow-eval"],
                input=payload, text=True, cwd=root, env=env,
                capture_output=True, timeout=10)
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise RuntimeError(f"cannot introspect {binary}: {exc}") from exc
    if proc.returncode != 0:
        raise RuntimeError(f"{binary} mcp exited with status {proc.returncode}: "
                           f"{proc.stderr.strip()[-2000:] or '(no stderr)'}")
    out = {"tools": [], "resources": [], "prompts": [], "server": {}}
    responses = {}
    for number, line in enumerate(proc.stdout.splitlines(), 1):
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"MCP stdout line {number} is not JSON: {exc}") from exc
        if not isinstance(msg, dict) or msg.get("jsonrpc") != "2.0":
            raise RuntimeError(f"MCP stdout line {number} is not a JSON-RPC 2.0 object")
        if "id" not in msg and isinstance(msg.get("method"), str):
            continue
        ident = msg.get("id")
        if type(ident) is not int or ident not in (1, 2, 3, 4) or ident in responses:
            raise RuntimeError(f"MCP stdout line {number} has an unexpected or duplicate response id")
        if "error" in msg:
            raise RuntimeError(f"MCP request {ident} failed: {json.dumps(msg['error'])}")
        if not isinstance(msg.get("result"), dict):
            raise RuntimeError(f"MCP request {ident} has no object result")
        responses[ident] = msg["result"]
    for ident, method in ((1, "initialize"), (2, "tools/list"),
                          (3, "resources/list"), (4, "prompts/list")):
        if ident not in responses:
            raise RuntimeError(f"MCP response missing for {method}; stderr: "
                               f"{proc.stderr.strip()[-2000:] or '(none)'}")
    server = responses[1].get("serverInfo")
    if (not isinstance(server, dict) or not isinstance(server.get("name"), str)
            or not isinstance(server.get("version"), str)
            or not isinstance(responses[1].get("protocolVersion"), str)):
        raise RuntimeError("MCP initialize response has invalid serverInfo/protocolVersion")
    out["server"] = server
    out["protocolVersion"] = responses[1]["protocolVersion"]
    for ident, key in ((2, "tools"), (3, "resources"), (4, "prompts")):
        items = responses[ident].get(key)
        if not isinstance(items, list) or (key == "tools" and not items):
            raise RuntimeError(f"MCP {key}/list must return a {'nonempty ' if key == 'tools' else ''}{key} array")
        names = set()
        for item in items:
            if not isinstance(item, dict) or not isinstance(item.get("name"), str) or not item["name"]:
                raise RuntimeError(f"MCP {key}/list entry has no valid name")
            if item["name"] in names:
                raise RuntimeError(f"MCP {key}/list contains duplicate name {item['name']}")
            names.add(item["name"])
            if "description" in item and not isinstance(item["description"], str):
                raise RuntimeError(f"MCP {key}/list entry has an invalid description")
            if key == "resources" and not isinstance(item.get("uri"), str):
                raise RuntimeError(f"MCP resource {item['name']} has no valid uri")
            if key == "prompts":
                arguments = item.get("arguments", [])
                if not isinstance(arguments, list) or any(
                        not isinstance(arg, dict) or not isinstance(arg.get("name"), str)
                        for arg in arguments):
                    raise RuntimeError(f"MCP prompt {item['name']} has invalid arguments")
            if key == "tools":
                schema = item.get("inputSchema")
                if not isinstance(schema, dict) or schema.get("type") != "object":
                    raise RuntimeError(f"MCP tool {item['name']} has invalid inputSchema")
                props = schema.get("properties", {})
                if not isinstance(props, dict) or any(not isinstance(v, dict) for v in props.values()):
                    raise RuntimeError(f"MCP tool {item['name']} has invalid schema properties")
        out[key] = items
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
    try:
        data = fetch_tools(binary)
        rendered = io.StringIO()
        with redirect_stdout(rendered):
            render_docs(data)
    except (RuntimeError, OSError, TypeError, ValueError, KeyError, AttributeError) as exc:
        print(f"gen-mcp-docs: {exc}", file=sys.stderr)
        return 1
    sys.stdout.write(rendered.getvalue())
    return 0


def render_docs(data: dict) -> None:
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
    p("> **Schema profile: eval-enabled** (`--safe --allow-program '*' --allow-eval`). "
      "Generation explicitly enables evaluation; this is not the default MCP configuration. "
      "Default safe mode still advertises evaluation tools but rejects their execution.")
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
    p(f"- **Protocol version:** `{data['protocolVersion']}`")
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


if __name__ == "__main__":
    sys.exit(main())
