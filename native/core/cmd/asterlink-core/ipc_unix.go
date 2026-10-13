//go:build !windows

package main

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
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

// openIPC returns the framed channel to the Helper.
func openIPC() (io.ReadWriter, error) {
	out, err := reserveIPCOutput()
	if err != nil {
		return nil, err
	}
	return stdio{out: out}, nil
}
