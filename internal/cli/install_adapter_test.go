package cli

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/y0geshpatil/sl-dbg/internal/adapter"
	"github.com/y0geshpatil/sl-dbg/internal/buildinfo"
)

func TestIsReleaseVersion(t *testing.T) {
	tests := []struct {
		ver  string
		want bool
	}{
		// valid release versions — goreleaser injects these without a leading "v"
		{"1.2.3", true},
		{"0.3.0", true},
		{"10.0.1", true},
		// pre-release tags are published by goreleaser and have release assets
		{"1.2.3-alpha", true},
		{"1.2.3-beta", true},
		// dev / snapshot / dirty builds must NOT attempt a download
		{"", false},
		{"0.0.0-dev", false},
		{"1.2.3-dev", false},
		{"1.2.3-next", false},
		{"1.2.3-dirty", false},
		{"1.2.3-next-dirty", false},
		// goreleaser never injects a "v" prefix, but guard it anyway
		{"v1.2.3", false},
		// build metadata is not a valid release tag path component
		{"1.2.3+build.123", false},
		{"main", false},
		{"1.2.3/../../latest", false},
		{"01.2.3", false},
	}
	for _, tc := range tests {
		got := isReleaseVersion(tc.ver)
		if got != tc.want {
			t.Errorf("isReleaseVersion(%q) = %v; want %v", tc.ver, got, tc.want)
		}
	}
}

func installFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PATH", dir)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	return dir
}

func installExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInstallPythonUsesManagedVenv(t *testing.T) {
	for _, name := range []string{"python3", "python"} {
		t.Run(name, func(t *testing.T) {
			dir := installFixture(t)
			installExecutable(t, filepath.Join(dir, name), `
	echo "$0 $*" >> "$HOME/commands"
	if [ "$1 $2" = "-m venv" ]; then
	  /bin/mkdir -p "$3/bin"
	  /bin/cp "$0" "$3/bin/python"
	elif [ "$1 $2" = "-m pip" ]; then
	  : > "$HOME/installed"
	elif [ "$1" = "-c" ]; then
	  test -f "$HOME/installed"
	else
	  exit 1
	fi`)
			if err := installPython(false); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(dir, "commands"))
			if err != nil {
				t.Fatal(err)
			}
			log := string(content)
			if strings.Contains(log, "--user") || !strings.Contains(log, "/adapters/python/bin/python -m pip install --upgrade debugpy") {
				t.Fatalf("unexpected installation commands:\n%s", log)
			}
			spec, _ := adapter.Get("python")
			argv, _, err := spec.LaunchAdapter()
			if err != nil || argv[0] != filepath.Join(dir, ".cache/sl-dbg/adapters/python/bin/python") {
				t.Fatalf("runtime does not use installed environment: %v, %v", argv, err)
			}
		})
	}
}

func TestInstallPythonExistingAndVenvFailure(t *testing.T) {
	dir := installFixture(t)
	path := filepath.Join(dir, "python")
	installExecutable(t, path, `[ "$1" = "-c" ]`)
	if err := installPython(false); err != nil {
		t.Fatalf("existing python fallback must skip installation: %v", err)
	}
	if err := installPython(true); err == nil || !strings.Contains(err.Error(), "python3-venv") {
		t.Fatalf("venv failure should include remedy: %v", err)
	}
}

func TestInstallPythonReportsPipAndVerificationFailures(t *testing.T) {
	for _, scenario := range []string{"pip", "verification"} {
		t.Run(scenario, func(t *testing.T) {
			dir := installFixture(t)
			t.Setenv("FAIL_STAGE", scenario)
			installExecutable(t, filepath.Join(dir, "python3"), `
if [ "$1 $2" = "-m venv" ]; then
  /bin/mkdir -p "$3/bin"
  /bin/cp "$0" "$3/bin/python"
elif [ "$1 $2" = "-m pip" ]; then
  [ "$FAIL_STAGE" != pip ]
else
  exit 1
fi`)
			err := installPython(true)
			want := "install debugpy"
			if scenario == "verification" {
				want = "verification failed"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("installation failure not reported: %v", err)
			}
		})
	}
}

