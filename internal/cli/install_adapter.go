package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/y0geshpatil/sl-dbg/internal/adapter"
	"github.com/y0geshpatil/sl-dbg/internal/buildinfo"
)

// newInstallAdapterCmdImpl returns the real `install-adapter` command.
// Supported languages: python, go, java, all.
func newInstallAdapterCmdImpl() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install-adapter <lang|all>",
		Short: "Install or refresh a language adapter (python|go|java|all)",
		Long: `Install the runtime adapter sl-dbg uses for a given language.

  python  →  install debugpy in an isolated, sl-dbg-managed virtual environment
  go      →  go install github.com/go-delve/delve/cmd/dlv@latest
  java    →  downloads pre-built sl-dbg-java-adapter.jar from GitHub Releases
            into ~/.cache/sl-dbg/adapters/ with SHA-256 verification.
            Development builds use local Maven source instead.
  all     →  attempt every adapter; report each failure and exit nonzero
            if any prerequisite or installation is unavailable.`,
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
		if py, err := adapter.PythonAdapterPath(); err == nil {
			stepOK("python", "debugpy already available via %s", py)
			return nil
		}
	}
	py, err := adapter.PythonPath()
	if err != nil {
		return err
	}
	dir, err := adapter.PythonVenvDir()
	if err != nil {
		return err
	}
	stepInfo("python", "creating isolated adapter environment at %s", dir)
	if err := runStreamed(py, "-m", "venv", dir); err != nil {
		return fmt.Errorf("create debugpy virtual environment: %w; install Python's venv support (Debian/Ubuntu: python3-venv)", err)
	}
	venvPython := filepath.Join(dir, "bin", "python")
	if err := runStreamed(venvPython, "-m", "pip", "install", "--upgrade", "debugpy"); err != nil {
		return fmt.Errorf("install debugpy in %s: %w", dir, err)
	}
	if err := runCheck(venvPython, "-c", "import debugpy"); err != nil {
		return fmt.Errorf("debugpy installation verification failed: %w", err)
	}
	stepOK("python", "installed debugpy in %s", dir)
	return nil
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
		return fmt.Errorf("`go` not found in PATH — install a current Go toolchain first " +
			"(brew install go, or https://go.dev/dl)")
	}
	stepInfo("go", "installing delve (go install github.com/go-delve/delve/cmd/dlv@latest)")
	if err := runStreamed("go", "install", "github.com/go-delve/delve/cmd/dlv@latest"); err != nil {
		return err
	}
	if path, err := adapter.DelvePath(); err != nil {
		return fmt.Errorf("go install completed but dlv is unavailable in %s: %w", goBinDir(), err)
	} else {
		stepOK("go", "dlv available at %s", path)
	}
	return nil
}

// dlvFound reports whether dlv is reachable either via PATH or via the
// standard go install location ($GOBIN, $GOPATH/bin, ~/go/bin).
func dlvFound() bool {
	_, err := adapter.DelvePath()
	return err == nil
}

func goBinDir() string {
	return adapter.GoBinDir()
}

// ----- java ----------------------------------------------------------------

const javaAdapterJar = "sl-dbg-java-adapter.jar"

