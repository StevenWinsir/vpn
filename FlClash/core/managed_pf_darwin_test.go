//go:build darwin && !cgo

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/metacubex/mihomo/tunnel"
)

// Executed only as a child of the isolated real-network regression. Exit
// without Close simulates SIGKILL without weakening the production cleanup.
func TestManagedMacOSAbandonedPFLease(t *testing.T) {
	if os.Getenv("RUN_MANAGED_PF_CRASH_CHILD") != "1" {
		t.Skip("isolated crash-recovery child only")
	}
	if os.Geteuid() != 0 {
		t.Fatal("crash fixture requires root")
	}
	cfg := managedMacOSTunConfig()
	cfg.AutoRoute, cfg.AutoDetectInterface = false, false
	cfg.RouteAddress, cfg.DNSHijack = nil, nil
	resources, err := openManagedMacOSTun(context.Background(), cfg, tunnel.Tunnel)
	if err != nil {
		if resources != nil {
			_ = resources.Close()
		}
		t.Fatal(err)
	}
	os.Exit(0)
}

func testManagedPFRecovery(t *testing.T) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestManagedMacOSAbandonedPFLease$", "-test.v")
	command.Env = append(os.Environ(), "RUN_MANAGED_PF_CRASH_CHILD=1")
	output, err := command.CombinedOutput()
	if err != nil {
		_ = recoverManagedPFLeases(context.Background())
		t.Fatalf("create abandoned PF fixture: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(managedPFDirectory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("crashed owner did not leave exactly one recoverable lease: %v", err)
	}
	runManagedMacOSTUNUDP(t, true)
	entries, err = os.ReadDir(managedPFDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("reconnect did not reclaim the dead owner's PF lease: %v", err)
	}
}

func TestManagedPFEgressRulesAndInputValidation(t *testing.T) {
	rules, err := managedPFRules("utun12")
	if err != nil || strings.Count(rules, "block drop out quick") != 4 {
		t.Fatal("missing dual-stack TCP/UDP egress policy")
	}
	for _, label := range []string{"tcp4", "udp4", "tcp6", "udp6"} {
		if !strings.Contains(rules, managedPFLabel+"_"+label) {
			t.Fatalf("missing observable policy for %s", label)
		}
	}
	if strings.Contains(rules, "pass ") || strings.Count(rules, "user != 0") != 4 || strings.Count(rules, "on ! utun12") != 4 {
		t.Fatal("PF policy bypasses another filter or blocks the privileged proxy")
	}
	for _, device := range []string{"", "en0", "utun", "utun1\npass all", "utun1;"} {
		if _, err := managedPFRules(device); err == nil {
			t.Fatalf("accepted unsafe interface %q", device)
		}
	}
	for _, token := range []string{"Token : 12345\n", "pf enabled\nToken: 999\n"} {
		if _, err := managedPFToken(token); err != nil {
			t.Fatal(err)
		}
	}
	for _, token := range []string{"", "Token: -1", "Token: 1;foo", "Token: 18446744073709551616"} {
		if _, err := managedPFToken(token); err == nil {
			t.Fatalf("accepted unsafe token %q", token)
		}
	}
	valid := managedPFLease{PID: 123, Anchor: "com.apple/000.FlClash.123", Token: "456"}
	if !managedPFValidLease(valid) {
		t.Fatal("rejected valid owned lease")
	}
	for _, lease := range []managedPFLease{
		{PID: 123, Anchor: "com.apple/some-other-app"},
		{PID: 123, Anchor: valid.Anchor, Token: "-1"},
		{PID: 123, Anchor: valid.Anchor, RootRule: "pass all"},
		{PID: 0, Anchor: "com.apple/000.FlClash.0"},
	} {
		if managedPFValidLease(lease) {
			t.Fatal("saved lease could alter another app's filter")
		}
	}
}

