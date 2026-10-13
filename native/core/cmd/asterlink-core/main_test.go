//go:build !windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"

	mlog "github.com/metacubex/mihomo/log"

	"asterlink/core/internal/ipc"
)

const childEnv = "ASTERLINK_CORE_STDOUT_CHILD"

// The child reserves the IPC pipe exactly like main, then lets the embedded Mihomo and
// plain fmt write to "stdout" before and after sending one frame. Only the frame may
// reach the parent's pipe.
func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		out, err := reserveIPCOutput()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		mlog.SetLevel(mlog.INFO)
		mlog.Warnln("[TUN] noise before the frame")
		fmt.Println("plain stdout noise")
		if err := ipc.WriteFrame(stdio{out: out}, []byte(`{"ok":true}`)); err != nil {
			os.Exit(4)
		}
		mlog.Infoln("noise after the frame")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestEngineLogsNeverReachIPCPipe(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{childEnv + "=1"}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("child failed: %v (stderr %q)", err, stderr.String())
	}
	frame, err := ipc.ReadFrame(&stdout, ipc.MaxFrame)
	if err != nil {
		t.Fatalf("pipe does not start with a valid frame: %v (raw %q)", err, stdout.String())
	}
	if string(frame) != `{"ok":true}` || stdout.Len() != 0 {
		t.Fatalf("pipe carried more than the frame: frame %q, trailing %q", frame, stdout.String())
	}
	for _, want := range []string{"noise before the frame", "plain stdout noise", "noise after the frame"} {
		if !bytes.Contains(stderr.Bytes(), []byte(want)) {
			t.Fatalf("diagnostic %q should have been diverted to stderr, got %q", want, stderr.String())
		}
	}
}
