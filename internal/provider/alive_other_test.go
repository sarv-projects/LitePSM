//go:build !unix

package provider

func processAlive(pid int) bool { return false }