// A fresh non-setuid copy is essential: executing the setuid CI test binary
// directly would silently test UID 0, not the ordinary browser user's policy.
func managedUnprivilegedProbeBinary(t *testing.T) (string, *syscall.Credential) {
	t.Helper()
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		var err error
		uid, err = strconv.Atoi(os.Getenv("SUDO_UID"))
		if err != nil || uid <= 0 {
			t.Fatal("isolated sudo runner must identify its original non-root UID")
		}
		gid, err = strconv.Atoi(os.Getenv("SUDO_GID"))
		if err != nil || gid < 0 {
			t.Fatal("isolated sudo runner must identify its original GID")
		}
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	directory, err := os.MkdirTemp("/tmp", "flclash-unprivileged-probe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "probe.test")
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy isolated non-setuid test probe: %v / %v", copyErr, closeErr)
	}
	// Byte-copying an arm64 Mach-O into a reused vnode can leave Darwin's
	// code-signing cache stale. Sign and verify this exact immutable probe,
	// rather than retrying a killed process or skipping a network assertion.
	for _, arguments := range [][]string{
		{"--force", "--sign", "-", path},
		{"--verify", "--strict", path},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		result, signErr := exec.CommandContext(ctx, "/usr/bin/codesign", arguments...).CombinedOutput()
		cancel()
		if signErr != nil {
			t.Fatalf("sign isolated ordinary-user probe: %v: %s", signErr, result)
		}
	}
	return path, &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
}

func runManagedUnprivilegedUDP(t *testing.T, binary string, credential *syscall.Credential, endpoint managedTunUDPEndpoint) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestManagedMacOSUnprivilegedUDPProbe$", "-test.v")
	command.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
	command.Env = []string{"RUN_MANAGED_UNPRIVILEGED_PROBE=1", "MANAGED_PROBE_NETWORK=" + endpoint.network, "MANAGED_PROBE_ADDRESS=" + endpoint.address}
	if endpoint.source != nil {
		command.Env = append(command.Env, "MANAGED_PROBE_SOURCE="+endpoint.source.String(), "MANAGED_PROBE_INTERFACE="+strconv.Itoa(endpoint.interfaceIndex))
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unprivileged UDP probe failed: %v\n%s", err, output)
	}
}

func TestManagedMacOSUnprivilegedUDPProbe(t *testing.T) {
	if os.Getenv("RUN_MANAGED_UNPRIVILEGED_PROBE") != "1" {
		t.Skip("child-only isolated TUN probe")
	}
	if os.Geteuid() == 0 {
		t.Fatal("probe retained root privilege instead of testing a browser UID")
	}
	dialer := &net.Dialer{Timeout: time.Second}
	source := os.Getenv("MANAGED_PROBE_SOURCE")
	if source != "" {
		dialer.LocalAddr = &net.UDPAddr{IP: net.ParseIP(source)}
		index, err := strconv.Atoi(os.Getenv("MANAGED_PROBE_INTERFACE"))
		if err != nil || index <= 0 {
			t.Fatal("missing physical interface for an explicitly scoped probe")
		}
		dialer.Control = managedPhysicalSocketControl(index)
	}
	connection, err := dialer.Dial(os.Getenv("MANAGED_PROBE_NETWORK"), os.Getenv("MANAGED_PROBE_ADDRESS"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	payload := []byte("managed-unprivileged-udp")
	_, writeErr := connection.Write(payload)
	if source != "" && writeErr != nil {
		// The parent must additionally prove that the real PF UDP counter rose.
		return
	}
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	buffer := make([]byte, 256)
	n, readErr := connection.Read(buffer)
	if source != "" {
		if readErr == nil {
			t.Fatal("source-bound traffic unexpectedly escaped the physical-egress guard")
		}
		return
	}
	if readErr != nil || !bytes.Equal(buffer[:n], payload) {
		t.Fatal(fmt.Errorf("normal unprivileged UDP must still traverse TUN: %w", readErr))
	}
}
