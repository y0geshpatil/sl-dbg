// Package buildinfo holds version metadata injected at build time via -ldflags.
package buildinfo

var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
	Date    = "unknown"
)
