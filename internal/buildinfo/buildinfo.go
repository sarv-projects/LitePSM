// Package buildinfo carries the build's version string to library packages
// that cannot import package main. cmd/litespm assigns Version from its
// ldflags-injected main.Version at start-up.
package buildinfo

// Version is the LiteSPM build version. The default only applies to binaries
// that never ran cmd/litespm's initializer (for example library tests).
var Version = "dev"
