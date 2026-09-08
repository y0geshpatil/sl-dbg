package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// defaultArgs mirrors what the CLI writes for `sl-dbg mcp install` without
// flag overrides: --safe so the daemon stays secure-by-default. Issue #70
// removed the implicit '*' allowlist; the daemon now auto-discovers from
// PATH instead, so the default args are just ["mcp", "--safe"].
var defaultArgs = buildMCPInvocationArgs(nil, false, false)

func TestBuildMCPInvocationArgs(t *testing.T) {
	got := buildMCPInvocationArgs(nil, false, false)
	want := []string{"mcp", "--safe"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("default args mismatch:\n  got:  %v\n  want: %v", got, want)
	}

	got = buildMCPInvocationArgs([]string{"/usr/local/bin/app", "/opt/svc"}, true, false)
	want = []string{"mcp", "--safe", "--read-only", "--allow-program", "/usr/local/bin/app", "--allow-program", "/opt/svc"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("custom args mismatch:\n  got:  %v\n  want: %v", got, want)
	}

	got = buildMCPInvocationArgs(nil, false, true)
	want = []string{"mcp"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("insecure args mismatch:\n  got:  %v\n  want: %v", got, want)
	}
}

func TestInstallJSON_FreshFile(t *testing.T) {
	dir := t.TempDir()
	target := agentTarget{name: "claude", kind: "json", path: filepath.Join(dir, "claude_desktop_config.json"), create: true}

	if err := installJSON(target, "/usr/local/bin/sl-dbg", defaultArgs, "sl-dbg", false, false); err != nil {
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
	args, _ := entry["args"].([]any)
	if len(args) != 2 || args[0] != "mcp" || args[1] != "--safe" {
		t.Fatalf("default args not written as --safe (auto-discovery): %v", args)
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

	if err := installJSON(target, "/bin/sl-dbg", defaultArgs, "sl-dbg", false, false); err != nil {
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

	err := installJSON(target, "/new", defaultArgs, "sl-dbg", false, false)
	if err == nil {
		t.Fatal("expected refusal without --force")
	}
	if !strings.Contains(err.Error(), "already present") {
		t.Fatalf("wrong error: %v", err)
	}
	// Force overwrites.
	if err := installJSON(target, "/new", defaultArgs, "sl-dbg", true, false); err != nil {
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
	if err := installJSON(target, "/bin/sl-dbg", defaultArgs, "sl-dbg", false, false); err != nil {
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

	if err := installTOML(target, "/usr/local/bin/sl-dbg", defaultArgs, "sl-dbg", false, false); err != nil {
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

	if err := installTOML(target, "/new", defaultArgs, "sl-dbg", false, false); err == nil {
		t.Fatal("expected refusal without --force")
	}
	if err := installTOML(target, "/new", defaultArgs, "sl-dbg", true, false); err != nil {
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
	out, found, err := stripMCPTable(in, "sl-dbg")
	if err != nil || !found {
		t.Fatalf("stripMCPTable: found=%v, err=%v", found, err)
	}
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
	if err := installJSON(target, "/bin/sl-dbg", defaultArgs, "sl-dbg", false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a file: %v", err)
	}
}

func TestUninstallJSON_RemovesOnlyOurEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	pre := `{"mcpServers":{"sl-dbg":{"command":"x","args":["mcp"]},"other":{"command":"y"}},"window":{"width":900}}`
	_ = os.WriteFile(path, []byte(pre), 0o600)
	target := agentTarget{name: "x", kind: "json", path: path, create: true}

	if err := uninstallJSON(target, "sl-dbg", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["sl-dbg"]; ok {
		t.Errorf("sl-dbg entry not removed: %s", data)
	}
	if _, ok := servers["other"]; !ok {
		t.Errorf("sibling entry removed: %s", data)
	}
	if doc["window"] == nil {
		t.Errorf("unrelated top-level key lost: %s", data)
	}
}

func TestUninstallJSON_DropsEmptyMcpServersBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"mcpServers":{"sl-dbg":{"command":"x"}},"theme":"dark"}`), 0o600)
	target := agentTarget{name: "x", kind: "json", path: path, create: true}
	if err := uninstallJSON(target, "sl-dbg", false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	if _, ok := doc["mcpServers"]; ok {
		t.Errorf("empty mcpServers block kept: %s", data)
	}
	if doc["theme"] != "dark" {
		t.Errorf("unrelated key lost: %s", data)
	}
}

func TestUninstallJSON_NoFile(t *testing.T) {
	target := agentTarget{name: "x", kind: "json", path: filepath.Join(t.TempDir(), "missing.json"), create: true}
	if err := uninstallJSON(target, "sl-dbg", false); err != nil {
		t.Fatalf("missing file should be a no-op, got %v", err)
	}
}

func TestUninstallJSON_NotPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	_ = os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"y"}}}`), 0o600)
	target := agentTarget{name: "x", kind: "json", path: path, create: true}
	pre, _ := os.ReadFile(path)
	if err := uninstallJSON(target, "sl-dbg", false); err != nil {
		t.Fatal(err)
	}
	post, _ := os.ReadFile(path)
	if string(pre) != string(post) {
		t.Errorf("file changed when entry was absent:\nbefore: %s\nafter:  %s", pre, post)
	}
}

func TestUninstallTOML_RemovesStanza(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pre := "[model]\nname = \"x\"\n\n[mcp_servers.sl-dbg]\ncommand = \"/bin/sl-dbg\"\nargs = [\"mcp\"]\n\n[other]\nz = 1\n"
	_ = os.WriteFile(path, []byte(pre), 0o600)
	target := agentTarget{name: "codex", kind: "toml", path: path, create: true}
	if err := uninstallTOML(target, "sl-dbg", false); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	s := string(out)
	if strings.Contains(s, "mcp_servers.sl-dbg") {
		t.Errorf("stanza not removed:\n%s", s)
	}
	if !strings.Contains(s, "[model]") || !strings.Contains(s, "[other]") {
		t.Errorf("siblings removed:\n%s", s)
	}
}

func TestUninstall_DryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	pre := `{"mcpServers":{"sl-dbg":{"command":"x"}}}`
	_ = os.WriteFile(path, []byte(pre), 0o600)
	target := agentTarget{name: "x", kind: "json", path: path, create: true}
	if err := uninstallJSON(target, "sl-dbg", true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != pre {
		t.Errorf("dry-run mutated file:\n%s", data)
	}
}

func TestMCPJSON_ClientContracts(t *testing.T) {
	for _, agent := range []string{"claude", "cursor", "vscode", "copilot"} {
		t.Run(agent, func(t *testing.T) {
			target := agentTarget{name: agent, kind: "json", path: filepath.Join(t.TempDir(), "mcp.json"), create: true}
			key := "mcpServers"
			if agent == "vscode" {
				key = "servers"
			}
			pre := fmt.Sprintf(`{%q:{"other":{"command":"keep"}},"inputs":[{"id":"token"}],"precise":9007199254740993}`, key)
			writeMCPFixture(t, target.path, pre)
			if err := installJSON(target, "/bin/sl-dbg", defaultArgs, "sl-dbg", false, false); err != nil {
				t.Fatal(err)
			}
			doc, servers, err := readMCPJSON([]byte(readMCPFixture(t, target.path)), key)
			if err != nil {
				t.Fatal(err)
			}
			var entry map[string]any
			if err := json.Unmarshal(servers["sl-dbg"], &entry); err != nil {
				t.Fatal(err)
			}
			if agent == "vscode" && entry["type"] != "stdio" || agent == "copilot" && entry["type"] != "local" {
				t.Fatalf("wrong transport: %v", entry)
			}
			if agent == "copilot" {
				tools, _ := entry["tools"].([]any)
				if len(tools) != 1 || tools[0] != "*" {
					t.Fatalf("Copilot tools missing: %v", entry)
				}
			}
			if _, ok := entry["env"]; ok {
				t.Fatal("safe registration must not export insecure env")
			}
			if string(doc["precise"]) != "9007199254740993" {
				t.Fatal("unrelated number lost precision")
			}
			if err := uninstallJSON(target, "sl-dbg", false); err != nil {
				t.Fatal(err)
			}
			doc, servers, err = readMCPJSON([]byte(readMCPFixture(t, target.path)), key)
			if err != nil || servers["other"] == nil || doc["inputs"] == nil || servers["sl-dbg"] != nil {
				t.Fatalf("uninstall lost unrelated config: %s; %v", readMCPFixture(t, target.path), err)
			}
			if agent == "vscode" && doc["mcpServers"] != nil {
				t.Fatal("VS Code must not acquire an mcpServers key")
			}
		})
	}
}

func TestMCPJSON_InvalidShapesNeverWrite(t *testing.T) {
	for _, agent := range []string{"cursor", "vscode"} {
		target := agentTarget{name: agent, kind: "json", create: true}
		configs := []string{"", "null", "[]", "42"}
		for _, container := range []string{"null", `["keep"]`, `"keep"`, "42", "true"} {
			configs = append(configs, fmt.Sprintf(`{%q:%s,"unrelated":"keep"}`, target.serverKey(), container))
		}
		for _, pre := range configs {
			t.Run(agent+"/"+pre, func(t *testing.T) {
				target.path = filepath.Join(t.TempDir(), "mcp.json")
				writeMCPFixture(t, target.path, pre)
				if err := installJSON(target, "/bin/sl-dbg", defaultArgs, "sl-dbg", true, false); err == nil {
					t.Fatal("install accepted malformed config")
				}
				if err := uninstallJSON(target, "sl-dbg", false); err == nil {
					t.Fatal("uninstall accepted malformed config")
				}
				if got := readMCPFixture(t, target.path); got != pre {
					t.Fatalf("malformed config changed: %q", got)
				}
				entries, _ := os.ReadDir(filepath.Dir(target.path))
				if len(entries) != 1 {
					t.Fatalf("failed operation wrote extra files: %v", entries)
				}
			})
		}
	}
}

func TestMCPJSON_InsecureExplicitOptIn(t *testing.T) {
	for _, agent := range []string{"claude", "vscode", "copilot"} {
		entry := mcpJSONEntry(agent, "/bin/sl-dbg", buildMCPInvocationArgs(nil, true, true))
		env, _ := entry["env"].(map[string]interface{})
		if env["SL_DBG_INSECURE"] != "1" {
			t.Fatalf("%s: missing explicit opt-in env: %v", agent, entry)
		}
	}
}

func TestResolveAgents_CanonicalPaths(t *testing.T) {
	home := isolateMCPConfig(t)
	copilot, err := resolveAgents("copilot")
	if err != nil || len(copilot) != 1 || copilot[0].path != filepath.Join(home, ".copilot", "mcp-config.json") {
		t.Fatalf("Copilot target: %v; %v", copilot, err)
	}
	workspace, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	vscode, err := resolveAgents("vscode")
	if err != nil || len(vscode) != 1 || vscode[0].path != filepath.Join(workspace, ".vscode", "mcp.json") {
		t.Fatalf("VS Code target: %v; %v", vscode, err)
	}
}

func TestMCPCommands_NoDetectedAgents(t *testing.T) {
	for _, uninstall := range []bool{false, true} {
		t.Run(fmt.Sprint(uninstall), func(t *testing.T) {
			home := isolateMCPConfig(t)
			cmd := newMCPInstallCmd()
			if uninstall {
				cmd = newMCPUninstallCmd()
			}
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{"all"})
			err := cmd.Execute()
			if uninstall {
				if err != nil || !strings.Contains(output.String(), "nothing to uninstall") {
					t.Fatalf("fresh uninstall: err=%v, output=%q", err, output.String())
				}
			} else if !errors.Is(err, errNoDetectedAgents) || !strings.Contains(err.Error(), "--print") {
				t.Fatalf("fresh install must fail with manual configuration guidance: %v", err)
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				t.Fatalf("fresh operation changed config directories: %v; %v", entries, err)
			}
		})
	}
}

func TestMCPUninstall_ConfigFailureIsNotNoOp(t *testing.T) {
	home := isolateMCPConfig(t)
	dir := filepath.Join(home, ".cursor")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mcp.json")
	writeMCPFixture(t, path, "null")
	cmd := newMCPUninstallCmd()
	cmd.SetArgs([]string{"all"})
	err := cmd.Execute()
	if err == nil || errors.Is(err, errNoDetectedAgents) || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("real config failure was suppressed: %v", err)
	}
	if got := readMCPFixture(t, path); got != "null" {
		t.Fatalf("malformed config changed: %q", got)
	}
}

func TestMCPCommands_PartialFailureIsAnError(t *testing.T) {
	for _, uninstall := range []bool{false, true} {
		t.Run(fmt.Sprint(uninstall), func(t *testing.T) {
			home := isolateMCPConfig(t)
			for _, dir := range []string{".cursor", ".vscode"} {
				if err := os.Mkdir(filepath.Join(home, dir), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			good := filepath.Join(home, ".cursor", "mcp.json")
			bad := filepath.Join(home, ".vscode", "mcp.json")
			writeMCPFixture(t, bad, "null")
			cmd := newMCPInstallCmd()
			if uninstall {
				writeMCPFixture(t, good, `{"mcpServers":{"sl-dbg":{"command":"old"}}}`)
				cmd = newMCPUninstallCmd()
			}
			cmd.SetArgs([]string{"all"})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "vscode") {
				t.Fatalf("partial failure was masked: %v", err)
			}
			if got := readMCPFixture(t, bad); got != "null" {
				t.Fatal("failed target changed")
			}
			got := readMCPFixture(t, good)
			if strings.Contains(got, `"sl-dbg"`) == uninstall {
				t.Fatalf("successful target did not update: %s", got)
			}
		})
	}
}

func TestMCPSnippets_ClientSchemas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old }()
	err = printMCPSnippets("/bin/sl-dbg", "sl-dbg", defaultArgs)
	os.Stdout = old
	if closeErr := f.Close(); err != nil || closeErr != nil {
		t.Fatalf("print: %v; close: %v", err, closeErr)
	}
	out := readMCPFixture(t, path)
	for _, want := range []string{`"servers":`, `"mcpServers":`, `"type": "local"`, `"type": "stdio"`, `"tools":`, "~/.copilot/mcp-config.json", "[mcp_servers.sl-dbg]"} {
		if !strings.Contains(out, want) {
			t.Errorf("snippet missing %q:\n%s", want, out)
		}
	}
}

