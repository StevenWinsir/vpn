//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

func privileged() bool { return windows.GetCurrentProcessToken().IsElevated() }

const privilegeHint = "start PowerShell with \"Run as administrator\" (a Wintun adapter and WFP filters need an elevated token)"

func machineFacts() map[string]any {
	v := windows.RtlGetVersion()
	return map[string]any{
		"os":         "windows",
		"version":    fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber),
		"client_sku": v.ProductType == 1,
		"goarch":     runtime.GOARCH,
		"elevated":   privileged(),
	}
}

var (
	probeOnce sync.Once
	probePath string
	probeErr  error
)

// probeExecutable returns a COPY of this exe at another path. sing-tun's firewall exemption
// is keyed on the executable path, so a child started from the harness's own path would
// bypass the tunnel (and the meter) exactly like the Core does; the copy behaves like any
// other application.
func probeExecutable() (string, error) {
	probeOnce.Do(func() {
		self, err := os.Executable()
		if err != nil {
			probeErr = err
			return
		}
		data, err := os.ReadFile(self)
		if err != nil {
			probeErr = err
			return
		}
		dir, err := os.MkdirTemp("", "asterlink-probe-")
		if err != nil {
			probeErr = err
			return
		}
		probePath = filepath.Join(dir, "probe-app.exe")
		probeErr = os.WriteFile(probePath, data, 0o700)
	})
	return probePath, probeErr
}

func cleanupProbe() {
	if probePath != "" {
		_ = os.RemoveAll(filepath.Dir(probePath))
	}
}
