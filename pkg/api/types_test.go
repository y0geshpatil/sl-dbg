package api_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/y0geshpatil/sl-dbg/pkg/api"
)

// All sl-dbg responses must include a "schema" field so clients can detect
// breaking changes at runtime. This is a load-bearing contract for AI agents.
func TestResponseEnvelopeAlwaysIncludesSchema(t *testing.T) {
	r := api.Response{
		Schema: api.SchemaVersion,
		OK:     true,
		Data:   map[string]string{"hello": "world"},
		TS:     time.Now().UTC(),
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"schema":"1"`) {
		t.Errorf("missing schema in response JSON: %s", b)
	}
	if !strings.Contains(string(b), `"ok":true`) {
		t.Errorf("missing ok field: %s", b)
	}
}

func TestErrorEnvelopeShape(t *testing.T) {
	r := api.Response{
		Schema: api.SchemaVersion,
		OK:     false,
		Error: &api.Error{
			Code:    api.ErrSessionNotFound,
			Message: "session not found",
			Hint:    "run `sl-dbg sessions` to list",
		},
		TS: time.Now().UTC(),
	}
	b, _ := json.Marshal(r)
	s := string(b)
	for _, want := range []string{`"ok":false`, `"code":"SESSION_NOT_FOUND"`, `"hint":"run`, `"schema":"1"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in error JSON: %s", want, s)
		}
	}
}

// Error codes are part of the public contract; clients switch on these strings.
// Adding a code is fine; renaming or removing one is a breaking change.
func TestErrorCodesAreStable(t *testing.T) {
	want := map[string]string{
		"USAGE_ERROR":          api.ErrUsage,
		"IPC_ERROR":            api.ErrIPC,
		"DAEMON_UNREACHABLE":   api.ErrDaemonUnreachable,
		"ADAPTER_NOT_FOUND":    api.ErrAdapterNotFound,
		"ADAPTER_FAILED":       api.ErrAdapterFailed,
		"SESSION_NOT_FOUND":    api.ErrSessionNotFound,
		"BREAKPOINT_DENIED":    api.ErrBreakpointDenied,
		"BREAKPOINT_PENDING":   api.ErrBreakpointPending,
		"TARGET_CRASHED":       api.ErrTargetCrashed,
		"TIMEOUT":              api.ErrTimeout,
		"READ_ONLY_MODE":       api.ErrReadOnly,
		"UNSUPPORTED_FEATURE":  api.ErrUnsupportedFeature,
		"INTERNAL_ERROR":       api.ErrInternal,
	}
	for literal, constant := range want {
		if literal != constant {
			t.Errorf("error code drift: literal=%q != constant=%q", literal, constant)
		}
	}
}

func TestSchemaVersionIsOne(t *testing.T) {
	// Bumping this constant signals a breaking change in the JSON envelope.
	// Bumping it requires a code review.
	if api.SchemaVersion != "1" {
		t.Errorf("SchemaVersion = %q, want %q. Confirm intentional breaking change.", api.SchemaVersion, "1")
	}
}
