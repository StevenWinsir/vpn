// Command asterlink-core is the Managed Core worker. It speaks the framed IPC protocol
// over stdin/stdout; the Helper owns the pipes. Diagnostics go to stderr
// and must never contain tokens, passwords or node configuration.
package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"

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
		fmt.Fprintln(os.Stderr, "asterlink-core accepts no arguments")
		os.Exit(2)
	}
	out, err := reserveIPCOutput()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot reserve ipc output:", err)
		os.Exit(1)
	}
	manager := managed.New(managed.Options{CoreVersion: version, Engine: newEngine()})
	if err := ipc.ServeWith(stdio{out: out}, ipc.BuildInfo{CoreVersion: version, MihomoRevision: mihomoRevision}, service.New(manager)); err != nil {
		fmt.Fprintln(os.Stderr, "ipc terminated:", err)
		os.Exit(1)
	}
}
