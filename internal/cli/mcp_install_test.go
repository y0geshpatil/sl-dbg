package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallJSON_FreshFile(t *testing.T) {
	dir := t.TempDir()
	target := agentTarget{name: "claude", kind: "json", path: filepath.Join(dir, "claude_desktop_config.json"), create: true}

	if err := installJSON(target, "/usr/local/bin/sl-dbg", "sl-dbg", false, false); err != nil {
		t.Fatalf("installJSON: %v", err)
	}

	data, err := os.ReadFile(target.path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, data)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	entry, _ := servers["sl-dbg"].(map[string]any)
	if entry["command"] != "/usr/local/bin/sl-dbg" {
		t.Fatalf("command not set: %v", entry)
	}
}

func TestInstallJSON_PreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_desktop_config.json")
	pre := `{"mcpServers":{"other":{"command":"x","args":["y"]}},"window":{"width":1200}}`
	if err := os.WriteFile(path, []byte(pre), 0o600); err != nil {
		t.Fatal(err)
	}
	target := agentTarget{name: "claude", kind: "json", path: path, create: true}

	if err := installJSON(target, "/bin/sl-dbg", "sl-dbg", false, false); err != nil {
		t.Fatalf("installJSON: %v", err)
	}

	var doc map[string]any
	data, _ := os.ReadFile(path)
	_ = json.Unmarshal(data, &doc)

	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Errorf("existing server entry was dropped: %s", data)
	}
	if _, ok := servers["sl-dbg"]; !ok {
		t.Errorf("sl-dbg entry missing: %s", data)
	}
	if win, ok := doc["window"].(map[string]any); !ok || win["width"] == nil {
		t.Errorf("unrelated top-level key lost: %s", data)
	}
}

func TestInstallJSON_RefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	pre := `{"mcpServers":{"sl-dbg":{"command":"old","args":["mcp"]}}}`
	_ = os.WriteFile(path, []byte(pre), 0o600)
	target := agentTarget{name: "x", kind: "json", path: path, create: true}

	err := installJSON(target, "/new", "sl-dbg", false, false)
	if err == nil {
		t.Fatal("expected refusal without --force")
	}
	if !strings.Contains(err.Error(), "already present") {
		t.Fatalf("wrong error: %v", err)
	}
	// Force overwrites.
	if err := installJSON(target, "/new", "sl-dbg", true, false); err != nil {
		t.Fatalf("force install failed: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"/new"`) {
		t.Fatalf("force did not overwrite: %s", data)
	}
}

func TestInstallJSON_BackupCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600)
	target := agentTarget{name: "x", kind: "json", path: path, create: true}
	if err := installJSON(target, "/bin/sl-dbg", "sl-dbg", false, false); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var hasBak bool
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak.") {
			hasBak = true
		}
	}
	if !hasBak {
		t.Fatalf("no backup file created: %v", entries)
	}
}

func TestInstallTOML_AppendsStanza(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(path, []byte("[model]\nname = \"gpt-5\"\n"), 0o600)
	target := agentTarget{name: "codex", kind: "toml", path: path, create: true}

	if err := installTOML(target, "/usr/local/bin/sl-dbg", "sl-dbg", false, false); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if !strings.Contains(s, "[mcp_servers.sl-dbg]") {
		t.Errorf("stanza missing:\n%s", s)
	}
	if !strings.Contains(s, `command = "/usr/local/bin/sl-dbg"`) {
		t.Errorf("command line missing:\n%s", s)
	}
	if !strings.Contains(s, "[model]") {
		t.Errorf("existing stanza removed:\n%s", s)
	}
}

func TestInstallTOML_ForceReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pre := "[mcp_servers.sl-dbg]\ncommand = \"/old\"\nargs = [\"mcp\"]\n\n[other]\nx = 1\n"
	_ = os.WriteFile(path, []byte(pre), 0o600)
	target := agentTarget{name: "codex", kind: "toml", path: path, create: true}

	if err := installTOML(target, "/new", "sl-dbg", false, false); err == nil {
		t.Fatal("expected refusal without --force")
	}
	if err := installTOML(target, "/new", "sl-dbg", true, false); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if strings.Contains(s, "/old") {
		t.Errorf("force did not remove old stanza:\n%s", s)
	}
	if !strings.Contains(s, "/new") {
		t.Errorf("new command missing:\n%s", s)
	}
	if !strings.Contains(s, "[other]") {
		t.Errorf("unrelated stanza removed:\n%s", s)
	}
}

func TestRemoveTOMLStanza(t *testing.T) {
	in := "[a]\nx = 1\n\n[mcp_servers.sl-dbg]\ncommand = \"x\"\nargs = []\n\n[b]\ny = 2\n"
	out := removeTOMLStanza(in, "[mcp_servers.sl-dbg]")
	if strings.Contains(out, "mcp_servers.sl-dbg") {
		t.Errorf("stanza not removed:\n%s", out)
	}
	if !strings.Contains(out, "[a]") || !strings.Contains(out, "[b]") {
		t.Errorf("siblings lost:\n%s", out)
	}
}

func TestResolveAgents_Unknown(t *testing.T) {
	if _, err := resolveAgents("blarg"); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestDryRun_DoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	target := agentTarget{name: "x", kind: "json", path: path, create: true}
	if err := installJSON(target, "/bin/sl-dbg", "sl-dbg", false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a file: %v", err)
	}
}
