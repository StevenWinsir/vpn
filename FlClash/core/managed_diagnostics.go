package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
)

const managedDiagnosticsName = "managed-diagnostics.log"
const managedDiagnosticsLimit = 128 << 10

var managedDiagnosticsMu sync.Mutex

// managedLogError keeps the Core log and a bounded local file in step so a
// failed connection can be explained after the app is closed. Messages must
// stay free of credentials, tokens and configuration content.
func managedLogError(format string, args ...any) {
	log.Errorln(format, args...)
	managedDiagnostic(format, args...)
}

func managedDiagnostic(format string, args ...any) {
	managedDiagnosticsMu.Lock()
	defer managedDiagnosticsMu.Unlock()
	path := filepath.Join(constant.Path.HomeDir(), managedDiagnosticsName)
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Size() > managedDiagnosticsLimit) {
		if !info.Mode().IsRegular() {
			return
		}
		_ = os.Remove(path)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s euid=%d uid=%d %s\n", time.Now().Format(time.RFC3339), os.Geteuid(), os.Getuid(), fmt.Sprintf(format, args...))
}
