// Command asterlink-core is the Managed Core worker. It speaks the framed IPC protocol
// over stdin/stdout; the Helper owns the pipes. Diagnostics go to stderr
// and must never contain tokens, passwords or node configuration.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"

	"asterlink/core/internal/engine"
	"asterlink/core/internal/ipc"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/service"
)

// Set with -ldflags "-X main.version=... -X main.mihomoRevision=...".
var (
	version        = "0.0.0-dev"
	mihomoRevision = "70f0570405c3c2c47bb113b88db95006d239b346"
)

type stdio struct{ out *os.File }

func (stdio) Read(p []byte) (int, error)    { return os.Stdin.Read(p) }
func (s stdio) Write(p []byte) (int, error) { return s.out.Write(p) }

// reserveIPCOutput keeps the Helper's stdout pipe for IPC frames only. The embedded
// Mihomo logs through logrus to os.Stdout; a single log line on the frame stream makes
// the Helper read text as a length header and kill the worker ("worker connection
// lost"). So the pipe is moved to a private descriptor and fd 1 is pointed at stderr,
// which the Helper discards, before any engine code runs.
func reserveIPCOutput() (*os.File, error) {
	fd, err := unix.Dup(1)
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	if err := unix.Dup2(2, 1); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), "ipc-out"), nil
}

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
	out, err := reserveIPCOutput()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot reserve ipc output:", err)
		os.Exit(1)
	}
	// A previous Core killed while connected may have left the system resolver pointing
	// into a tunnel that no longer exists.
	if err := engine.RestoreSystemDNS(); err != nil {
		fmt.Fprintln(os.Stderr, "network recovery failed:", err)
	}
	// The Helper's terminate() and a logout send signals that would otherwise exit
	// without Stop; the resolver must not stay pointed at a vanished tunnel.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-signals
		_ = engine.RestoreSystemDNS()
		os.Exit(1)
	}()
	manager := managed.New(managed.Options{CoreVersion: version, Engine: newEngine()})
	if err := ipc.ServeWith(stdio{out: out}, ipc.BuildInfo{CoreVersion: version, MihomoRevision: mihomoRevision}, service.New(manager)); err != nil {
		fmt.Fprintln(os.Stderr, "ipc terminated:", err)
		os.Exit(1)
	}
}
