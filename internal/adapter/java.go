package adapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Java adapter: wraps Microsoft's java-debug.
//
// Two ways to find java-debug.jar:
//   1. SL_DBG_JAVA_DEBUG_JAR env var
//   2. ~/.cache/sl-dbg/adapters/java-debug.jar  (auto-downloaded — Phase 5 work)
//
// java-debug uses STDIO transport when launched as:
//   java -cp java-debug.jar com.microsoft.java.debug.core.adapter.JdiDebugAdapter
//
// For attach mode, we forward host:port to JDWP via DAP attach args.
func init() {
	Register(Spec{
		Lang:      "java",
		AdapterID: "java",
		Detect: func() (string, error) {
			jar := javaDebugJarPath()
			if jar == "" {
				return "", fmt.Errorf("java-debug jar not found")
			}
			if _, err := exec.LookPath("java"); err != nil {
				return "", fmt.Errorf("`java` not in PATH")
			}
			if _, err := os.Stat(jar); err != nil {
				return "", fmt.Errorf("jar missing at %s: %w", jar, err)
			}
			return jar, nil
		},
		LaunchAdapter: func() ([]string, string, error) {
			jar := javaDebugJarPath()
			if jar == "" {
				return nil, "", fmt.Errorf("java-debug jar not configured (set SL_DBG_JAVA_DEBUG_JAR)")
			}
			java, err := exec.LookPath("java")
			if err != nil {
				return nil, "", err
			}
			return []string{
				java, "-cp", jar,
				"com.microsoft.java.debug.core.adapter.JdiDebugAdapter",
			}, "stdio", nil
		},
		BuildLaunchArgs: func(cfg LaunchCfg) (map[string]interface{}, error) {
			if cfg.MainClass == "" {
				return nil, fmt.Errorf("java launch requires --main <mainClass>")
			}
			args := map[string]interface{}{
				"name":       "sl-dbg launch",
				"type":       "java",
				"request":    "launch",
				"mainClass":  cfg.MainClass,
				"classPaths": splitClasspath(cfg.Classpath),
				"args":       joinArgs(cfg.Args),
			}
			if cfg.StopOnEntry {
				args["stopOnEntry"] = true
			}
			if cfg.Cwd != "" {
				args["cwd"] = cfg.Cwd
			}
			return args, nil
		},
		BuildAttachArgs: func(cfg AttachCfg) (map[string]interface{}, error) {
			if cfg.Port == 0 {
				return nil, fmt.Errorf("java attach requires --port")
			}
			args := map[string]interface{}{
				"name":     "sl-dbg attach",
				"type":     "java",
				"request":  "attach",
				"hostName": cfgHost(cfg.Host),
				"port":     cfg.Port,
			}
			if len(cfg.SourceRoots) > 0 {
				args["sourcePaths"] = cfg.SourceRoots
			}
			return args, nil
		},
		InstallHint: "java-debug does not ship a standalone DAP server JAR. " +
			"Workaround: clone https://github.com/microsoft/java-debug and run `mvn package` to build " +
			"com.microsoft.java.debug.plugin-<ver>.jar plus its dependencies, then set " +
			"SL_DBG_JAVA_DEBUG_JAR=/path/to/<plugin-with-deps>.jar. See examples/java/README.md for details.",
	})
}

func javaDebugJarPath() string {
	if p := os.Getenv("SL_DBG_JAVA_DEBUG_JAR"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	candidate := filepath.Join(home, ".cache", "sl-dbg", "adapters", "java-debug.jar")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

func splitClasspath(cp string) []string {
	if cp == "" {
		return nil
	}
	sep := string(os.PathListSeparator)
	out := []string{}
	cur := ""
	for i := 0; i < len(cp); i++ {
		if string(cp[i]) == sep {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(cp[i])
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
