package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newMCPInstallCmd registers sl-dbg as an MCP server with one of the
// supported AI agents by editing that agent's config file in place
// (atomic write + .bak backup, never silently destructive).
//
// This is the explicit opt-in path users prefer to a curl|bash script
// silently mutating files outside its own install directory.
func newMCPInstallCmd() *cobra.Command {
	var (
		printOnly     bool
		force         bool
		dryRun        bool
		serverID      string
		allowProgram  []string
		readOnly      bool
		insecure      bool
	)

	c := &cobra.Command{
		Use:   "install [agent]",
		Short: "Register sl-dbg with an MCP-aware agent (claude|cursor|vscode|codex|copilot|all)",
		Long: `Add sl-dbg to the chosen agent's MCP config so the next time the
agent starts it picks up every sl-dbg tool automatically.

Supported agents:
  claude    Claude Desktop (~/Library/Application Support/Claude/claude_desktop_config.json on macOS,
            %APPDATA%\Claude\claude_desktop_config.json on Windows,
            ~/.config/Claude/claude_desktop_config.json on Linux)
  cursor    Cursor (~/.cursor/mcp.json)
  vscode    VS Code workspace (./.vscode/mcp.json, created if missing)
  codex     Codex CLI (~/.codex/config.toml)
  copilot   GitHub Copilot CLI (~/.config/github-copilot/mcp.json)
  all       Run install for every detected agent on this machine

By default the registered command runs 'sl-dbg mcp --safe --allow-program *'.
That keeps the daemon in secure-by-default mode (source jail on, eval off,
session cap on, audit log on) while leaving the program allowlist
permissive enough that any 'debug_start' call succeeds. Tighten it with
--allow-program /path/to/your/program (repeatable). Use --read-only to
hide every mutating tool from the agent, or --insecure to register the
legacy permissive mode (NOT recommended — exports SL_DBG_INSECURE=1
inside the agent process).

The command does a read-merge-write with a timestamped .bak backup. If an
entry with the same server id already exists, the command refuses to
overwrite unless --force is passed. Use --print to dump the JSON snippet
without touching anything.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			binPath, err := os.Executable()
			if err != nil || binPath == "" {
				binPath = "sl-dbg"
			} else {
				binPath, _ = filepath.EvalSymlinks(binPath)
			}

			mcpArgs := buildMCPInvocationArgs(allowProgram, readOnly, insecure)

			if printOnly {
				return printMCPSnippets(binPath, serverID, mcpArgs)
			}

			if len(args) == 0 {
				return errors.New("agent name required: claude|cursor|vscode|codex|copilot|all")
			}
			agent := strings.ToLower(args[0])

			targets, err := resolveAgents(agent)
			if err != nil {
				return err
			}

			var anyOK bool
			for _, t := range targets {
				if err := installToAgent(t, binPath, mcpArgs, serverID, force, dryRun); err != nil {
					fmt.Fprintf(os.Stderr, "✗ %s: %v\n", t.name, err)
					continue
				}
				anyOK = true
			}
			if !anyOK {
				return errors.New("no agents updated")
			}
			if !insecure && containsString(mcpArgs, "*") {
				fmt.Fprintln(os.Stderr,
					"note: program allowlist is wide-open ('*'). Re-run with --allow-program /path/to/your/program to lock it down.")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&printOnly, "print", false, "print the JSON/TOML snippet for every agent; do not modify any file")
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing entry with the same server id")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change but do not write")
	c.Flags().StringVar(&serverID, "id", "sl-dbg", "MCP server id to register under")
	c.Flags().StringSliceVar(&allowProgram, "allow-program", nil, "program glob to permit for debug_start (repeatable; default '*')")
	c.Flags().BoolVar(&readOnly, "read-only", false, "register the MCP server in --read-only mode (hides every mutating tool)")
	c.Flags().BoolVar(&insecure, "insecure", false, "register in legacy permissive mode (sets SL_DBG_INSECURE=1; NOT recommended)")
	return c
}

// buildMCPInvocationArgs assembles the argv that the registered agent
// command will invoke. We always pass --safe so the daemon stays in
// secure-by-default mode even when the user added the registration via
// a one-liner; --allow-program defaults to '*' which keeps debug_start
// working while still exporting SL_DBG_ALLOW_PROGRAM so the operator
// sees a clear "lock me down" knob to tighten.
func buildMCPInvocationArgs(allowProgram []string, readOnly, insecure bool) []string {
	if insecure {
		// legacy permissive — caller opted out of --safe; SL_DBG_INSECURE is
		// the explicit acknowledgement the daemon insists on.
		out := []string{"mcp"}
		if readOnly {
			out = append(out, "--read-only")
		}
		return out
	}
	out := []string{"mcp", "--safe"}
	if readOnly {
		out = append(out, "--read-only")
	}
	if len(allowProgram) == 0 {
		out = append(out, "--allow-program", "*")
	} else {
		for _, p := range allowProgram {
			out = append(out, "--allow-program", p)
		}
	}
	return out
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// newMCPUninstallCmd removes the sl-dbg entry from an agent's MCP config.
// It is the exact inverse of newMCPInstallCmd: it touches only the named
// server id, leaves every other key intact, and writes a .bak backup
// before mutating anything.
func newMCPUninstallCmd() *cobra.Command {
	var (
		dryRun   bool
		serverID string
	)
	c := &cobra.Command{
		Use:   "uninstall [agent]",
		Short: "Remove sl-dbg from an MCP-aware agent's config (claude|cursor|vscode|codex|copilot|all)",
		Long: `Remove the sl-dbg server entry from the chosen agent's MCP config.

Only the entry with the matching server id (default "sl-dbg") is removed;
every other key in the config file is preserved. A timestamped .bak of
the prior file is written before any change.

Pass --dry-run to preview what would change without touching disk.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("agent name required: claude|cursor|vscode|codex|copilot|all")
			}
			targets, err := resolveAgents(strings.ToLower(args[0]))
			if err != nil {
				return err
			}
			var anyOK bool
			for _, t := range targets {
				if err := uninstallFromAgent(t, serverID, dryRun); err != nil {
					fmt.Fprintf(os.Stderr, "✗ %s: %v\n", t.name, err)
					continue
				}
				anyOK = true
			}
			if !anyOK {
				return errors.New("no agents updated")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change but do not write")
	c.Flags().StringVar(&serverID, "id", "sl-dbg", "MCP server id to remove")
	return c
}

func uninstallFromAgent(t agentTarget, serverID string, dryRun bool) error {
	switch t.kind {
	case "json":
		return uninstallJSON(t, serverID, dryRun)
	case "toml":
		return uninstallTOML(t, serverID, dryRun)
	default:
		return fmt.Errorf("unsupported config kind %q", t.kind)
	}
}

func uninstallJSON(t agentTarget, serverID string, dryRun bool) error {
	data, err := os.ReadFile(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("• %s: %s does not exist, nothing to do\n", t.name, t.path)
			return nil
		}
		return err
	}
	doc := map[string]interface{}{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("existing config is not valid JSON: %w", err)
	}
	servers, _ := doc["mcpServers"].(map[string]interface{})
	if servers == nil {
		fmt.Printf("• %s: no mcpServers block in %s, nothing to do\n", t.name, t.path)
		return nil
	}
	if _, ok := servers[serverID]; !ok {
		fmt.Printf("• %s: %q not present in %s, nothing to do\n", t.name, serverID, t.path)
		return nil
	}
	delete(servers, serverID)
	if len(servers) == 0 {
		delete(doc, "mcpServers")
	} else {
		doc["mcpServers"] = servers
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	if dryRun {
		fmt.Printf("# would write %s\n%s\n", t.path, string(out))
		return nil
	}
	if err := atomicWrite(t.path, out, false); err != nil {
		return err
	}
	fmt.Printf("✓ %s: removed %q from %s\n", t.name, serverID, t.path)
	return nil
}

func uninstallTOML(t agentTarget, serverID string, dryRun bool) error {
	data, err := os.ReadFile(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("• %s: %s does not exist, nothing to do\n", t.name, t.path)
			return nil
		}
		return err
	}
	header := fmt.Sprintf("[mcp_servers.%s]", serverID)
	if !strings.Contains(string(data), header) {
		fmt.Printf("• %s: %q not present in %s, nothing to do\n", t.name, serverID, t.path)
		return nil
	}
	out := removeTOMLStanza(string(data), header)
	// Collapse 3+ consecutive blank lines that may result from the removal.
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	if dryRun {
		fmt.Printf("# would write %s\n%s\n", t.path, out)
		return nil
	}
	if err := atomicWrite(t.path, []byte(out), false); err != nil {
		return err
	}
	fmt.Printf("✓ %s: removed %q from %s\n", t.name, serverID, t.path)
	return nil
}

