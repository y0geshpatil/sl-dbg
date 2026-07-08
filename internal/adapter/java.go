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
				return "", fmt.Errorf("sl-dbg java adapter jar not found — run `sl-dbg install-adapter java` to install it")
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
			cpEntries := splitClasspath(cfg.Classpath)
			args := map[string]interface{}{
				"name":       "sl-dbg launch",
				"type":       "java",
				"request":    "launch",
				"mainClass":  cfg.MainClass,
				"classPaths": cpEntries,
				"args":       joinArgs(cfg.Args),
			}
			if cfg.StopOnEntry {
				args["stopOnEntry"] = true
			}
			if cfg.Cwd != "" {
				args["cwd"] = cfg.Cwd
			}
			// Source paths: explicit --source-root wins; otherwise infer from
			// classpath entries (directories often double as source roots in
			// simple projects) plus cwd. Without this, the Java adapter falls
			// back to cwd-relative path fabrication and the CLI's `source`
			// command can read an unrelated file with the same basename.
			roots := cfg.SourceRoots
			if len(roots) == 0 {
				roots = inferJavaSourceRoots(cfg.Cwd, cpEntries)
			}
			if len(roots) > 0 {
				args["sourcePaths"] = roots
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
		InstallHint: "Run `sl-dbg install-adapter java` to download and install the adapter automatically (no Maven required). " +
			"Override the jar location with SL_DBG_JAVA_DEBUG_JAR.",
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

// inferJavaSourceRoots picks reasonable defaults for sourcePaths when the
// user did not pass --source-root. We use:
//   - cfg.Cwd (so a simple "javac Foo.java && sl-dbg start --main Foo" works)
//   - every directory entry on the classpath (often these are bin/ but in
//     simple workflows the .java lives next to the .class)
//   - common Maven/Gradle source layouts under cwd ("src/main/java",
//     "src/test/java") when they exist.
//
// Returning a superset is safe: the provider only echoes paths that exist.
func inferJavaSourceRoots(cwd string, classpath []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			seen[p] = true
			out = append(out, p)
		}
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	add(cwd)
	for _, e := range classpath {
		add(e)
	}
	if cwd != "" {
		for _, conv := range []string{"src/main/java", "src/test/java", "src"} {
			add(filepath.Join(cwd, conv))
		}
	}
	return out
}
