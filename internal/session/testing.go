package session

// RegisterForTest exposes Manager.register to other packages' tests
// (e.g. internal/daemon). Test-only seam — not part of the public API.
func (m *Manager) RegisterForTest(s *Session) { m.register(s) }