func TestStripMCPTable_PreservesOtherText(t *testing.T) {
	for _, header := range []string{
		"[mcp_servers.sl-dbg]",
		`[mcp_servers."sl-dbg"] # configured manually`,
		`[ mcp_servers . 'sl-dbg' ]`,
	} {
		t.Run(header, func(t *testing.T) {
			prefix := "# [mcp_servers.sl-dbg] example\nmodel = \"gpt-5\"\n\n\n"
			sibling := "[mcp_servers.other]\ncommand = \"keep\"\n\n\n"
			pre := prefix + header + "\ncommand = \"old\"\n\n# still the same table\nargs = [\n\"mcp\",\n]\n[mcp_servers.sl-dbg.env]\nTOKEN = \"old\"\n" + sibling
			out, found, err := stripMCPTable(pre, "sl-dbg")
			if err != nil || !found || out != prefix+sibling {
				t.Fatalf("out=%q, found=%v, err=%v", out, found, err)
			}
		})
	}
}

func TestStripMCPTable_IgnoresStringsAndComments(t *testing.T) {
	for _, quote := range []string{`"""`, "'''"} {
		pre := "notes = " + quote + "\n[mcp_servers.sl-dbg]\ntext = 'keep'\n" + quote + "\n# [mcp_servers.sl-dbg]\n[mcp_servers.other]\ncommand = \"keep\"\n"
		out, found, err := stripMCPTable(pre, "sl-dbg")
		if err != nil || found || out != pre {
			t.Fatalf("string/comment treated as table: %q; found=%v; err=%v", out, found, err)
		}
	}
}

