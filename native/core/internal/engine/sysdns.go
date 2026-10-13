//go:build !windows

package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"asterlink/core/internal/network"
)

// System DNS capture (handoff_app.md section 6.2: "业务 DNS 由受控 resolver 经选中节点处理").
//
// The pinned sing-tun only installs routes on macOS; it never touches the system
// resolver. dns-hijack therefore sees a query only if its packet enters the utun, and
// two common setups never do:
//   - DHCP DNS on the LAN (the router, or an IPv6 link-local RDNSS): the on-link subnet
//     route is more specific than the TUN's split routes, so mDNSResponder's queries
//     leave through Wi-Fi/Ethernet and the ISP resolves every name.
//   - A public resolver such as 1.1.1.1 / 8.8.8.8: Chrome recognises it and silently
//     upgrades to DoH on TCP 443, which the hijack cannot see, so the provider resolves
//     names from its own location instead of the node's.
// While connected the Core points every enabled hardware network service at an address
// inside the TUN's /30, so all system lookups are answered by the fake-ip resolver and
// the node resolves the real name. Before anything changes, the previous servers are
// written to SystemDNSRecord; Stop, the next Core start, the Helper (after an abnormal
// worker exit) and the uninstaller restore from it, and only services still pointing
// at TunnelDNS are restored, so a change the user made meanwhile is never overwritten.

// TunnelDNS, DNSCaptured and ErrDNSCapture are shared with Windows: sysdns_common.go.

// SystemDNSRecord is root-owned like the worker store. A variable only for tests.
var SystemDNSRecord = "/Library/Application Support/AsterLink/dns-override.json"

type dnsService struct {
	Name string `json:"name"`
	// Servers are the manually configured servers; empty means automatic (DHCP/RA).
	Servers []string `json:"servers"`
}

type dnsRecord struct {
	Version   int          `json:"version"`
	TunnelDNS string       `json:"tunnel_dns"`
	Services  []dnsService `json:"services"`
}

// networksetup runs /usr/sbin/networksetup with a fixed argument vector (no shell).
var networksetup = func(args ...string) (string, error) {
	out, err := exec.Command("/usr/sbin/networksetup", args...).CombinedOutput()
	return string(out), err
}

// flushResolverCache drops answers cached before or during the session.
var flushResolverCache = func() {
	_ = exec.Command("/usr/bin/dscacheutil", "-flushcache").Run()
	_ = exec.Command("/usr/bin/killall", "-HUP", "mDNSResponder").Run()
}

// resolvConf is where the machine's current default resolvers are read from.
var resolvConf = "/etc/resolv.conf"

// sysDNSMu serialises capture and restore across Stop, recovery and signal handling.
var sysDNSMu sync.Mutex

type hardwareService struct{ name, device string }

// hardwareServices parses `networksetup -listnetworkserviceorder`: enabled services
// bound to a device. VPN services (empty device) and disabled ones ("(*)") are skipped.
func hardwareServices() ([]hardwareService, error) {
	out, err := networksetup("-listnetworkserviceorder")
	if err != nil {
		return nil, err
	}
	var found []hardwareService
	name := ""
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "(Hardware Port:"):
			device := ""
			if i := strings.LastIndex(line, "Device:"); i >= 0 {
				device = strings.TrimSpace(strings.TrimSuffix(line[i+len("Device:"):], ")"))
			}
			if name != "" && device != "" && !strings.HasPrefix(device, "utun") && !strings.HasPrefix(device, "ppp") {
				found = append(found, hardwareService{name, device})
			}
			name = ""
		case strings.HasPrefix(line, "(*)"):
			name = ""
		case strings.HasPrefix(line, "("):
			if i := strings.Index(line, ") "); i > 0 {
				name = line[i+2:]
			}
		}
	}
	return found, nil
}

