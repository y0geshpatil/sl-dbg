package adapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// goInstallDirs returns the candidate directories where `go install` may have
// placed binaries: $GOBIN, then each $GOPATH/bin, then ~/go/bin as the
// documented fallback. Used by the go adapter Detect to avoid the
// PATH-vs-GOPATH/bin mismatch in #44.
func goInstallDirs() []string {
	var dirs []string
	if v := os.Getenv("GOBIN"); v != "" {
		dirs = append(dirs, v)
	}
	if v := os.Getenv("GOPATH"); v != "" {
		for _, p := range filepath.SplitList(v) {
			dirs = append(dirs, filepath.Join(p, "bin"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}
	return dirs
}

// Go adapter: wraps `dlv dap` (Delve).
//
// Delve speaks DAP natively via `dlv dap`. By default it listens on a TCP
// port; with `--client-addr=stdio` it speaks over stdio. We prefer stdio
// to match the rest of sl-dbg's adapter handling.
//
// Install: `go install github.com/go-delve/delve/cmd/dlv@latest`
func init() {
	Register(Spec{
		Lang:      "go",
		AdapterID: "go",
		Detect: func() (string, error) {
			// Issue #44: `go install` writes binaries to $GOBIN, $GOPATH/bin,
			// or ~/go/bin. Those locations aren't always on PATH (especially
			// in newer Go installs where users rely on `go run`). Fall back
			// to the standard install dirs so `install-adapter go` and
			// `adapters` agree.
			if p, err := exec.LookPath("dlv"); err == nil {
				return p, nil
			}
			for _, dir := range goInstallDirs() {
				cand := filepath.Join(dir, "dlv")
				if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
					return cand, nil
				}
			}
			return "", fmt.Errorf("dlv not found in PATH or in $GOBIN / $GOPATH/bin / ~/go/bin")
		},
		LaunchAdapter: func() ([]string, Transport, error) {
			p, err := exec.LookPath("dlv")
			if err != nil {
				for _, dir := range goInstallDirs() {
					cand := filepath.Join(dir, "dlv")
					if fi, statErr := os.Stat(cand); statErr == nil && !fi.IsDir() {
						p = cand
						err = nil
						break
					}
				}
			}
			if err != nil {
				return nil, TransportStdio, err
			}
			// dlv dap is TCP only. Use {PORT} placeholder; session.startAdapter
			// substitutes a free port and connects via TCP.
			return []string{p, "dap", "--listen=127.0.0.1:{PORT}"}, TransportTCPListen, nil
		},
		BuildLaunchArgs: func(cfg LaunchCfg) (map[string]interface{}, error) {
			if cfg.Program == "" {
				return nil, fmt.Errorf("go launch requires --program (path to a .go file, package, or built binary)")
			}
			args := map[string]interface{}{
				"name":        "sl-dbg launch",
				"type":        "go",
				"request":     "launch",
				"mode":        "debug", // build & debug the package/file
				"program":     cfg.Program,
				"stopOnEntry": cfg.StopOnEntry,
			}
			if len(cfg.Args) > 0 {
				args["args"] = cfg.Args
			}
			if cfg.Cwd != "" {
				args["cwd"] = cfg.Cwd
			}
			if len(cfg.Env) > 0 {
				envMap := map[string]string{}
				for _, kv := range cfg.Env {
					for i := 0; i < len(kv); i++ {
						if kv[i] == '=' {
							envMap[kv[:i]] = kv[i+1:]
							break
						}
					}
				}
				args["env"] = envMap
			}
			return args, nil
		},
		BuildAttachArgs: func(cfg AttachCfg) (map[string]interface{}, error) {
			if cfg.PID == 0 && cfg.Port == 0 {
				return nil, fmt.Errorf("go attach requires --pid (local) or --port (headless dlv)")
			}
			args := map[string]interface{}{
				"name":    "sl-dbg attach",
				"type":    "go",
				"request": "attach",
			}
			if cfg.PID != 0 {
				args["mode"] = "local"
				args["processId"] = cfg.PID
			} else {
				args["mode"] = "remote"
				args["host"] = cfgHost(cfg.Host)
				args["port"] = cfg.Port
			}
			return args, nil
		},
		InstallHint: "go install github.com/go-delve/delve/cmd/dlv@latest",
	})
}
