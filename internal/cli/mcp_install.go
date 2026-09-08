package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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
		printOnly    bool
		force        bool
		dryRun       bool
		serverID     string
		allowProgram []string
		readOnly     bool
		insecure     bool
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
  copilot   GitHub Copilot CLI (~/.copilot/mcp-config.json)
  all       Run install for every detected agent on this machine

By default the registered command runs 'sl-dbg mcp --safe', and the daemon
auto-discovers a tight program allowlist from PATH (java, python3, node,
dlv). That keeps the daemon in secure-by-default mode (source jail on,
eval off, session cap on, audit log on) while still letting common
debugging workflows succeed. Tighten or extend it with --allow-program
/path/to/your/program (repeatable). Use --read-only to hide every mutating
tool from the agent, or --insecure to register the legacy permissive mode
(NOT recommended — exports SL_DBG_INSECURE=1 inside the agent process).

The command does a read-merge-write with a timestamped .bak backup. If an
entry with the same server id already exists, the command refuses to
overwrite unless --force is passed. Use --print to dump the JSON snippet
without touching anything.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			binPath, err := os.Executable()
			if err != nil || binPath == "" {
				binPath = "sl-dbg"
			} else if resolved, err := filepath.EvalSymlinks(binPath); err == nil {
				binPath = resolved
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

			var failures []error
			for _, t := range targets {
				if err := installToAgent(t, binPath, mcpArgs, serverID, force, dryRun); err != nil {
					fmt.Fprintf(os.Stderr, "✗ %s: %v\n", t.name, err)
					failures = append(failures, fmt.Errorf("%s: %w", t.name, err))
					continue
				}
			}
			if len(failures) > 0 {
				return errors.Join(failures...)
			}
			if !insecure && len(allowProgram) == 0 {
				fmt.Fprintln(os.Stderr,
					"note: --allow-program omitted; the daemon will auto-discover (java, python3, node, dlv) on PATH. Re-run with --allow-program /path/to/your/program to lock it down further.")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&printOnly, "print", false, "print the JSON/TOML snippet for every agent; do not modify any file")
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing entry with the same server id")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change but do not write")
	c.Flags().StringVar(&serverID, "id", "sl-dbg", "MCP server id to register under")
	c.Flags().StringSliceVar(&allowProgram, "allow-program", nil, "program glob to permit for debug_start (repeatable; default: auto-discover on PATH)")
	c.Flags().BoolVar(&readOnly, "read-only", false, "register the MCP server in --read-only mode (hides every mutating tool)")
	c.Flags().BoolVar(&insecure, "insecure", false, "register in legacy permissive mode (sets SL_DBG_INSECURE=1; NOT recommended)")
	return c
}