// manualServers returns a service's manual DNS servers; networksetup reports
// "There aren't any DNS Servers set on <name>." for automatic DNS.
func manualServers(service string) ([]string, error) {
	out, err := networksetup("-getdnsservers", service)
	if err != nil {
		return nil, err
	}
	var servers []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if _, err := netip.ParseAddr(line); err == nil {
			servers = append(servers, line)
		}
	}
	return servers, nil
}

func setServers(service string, servers []string) error {
	args := []string{"-setdnsservers", service}
	if len(servers) == 0 {
		args = append(args, "Empty")
	} else {
		args = append(args, servers...)
	}
	_, err := networksetup(args...)
	return err
}

// currentResolvers reads the default resolvers macOS publishes in /etc/resolv.conf.
func currentResolvers() []string {
	data, err := os.ReadFile(resolvConf)
	if err != nil {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "nameserver" && f[1] != TunnelDNS {
			servers = append(servers, f[1])
		}
	}
	return servers
}

func writeRecord(record dnsRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(SystemDNSRecord), 0o755); err != nil {
		return err
	}
	tmp := SystemDNSRecord + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, SystemDNSRecord)
}

// captureSystemDNS points the system resolver into the tunnel. physical is the device
// carrying the outer connection; if its service cannot be captured everything already
// changed is restored and ErrDNSCapture is returned.
func captureSystemDNS(physical string) error {
	sysDNSMu.Lock()
	defer sysDNSMu.Unlock()
	// A record left by a killed Core is settled first so its originals are not lost.
	if err := restoreLocked(); err != nil {
		return ErrDNSCapture
	}
	services, err := hardwareServices()
	if err != nil {
		return ErrDNSCapture
	}
	record := dnsRecord{Version: 1, TunnelDNS: TunnelDNS}
	for _, s := range services {
		servers, err := manualServers(s.name)
		if err != nil {
			continue
		}
		record.Services = append(record.Services, dnsService{Name: s.name, Servers: servers})
	}
	if len(record.Services) == 0 {
		return ErrDNSCapture
	}
	bootstrap := currentResolvers()
	if len(bootstrap) == 0 {
		bootstrap = []string{"1.1.1.1"}
	}
	// The record must be durable before the first change.
	if err := writeRecord(record); err != nil {
		return ErrDNSCapture
	}
	network.Default.SetResolvers(bootstrap)
	physicalCaptured := false
	for _, s := range record.Services {
		if setServers(s.Name, []string{TunnelDNS}) != nil {
			continue
		}
		for _, h := range services {
			if h.name == s.Name && h.device == physical {
				physicalCaptured = true
			}
		}
	}
	if !physicalCaptured {
		_ = restoreLocked()
		return ErrDNSCapture
	}
	flushResolverCache()
	return nil
}

// RestoreSystemDNS undoes a capture recorded in SystemDNSRecord, if any. Services
// whose servers are no longer exactly TunnelDNS were changed by someone else and are
// left alone. It is safe to call at any time and from any process running as root.
func RestoreSystemDNS() error {
	sysDNSMu.Lock()
	defer sysDNSMu.Unlock()
	return restoreLocked()
}

func restoreLocked() error {
	data, err := os.ReadFile(SystemDNSRecord)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record dnsRecord
	if json.Unmarshal(data, &record) != nil || record.TunnelDNS == "" {
		// Unreadable: nothing trustworthy to restore from, so do not guess.
		return os.Remove(SystemDNSRecord)
	}
	var failed error
	for _, s := range record.Services {
		current, err := manualServers(s.Name)
		if err != nil || !slices.Equal(current, []string{record.TunnelDNS}) {
			continue // removed, renamed or changed by the user since the capture
		}
		if err := setServers(s.Name, s.Servers); err != nil {
			failed = err
		}
	}
	if failed != nil {
		return failed // keep the record so a later attempt can finish the job
	}
	network.Default.SetResolvers(nil)
	flushResolverCache()
	return os.Remove(SystemDNSRecord)
}