func TestInstallTOML_QuotedIDAndNestedReplacement(t *testing.T) {
	target := agentTarget{name: "codex", kind: "toml", path: filepath.Join(t.TempDir(), "config.toml"), create: true}
	id := "debug.local"
	pre := "[mcp_servers.'debug.local'] # comment\ncommand = 'old'\n\nargs = []\n[mcp_servers.'debug.local'.env]\nOLD = 'value'\n[other]\nkeep = true\n"
	writeMCPFixture(t, target.path, pre)
	if err := installTOML(target, "/new", defaultArgs, id, false, false); err == nil {
		t.Fatal("quoted existing ID bypassed overwrite refusal")
	}
	if err := installTOML(target, "/new", defaultArgs, id, true, false); err != nil {
		t.Fatal(err)
	}
	out := readMCPFixture(t, target.path)
	if strings.Contains(out, "OLD") || strings.Contains(out, "'old'") || !strings.Contains(out, `[mcp_servers."debug.local"]`) {
		t.Fatalf("wrong replacement: %s", out)
	}
	if err := uninstallTOML(target, id, false); err != nil {
		t.Fatal(err)
	}
	if out := readMCPFixture(t, target.path); strings.Contains(out, "mcp_servers") || !strings.Contains(out, "keep = true") {
		t.Fatalf("wrong removal: %s", out)
	}
}