// buildMCPInvocationArgs assembles the argv that the registered agent
// command will invoke. We always pass --safe so the daemon stays in
// secure-by-default mode even when the user added the registration via
// a one-liner. When the caller does not pass --allow-program, we omit it
// entirely so the daemon's auto-discovery (issue #70) picks a tight
// allowlist from PATH — a strict improvement over the v0.5.x '*' default.
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
	for _, p := range allowProgram {
		out = append(out, "--allow-program", p)
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

With "all", no detected agents is a successful no-op.
Pass --dry-run to preview what would change without touching disk.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("agent name required: claude|cursor|vscode|codex|copilot|all")
			}
			targets, err := resolveAgents(strings.ToLower(args[0]))
			if errors.Is(err, errNoDetectedAgents) {
				fmt.Fprintln(cmd.OutOrStdout(), "• no MCP-aware agents detected; nothing to uninstall")
				return nil
			}
			if err != nil {
				return err
			}
			var failures []error
			for _, t := range targets {
				if err := uninstallFromAgent(t, serverID, dryRun); err != nil {
					fmt.Fprintf(os.Stderr, "✗ %s: %v\n", t.name, err)
					failures = append(failures, fmt.Errorf("%s: %w", t.name, err))
					continue
				}
			}
			return errors.Join(failures...)
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
	doc, servers, err := readMCPJSON(data, t.serverKey())
	if err != nil {
		return err
	}
	if servers == nil {
		fmt.Printf("• %s: no %s block in %s, nothing to do\n", t.name, t.serverKey(), t.path)
		return nil
	}
	if _, ok := servers[serverID]; !ok {
		fmt.Printf("• %s: %q not present in %s, nothing to do\n", t.name, serverID, t.path)
		return nil
	}
	delete(servers, serverID)
	if len(servers) == 0 {
		delete(doc, t.serverKey())
	} else {
		doc[t.serverKey()], _ = json.Marshal(servers)
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
	out, found, err := stripMCPTable(string(data), serverID)
	if err != nil {
		return err
	}
	if !found {
		fmt.Printf("• %s: %q not present in %s, nothing to do\n", t.name, serverID, t.path)
		return nil
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

var errNoDetectedAgents = errors.New("no known MCP-aware agents detected on this machine")

type agentTarget struct {
	name   string // user-facing name
	kind   string // "json" or "toml"
	path   string // resolved absolute config path
	create bool   // true if the file may be created when absent
}

func (t agentTarget) serverKey() string {
	if t.name == "vscode" {
		return "servers"
	}
	return "mcpServers"
}

func resolveAgents(agent string) ([]agentTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	all := map[string]agentTarget{
		"claude":  {name: "claude", kind: "json", path: claudeDesktopConfigPath(home), create: true},
		"cursor":  {name: "cursor", kind: "json", path: filepath.Join(home, ".cursor", "mcp.json"), create: true},
		"vscode":  {name: "vscode", kind: "json", path: filepath.Join(cwd, ".vscode", "mcp.json"), create: true},
		"codex":   {name: "codex", kind: "toml", path: filepath.Join(home, ".codex", "config.toml"), create: true},
		"copilot": {name: "copilot", kind: "json", path: filepath.Join(home, ".copilot", "mcp-config.json"), create: true},
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
			return nil, fmt.Errorf("%w; pass one explicitly (claude|cursor|vscode|codex|copilot) or use --print for manual configuration", errNoDetectedAgents)
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

func readMCPJSON(data []byte, key string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("existing config is not valid JSON: %w", err)
	}
	if doc == nil {
		return nil, nil, errors.New("existing config must be a JSON object, not null")
	}
	var servers map[string]json.RawMessage
	if raw, exists := doc[key]; exists {
		if err := json.Unmarshal(raw, &servers); err != nil || servers == nil {
			return nil, nil, fmt.Errorf("existing %s must be a JSON object", key)
		}
	}
	return doc, servers, nil
}

func installJSON(t agentTarget, binPath string, mcpArgs []string, serverID string, force, dryRun bool) error {
	doc := map[string]json.RawMessage{}
	var servers map[string]json.RawMessage
	if data, err := os.ReadFile(t.path); err == nil {
		doc, servers, err = readMCPJSON(data, t.serverKey())
		if err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	if servers == nil {
		servers = map[string]json.RawMessage{}
	}
	if _, exists := servers[serverID]; exists && !force {
		return fmt.Errorf("server id %q already present in %s (use --force to overwrite)", serverID, t.path)
	}

	servers[serverID], _ = json.Marshal(mcpJSONEntry(t.name, binPath, mcpArgs))
	doc[t.serverKey()], _ = json.Marshal(servers)

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

	contents, found, err := stripMCPTable(string(existing), serverID)
	if err != nil {
		return err
	}
	if found {
		if !force {
			return fmt.Errorf("server id %q already present in %s (use --force to overwrite)", serverID, t.path)
		}
	}

	if contents != "" && !strings.HasSuffix(contents, "\n") {
		contents += "\n"
	}
	if contents != "" {
		contents += "\n"
	}
	contents += mcpTOMLStanza(binPath, serverID, mcpArgs)

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

func tomlKey(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) == -1 {
		return s
	}
	quoted, _ := json.Marshal(s)
	return string(quoted)
}

func mcpTOMLStanza(binPath, serverID string, mcpArgs []string) string {
	command, _ := json.Marshal(binPath)
	args, _ := json.Marshal(mcpArgs)
	stanza := fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = %s\n", tomlKey(serverID), command, args)
	if !containsString(mcpArgs, "--safe") {
		stanza += "env = { SL_DBG_INSECURE = \"1\" }\n"
	}
	return stanza
}

// Edit table spans rather than reserializing the document, preserving unrelated comments.
func stripMCPTable(s, serverID string) (string, bool, error) {
	var out strings.Builder
	var multiline string
	var table []string
	skip, found := false, false
	for _, line := range strings.SplitAfter(s, "\n") {
		trim := strings.TrimSpace(line)
		if multiline == "" && strings.HasPrefix(trim, "[") {
			keys, err := tomlTableKeys(trim)
			if err != nil {
				return "", false, fmt.Errorf("cannot safely edit TOML table: %w", err)
			}
			table = keys
			skip = len(keys) >= 2 && keys[0] == "mcp_servers" && keys[1] == serverID
			found = found || skip
		} else if multiline == "" {
			if before, _, ok := strings.Cut(trim, "="); ok {
				// Inline/dotted registrations need a full TOML editor; never append a conflicting table.
				keys, err := tomlTableKeys("[" + strings.TrimSpace(before) + "]")
				if err == nil && len(keys) > 0 &&
					(len(table) == 0 && keys[0] == "mcp_servers" ||
						len(table) == 1 && table[0] == "mcp_servers" && keys[0] == serverID) {
					return "", false, errors.New("inline or dotted MCP registrations must be edited manually")
				}
			}
		}
		if !skip {
			out.WriteString(line)
		}
		multiline = tomlMultilineState(line, multiline)
	}
	if multiline != "" {
		return "", false, errors.New("existing TOML has an unterminated multiline string")
	}
	return out.String(), found, nil
}

func tomlTableKeys(line string) ([]string, error) {
	s := strings.TrimSpace(strings.TrimPrefix(line, "["))
	array := strings.HasPrefix(s, "[")
	if array {
		s = strings.TrimSpace(s[1:])
	}
	var keys []string
	for s != "" {
		var key string
		if s[0] == '"' || s[0] == '\'' {
			quote, end := s[0], 1
			for end < len(s) && s[end] != quote {
				if quote == '"' && s[end] == '\\' {
					end++
				}
				end++
			}
			if end >= len(s) {
				break
			}
			key = s[1:end]
			if quote == '"' {
				var err error
				key, err = strconv.Unquote(s[:end+1])
				if err != nil {
					return nil, err
				}
			}
			s = s[end+1:]
		} else {
			end := strings.IndexAny(s, ".] \t")
			if end <= 0 {
				break
			}
			key, s = s[:end], s[end:]
			if tomlKey(key) != key {
				break
			}
		}
		keys = append(keys, key)
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, ".") {
			s = strings.TrimSpace(s[1:])
			continue
		}
		close := "]"
		if array {
			close = "]]"
		}
		if strings.HasPrefix(s, close) {
			rest := strings.TrimSpace(strings.TrimPrefix(s, close))
			if rest == "" || strings.HasPrefix(rest, "#") {
				return keys, nil
			}
		}
		break
	}
	return nil, errors.New("unsupported or malformed table header")
}

func tomlMultilineState(line, state string) string {
	for i := 0; i < len(line); i++ {
		if state != "" {
			if state == `"""` && line[i] == '\\' {
				i++
			} else if strings.HasPrefix(line[i:], state) {
				quote := state[0]
				for i+1 < len(line) && line[i+1] == quote {
					i++
				}
				state = ""
			}
			continue
		}
		if line[i] == '#' {
			break
		}
		if line[i] != '"' && line[i] != '\'' {
			continue
		}
		quote := line[i]
		triple := strings.Repeat(string(quote), 3)
		if strings.HasPrefix(line[i:], triple) {
			state = triple
			i += 2
			continue
		}
		for i++; i < len(line) && line[i] != quote; i++ {
			if quote == '"' && line[i] == '\\' {
				i++
			}
		}
	}
	return state
}

// ---------------------------------------------------------------------------
// IO helpers
// ---------------------------------------------------------------------------

func atomicWrite(path string, data []byte, createIfMissing bool) error {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve config symlink: %w", err)
		}
		path = resolved
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if !createIfMissing {
			return fmt.Errorf("parent dir %s does not exist", dir)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if cur, err := os.ReadFile(path); err == nil {
		bak, err := os.CreateTemp(dir, filepath.Base(path)+".bak."+time.Now().UTC().Format("20060102T150405")+".*")
		if err != nil {
			return fmt.Errorf("create config backup: %w", err)
		}
		_, writeErr := bak.Write(cur)
		closeErr := bak.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			_ = os.Remove(bak.Name())
			return fmt.Errorf("write config backup: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sl-dbg-mcp-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
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

func mcpJSONEntry(agent, binPath string, mcpArgs []string) map[string]interface{} {
	entry := map[string]interface{}{
		"command": binPath,
		"args":    mcpArgs,
	}
	if agent == "copilot" {
		entry["type"] = "local"
		entry["tools"] = []string{"*"}
	} else if agent == "vscode" {
		entry["type"] = "stdio"
	}
	if !containsString(mcpArgs, "--safe") {
		entry["env"] = map[string]interface{}{"SL_DBG_INSECURE": "1"}
	}
	return entry
}

func printMCPSnippets(binPath, serverID string, mcpArgs []string) error {
	for _, agent := range []struct{ name, label string }{
		{"claude", "Claude Desktop / Cursor (JSON)"},
		{"vscode", "VS Code workspace (.vscode/mcp.json)"},
		{"copilot", "Copilot CLI (~/.copilot/mcp-config.json)"},
	} {
		key := (agentTarget{name: agent.name}).serverKey()
		snippet, _ := json.MarshalIndent(map[string]interface{}{
			key: map[string]interface{}{serverID: mcpJSONEntry(agent.name, binPath, mcpArgs)},
		}, "", "  ")
		fmt.Println("# " + agent.label)
		fmt.Println(string(snippet))
		fmt.Println()
	}
	fmt.Println("# Codex CLI (TOML — append to ~/.codex/config.toml)")
	fmt.Println(mcpTOMLStanza(binPath, serverID, mcpArgs))
	return nil
}
