package main

import "github.com/sarv-projects/litespm/internal/buildinfo"

// Release builds inject main.Version with -ldflags; publish it to the library
// packages (IPC client, MCP bridge) so they report the real build version
// instead of a hardcoded literal.
func init() {
	buildinfo.Version = Version
}
