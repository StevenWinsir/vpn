//go:build windows

package main

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

func TestInFakeRange(t *testing.T) {
	cases := map[string]bool{
		"198.18.0.7":               true,
		"93.184.216.34,198.18.1.1": true,
		"93.184.216.34":            false,
		"":                         false,
		"198.19.0.1":               false,
		"not an address":           false,
	}
	for in, want := range cases {
		if got := inFakeRange(in); got != want {
			t.Errorf("inFakeRange(%q) = %v, want %v", in, got, want)
		}
	}
}

func sntpServer(t *testing.T, reply bool) string {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 128)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if reply && n >= 48 {
				answer := make([]byte, 48)
				answer[0] = 0x24 // LI=0, VN=4, mode=4 (server)
				_, _ = conn.WriteTo(answer, addr)
			}
		}
	}()
	return conn.LocalAddr().String()
}

func TestNtpQueryDistinguishesReplyFromSilence(t *testing.T) {
	old := ntpWait
	ntpWait = 150 * time.Millisecond
	defer func() { ntpWait = old }()
	ctx := context.Background()
	if got := ntpQuery(ctx, sntpServer(t, true)); got != "reply" {
		t.Fatalf("answering server: %q", got)
	}
	if got := ntpQuery(ctx, sntpServer(t, false)); got != "noreply" {
		t.Fatalf("silent server: %q", got)
	}
}

// A child must run from a different path than the harness, or it would share the Core's
// WFP exemption (ALE_APP_ID is the executable path).
func TestProbeExecutableIsADifferentPath(t *testing.T) {
	defer cleanupProbe()
	path, err := probeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if self, _ := os.Executable(); path == self {
		t.Fatal("the probe shares the harness's path")
	}
}