func installJava(force bool) error {
	if _, err := exec.LookPath("java"); err != nil {
		return fmt.Errorf("`java` not found in PATH — install JDK 11+ first")
	}
	dest, err := adapterCacheDir()
	if err != nil {
		return err
	}
	destPath := filepath.Join(dest, javaAdapterJar)
	if !force {
		if err := adapter.ValidateJavaJar(destPath); err == nil {
			stepOK("java", "%s already present", destPath)
			return nil
		}
	}

	// Strategy 1: download a prebuilt jar from GitHub Releases if the user
	// gave us a URL (set by CI/release flow). Useful for end users who don't
	// want to install Maven.
	if url := os.Getenv("SL_DBG_JAVA_ADAPTER_URL"); url != "" {
		stepInfo("java", "downloading %s", url)
		return downloadFile(url, destPath, os.Getenv("SL_DBG_JAVA_ADAPTER_SHA256"))
	}

	// Strategy 2: auto-download the pre-built jar from the GitHub release that
	// matches the running binary's version. This is the normal path for users
	// who installed sl-dbg via the install script or Homebrew and don't have a
	// source checkout or Maven available.
	//
	// buildinfo.Version is injected by goreleaser as the bare semver ("1.2.3"),
	// without a leading "v". GitHub release tags use the "v" prefix, so we add
	// it when constructing the download URL.
	if binaryVersion := buildinfo.Version; isReleaseVersion(binaryVersion) {
		url := fmt.Sprintf("https://github.com/y0geshpatil/sl-dbg/releases/download/v%s/sl-dbg-java-adapter.jar", binaryVersion)
		stepInfo("java", "downloading pre-built adapter jar from GitHub Releases (%s)", url)
		releaseErr := downloadJavaRelease(url, destPath)
		if releaseErr == nil {
			stepOK("java", "installed %s", destPath)
			return nil
		}
		// A release must never silently execute a different local adapter build.
		return fmt.Errorf("Java release adapter download failed: %w; retry or explicitly build adapters/java-launcher with Maven", releaseErr)
	}

	// Strategy 3: build from in-tree source if we can find the Maven project.
	src := findJavaLauncherSource()
	if src == "" {
		hint := "  Options:\n" +
			"    - Set SL_DBG_JAVA_ADAPTER_URL and SL_DBG_JAVA_ADAPTER_SHA256, or\n" +
			"    - Place sl-dbg-java-adapter.jar manually in ~/.cache/sl-dbg/adapters/"
		return fmt.Errorf(
			"no prebuilt jar available and the in-tree Maven project " +
				"adapters/java-launcher was not found.\n" +
				"  Use a released binary (install.sh) so the jar is downloaded automatically, or:\n" + hint)
	}
	if _, err := exec.LookPath("mvn"); err != nil {
		return fmt.Errorf("Maven not found in PATH — install with `brew install maven` " +
			"(or set SL_DBG_JAVA_ADAPTER_URL and SL_DBG_JAVA_ADAPTER_SHA256 to skip the build)")
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

// isReleaseVersion reports whether ver looks like a published release (e.g.
// "1.2.3") rather than a dev build ("0.0.0-dev") or snapshot ("1.2.3-next").
func isReleaseVersion(ver string) bool {
	return releaseVersionPattern.MatchString(ver) &&
		ver != "0.0.0-dev" &&
		!strings.HasPrefix(ver, "v") &&
		!strings.HasSuffix(ver, "-dev") &&
		!strings.HasSuffix(ver, "-next") &&
		!strings.Contains(ver, "-dirty") &&
		!strings.Contains(ver, "+")
}

var releaseVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`)

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
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
	return writeJavaJar(in, dst, "")
}

var adapterHTTPClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("adapter downloads require HTTPS")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

func adapterDownload(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("adapter downloads require HTTPS")
	}
	resp, err := adapterHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

func validSHA256(sum string) bool {
	decoded, err := hex.DecodeString(sum)
	return err == nil && len(decoded) == sha256.Size
}

func downloadJavaRelease(url, dst string) error {
	resp, err := adapterDownload(url + ".sha256")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return err
	}
	fields := strings.Fields(string(content))
	if len(content) > 4096 || len(fields) != 2 || !validSHA256(fields[0]) || strings.TrimPrefix(fields[1], "*") != javaAdapterJar {
		return fmt.Errorf("invalid Java adapter SHA-256 sidecar")
	}
	return downloadFile(url, dst, fields[0])
}

func downloadFile(url, dst, checksum string) error {
	if !validSHA256(checksum) {
		return fmt.Errorf("a valid SHA-256 checksum is required (set SL_DBG_JAVA_ADAPTER_SHA256 for a custom URL)")
	}
	resp, err := adapterDownload(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return writeJavaJar(resp.Body, dst, checksum)
}

func writeJavaJar(in io.Reader, dst, checksum string) error {
	out, err := os.CreateTemp(filepath.Dir(dst), ".java-adapter-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	hash := sha256.New()
	const maxJarSize = 256 << 20
	n, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, maxJarSize+1))
	if err != nil {
		return err
	}
	if n > maxJarSize {
		return fmt.Errorf("Java adapter exceeds 256 MiB download limit")
	}
	if checksum != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), checksum) {
		return fmt.Errorf("Java adapter SHA-256 mismatch")
	}
	if err := out.Chmod(0o644); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := adapter.ValidateJavaJar(out.Name()); err != nil {
		return err
	}
	return os.Rename(out.Name(), dst)
}