func TestMCPTable_RefusesUnsafeEdits(t *testing.T) {
	for _, pre := range []string{
		`mcp_servers = { sl-dbg = { command = "old" } }`,
		`mcp_servers.sl-dbg.command = "old"`,
		"[mcp_servers]\nsl-dbg = { command = \"old\" }\n",
		"[mcp_servers.sl-dbg\ncommand = \"old\"\n",
		"notes = \"\"\"\n[mcp_servers.sl-dbg]\n",
	} {
		t.Run(pre, func(t *testing.T) {
			target := agentTarget{name: "codex", kind: "toml", path: filepath.Join(t.TempDir(), "config.toml"), create: true}
			writeMCPFixture(t, target.path, pre)
			if err := installTOML(target, "/new", defaultArgs, "sl-dbg", true, false); err == nil {
				t.Fatal("install did not reject unsupported syntax")
			}
			if err := uninstallTOML(target, "sl-dbg", false); err == nil {
				t.Fatal("uninstall did not reject unsupported syntax")
			}
			if got := readMCPFixture(t, target.path); got != pre {
				t.Fatalf("config changed: %s", got)
			}
		})
	}
}

func TestAtomicWrite_PreservesDistinctBackupsAndSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath, link := filepath.Join(dir, "real.json"), filepath.Join(dir, "link.json")
	writeMCPFixture(t, realPath, "original")
	if err := os.Symlink(realPath, link); err != nil {
		t.Fatal(err)
	}
	for _, contents := range []string{"first", "second"} {
		if err := atomicWrite(link, []byte(contents), false); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Lstat(link)
	if err != nil || st.Mode()&os.ModeSymlink == 0 || readMCPFixture(t, realPath) != "second" {
		t.Fatalf("config symlink was replaced or target not updated: %v", err)
	}
	backups, err := filepath.Glob(realPath + ".bak.*")
	if err != nil || len(backups) != 2 {
		t.Fatalf("distinct backups missing: %v; %v", backups, err)
	}
	saved := map[string]bool{}
	for _, backup := range backups {
		saved[readMCPFixture(t, backup)] = true
	}
	if !saved["original"] || !saved["first"] {
		t.Fatalf("backups overwritten: %v", saved)
	}
}

