// Package adapter contains the registry of language DAP adapters: how to
// detect them, how to launch them, and what auxiliary args they need.
package adapter

import (
	"errors"
	"fmt"
)

// Spec describes one language's DAP adapter.
type Spec struct {
	// Lang is the user-facing language id ("python", "java", "go", ...).
	Lang string
	// AdapterID is the value sent in the DAP `initialize` request.
	AdapterID string
	// Detect returns nil if the adapter binary is available on the system.
	// path is the resolved binary or jar path (empty if Detect fails).
	Detect func() (path string, err error)
	// LaunchAdapter returns the argv to spawn the adapter process and the
	// transport mode it speaks ("stdio" or "tcp:<port>").
	LaunchAdapter func() (argv []string, transport string, err error)
	// BuildLaunchArgs builds the DAP-level `launch` request arguments for
	// starting a fresh target.
	BuildLaunchArgs func(cfg LaunchCfg) (map[string]interface{}, error)
	// BuildAttachArgs builds the DAP-level `attach` request arguments for
	// attaching to a running target.
	BuildAttachArgs func(cfg AttachCfg) (map[string]interface{}, error)
	// InstallHint is shown when Detect fails.
	InstallHint string
}

// LaunchCfg captures the user-visible parameters of `sl-dbg start`.
type LaunchCfg struct {
	Program     string
	Args        []string
	Cwd         string
	Env         []string
	StopOnEntry bool
	MainClass   string
	Classpath   string
}

// AttachCfg captures the user-visible parameters of `sl-dbg attach`.
type AttachCfg struct {
	Host        string
	Port        int
	PID         int
	SourceRoots []string
}

var registry = map[string]Spec{}

// Register adds a Spec to the registry. Called from per-language init().
func Register(s Spec) {
	registry[s.Lang] = s
}

// Get returns the Spec for a language, or ErrUnknownLang.
func Get(lang string) (Spec, error) {
	s, ok := registry[lang]
	if !ok {
		return Spec{}, fmt.Errorf("%w: %q", ErrUnknownLang, lang)
	}
	return s, nil
}

// List returns all registered language ids.
func List() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// ErrUnknownLang is returned by Get when no adapter is registered for a language.
var ErrUnknownLang = errors.New("unknown language")
