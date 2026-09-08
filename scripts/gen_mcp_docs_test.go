package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const mcpDocsFixture = `
import json, os, pathlib, sys
root = pathlib.Path.cwd()
for key in ("HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
            "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP"):
    path = pathlib.Path(os.environ[key])
    assert root in path.parents, (key, path, root)
    assert path.is_dir(), key
assert os.environ["SL_DBG_SOCKET"] == "daemon.sock"
assert "SL_DBG_AUDIT_LOG" not in os.environ
assert "SL_DBG_INSECURE" not in os.environ
assert "SL_DBG_READ_ONLY" not in os.environ
assert sys.argv[1:] == ["mcp", "--safe", "--allow-program", "*", "--allow-eval"]
requests = [json.loads(line) for line in sys.stdin]
assert [r["method"] for r in requests] == [
    "initialize", "notifications/initialized", "tools/list", "resources/list", "prompts/list"]
(pathlib.Path(os.environ["XDG_STATE_HOME"]) / "safe-policy.env").write_text("isolated")
replies = [
    {"jsonrpc": "2.0", "id": 1, "result": {"protocolVersion": "2024-11-05",
        "serverInfo": {"name": "fixture", "version": "1"}}},
    {"jsonrpc": "2.0", "id": 2, "result": {"tools": [
        {"name": "debug_eval", "description": "Evaluate.",
         "inputSchema": {"type": "object", "properties": {"expression": {"type": "string"}}}}]}},
    {"jsonrpc": "2.0", "id": 3, "result": {"resources": []}},
    {"jsonrpc": "2.0", "id": 4, "result": {"prompts": []}},
]
case = os.environ.get("MCP_DOCS_CASE", "")
if case == "nonzero":
    print("adapter startup failed", file=sys.stderr)
    sys.exit(23)
if case == "malformed":
    print("not json")
elif case == "scalar":
    print("[]")
elif case == "missing":
    replies.pop()
elif case == "error":
    replies[1] = {"jsonrpc": "2.0", "id": 2, "error": {"code": -32603, "message": "broken"}}
elif case == "empty":
    replies[1]["result"]["tools"] = []
elif case == "wrong-array":
    replies[2]["result"]["resources"] = {}
elif case == "bad-entry":
    replies[1]["result"]["tools"] = [{}]
elif case == "bad-schema":
    replies[1]["result"]["tools"][0]["inputSchema"] = []
elif case == "duplicate":
    replies.append(replies[1])
elif case == "wrong-version":
    replies[0]["jsonrpc"] = "1.0"
elif case == "bad-server":
    replies[0]["result"]["serverInfo"] = []
elif case == "bad-result":
    replies[1]["result"] = []
elif case == "bad-property-description":
    replies[1]["result"]["tools"][0]["inputSchema"]["properties"]["expression"]["description"] = 42
elif case == "bad-prompt":
    replies[3]["result"]["prompts"] = [{"name": "broken", "arguments": [42]}]
elif case == "timeout":
    import time
    time.sleep(30)
for reply in replies:
    print(json.dumps(reply))
`

func TestMCPDocsGenerator(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	generator, err := filepath.Abs("gen-mcp-docs.py")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(".", ".mcp-docs-test-")
	if err != nil {
		t.Fatal(err)
	}
	root, _ = filepath.Abs(root)
	t.Cleanup(func() { os.RemoveAll(root) })
	fake := filepath.Join(root, "fake-mcp")
	if err := os.WriteFile(fake, []byte("#!"+python+"\n"+mcpDocsFixture), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, want string }{
		{"success", ""},
		{"nonzero", "status 23: adapter startup failed"},
		{"malformed", "not JSON"},
		{"scalar", "not a JSON-RPC"},
		{"missing", "missing for prompts/list"},
		{"error", "request 2 failed"},
		{"empty", "nonempty tools array"},
		{"wrong-array", "resources array"},
		{"bad-entry", "valid name"},
		{"bad-schema", "invalid inputSchema"},
		{"duplicate", "duplicate response id"},
		{"wrong-version", "not a JSON-RPC"},
		{"bad-server", "invalid serverInfo"},
		{"bad-result", "no object result"},
		{"bad-property-description", "gen-mcp-docs:"},
		{"bad-prompt", "invalid arguments"},
		{"timeout", "timed out"},
		{"missing-binary", "cannot introspect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := fake
			if tc.name == "missing-binary" {
				binary += "-missing"
			}
			cmd := exec.Command(python, generator, binary)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "MCP_DOCS_CASE="+tc.name,
				"SL_DBG_SOCKET=user-daemon.sock", "SL_DBG_INSECURE=1",
				"SL_DBG_READ_ONLY=1", "SL_DBG_AUDIT_LOG=user-audit.log")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("generator: %v\n%s", err, stderr.String())
				}
				for _, want := range []string{"Schema profile: eval-enabled", "Default safe mode still advertises evaluation tools but rejects their execution", "#### `debug_eval`"} {
					if !strings.Contains(stdout.String(), want) {
						t.Errorf("missing %q in generated docs", want)
					}
				}
			} else if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("got err=%v stdout=%q stderr=%q; want no docs and error %q",
					err, stdout.String(), stderr.String(), tc.want)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "fake-mcp" {
				t.Fatalf("generator left files outside its cleaned scratch directory: %v", entries)
			}
		})
	}
}
