package adapter

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Java adapter: spawns the sl-dbg Java launcher (a small fat-jar that embeds
// Microsoft's java-debug-core and serves DAP over a TCP socket without needing
// Eclipse JDT-LS).
//
// Jar resolution order:
//   1. $SL_DBG_JAVA_DEBUG_JAR
//   2. ~/.cache/sl-dbg/adapters/sl-dbg-java-adapter.jar (installed)
//   3. <repo>/adapters/java-launcher/target/sl-dbg-java-adapter.jar (dev build)
func init() {
	Register(Spec{
		Lang:      "java",
		AdapterID: "java",
		Detect: func() (string, error) {
			jar := javaDebugJarPath()
			if jar == "" {
				return "", fmt.Errorf("sl-dbg java adapter jar not found; build it with `make java-adapter` or set SL_DBG_JAVA_DEBUG_JAR")
			}
			if _, err := exec.LookPath("java"); err != nil {
				return "", fmt.Errorf("`java` not in PATH")
			}
			if _, err := os.Stat(jar); err != nil {
				return "", fmt.Errorf("jar missing at %s: %w", jar, err)
			}
			return jar, nil
		},
		LaunchAdapter: func() ([]string, Transport, error) {
			jar := javaDebugJarPath()
			if jar == "" {
				return nil, TransportTCPListen, fmt.Errorf("sl-dbg java adapter jar not configured")
			}
			java, err := exec.LookPath("java")
			if err != nil {
				return nil, TransportTCPListen, err
			}
			return []string{
				java,
				"--add-exports=jdk.jdi/com.sun.tools.example.debug.expr=ALL-UNNAMED",
				"-jar", jar, "--port={PORT}",
			}, TransportTCPListen, nil
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
		InstallHint: "Build the embedded launcher with `make java-adapter` (requires Maven + JDK 11+), " +
			"or download a prebuilt sl-dbg-java-adapter.jar into ~/.cache/sl-dbg/adapters/. " +
			"Override location with SL_DBG_JAVA_DEBUG_JAR.",
	})
}

func javaDebugJarPath() string {
	if p := os.Getenv("SL_DBG_JAVA_DEBUG_JAR"); p != "" {
		return p
	}
	candidates := []string{}
	if home, _ := os.UserHomeDir(); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".cache", "sl-dbg", "adapters", "sl-dbg-java-adapter.jar"),
		)
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "..", "adapters", "java-launcher", "target", "sl-dbg-java-adapter.jar"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "adapters", "java-launcher", "target", "sl-dbg-java-adapter.jar"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
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
