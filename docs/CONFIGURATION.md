# Configuration

> Config-file support is being rolled out — until then, all settings are CLI flags only (see `sl-dbg --help`).

The intended user config path is:

```text
~/.config/sl-dbg/config.toml
```

When config-file support lands, CLI flags should continue to override values from this file so one-off sessions remain explicit and scriptable.

## Schema Overview

### `[security]`

Controls what the MCP server and daemon are allowed to forward to adapters or target programs.

| Key | Type | Intended meaning |
|---|---|---|
| `default_read_only` | bool | Start sessions in read-only mode unless a CLI flag overrides it. |
| `allow_programs` | array of strings | Executable names or absolute paths that `start` may launch. |
| `allow_source_roots` | array of strings | Directory roots that source reads and source breakpoints may access. |
| `deny_source_globs` | array of strings | Glob patterns that remain blocked even inside allowed roots. |
| `allow_eval` | bool | Permit expression evaluation. Production MCP deployments should set this to `false` or use read-only mode. |

### `[limits]`

Bounds resource use for local DoS resistance and predictable automation.

| Key | Type | Intended meaning |
|---|---|---|
| `max_sessions` | integer | Maximum concurrent debugger sessions. |
| `command_timeout` | duration string | Default timeout for daemon operations. |
| `max_eval_bytes` | integer | Maximum returned eval payload size before truncation/refusal. |
| `max_output_bytes` | integer | Maximum buffered target output retained per session. |
| `max_source_bytes` | integer | Maximum source bytes returned by one source request. |

### `[audit]`

Controls the local JSONL audit trail.

| Key | Type | Intended meaning |
|---|---|---|
| `enabled` | bool | Write audit records for sensitive operations. |
| `path` | string | Audit log destination. |
| `redact_values` | bool | Redact likely secret values from arguments and results. |
| `include_read_only_commands` | bool | Include inspection commands such as `locals`, `stack`, and `source`. |

### `[defaults]`

Convenience defaults for interactive and agent-driven use.

| Key | Type | Intended meaning |
|---|---|---|
| `lang` | string | Default adapter language when a command omits `--lang`. |
| `cwd` | string | Default working directory for launch requests. |
| `source_root` | string | Default source root for source lookup. |
| `pretty` | bool | Pretty-print JSON on TTY output. |
| `stop_on_entry` | bool | Pause launched programs before user code runs. |

## Annotated Example

```toml
[security]
# Prefer inspection over mutation for MCP clients and production attach sessions.
default_read_only = true

# Only these programs may be launched by sl-dbg start.
allow_programs = ["java", "python3"]

# Source reads and source breakpoints must stay under these roots.
allow_source_roots = ["~/work", "~/src"]

# Keep common secret-bearing paths blocked even under allowed roots.
deny_source_globs = ["**/.env*", "**/secrets/**", "**/*credentials*"]

# Eval is powerful; leave disabled for unattended agent workflows.
allow_eval = false

[limits]
max_sessions = 4
command_timeout = "10s"
max_eval_bytes = 1048576
max_output_bytes = 4194304
max_source_bytes = 262144

[audit]
enabled = true
path = "~/.local/state/sl-dbg/audit.log"
redact_values = true
include_read_only_commands = false

[defaults]
lang = "python"
cwd = "~/work/current-service"
source_root = "~/work"
pretty = true
stop_on_entry = false
```

Until the loader is available, express the same policy with CLI flags, for example:

```bash
sl-dbg mcp \
  --read-only \
  --allow-program "java" \
  --allow-program "python3" \
  --allow-source-root ~/work \
  --max-sessions 4 \
  --audit-log ~/.local/state/sl-dbg/audit.log
```
