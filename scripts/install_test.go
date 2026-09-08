package scripts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type installFixture struct {
	home, bin, downloads, temp, tools string
}

func writeFixture(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

func newInstallFixture(t *testing.T, binary string) installFixture {
	t.Helper()
	root := t.TempDir()
	f := installFixture{
		home: filepath.Join(root, "home"), bin: filepath.Join(root, "custom bin"),
		downloads: filepath.Join(root, "downloads"), temp: filepath.Join(root, "tmp"),
		tools: filepath.Join(root, "tools"),
	}
	for _, dir := range []string{f.home, f.bin, f.downloads, f.temp, f.tools} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "sl-dbg", Mode: 0755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(binary)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("sl-dbg_1.2.3_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	writeFixture(t, filepath.Join(f.downloads, name), archive.String(), 0600)
	writeFixture(t, filepath.Join(f.downloads, "sl-dbg_1.2.3_checksums.txt"),
		fmt.Sprintf("%x  %s\n", sha256.Sum256(archive.Bytes()), name), 0600)
	writeFixture(t, filepath.Join(f.downloads, "latest"), `{"tag_name":"v1.2.3"}`, 0600)
	// curl is replaced, so these tests never contact or execute a remote release.
	writeFixture(t, filepath.Join(f.tools, "curl"), `#!/bin/bash
set -eu
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) out="$2"; shift 2 ;;
    --proto|--proto-redir|--connect-timeout|--max-time|--retry) shift 2 ;;
    --*) shift ;;
    *) url="$1"; shift ;;
  esac
done
cp "$FIXTURE_DOWNLOADS/${url##*/}" "$out"
`, 0755)
	return f
}

func (f installFixture) run(t *testing.T, script string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append([]string{
		"HOME=" + f.home, "PATH=" + f.tools + ":" + os.Getenv("PATH"),
		"TMPDIR=" + f.temp, "INSTALL_DIR=" + f.bin, "FIXTURE_DOWNLOADS=" + f.downloads,
	}, env...)
	out, err := cmd.CombinedOutput()
	entries, readErr := os.ReadDir(f.temp)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("temporary downloads leaked: %v, %v", entries, readErr)
	}
	staged, _ := filepath.Glob(filepath.Join(f.bin, ".sl-dbg.*"))
	if len(staged) != 0 {
		t.Fatalf("staging files leaked: %v", staged)
	}
	return string(out), err
}

const workingBinary = "#!/bin/sh\n[ \"$1\" = version ] || exit 1\nprintf '%s\\n' '{\"version\":\"1.2.3\"}'\n"

func TestInstaller(t *testing.T) {
	for _, version := range []string{"latest", "v1.2.3", "1.2.3"} {
		t.Run(version, func(t *testing.T) {
			f := newInstallFixture(t, workingBinary)
			out, err := f.run(t, "install.sh", nil, version)
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			got, err := os.ReadFile(filepath.Join(f.bin, "sl-dbg"))
			if err != nil || string(got) != workingBinary {
				t.Fatalf("installed binary: %q, %v", got, err)
			}
			if !strings.Contains(out, "export PATH=") {
				t.Fatalf("missing PATH instruction: %s", out)
			}
		})
	}
}

func TestInstallerDefaultUserDirectory(t *testing.T) {
	f := newInstallFixture(t, workingBinary)
	out, err := f.run(t, "install.sh", []string{"INSTALL_DIR="}, "v1.2.3")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".local", "bin", "sl-dbg")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallerFailurePreservesExistingBinary(t *testing.T) {
	for _, failure := range []string{"checksum", "manifest-missing", "archive-missing", "invalid-version", "binary-fails", "skip-verification", "duplicate-checksum"} {
		t.Run(failure, func(t *testing.T) {
			binary := workingBinary
			if failure == "binary-fails" {
				binary = "#!/bin/sh\nexit 1\n"
			}
			f := newInstallFixture(t, binary)
			version := "v1.2.3"
			var env []string
			manifest := filepath.Join(f.downloads, "sl-dbg_1.2.3_checksums.txt")
			switch failure {
			case "checksum":
				writeFixture(t, manifest, fmt.Sprintf("%s  sl-dbg_1.2.3_%s_%s.tar.gz\n", strings.Repeat("0", 64), runtime.GOOS, runtime.GOARCH), 0600)
			case "manifest-missing":
				if err := os.Remove(manifest); err != nil {
					t.Fatal(err)
				}
			case "archive-missing":
				version = "v9.9.9"
			case "invalid-version":
				version = "../../bad"
			case "skip-verification":
				env = []string{"INSTALL_SKIP_VERIFY=1"}
			case "duplicate-checksum":
				data, err := os.ReadFile(manifest)
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, manifest, string(data)+string(data), 0600)
			}
			dest := filepath.Join(f.bin, "sl-dbg")
			writeFixture(t, dest, "old binary", 0755)
			out, err := f.run(t, "install.sh", env, version)
			if err == nil {
				t.Fatalf("expected failure: %s", out)
			}
			got, err := os.ReadFile(dest)
			if err != nil || string(got) != "old binary" {
				t.Fatalf("old binary changed: %q, %v", got, err)
			}
		})
	}
}

func TestUninstaller(t *testing.T) {
	for _, mode := range []string{"success", "cleanup-fails", "keep-mcp", "unknown-arg", "missing", "dangling-symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := newInstallFixture(t, workingBinary)
			dest := filepath.Join(f.bin, "sl-dbg")
			var args []string
			binary := "#!/bin/sh\nexit 0\n"
			if mode == "cleanup-fails" || mode == "keep-mcp" {
				binary = "#!/bin/sh\nexit 1\n"
			}
			if mode != "missing" && mode != "dangling-symlink" {
				writeFixture(t, dest, binary, 0755)
			}
			if mode == "dangling-symlink" {
				if err := os.Symlink(filepath.Join(f.bin, "missing"), dest); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "keep-mcp" {
				args = []string{"--keep-mcp"}
			} else if mode == "unknown-arg" {
				args = []string{"--typo"}
			}
			out, err := f.run(t, "uninstall.sh", nil, args...)
			wantFailure := mode == "cleanup-fails" || mode == "unknown-arg"
			if (err != nil) != wantFailure {
				t.Fatalf("failure=%v, want %v: %s", err, wantFailure, out)
			}
			_, statErr := os.Lstat(dest)
			if wantFailure && statErr != nil {
				t.Fatalf("binary not retained: %v", statErr)
			}
			if !wantFailure && !os.IsNotExist(statErr) {
				t.Fatalf("binary not removed: %v", statErr)
			}
		})
	}
}