// ---------------------------------------------------------------------------
// agents
// ---------------------------------------------------------------------------

type agentTarget struct {
	name   string // user-facing name
	kind   string // "json" or "toml"
	path   string // resolved absolute config path
	create bool   // true if the file may be created when absent
}

func resolveAgents(agent string) ([]agentTarget, error) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	all := map[string]agentTarget{
		"claude":  {name: "claude", kind: "json", path: claudeDesktopConfigPath(home), create: true},
		"cursor":  {name: "cursor", kind: "json", path: filepath.Join(home, ".cursor", "mcp.json"), create: true},
		"vscode":  {name: "vscode", kind: "json", path: filepath.Join(cwd, ".vscode", "mcp.json"), create: true},
		"codex":   {name: "codex", kind: "toml", path: filepath.Join(home, ".codex", "config.toml"), create: true},
		"copilot": {name: "copilot", kind: "json", path: filepath.Join(home, ".config", "github-copilot", "mcp.json"), create: true},
	}

	if agent == "all" {
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var detected []agentTarget
		for _, k := range keys {
			t := all[k]
			// For "all", only include agents whose parent dir exists — avoids
			// creating ~/.cursor on machines without Cursor installed.
			if parentDirExists(t.path) {
				detected = append(detected, t)
			}
		}
		if len(detected) == 0 {
			return nil, errors.New("no known MCP-aware agents detected on this machine; pass one explicitly (claude|cursor|vscode|codex|copilot)")
		}
		return detected, nil
	}

	t, ok := all[agent]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (want claude|cursor|vscode|codex|copilot|all)", agent)
	}
	return []agentTarget{t}, nil
}

