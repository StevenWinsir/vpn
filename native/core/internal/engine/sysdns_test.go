//go:build !windows && !android

package engine

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"asterlink/core/internal/network"
)

const serviceOrder = `An asterisk (*) denotes that a network service is disabled.
(1) Ethernet
(Hardware Port: Ethernet, Device: en0)

(2) Wi-Fi
(Hardware Port: Wi-Fi, Device: en1)

(*) Thunderbolt Bridge
(Hardware Port: Thunderbolt Bridge, Device: bridge0)

(3) Shadowrocket
(Hardware Port: com.liguangming.Shadowrocket, Device: )
`

// fakeNetworkSetup emulates networksetup over an in-memory service table.
type fakeNetworkSetup struct {
	dns     map[string][]string
	failSet map[string]bool
	sets    []string
}

func installFakeSystemDNS(t *testing.T, dns map[string][]string) *fakeNetworkSetup {
	t.Helper()
	fake := &fakeNetworkSetup{dns: dns, failSet: map[string]bool{}}
	dir := t.TempDir()
	conf := filepath.Join(dir, "resolv.conf")
	if err := os.WriteFile(conf, []byte("# generated\nnameserver 192.168.1.1\nnameserver fe80::1%en1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldSetup, oldFlush, oldRecord, oldConf := networksetup, flushResolverCache, SystemDNSRecord, resolvConf
	networksetup = func(args ...string) (string, error) {
		switch args[0] {
		case "-listnetworkserviceorder":
			return serviceOrder, nil
		case "-getdnsservers":
			servers, ok := fake.dns[args[1]]
			if !ok {
				return args[1] + " is not a recognized network service.\n", errors.New("exit status 4")
			}
			if len(servers) == 0 {
				return "There aren't any DNS Servers set on " + args[1] + ".\n", nil
			}
			return strings.Join(servers, "\n") + "\n", nil
		case "-setdnsservers":
			fake.sets = append(fake.sets, args[1])
			if fake.failSet[args[1]] {
				return "", errors.New("exit status 1")
			}
			if args[2] == "Empty" {
				fake.dns[args[1]] = nil
			} else {
				fake.dns[args[1]] = slices.Clone(args[2:])
			}
			return "", nil
		}
		t.Fatalf("unexpected networksetup %v", args)
		return "", nil
	}
	flushResolverCache = func() {}
	SystemDNSRecord = filepath.Join(dir, "state", "dns-override.json")
	resolvConf = conf
	t.Cleanup(func() {
		networksetup, flushResolverCache, SystemDNSRecord, resolvConf = oldSetup, oldFlush, oldRecord, oldConf
		network.Default.SetResolvers(nil)
	})
	return fake
}

func recordExists() bool {
	_, err := os.Stat(SystemDNSRecord)
	return err == nil
}

func TestHardwareServicesSkipDisabledAndVPN(t *testing.T) {
	installFakeSystemDNS(t, map[string][]string{})
	services, err := hardwareServices()
	if err != nil {
		t.Fatal(err)
	}
	want := []hardwareService{{"Ethernet", "en0"}, {"Wi-Fi", "en1"}}
	if !slices.Equal(services, want) {
		t.Fatalf("services = %v, want %v", services, want)
	}
}

// Both the DHCP router case and the manual 1.1.1.1 case end up on TunnelDNS, and
// both come back exactly as they were.
func TestCaptureAndRestoreSystemDNS(t *testing.T) {
	fake := installFakeSystemDNS(t, map[string][]string{"Ethernet": {"1.1.1.1"}, "Wi-Fi": nil, "Thunderbolt Bridge": nil})
	if err := captureSystemDNS("en1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Ethernet", "Wi-Fi"} {
		if !slices.Equal(fake.dns[name], []string{TunnelDNS}) {
			t.Fatalf("%s not captured: %v", name, fake.dns[name])
		}
	}
	if fake.dns["Thunderbolt Bridge"] != nil {
		t.Fatal("a disabled service was changed")
	}
	if !recordExists() {
		t.Fatal("no restore record while captured")
	}
	if info, _ := os.Stat(SystemDNSRecord); info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode %v", info.Mode().Perm())
	}
	if got := network.Default.Resolvers(); !slices.Equal(got, []string{"192.168.1.1", "fe80::1%en1"}) {
		t.Fatalf("control plane bootstrap resolvers = %v", got)
	}

	if err := RestoreSystemDNS(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fake.dns["Ethernet"], []string{"1.1.1.1"}) || fake.dns["Wi-Fi"] != nil {
		t.Fatalf("not restored: %v", fake.dns)
	}
	if recordExists() || network.Default.Resolvers() != nil {
		t.Fatal("record or bootstrap resolvers left behind")
	}
	if err := RestoreSystemDNS(); err != nil { // idempotent
		t.Fatal(err)
	}
}

// Ownership: a service the user re-configured during the session keeps the new value.
func TestRestoreLeavesUserChanges(t *testing.T) {
	fake := installFakeSystemDNS(t, map[string][]string{"Ethernet": {"1.1.1.1"}, "Wi-Fi": {"9.9.9.9"}})
	if err := captureSystemDNS("en1"); err != nil {
		t.Fatal(err)
	}
	fake.dns["Wi-Fi"] = []string{"8.8.4.4"}
	if err := RestoreSystemDNS(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fake.dns["Wi-Fi"], []string{"8.8.4.4"}) || !slices.Equal(fake.dns["Ethernet"], []string{"1.1.1.1"}) {
		t.Fatalf("restore overwrote a newer setting: %v", fake.dns)
	}
}

// Connecting without the physical service captured would leak every lookup.
func TestCaptureFailsClosedAndRollsBack(t *testing.T) {
	fake := installFakeSystemDNS(t, map[string][]string{"Ethernet": {"1.1.1.1"}, "Wi-Fi": nil})
	fake.failSet["Wi-Fi"] = true
	if err := captureSystemDNS("en1"); !errors.Is(err, ErrDNSCapture) {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(fake.dns["Ethernet"], []string{"1.1.1.1"}) || fake.dns["Wi-Fi"] != nil {
		t.Fatalf("partial capture not rolled back: %v", fake.dns)
	}
	if recordExists() {
		t.Fatal("record left after rollback")
	}
}

// A Core killed while connected leaves its record; the next capture (or start-up
// recovery) restores the true originals instead of recording TunnelDNS as original.
func TestStaleRecordIsRecoveredBeforeNewCapture(t *testing.T) {
	fake := installFakeSystemDNS(t, map[string][]string{"Ethernet": nil, "Wi-Fi": {"1.1.1.1"}})
	if err := captureSystemDNS("en1"); err != nil {
		t.Fatal(err)
	}
	// Simulated kill: nothing restored, record still on disk.
	if err := captureSystemDNS("en1"); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSystemDNS(); err != nil {
		t.Fatal(err)
	}
	if fake.dns["Ethernet"] != nil || !slices.Equal(fake.dns["Wi-Fi"], []string{"1.1.1.1"}) {
		t.Fatalf("originals lost across a crash: %v", fake.dns)
	}
}

func TestUnreadableRecordIsDiscardedWithoutChanges(t *testing.T) {
	fake := installFakeSystemDNS(t, map[string][]string{"Wi-Fi": {TunnelDNS}})
	if err := os.MkdirAll(filepath.Dir(SystemDNSRecord), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SystemDNSRecord, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSystemDNS(); err != nil {
		t.Fatal(err)
	}
	if len(fake.sets) != 0 || recordExists() {
		t.Fatalf("guessed a restore from a corrupt record: sets=%v", fake.sets)
	}
}
