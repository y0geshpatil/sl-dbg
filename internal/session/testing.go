package session

import "github.com/y0geshpatil/sl-dbg/internal/dap"

// RegisterForTest exposes Manager.register to other packages' tests
// (e.g. internal/daemon). Test-only seam — not part of the public API.
func (m *Manager) RegisterForTest(s *Session) { m.register(s) }

// NewForTest connects handler tests to an in-memory DAP transport.
func NewForTest(cli *dap.Client, lang string, pendingConfigDone bool) *Session {
	s := newSession(cli, nil, lang)
	s.caps = cli.Caps
	s.pendingConfigDone = pendingConfigDone
	s.startEventPump(make(chan struct{}, 1))
	return s
}
