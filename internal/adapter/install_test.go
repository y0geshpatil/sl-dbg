package adapter

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func adapterFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PATH", dir)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	return dir
}

func fixtureExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPythonDetectionAndLaunchAgree(t *testing.T) {
	for _, scenario := range []string{"python3", "python", "managed", "fallback", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			dir := adapterFixture(t)
			var want string
			switch scenario {
			case "python3", "python":
				want = filepath.Join(dir, scenario)
				fixtureExecutable(t, want, "exit 0")
			case "managed":
				want = filepath.Join(dir, ".cache/sl-dbg/adapters/python/bin/python")
				fixtureExecutable(t, want, "exit 0")
				fixtureExecutable(t, filepath.Join(dir, "python3"), "exit 0")
			case "fallback":
				fixtureExecutable(t, filepath.Join(dir, "python3"), "exit 1")
				want = filepath.Join(dir, "python")
				fixtureExecutable(t, want, "exit 0")
			}
			spec, _ := Get("python")
			path, detectErr := spec.Detect()
			argv, transport, launchErr := spec.LaunchAdapter()
			if scenario == "missing" {
				if detectErr == nil || launchErr == nil {
					t.Fatal("missing debugpy reported as available")
				}
				return
			}
			if detectErr != nil || launchErr != nil || path != want || argv[0] != want || transport != TransportStdio {
				t.Fatalf("detect=%q/%v launch=%v/%v", path, detectErr, argv, launchErr)
			}
			if scenario == "managed" {
				args, err := spec.BuildLaunchArgs(LaunchCfg{Program: "app.py"})
				if err != nil || args["python"] != filepath.Join(dir, "python3") {
					t.Fatalf("target must not use adapter venv: %v, %v", args, err)
				}
			}
		})
	}
}

func TestDelveDetectionAndLaunchAgree(t *testing.T) {
	for _, scenario := range []string{"path", "gobin", "gopath", "home", "go-env", "not-executable", "directory"} {
		t.Run(scenario, func(t *testing.T) {
			dir := adapterFixture(t)
			want := filepath.Join(dir, "dlv")
			switch scenario {
			case "gobin", "not-executable", "directory":
				t.Setenv("GOBIN", filepath.Join(dir, "bin"))
				want = filepath.Join(dir, "bin/dlv")
			case "gopath":
				t.Setenv("GOPATH", filepath.Join(dir, "workspace"))
				want = filepath.Join(dir, "workspace/bin/dlv")
			case "home":
				want = filepath.Join(dir, "go/bin/dlv")
			case "go-env":
				want = filepath.Join(dir, "persisted/bin/dlv")
				fixtureExecutable(t, filepath.Join(dir, "go"), `printf '{"GOBIN":"%s/persisted/bin","GOPATH":""}' "$HOME"`)
			}
			fixtureExecutable(t, want, "exit 0")
			if scenario == "not-executable" {
				if err := os.Chmod(want, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "directory" {
				if err := os.Remove(want); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(want, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			spec, _ := Get("go")
			path, detectErr := spec.Detect()
			argv, transport, launchErr := spec.LaunchAdapter()
			if scenario == "not-executable" || scenario == "directory" {
				if detectErr == nil || launchErr == nil {
					t.Fatalf("invalid executable accepted: %q", want)
				}
				return
			}
			if detectErr != nil || launchErr != nil || path != want || argv[0] != want || transport != TransportTCPListen {
				t.Fatalf("detect=%q/%v launch=%v/%v", path, detectErr, argv, launchErr)
			}
		})
	}
}

func TestValidateJavaJar(t *testing.T) {
	for _, scenario := range []string{"valid", "empty", "html", "missing-manifest", "directory"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "adapter.jar")
			var content bytes.Buffer
			switch scenario {
			case "valid", "missing-manifest":
				writer := zip.NewWriter(&content)
				name := "Other.class"
				if scenario == "valid" {
					name = "META-INF/MANIFEST.MF"
				}
				entry, _ := writer.Create(name)
				entry.Write([]byte("Manifest-Version: 1.0\n"))
				writer.Close()
			case "html":
				content.WriteString("<html>proxy error</html>")
			case "directory":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "directory" {
				if err := os.WriteFile(path, content.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := ValidateJavaJar(path); (err == nil) != (scenario == "valid") {
				t.Fatalf("ValidateJavaJar=%v", err)
			}
		})
	}
}
