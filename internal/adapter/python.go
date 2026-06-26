package adapter

import (
	"fmt"
	"os/exec"
)

func init() {
	Register(Spec{
		Lang:      "python",
		AdapterID: "debugpy",
		Detect: func() (string, error) {
			py, err := exec.LookPath("python3")
			if err != nil {
				py, err = exec.LookPath("python")
				if err != nil {
					return "", fmt.Errorf("python not found in PATH")
				}
			}
			// Probe: python -c "import debugpy"
			cmd := exec.Command(py, "-c", "import debugpy")
			if out, err := cmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("debugpy not importable from %s: %s", py, string(out))
			}
			return py, nil
		},
		LaunchAdapter: func() ([]string, string, error) {
			py, err := exec.LookPath("python3")
			if err != nil {
				py, err = exec.LookPath("python")
				if err != nil {
					return nil, "", err
				}
			}
			return []string{py, "-m", "debugpy.adapter"}, "stdio", nil
		},
		BuildLaunchArgs: func(cfg LaunchCfg) (map[string]interface{}, error) {
			if cfg.Program == "" {
				return nil, fmt.Errorf("python launch requires --program")
			}
			py, _ := exec.LookPath("python3")
			if py == "" {
				py, _ = exec.LookPath("python")
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
		InstallHint: "pip install --user debugpy",
	})
}

func cfgHost(h string) string {
	if h == "" {
		return "127.0.0.1"
	}
	return h
}