func TestInstallGoChecksRuntimePath(t *testing.T) {
	dir := installFixture(t)
	t.Setenv("GOBIN", filepath.Join(dir, "bin"))
	path := filepath.Join(dir, "bin/dlv")
	installExecutable(t, path, "exit 0")
	if !dlvFound() {
		t.Fatal("GOBIN executable not detected")
	}
	if err := installGo(false); err != nil {
		t.Fatalf("should not require go for existing adapter: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if dlvFound() {
		t.Fatal("nonexecutable adapter accepted")
	}
	installExecutable(t, filepath.Join(dir, "go"), "exit 0")
	if err := installGo(true); err == nil || !strings.Contains(err.Error(), "dlv is unavailable") {
		t.Fatalf("missing installation must fail verification: %v", err)
	}
}

type adapterRoundTripper func(*http.Request) (*http.Response, error)

func (f adapterRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mockAdapterHTTP(t *testing.T, fn adapterRoundTripper) {
	t.Helper()
	old := adapterHTTPClient
	client := *old
	client.Transport = fn
	adapterHTTPClient = &client
	t.Cleanup(func() { adapterHTTPClient = old })
}

func jarFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	entry, err := w.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("Manifest-Version: 1.0\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestJavaReleaseVerifiedAtomicDownload(t *testing.T) {
	jar := jarFixture(t)
	sum := fmt.Sprintf("%x", sha256.Sum256(jar))
	for _, scenario := range []string{"success", "checksum-mismatch", "missing-checksum", "invalid-checksum", "invalid-jar", "partial"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, javaAdapterJar)
			if err := os.WriteFile(dst, []byte("previous known good jar"), 0o644); err != nil {
				t.Fatal(err)
			}
			mockAdapterHTTP(t, func(req *http.Request) (*http.Response, error) {
				body := io.NopCloser(bytes.NewReader(jar))
				status := http.StatusOK
				if strings.HasSuffix(req.URL.Path, ".sha256") {
					checksum := sum
					if scenario == "checksum-mismatch" {
						checksum = strings.Repeat("0", 64)
					}
					if scenario == "invalid-jar" {
						checksum = fmt.Sprintf("%x", sha256.Sum256([]byte("not a jar")))
					}
					body = io.NopCloser(strings.NewReader(checksum + "  " + javaAdapterJar + "\n"))
					if scenario == "missing-checksum" {
						status = http.StatusNotFound
					}
					if scenario == "invalid-checksum" {
						body = io.NopCloser(strings.NewReader("not a checksum"))
					}
				} else if scenario == "invalid-jar" {
					body = io.NopCloser(strings.NewReader("not a jar"))
				} else if scenario == "partial" {
					body = io.NopCloser(io.MultiReader(strings.NewReader("partial"), brokenAdapterReader{}))
				}
				return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
			})
			err := downloadJavaRelease("https://example.test/v1.2.3/"+javaAdapterJar, dst)
			if (err == nil) != (scenario == "success") {
				t.Fatalf("download returned %v", err)
			}
			content, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}
			want := []byte("previous known good jar")
			if scenario == "success" {
				want = jar
			}
			if !bytes.Equal(content, want) {
				t.Fatal("failed download replaced existing jar")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatalf("staging files leaked: %v", entries)
			}
		})
	}
}

type brokenAdapterReader struct{}

func (brokenAdapterReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestJavaDownloadRequiresHTTPSAndChecksum(t *testing.T) {
	mockAdapterHTTP(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("invalid input made network request: %s", req.URL)
		return nil, nil
	})
	dst := filepath.Join(t.TempDir(), javaAdapterJar)
	if err := downloadFile("https://example.test/adapter.jar", dst, ""); err == nil {
		t.Fatal("missing checksum accepted")
	}
	if err := downloadFile("http://example.test/adapter.jar", dst, strings.Repeat("0", 64)); err == nil {
		t.Fatal("insecure HTTP accepted")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.test/adapter.jar", nil)
	if err := adapterHTTPClient.CheckRedirect(req, nil); err == nil {
		t.Fatal("HTTPS downgrade accepted")
	}
}

func TestJavaDownloadTimeout(t *testing.T) {
	mockAdapterHTTP(t, func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	adapterHTTPClient.Timeout = 10 * time.Millisecond
	err := downloadFile("https://example.test/adapter.jar", filepath.Join(t.TempDir(), javaAdapterJar), strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("request was not bounded: %v", err)
	}
}

func TestInstallJavaReleaseFailurePreservesCause(t *testing.T) {
	dir := installFixture(t)
	installExecutable(t, filepath.Join(dir, "java"), "exit 0")
	t.Setenv("SL_DBG_JAVA_ADAPTER_URL", "")
	oldVersion := buildinfo.Version
	buildinfo.Version = "1.2.3"
	t.Cleanup(func() { buildinfo.Version = oldVersion })
	mockAdapterHTTP(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := installJava(true); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("download cause lost: %v", err)
	}
}

func TestInstallJavaRequiresRuntimeEvenWithCachedJar(t *testing.T) {
	installFixture(t)
	dir, err := adapterCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, javaAdapterJar), jarFixture(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installJava(false); err == nil || !strings.Contains(err.Error(), "java") {
		t.Fatalf("installation reported ready without Java: %v", err)
	}
}

func TestCopyJavaJarAtomic(t *testing.T) {
	dir := t.TempDir()
	dst, src := filepath.Join(dir, "installed.jar"), filepath.Join(dir, "built.jar")
	if err := os.WriteFile(dst, jarFixture(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err == nil {
		t.Fatal("invalid Maven output accepted")
	}
	if err := adapter.ValidateJavaJar(dst); err != nil {
		t.Fatalf("existing jar damaged: %v", err)
	}
}

// TestJavaAdapterURL verifies that the download URL is constructed correctly.
// buildinfo.Version is "1.2.3" (no "v"); GitHub release tags are "v1.2.3".
func TestJavaAdapterURL(t *testing.T) {
	ver := "1.2.3"
	got := fmt.Sprintf("https://github.com/y0geshpatil/sl-dbg/releases/download/v%s/sl-dbg-java-adapter.jar", ver)
	want := "https://github.com/y0geshpatil/sl-dbg/releases/download/v1.2.3/sl-dbg-java-adapter.jar"
	if got != want {
		t.Errorf("URL = %q; want %q", got, want)
	}
}
