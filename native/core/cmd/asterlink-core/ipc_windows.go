//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

// The Windows supervisor (the Service, round 3) hands the Core two anonymous pipe
// handles through the environment: the read end of the command pipe and the write end
// of the reply pipe. They are the only IPC channel; stdin/stdout/stderr are NOT used,
// because the embedded Mihomo captured the original os.Stdout at package init (its
// log package calls logrus.SetOutput(os.Stdout)), and on Windows a handle value cannot
// be re-pointed the way dup2 re-points file descriptor 1. A channel that is never
// stdout cannot be polluted by any log line, however it is produced (handoff_app.md
// PR #16; handoff_app_windows.md 4.2).
const (
	envIPCIn  = "ASTERLINK_IPC_IN"  // decimal handle value of the pipe the Core reads
	envIPCOut = "ASTERLINK_IPC_OUT" // decimal handle value of the pipe the Core writes
)

type pipeChannel struct{ in, out *os.File }

func (p pipeChannel) Read(b []byte) (int, error)  { return p.in.Read(b) }
func (p pipeChannel) Write(b []byte) (int, error) { return p.out.Write(b) }

var errNoChannel = errors.New("ipc handles are not set: the Core is started by its supervisor, not by hand")

func handleFromEnv(name string) (windows.Handle, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, errNoChannel
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s is not a handle value", name)
	}
	handle := windows.Handle(value)
	// A wrong or stale number must not be adopted as a channel: it could be a file or
	// the console. Only a pipe qualifies.
	kind, err := windows.GetFileType(handle)
	if err != nil || kind != windows.FILE_TYPE_PIPE {
		return 0, fmt.Errorf("%s does not refer to a pipe", name)
	}
	return handle, nil
}

// openIPC adopts the inherited pipe handles and diverts everything else away from them.
func openIPC() (io.ReadWriter, error) {
	in, err := handleFromEnv(envIPCIn)
	if err != nil {
		return nil, err
	}
	out, err := handleFromEnv(envIPCOut)
	if err != nil {
		return nil, err
	}
	if in == out {
		return nil, errors.New("the command and reply pipes must be distinct")
	}
	// Whatever the Core starts later must not inherit the channel.
	for _, h := range []windows.Handle{in, out} {
		if err := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			return nil, fmt.Errorf("cannot make the channel non-inheritable: %w", err)
		}
	}
	_ = os.Unsetenv(envIPCIn)
	_ = os.Unsetenv(envIPCOut)
	divertStdout()
	return pipeChannel{in: os.NewFile(uintptr(in), "ipc-in"), out: os.NewFile(uintptr(out), "ipc-out")}, nil
}

// divertStdout points every remaining "stdout" writer at stderr, which the supervisor
// discards: the process stdout handle, os.Stdout, and the logrus logger Mihomo writes to.
func divertStdout() {
	if h, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE); err == nil && h != 0 && h != windows.InvalidHandle {
		_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, h)
	}
	os.Stdout = os.Stderr
	logrus.SetOutput(os.Stderr)
}
