// Package dap is a thin client wrapper over the Debug Adapter Protocol.
//
// It will be implemented on top of github.com/google/go-dap. Each Session in
// internal/daemon owns one dap.Client which speaks DAP over the adapter's
// stdin/stdout.
//
// Status: scaffold only. See docs/ROADMAP.md (Phase 1).
package dap
