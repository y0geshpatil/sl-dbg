// Watch-expression persistence across stop/start within the same daemon.
// Issue #4. The cache file is keyed on (lang, program, cwd) so each
// distinct debug-target gets its own watch set. Stored under the user's
// XDG state directory (or HOME fallback) with 0600 perms.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// watchStoreDir returns the directory where watch caches live. Best-effort:
// returns empty string if no usable home/state dir is available, in which
// case persistence becomes a no-op.
func watchStoreDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "sl-dbg")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "state", "sl-dbg")
	}
	return ""
}

// watchKey is the stable hash for the (lang, program, cwd) tuple. Empty
// string when there's no program (attach-only sessions don't persist).
func watchKey(lang, program, cwd string) string {
	if program == "" {
		return ""
	}
	h := sha256.Sum256([]byte(lang + "\x00" + program + "\x00" + cwd))
	return hex.EncodeToString(h[:16])
}

func watchFilePath(key string) string {
	d := watchStoreDir()
	if d == "" || key == "" {
		return ""
	}
	return filepath.Join(d, "watches-"+key+".json")
}

type persistedWatch struct {
	Expression string `json:"expression"`
}

// SaveWatches writes the session's current watch expressions to disk so a
// later session against the same target can repopulate them. No-op when
// there are no watches or no program key.
func (s *Session) SaveWatches() error {
	key := watchKey(s.Lang, s.Program, s.Cwd)
	path := watchFilePath(key)
	if path == "" {
		return nil
	}
	ws := s.Watches()
	if len(ws) == 0 {
		// Remove any stale file so an empty watch set after explicit Clear
		// doesn't get resurrected next run.
		_ = os.Remove(path)
		return nil
	}
	out := make([]persistedWatch, 0, len(ws))
	for _, w := range ws {
		out = append(out, persistedWatch{Expression: w.Expression})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// LoadWatches restores any persisted watches for this (lang, program, cwd)
// tuple. Called once after a fresh session is registered. Silently no-ops
// when no cache exists.
func (s *Session) LoadWatches() error {
	key := watchKey(s.Lang, s.Program, s.Cwd)
	path := watchFilePath(key)
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var rec []persistedWatch
	if err := json.Unmarshal(b, &rec); err != nil {
		// Corrupt cache: best-effort delete and continue.
		_ = os.Remove(path)
		return fmt.Errorf("corrupt watch cache at %s: %w", path, err)
	}
	for _, r := range rec {
		if r.Expression != "" {
			_ = s.AddWatch(r.Expression)
		}
	}
	return nil
}
