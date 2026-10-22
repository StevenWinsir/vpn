// Command asterlink-core is the Managed Core worker. It speaks the framed IPC protocol
// over a private channel owned by its supervisor (stdin/stdout pipes on macOS, inherited
// anonymous-pipe handles on Windows; see ipc_unix.go and ipc_windows.go). Diagnostics go
// to stderr and must never contain tokens, passwords or node configuration.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"asterlink/core/internal/engine"
	"asterlink/core/internal/ipc"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/service"
)

// signalShutdownBudget bounds the session's end after a signal: well inside the Helper's
// 12-second wait and the few seconds a system shutdown leaves a process.
const signalShutdownBudget = 4 * time.Second

// Set with -ldflags "-X main.version=... -X main.mihomoRevision=...".
var (
	version        = "0.0.0-dev"
	mihomoRevision = "70f0570405c3c2c47bb113b88db95006d239b346"
)

func main() {
	if len(os.Args) > 1 {
		if os.Args[1] == "--version" {
			fmt.Println(version, mihomoRevision)
			return
		}
		// Run by the Helper after an abnormal worker exit and by the uninstaller: puts
		// back a system DNS capture a killed Core left behind (engine/sysdns.go).
		if len(os.Args) == 2 && os.Args[1] == "--recover-network" {
			if err := engine.RestoreSystemDNS(); err != nil {
				fmt.Fprintln(os.Stderr, "network recovery failed:", err)
				os.Exit(1)
			}
			return
		}
		fmt.Fprintln(os.Stderr, "asterlink-core accepts no arguments")
		os.Exit(2)
	}
	channel, err := openIPC()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot open the ipc channel:", err)
		os.Exit(1)
	}
	// A previous Core killed while connected may have left the system resolver pointing
	// into a tunnel that no longer exists.
	if err := engine.RestoreSystemDNS(); err != nil {
		fmt.Fprintln(os.Stderr, "network recovery failed:", err)
	}
	manager := managed.New(managed.Options{CoreVersion: version, Engine: newEngine()})
	// The Helper's terminate() and a logout send signals that would otherwise exit
	// without Stop; the resolver must not stay pointed at a vanished tunnel. On Windows
	// the same channel carries console control events (Ctrl+C, Ctrl+Break, logoff,
	// shutdown), which the Go runtime delivers as SIGINT/SIGTERM. The resolver comes back
	// first; then, within a short budget, the session ends as on a normal exit (the data
	// path closed, the last batch settled, the online slot released), so a machine that
	// shuts down shows the device offline at once instead of after the lease runs out.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-signals
		_ = engine.RestoreSystemDNS()
		ctx, cancel := context.WithTimeout(context.Background(), signalShutdownBudget)
		done := make(chan struct{})
		go func() {
			manager.Shutdown(ctx)
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
		}
		cancel()
		_ = engine.RestoreSystemDNS()
		os.Exit(1)
	}()
	if err := ipc.ServeWith(channel, ipc.BuildInfo{CoreVersion: version, MihomoRevision: mihomoRevision}, service.New(manager)); err != nil {
		fmt.Fprintln(os.Stderr, "ipc terminated:", err)
		os.Exit(1)
	}
}
