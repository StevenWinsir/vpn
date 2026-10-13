//go:build windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"

	mlog "github.com/metacubex/mihomo/log"

	"asterlink/core/internal/ipc"
)

const childMode = "ASTERLINK_CORE_CHILD_MODE"

// The child adopts the IPC handles exactly like main, then lets the embedded Mihomo and
// plain fmt write to "stdout" before and after sending one frame. Only the frame may reach
// the parent's reply pipe (case S03, "真实 logger 不污染控制帧").
func TestMain(m *testing.M) {
	switch os.Getenv(childMode) {
	case "frame":
		channel, err := openIPC()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		mlog.SetLevel(mlog.INFO)
		mlog.Warnln("[TUN] noise before the frame")
		fmt.Println("plain stdout noise")
		// The command pipe is the Core's input: echo what the parent sent as the frame body.
		request, err := ipc.ReadFrame(channel, ipc.MaxFrame)
		if err != nil {
			os.Exit(5)
		}
		if err := ipc.WriteFrame(channel, request); err != nil {
			os.Exit(4)
		}
		mlog.Infoln("noise after the frame")
		os.Exit(0)
	case "open":
		if _, err := openIPC(); err != nil {
			fmt.Fprintln(os.Stderr, "REFUSED:", err)
			os.Exit(7)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestEngineLogsNeverReachIPCPipes(t *testing.T) {
	// parent -> child commands
	cmdRead, cmdWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer cmdRead.Close()
	defer cmdWrite.Close()
	// child -> parent replies
	replyRead, replyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer replyRead.Close()
	defer replyWrite.Close()

	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(),
		childMode+"=frame",
		envIPCIn+"="+strconv.FormatUint(uint64(cmdRead.Fd()), 10),
		envIPCOut+"="+strconv.FormatUint(uint64(replyWrite.Fd()), 10),
	)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	child.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(cmdRead.Fd()), syscall.Handle(replyWrite.Fd())}}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := ipc.WriteFrame(cmdWrite, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	_ = replyWrite.Close() // so the parent sees EOF once the child exits
	frame, readErr := ipc.ReadFrame(replyRead, ipc.MaxFrame)
	waitErr := child.Wait()
	if readErr != nil || waitErr != nil {
		t.Fatalf("read=%v wait=%v stdout=%q stderr=%q", readErr, waitErr, stdout.String(), stderr.String())
	}
	if string(frame) != `{"ok":true}` {
		t.Fatalf("frame = %q", frame)
	}
	// Nothing but the one frame travelled on the reply pipe.
	var rest bytes.Buffer
	if _, err := rest.ReadFrom(replyRead); err != nil || rest.Len() != 0 {
		t.Fatalf("reply pipe carried more than the frame: %q (%v)", rest.String(), err)
	}
	// Every kind of log line was diverted to the diagnostic stream, none to a channel.
	all := stdout.String() + stderr.String()
	for _, want := range []string{"noise before the frame", "plain stdout noise", "noise after the frame"} {
		if !bytes.Contains([]byte(all), []byte(want)) {
			t.Fatalf("diagnostic %q missing from stdout/stderr: %q", want, all)
		}
	}
}

func runOpen(t *testing.T, env ...string) (int, string) {
	t.Helper()
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), append([]string{childMode + "=open"}, env...)...)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	err := child.Run()
	if err == nil {
		return 0, stderr.String()
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), stderr.String()
	}
	t.Fatal(err)
	return -1, ""
}

// Started by hand (or with a wrong environment) the Core must refuse rather than fall
// back to stdout, where a log line could be mistaken for a frame header.
func TestOpenIPCFailsClosed(t *testing.T) {
	if code, out := runOpen(t); code != 7 {
		t.Fatalf("no handles: exit %d (%s), want refusal", code, out)
	}
	if code, out := runOpen(t, envIPCIn+"=abc", envIPCOut+"=4"); code != 7 {
		t.Fatalf("garbage handle: exit %d (%s)", code, out)
	}
	file, err := os.CreateTemp(t.TempDir(), "notapipe")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	handle := strconv.FormatUint(uint64(file.Fd()), 10)
	// The temp file is not inherited by the child, and even a valid number in this
	// process would not be a pipe: either way the child must refuse.
	if code, out := runOpen(t, envIPCIn+"="+handle, envIPCOut+"="+handle); code != 7 {
		t.Fatalf("non-pipe handle: exit %d (%s)", code, out)
	}
}
