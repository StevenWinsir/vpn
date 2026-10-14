//go:build !windows

package main

import "os"

func privileged() bool { return os.Geteuid() == 0 }

const privilegeHint = "root is required to create a utun device (run with sudo -E)"

func machineFacts() map[string]any { return map[string]any{"os": "unix"} }

// probeExecutable is the program that stands in for an ordinary application.
func probeExecutable() (string, error) { return os.Executable() }

func cleanupProbe() {}
