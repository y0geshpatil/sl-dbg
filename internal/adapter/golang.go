package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// GoBinDir includes settings persisted with `go env -w`, not just shell exports.
func GoBinDir() string {
	if bin := os.Getenv("GOBIN"); bin != "" {
		return bin
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "env", "-json", "GOBIN", "GOPATH").Output()
	var env struct{ GOBIN, GOPATH string }
	if err == nil && json.Unmarshal(out, &env) == nil {
		if env.GOBIN != "" {
			return env.GOBIN
		}
		if paths := filepath.SplitList(env.GOPATH); len(paths) > 0 {
			return filepath.Join(paths[0], "bin")
		}
	}
	if paths := filepath.SplitList(os.Getenv("GOPATH")); len(paths) > 0 {
		return filepath.Join(paths[0], "bin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "bin")
}

// goInstallDirs returns the candidate directories where `go install` may have
// placed binaries: $GOBIN, then each $GOPATH/bin, then ~/go/bin as the
// documented fallback. Used by the go adapter Detect to avoid the
// PATH-vs-GOPATH/bin mismatch in #44.
func goInstallDirs() []string {
	var dirs []string
	if dir := GoBinDir(); dir != "" {
		dirs = append(dirs, dir)
	}
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

func DelvePath() (string, error) {
	if p, err := exec.LookPath("dlv"); err == nil {
		return p, nil
	}
	for _, dir := range goInstallDirs() {
		if !filepath.IsAbs(dir) {
			continue
		}
		if p, err := exec.LookPath(filepath.Join(dir, "dlv")); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("executable dlv not found in PATH or Go install directories; run `sl-dbg install-adapter go`")
}

// Go adapter: wraps `dlv dap` (Delve).
//
// Delve speaks DAP over TCP via `dlv dap`.
//
// Install: `go install github.com/go-delve/delve/cmd/dlv@latest`
func init() {
	Register(Spec{
		Lang:      "go",
		AdapterID: "go",
		Detect:    DelvePath,
		LaunchAdapter: func() ([]string, Transport, error) {
			p, err := DelvePath()
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
