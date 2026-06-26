// Package adapter contains the registry of language DAP adapters, their
// detection and auto-install logic, and per-language launch configs.
//
// To add a new language, drop a file <lang>.go in this package with a
// package-level init() that calls Register(Adapter{...}).
//
// Status: scaffold only.
package adapter
