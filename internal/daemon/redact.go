// Redaction helpers for the daemon log. We never want secrets (env vars,
// program arguments matching credential patterns) ending up in the
// unrotated log file at $TMPDIR/sl-dbg-<uid>/daemon.sock.log.
package daemon

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Keys whose entire value we replace with "<redacted>" before logging.
// Match by case-insensitive substring on the JSON key name. We deliberately
// do NOT include "env" — env vars are a structured list, handled per-entry
// in scrubEnvList so that non-sensitive entries (PATH, JAVA_HOME, ...)
// stay visible.
var redactKeySubstrings = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"api_key",
	"apikey",
	"auth",
	"credential",
	"private",
	"cookie",
	"envfile", // path-to-env-file shouldn't go to logs either
}

// Patterns that mark a value as sensitive even when its key looks innocent
// (e.g. inside program args). We only check string values.
var sensitiveValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(sk|pk|ghp|github_pat|xox[abprs])_[a-z0-9_\-]{16,}`), // OpenAI / GitHub / Slack
	regexp.MustCompile(`(?i)aws_(access|secret)_key_id`),
	regexp.MustCompile(`(?i)(?:password|secret|token|api[_-]?key)\s*[=:]\s*[^\s,]+`),
}

// redactArgs returns a log-safe string rendering of a request's args. Falls
// back to the literal "<binary>" marker if the args aren't valid JSON.
func redactArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "<unparseable>"
	}
	scrub(&v)
	b, err := json.Marshal(v)
	if err != nil {
		return "<unmarshalable>"
	}
	return string(b)
}

func scrub(v *interface{}) {
	switch x := (*v).(type) {
	case map[string]interface{}:
		for k, val := range x {
			// Lists of "KEY=value" strings (env, envFile) get per-entry
			// treatment so non-sensitive entries survive in the log.
			if arr, ok := val.([]interface{}); ok && looksLikeEnvList(arr) {
				scrubEnvList(arr)
				x[k] = arr
				continue
			}
			if matchesRedactKey(k) {
				x[k] = "<redacted>"
				continue
			}
			scrubValue(&val)
			x[k] = val
		}
	case []interface{}:
		for i := range x {
			scrubValue(&x[i])
		}
	}
}

func looksLikeEnvList(arr []interface{}) bool {
	if len(arr) == 0 {
		return false
	}
	for _, it := range arr {
		s, ok := it.(string)
		if !ok || !strings.Contains(s, "=") {
			return false
		}
	}
	return true
}

func scrubEnvList(arr []interface{}) {
	for i, it := range arr {
		s := it.(string)
		idx := strings.IndexByte(s, '=')
		if idx <= 0 {
			continue
		}
		name := s[:idx]
		if matchesRedactKey(name) || hasSensitiveValue(s) {
			arr[i] = name + "=<redacted>"
		}
	}
}

func scrubValue(v *interface{}) {
	switch x := (*v).(type) {
	case map[string]interface{}:
		i := interface{}(x)
		scrub(&i)
	case []interface{}:
		if looksLikeEnvList(x) {
			scrubEnvList(x)
			return
		}
		i := interface{}(x)
		scrub(&i)
	case string:
		if hasSensitiveValue(x) {
			*v = "<redacted>"
		}
	}
}

func matchesRedactKey(k string) bool {
	lk := strings.ToLower(k)
	for _, sub := range redactKeySubstrings {
		if strings.Contains(lk, sub) {
			return true
		}
	}
	return false
}

func hasSensitiveValue(s string) bool {
	for _, p := range sensitiveValuePatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}