func claudeDesktopConfigPath(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			return filepath.Join(ad, "Claude", "claude_desktop_config.json")
		}
		return filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

func parentDirExists(p string) bool {
	st, err := os.Stat(filepath.Dir(p))
	return err == nil && st.IsDir()
}

// ---------------------------------------------------------------------------
// install
// ---------------------------------------------------------------------------

func installToAgent(t agentTarget, binPath string, mcpArgs []string, serverID string, force, dryRun bool) error {
	switch t.kind {
	case "json":
		return installJSON(t, binPath, mcpArgs, serverID, force, dryRun)
	case "toml":
		return installTOML(t, binPath, mcpArgs, serverID, force, dryRun)
	default:
		return fmt.Errorf("unsupported config kind %q", t.kind)
	}
}

// installJSON handles Claude / Cursor / VS Code / Copilot — all use the
// same "mcpServers" object shape.
func installJSON(t agentTarget, binPath string, mcpArgs []string, serverID string, force, dryRun bool) error {
	doc := map[string]interface{}{}
	if data, err := os.ReadFile(t.path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("existing config is not valid JSON: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	servers, _ := doc["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	if _, exists := servers[serverID]; exists && !force {
		return fmt.Errorf("server id %q already present in %s (use --force to overwrite)", serverID, t.path)
	}

	entry := map[string]interface{}{
		"command": binPath,
		"args":    mcpArgs,
	}
	if !containsString(mcpArgs, "--safe") {
		// legacy / --insecure path — surface the escape hatch in the
		// agent's own env block so users can audit why eval is on.
		entry["env"] = map[string]interface{}{"SL_DBG_INSECURE": "1"}
	}
	servers[serverID] = entry
	doc["mcpServers"] = servers

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	if dryRun {
		fmt.Printf("# would write %s\n%s\n", t.path, string(out))
		return nil
	}
	if err := atomicWrite(t.path, out, t.create); err != nil {
		return err
	}
	fmt.Printf("✓ %s: registered %q in %s\n", t.name, serverID, t.path)
	return nil
}

// installTOML handles the Codex CLI ~/.codex/config.toml format which
// uses [mcp_servers.<id>] tables.
//
// We avoid pulling in a TOML library by appending a well-formed stanza;
// if an existing stanza for the same id is found we either refuse (no
// --force) or replace it.
func installTOML(t agentTarget, binPath string, mcpArgs []string, serverID string, force, dryRun bool) error {
	existing, err := os.ReadFile(t.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	header := fmt.Sprintf("[mcp_servers.%s]", serverID)
	argsTOML, _ := json.Marshal(mcpArgs)
	stanza := fmt.Sprintf("%s\ncommand = %q\nargs = %s\n", header, binPath, string(argsTOML))
	if !containsString(mcpArgs, "--safe") {
		stanza += "env = { SL_DBG_INSECURE = \"1\" }\n"
	}

	contents := string(existing)
	if strings.Contains(contents, header) {
		if !force {
			return fmt.Errorf("server id %q already present in %s (use --force to overwrite)", serverID, t.path)
		}
		contents = removeTOMLStanza(contents, header)
	}

	if contents != "" && !strings.HasSuffix(contents, "\n") {
		contents += "\n"
	}
	if contents != "" {
		contents += "\n"
	}
	contents += stanza

	if dryRun {
		fmt.Printf("# would write %s\n%s\n", t.path, contents)
		return nil
	}
	if err := atomicWrite(t.path, []byte(contents), t.create); err != nil {
		return err
	}
	fmt.Printf("✓ %s: registered %q in %s\n", t.name, serverID, t.path)
	return nil
}

// removeTOMLStanza strips the named table header and every contiguous
// non-blank, non-header line below it. Good enough for our minimal
// flat-key writes; we never produce nested tables.
func removeTOMLStanza(s, header string) string {
	lines := strings.Split(s, "\n")
	var out []string
	skip := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == header {
			skip = true
			continue
		}
		if skip {
			if trim == "" || strings.HasPrefix(trim, "[") {
				skip = false
				// fall through so this line is kept
			} else {
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// ---------------------------------------------------------------------------
// IO helpers
// ---------------------------------------------------------------------------

func atomicWrite(path string, data []byte, createIfMissing bool) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if !createIfMissing {
			return fmt.Errorf("parent dir %s does not exist", dir)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// Best-effort backup of any existing file.
	if cur, err := os.ReadFile(path); err == nil && len(cur) > 0 {
		bak := fmt.Sprintf("%s.bak.%s", path, time.Now().UTC().Format("20060102T150405"))
		_ = os.WriteFile(bak, cur, 0o600)
	}
	tmp, err := os.CreateTemp(dir, ".sl-dbg-mcp-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// ---------------------------------------------------------------------------
// --print
// ---------------------------------------------------------------------------

func printMCPSnippets(binPath, serverID string, mcpArgs []string) error {
	entry := map[string]interface{}{
		"command": binPath,
		"args":    mcpArgs,
	}
	if !containsString(mcpArgs, "--safe") {
		entry["env"] = map[string]interface{}{"SL_DBG_INSECURE": "1"}
	}
	jsonSnippet, _ := json.MarshalIndent(map[string]interface{}{
		"mcpServers": map[string]interface{}{serverID: entry},
	}, "", "  ")
	argsTOML, _ := json.Marshal(mcpArgs)
	toml := fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = %s\n", serverID, binPath, string(argsTOML))
	if !containsString(mcpArgs, "--safe") {
		toml += "env = { SL_DBG_INSECURE = \"1\" }\n"
	}

	fmt.Println("# Claude Desktop / Cursor / VS Code / Copilot CLI (JSON)")
	fmt.Println(string(jsonSnippet))
	fmt.Println()
	fmt.Println("# Codex CLI (TOML — append to ~/.codex/config.toml)")
	fmt.Println(toml)
	return nil
}
