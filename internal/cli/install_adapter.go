package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// newInstallAdapterCmdImpl returns the real `install-adapter` command.
// Supported languages: python, go, java, all.
func newInstallAdapterCmdImpl() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install-adapter <lang|all>",
		Short: "Install or refresh a language adapter (python|go|java|all)",
		Long: `Install the runtime adapter sl-dbg uses for a given language.

  python  →  pip install --user debugpy
  go      →  go install github.com/go-delve/delve/cmd/dlv@latest
  java    →  build the embedded launcher fat-jar (requires Maven + JDK 11+)
            and copy it to ~/.cache/sl-dbg/adapters/sl-dbg-java-adapter.jar
  all     →  install every adapter for which the prerequisite toolchain
            is available; skip the others with a clear hint.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lang := strings.ToLower(args[0])
			switch lang {
			case "python", "py":
				return installPython(force)
			case "go", "golang":
				return installGo(force)
			case "java":
				return installJava(force)
			case "all":
				return installAll(force)
			default:
				return Usage("unknown adapter %q (want: python|go|java|all)", lang)
			}
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "reinstall even if the adapter is already present")
	return cmd
}

// ----- python --------------------------------------------------------------

func installPython(force bool) error {
	if !force {
		if _, err := exec.LookPath("python3"); err == nil {
			if err := runCheck("python3", "-c", "import debugpy"); err == nil {
				stepOK("python", "debugpy already installed")
				return nil
			}
		}
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		py, err = exec.LookPath("python")
	}
	if err != nil {
		return fmt.Errorf("python3 not found in PATH — install Python 3.8+ first")
	}
	stepInfo("python", "installing debugpy via %s -m pip", py)
	return runStreamed(py, "-m", "pip", "install", "--user", "--upgrade", "debugpy")
}

// ----- go ------------------------------------------------------------------

func installGo(force bool) error {
	if !force {
		if dlvFound() {
			stepOK("go", "dlv already installed")
			return nil
		}
	}
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("`go` not found in PATH — install Go 1.21+ first " +
			"(brew install go, or https://go.dev/dl)")
	}
	stepInfo("go", "installing delve (go install github.com/go-delve/delve/cmd/dlv@latest)")
	if err := runStreamed("go", "install", "github.com/go-delve/delve/cmd/dlv@latest"); err != nil {
		return err
	}
	if _, err := exec.LookPath("dlv"); err != nil {
		gobin := goBinDir()
		fmt.Fprintf(os.Stderr,
			"  ⚠ dlv installed to %s but that directory is not on $PATH.\n"+
				"     Add it: export PATH=\"%s:$PATH\"\n", gobin, gobin)
	}
	return nil
}

// dlvFound reports whether dlv is reachable either via PATH or via the
// standard go install location ($GOBIN, $GOPATH/bin, ~/go/bin).
func dlvFound() bool {
	if _, err := exec.LookPath("dlv"); err == nil {
		return true
	}
	candidate := filepath.Join(goBinDir(), "dlv")
	if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

func goBinDir() string {
	if v := os.Getenv("GOBIN"); v != "" {
		return v
	}
	if v := os.Getenv("GOPATH"); v != "" {
		return filepath.Join(strings.Split(v, string(os.PathListSeparator))[0], "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "go", "bin")
}

// ----- java ----------------------------------------------------------------

const javaAdapterJar = "sl-dbg-java-adapter.jar"

func installJava(force bool) error {
	dest, err := adapterCacheDir()
	if err != nil {
		return err
	}
	destPath := filepath.Join(dest, javaAdapterJar)
	if !force {
		if _, err := os.Stat(destPath); err == nil {
			stepOK("java", "%s already present", destPath)
			return nil
		}
	}
	if _, err := exec.LookPath("java"); err != nil {
		return fmt.Errorf("`java` not found in PATH — install JDK 11+ first")
	}

	// Strategy 1: download a prebuilt jar from GitHub Releases if the user
	// gave us a URL (set by CI/release flow). Useful for end users who don't
	// want to install Maven.
	if url := os.Getenv("SL_DBG_JAVA_ADAPTER_URL"); url != "" {
		stepInfo("java", "downloading %s", url)
		return downloadFile(url, destPath)
	}

	// Strategy 2: build from in-tree source if we can find the Maven project.
	src := findJavaLauncherSource()
	if src == "" {
		return fmt.Errorf(
			"no prebuilt jar available and the in-tree Maven project " +
				"adapters/java-launcher was not found.\n" +
				"  Either:\n" +
				"    - run sl-dbg from a source checkout so `make java-adapter` can run, or\n" +
				"    - set SL_DBG_JAVA_ADAPTER_URL to a downloadable jar URL")
	}
	if _, err := exec.LookPath("mvn"); err != nil {
		return fmt.Errorf("Maven not found in PATH — install with `brew install maven` " +
			"(or set SL_DBG_JAVA_ADAPTER_URL to skip the build)")
	}
	stepInfo("java", "building launcher via Maven at %s", src)
	if err := runStreamedIn(src, "mvn", "-q", "-DskipTests", "package"); err != nil {
		return err
	}
	built := filepath.Join(src, "target", javaAdapterJar)
	if _, err := os.Stat(built); err != nil {
		return fmt.Errorf("Maven build did not produce %s: %w", built, err)
	}
	stepInfo("java", "installing %s → %s", built, destPath)
	return copyFile(built, destPath)
}

func findJavaLauncherSource() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "..", "adapters", "java-launcher"),
			filepath.Join(exeDir, "adapters", "java-launcher"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "adapters", "java-launcher"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "pom.xml")); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

func adapterCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cache", "sl-dbg", "adapters")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ----- all -----------------------------------------------------------------

func installAll(force bool) error {
	type step struct {
		name string
		fn   func(bool) error
	}
	steps := []step{
		{"python", installPython},
		{"go", installGo},
		{"java", installJava},
	}
	var failed []string
	for _, s := range steps {
		fmt.Fprintf(os.Stderr, "\n── %s ──\n", s.name)
		if err := s.fn(force); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", s.name, err)
			failed = append(failed, s.name)
		}
	}
	fmt.Fprintln(os.Stderr)
	if len(failed) == 0 {
		fmt.Fprintln(os.Stderr, "✓ all adapters installed")
		return nil
	}
	return fmt.Errorf("install-adapter all: %d failed (%s) — see messages above",
		len(failed), strings.Join(failed, ", "))
}

// ----- helpers -------------------------------------------------------------

func runCheck(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	return c.Run()
}

func runStreamed(name string, args ...string) error {
	return runStreamedIn("", name, args...)
}

func runStreamedIn(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	if dir != "" {
		c.Dir = dir
	}
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}

func stepInfo(tag, format string, a ...any) {
	fmt.Fprintf(os.Stderr, "  → [%s] %s\n", tag, fmt.Sprintf(format, a...))
}

func stepOK(tag, format string, a ...any) {
	fmt.Fprintf(os.Stderr, "  ✓ [%s] %s\n", tag, fmt.Sprintf(format, a...))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func downloadFile(url, dst string) error {
	resp, err := http.Get(url) //nolint:gosec — user-supplied URL is the intent
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// keep linter happy when GOOS-specific paths aren't used
var _ = runtime.GOOS
