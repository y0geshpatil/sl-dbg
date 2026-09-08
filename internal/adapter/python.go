package adapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// PythonPath resolves the target interpreter independently of the adapter venv.
func PythonPath() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if py, err := exec.LookPath(name); err == nil {
			return py, nil
		}
	}
	return "", fmt.Errorf("Python 3 not found in PATH")
}

func PythonVenvDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "sl-dbg", "adapters", "python"), nil
}

func PythonAdapterPath() (string, error) {
	var candidates []string
	if dir, err := PythonVenvDir(); err == nil {
		candidates = append(candidates, filepath.Join(dir, "bin", "python"))
	}
	for _, name := range []string{"python3", "python"} {
		if py, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, py)
		}
	}
	for _, py := range candidates {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := exec.CommandContext(ctx, py, "-c", "import sys; assert sys.version_info[0] == 3; import debugpy").Run()
		cancel()
		if err == nil {
			return py, nil
		}
	}
	return "", fmt.Errorf("debugpy not importable; run `sl-dbg install-adapter python`")
}

func init() {
	Register(Spec{
		Lang:      "python",
		AdapterID: "debugpy",
		Detect:    PythonAdapterPath,
		LaunchAdapter: func() ([]string, Transport, error) {
			py, err := PythonAdapterPath()
			if err != nil {
				return nil, TransportStdio, err
			}
			return []string{py, "-m", "debugpy.adapter"}, TransportStdio, nil
		},
		BuildLaunchArgs: func(cfg LaunchCfg) (map[string]interface{}, error) {
			if cfg.Program == "" {
				return nil, fmt.Errorf("python launch requires --program")
			}
			py, err := PythonPath()
			if err != nil {
				return nil, err
			}
			args := map[string]interface{}{
				"name":        "sl-dbg launch",
				"type":        "python",
				"request":     "launch",
				"program":     cfg.Program,
				"console":     "internalConsole",
				"stopOnEntry": cfg.StopOnEntry,
				"justMyCode":  true,
				"python":      py,
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
			if cfg.Port == 0 && cfg.PID == 0 {
				return nil, fmt.Errorf("python attach requires --port or --pid")
			}
			args := map[string]interface{}{
				"name":    "sl-dbg attach",
				"type":    "python",
				"request": "attach",
			}
			if cfg.Port != 0 {
				args["connect"] = map[string]interface{}{
					"host": cfgHost(cfg.Host),
					"port": cfg.Port,
				}
			} else {
				args["processId"] = cfg.PID
			}
			return args, nil
		},
		InstallHint: "Run `sl-dbg install-adapter python` (installs debugpy in an isolated virtual environment).",
	})
}

func cfgHost(h string) string {
	if h == "" {
		return "127.0.0.1"
	}
	return h
}
