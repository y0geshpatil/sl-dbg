package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/y0geshpatil/sl-dbg/pkg/api"
)

// emit prints a successful response to stdout in JSON form.
// Honors --pretty and --quiet.
func emit(data interface{}) {
	if gFlags.Quiet {
		return
	}
	resp := api.Response{Schema: api.SchemaVersion, OK: true, Data: data, TS: time.Now().UTC()}
	writeJSON(resp)
}

// emitErr prints an error response to stdout (still structured) and stderr.
func emitErr(code, msg, hint string) {
	resp := api.Response{
		Schema: api.SchemaVersion,
		OK:     false,
		Error:  &api.Error{Code: code, Message: msg, Hint: hint},
		TS:     time.Now().UTC(),
	}
	writeJSON(resp)
}

func writeJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	if gFlags.Pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "sl-dbg: failed to encode response: %v\n", err)
	}
}