func TestAtomicWrite_DanglingSymlinkIsNotReplaced(t *testing.T) {
	link := filepath.Join(t.TempDir(), "config.json")
	if err := os.Symlink("missing.json", link); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(link, []byte("{}"), true); err == nil {
		t.Fatal("expected symlink resolution error")
	}
	st, err := os.Lstat(link)
	if err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink was replaced: %v", err)
	}
}

func TestAtomicWrite_BackupFailureLeavesConfigUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("c", 240))
	writeMCPFixture(t, path, "original")
	// Appending the backup suffix exceeds the filesystem's per-component name limit.
	if err := atomicWrite(path, []byte("changed"), false); err == nil {
		t.Fatal("backup failure must abort the update")
	}
	if got := readMCPFixture(t, path); got != "original" {
		t.Fatalf("config changed without a backup: %q", got)
	}
}

func TestMCPTable_DryRunPreservesConfig(t *testing.T) {
	target := agentTarget{name: "codex", kind: "toml", path: filepath.Join(t.TempDir(), "config.toml"), create: true}
	pre := "[mcp_servers.sl-dbg]\ncommand = 'old'\n\nargs = []\n[other]\nkeep = true\n"
	writeMCPFixture(t, target.path, pre)
	if err := installTOML(target, "/new", defaultArgs, "sl-dbg", true, true); err != nil {
		t.Fatal(err)
	}
	if err := uninstallTOML(target, "sl-dbg", true); err != nil {
		t.Fatal(err)
	}
	if got := readMCPFixture(t, target.path); got != pre {
		t.Fatalf("dry run changed config: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(target.path))
	if len(entries) != 1 {
		t.Fatalf("dry run created files: %v", entries)
	}
}
func writeMCPFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readMCPFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func isolateMCPConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", filepath.Join(dir, "AppData"))
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Error(err)
		}
	})
	return dir
}
