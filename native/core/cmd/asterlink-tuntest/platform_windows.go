//go:build windows

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"asterlink/core/internal/engine"
	"asterlink/core/internal/managed"
	"asterlink/core/internal/network"
)

// Windows side of the acceptance harness (handoff_app_windows.md round 2, cases T01-T07, W01).
//
// Two Windows facts shape it:
//  1. The harness is the Core process. sing-tun's strict-route filters exempt "the process
//     that created the TUN", so an in-process probe would not see the DNS and IPv6 blocks
//     an ordinary application meets. Every probe that stands in for an application runs in
//     a child process (childFlag); only the control-plane check (apiCheck) runs in-process,
//     because the Core's own API client is exactly that exempt process.
//  2. System state is read through structured PowerShell cmdlets (Get-NetRoute,
//     Get-DnsClientServerAddress), not by parsing localised `netsh`/`route print` text.

func privileged() bool { return windows.GetCurrentProcessToken().IsElevated() }

const privilegeHint = "start PowerShell with \"Run as administrator\" (a Wintun adapter and WFP filters need an elevated token)"

func machineFacts() map[string]any {
	v := windows.RtlGetVersion()
	facts := map[string]any{
		"os":           "windows",
		"version":      fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber),
		"client_sku":   v.ProductType == 1, // VER_NT_WORKSTATION; 2 = domain controller, 3 = server
		"goarch":       runtime.GOARCH,
		"elevated":     privileged(),
		"system_proxy": systemProxy(),
	}
	return facts
}

// ---- state capture ---------------------------------------------------------------------------

func powershell(ctx context.Context, script string) (string, error) {
	exe := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	full := "[Console]::OutputEncoding=[Text.Encoding]::UTF8; $ErrorActionPreference='Stop'; " + script
	out, err := exec.CommandContext(ctx, exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", full).Output()
	return string(out), err
}

func psLines(script string) ([]string, error) {
	out, err := powershell(context.Background(), script)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	sort.Strings(lines)
	return lines, nil
}

// systemProxy is the per-user WinINET proxy configuration. The Core never writes it; the
// harness records it before and after to prove a "real TUN" did not fall back to one.
func systemProxy() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "unreadable"
	}
	defer key.Close()
	enable, _, _ := key.GetIntegerValue("ProxyEnable")
	server, _, _ := key.GetStringValue("ProxyServer")
	pac, _, _ := key.GetStringValue("AutoConfigURL")
	return fmt.Sprintf("enable=%d server=%q pac=%q", enable, server, pac)
}

func capture() snapshot {
	var s snapshot
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		s.Interfaces = append(s.Interfaces, i.Name)
	}
	sort.Strings(s.Interfaces)
	var err error
	// Multicast and neighbour-cache style entries change on their own and are not routes the
	// tunnel owns.
	routes := func(family string) string {
		return `Get-NetRoute -AddressFamily ` + family + ` | Where-Object { $_.DestinationPrefix -notlike 'ff*' -and $_.DestinationPrefix -notlike '224.*' -and $_.DestinationPrefix -notlike '255.255.255.255*' } | ForEach-Object { '{0} {1} {2} {3}' -f $_.DestinationPrefix,$_.NextHop,$_.InterfaceAlias,$_.RouteMetric }`
	}
	if s.RoutesV4, err = psLines(routes("IPv4")); err != nil {
		s.Err = "Get-NetRoute IPv4: " + err.Error()
	}
	if s.RoutesV6, err = psLines(routes("IPv6")); err != nil && s.Err == "" {
		s.Err = "Get-NetRoute IPv6: " + err.Error()
	}
	dns, derr := psLines(`Get-DnsClientServerAddress | ForEach-Object { '{0} {1} {2}' -f $_.InterfaceAlias,$_.AddressFamily,($_.ServerAddresses -join ',') }`)
	if derr != nil && s.Err == "" {
		s.Err = "Get-DnsClientServerAddress: " + derr.Error()
	}
	sum := sha256.Sum256([]byte(strings.Join(dns, "\n")))
	s.DNSDigest = hex.EncodeToString(sum[:])
	s.Proxy = systemProxy()
	return s
}

// ownAdapters lists adapters that belong to the TUN driver, by name or driver description.
func ownAdapters() []string {
	adapters, err := network.Adapters()
	if err != nil {
		return nil
	}
	var found []string
	for _, a := range adapters {
		if a.IsOwn() || strings.Contains(strings.ToLower(a.Description), "wintun") && strings.EqualFold(a.Name, network.TunName) {
			found = append(found, a.Name)
		}
	}
	return found
}

func newDevices(before snapshot) []string {
	known := map[string]bool{}
	for _, n := range before.Interfaces {
		known[n] = true
	}
	var added []string
	for _, n := range ownAdapters() {
		if !known[n] {
			added = append(added, n)
		}
	}
	return added
}

func diff(before, after snapshot) []string {
	var problems []string
	if before.Err != "" || after.Err != "" {
		return []string{"could not read the network state (" + strings.TrimSpace(before.Err+" "+after.Err) + "): the comparison would be meaningless"}
	}
	if added := newDevices(before); len(added) > 0 {
		problems = append(problems, "TUN adapter still present: "+strings.Join(added, ","))
	}
	if strings.Join(before.RoutesV4, "\n") != strings.Join(after.RoutesV4, "\n") {
		problems = append(problems, "IPv4 route table differs from before the run")
	}
	if strings.Join(before.RoutesV6, "\n") != strings.Join(after.RoutesV6, "\n") {
		problems = append(problems, "IPv6 route table differs from before the run")
	}
	if before.DNSDigest != after.DNSDigest {
		problems = append(problems, "per-adapter DNS servers differ from before the run")
	}
	if before.Proxy != after.Proxy {
		problems = append(problems, "the system proxy setting changed: "+before.Proxy+" -> "+after.Proxy)
	}
	return problems
}

func openFDs() int {
	var count uint32
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	handle, _ := windows.GetCurrentProcess()
	if r, _, _ := proc.Call(uintptr(handle), uintptr(unsafe.Pointer(&count))); r == 0 {
		return 0
	}
	return int(count)
}

// ---- child probes (stand-ins for ordinary applications) -----------------------------------

var (
	probeOnce sync.Once
	probePath string
	probeErr  error
)

// probeExecutable is a copy of the harness under a different path. The WFP permit rule that
// sing-tun installs for "the Core" matches the executable's path (ALE_APP_ID), not the
// process: a child started from the harness's own path would be exempt from the DNS and
// IPv6 blocks too and would prove nothing about ordinary applications.
func probeExecutable() (string, error) {
	probeOnce.Do(func() {
		self, err := os.Executable()
		if err != nil {
			probeErr = err
			return
		}
		data, err := os.ReadFile(self)
		if err != nil {
			probeErr = err
			return
		}
		dir, err := os.MkdirTemp("", "asterlink-probe-")
		if err != nil {
			probeErr = err
			return
		}
		probePath = filepath.Join(dir, "probe-app.exe")
		probeErr = os.WriteFile(probePath, data, 0o700)
	})
	return probePath, probeErr
}

// cleanupProbe removes the copy made by probeExecutable.
func cleanupProbe() {
	if probePath != "" {
		_ = os.RemoveAll(filepath.Dir(probePath))
	}
}

func child(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	exe, err := probeExecutable()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, append([]string{childFlag}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// runChild is the body of a probe process. It prints one line and exits.
func runChild(args []string) int {
	if len(args) == 0 {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	switch args[0] {
	case "fetch": // fetch <tcp4|tcp6> <url,url>
		if len(args) != 3 {
			return 2
		}
		fmt.Println(fetch(ctx, args[2], args[1]))
	case "lookup": // lookup <name>: the system path, i.e. GetAddrInfoW through the DNS Client service
		addrs, err := net.DefaultResolver.LookupHost(ctx, args[1])
		fmt.Println(strings.Join(addrs, ","), errText(err))
	case "dns53": // dns53 <server:53> <name>: plain DNS aimed at an arbitrary resolver
		resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", args[1])
		}}
		addrs, err := resolver.LookupHost(ctx, args[2])
		fmt.Println(strings.Join(addrs, ","), errText(err))
	case "dial6": // dial6 [addr]:port
		started := time.Now()
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp6", args[1])
		elapsed := time.Since(started).Milliseconds()
		if err != nil {
			fmt.Printf("refused %d %s\n", elapsed, errText(err))
			return 0
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: " + args[1] + "\r\nConnection: close\r\n\r\n"))
		head := make([]byte, 5)
		n, _ := io.ReadFull(conn, head)
		fmt.Printf("connected %d http=%v\n", elapsed, string(head[:n]) == "HTTP/")
	case "ntp": // ntp host:port
		fmt.Println(ntpQuery(ctx, args[1]))
	default:
		return 2
	}
	return 0
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return "ERR(" + strings.ReplaceAll(err.Error(), "\n", " ") + ")"
}

// ntpWait is how long one SNTP attempt waits for a reply (a variable so tests stay fast).
var ntpWait = 3 * time.Second

// ntpQuery sends one SNTP request over UDP and reports whether a well-formed reply came back.
func ntpQuery(ctx context.Context, target string) string {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "udp", target)
	if err != nil {
		return "noreply dial: " + err.Error()
	}
	defer conn.Close()
	request := make([]byte, 48)
	request[0] = 0x1b // LI=0, VN=3, mode=3 (client)
	binary.BigEndian.PutUint64(request[40:], uint64(time.Now().UnixNano()))
	buffer := make([]byte, 128)
	for range 3 {
		_ = conn.SetDeadline(time.Now().Add(ntpWait))
		if _, err := conn.Write(request); err != nil {
			continue
		}
		if n, err := conn.Read(buffer); err == nil && n >= 48 && buffer[0]&7 == 4 {
			return "reply"
		}
	}
	return "noreply"
}

// ---- cases ------------------------------------------------------------------------------------

func inFakeRange(list string) bool {
	fake := netip.MustParsePrefix("198.18.0.0/16")
	for _, a := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }) {
		if ip, err := netip.ParseAddr(a); err == nil && fake.Contains(ip) {
			return true
		}
	}
	return false
}

func platformChecks(ctx context.Context, eng *engine.Mihomo, o options, baseline6 string) {
	// W-PROXY: a "TUN" that quietly set a system proxy would pass every other case.
	if now := systemProxy(); now == baselineSnapshot.Proxy {
		record("T01-noproxy", "no WinINET system proxy was set or changed while connected", now, "pass")
	} else {
		record("T01-noproxy", "no WinINET system proxy was set or changed while connected", baselineSnapshot.Proxy+" -> "+now, "fail")
	}

	// The adapter must carry the tunnel's resolver; this is what sing-tun configures.
	tunDNS := ""
	if adapters, err := network.Adapters(); err == nil {
		for _, a := range adapters {
			if a.IsOwn() {
				tunDNS = fmt.Sprint(a.DNS)
			}
		}
	}
	record("T03-dns-adapter", "the TUN adapter's DNS server is "+engine.TunnelDNS, "mode="+eng.DNSMode()+" adapter dns="+tunDNS,
		verdict(eng.DNSMode() == engine.DNSCaptured && strings.Contains(tunDNS, engine.TunnelDNS)))

	name := fmt.Sprintf("al-%d.example.com", time.Now().UnixNano())
	out, err := child(ctx, 30*time.Second, "lookup", name)
	record("T03-dns-system", "an ordinary application's lookup (Windows DNS Client) is answered by the tunnel's resolver (fake-ip), not a LAN/ISP resolver",
		fmt.Sprintf("out=%q err=%v", out, err), verdict(err == nil && inFakeRange(out)))

	out, err = child(ctx, 30*time.Second, "dns53", "9.9.9.9:53", "example.org")
	record("T03-dns-any53", "port-53 queries from an application to an arbitrary resolver are hijacked or blocked, never answered by that resolver",
		fmt.Sprintf("out=%q err=%v", out, err), verdict(err == nil && inFakeRange(out)))

	// UDP through the node: a public SNTP server needs no server of our own.
	out, err = child(ctx, 40*time.Second, "ntp", o.ntpHost)
	switch {
	case err != nil:
		record("T02-udp", "UDP (SNTP) round trip through the node", err.Error(), "fail")
	case out == "reply":
		record("T02-udp", "UDP (SNTP) round trip through the node", "reply received while connected", "pass")
	default:
		record("T02-udp", "UDP (SNTP) round trip through the node", out+" (the node may not forward UDP; the reject case below is the safety-critical one)", "fail")
	}

	exit6, err := child(ctx, 60*time.Second, "fetch", "tcp6", o.probe6)
	switch {
	case err != nil:
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", err.Error(), "fail")
	case exit6 == "" && baseline6 == "":
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "this network has no IPv6 (nothing to leak)", "pass")
	case exit6 == "":
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 requests fail while the tunnel is up (blocked, not leaked)", "pass")
	case exit6 == baseline6:
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 exit unchanged: v6 traffic bypasses the tunnel", "fail")
	default:
		record("T04-ipv6", "IPv6 is carried by the tunnel or blocked, never direct", "IPv6 exit changed ("+hash(exit6)+")", "pass")
	}

	mode := eng.IPv6Mode()
	const id, expected = "T04-ipv6-fallback", "IPv6 is carried by the node, or refused before the handshake (Happy Eyeballs then uses IPv4)"
	out, err = child(ctx, 30*time.Second, "dial6", engine.IPv6Probes[0])
	fields := strings.Fields(out)
	switch {
	case err != nil || len(fields) < 2:
		record(id, expected, fmt.Sprintf("mode=%s: probe failed: %v %q", mode, err, out), "fail")
	case fields[0] == "refused":
		ms, _ := strconv.Atoi(fields[1])
		record(id, expected, fmt.Sprintf("mode=%s: connect refused after %dms (%s)", mode, ms, strings.Join(fields[2:], " ")), verdict(mode == engine.IPv6Rejected && ms < 1000))
	default:
		record(id, expected, fmt.Sprintf("mode=%s: %s", mode, out), verdict(mode == engine.IPv6Tunnel && strings.Contains(out, "http=true")))
	}
}

// platformExtras: the UDP-refusal session and the hard-kill session, both optional because
// each re-creates the tunnel.
func platformExtras(ctx context.Context, o options, proxy map[string]any, before snapshot) {
	if o.udpReject {
		udpRejectSession(ctx, o, proxy)
	}
	if o.killTest {
		killSession(ctx, o, before)
	}
}

// udpRejectSession starts a second session whose node declares udp:false. The runtime
// template ends with MATCH,REJECT, so UDP must fail; a reply would mean it left directly.
func udpRejectSession(ctx context.Context, o options, proxy map[string]any) {
	const id, expected = "T02-udp-reject", "a node without UDP support makes UDP fail; it is never sent direct"
	noUDP := maps.Clone(proxy)
	noUDP["udp"] = false
	eng := engine.New()
	iface, err := eng.Preflight(ctx)
	if err != nil {
		record(id, expected, "preflight: "+err.Error(), "blocked")
		return
	}
	network.Default.Set(iface)
	defer func() { _ = eng.Stop(); network.Default.Clear() }()
	if err := eng.Start(ctx, managed.Profile{NodeID: "tuntest-noudp", Version: "tuntest", Proxy: noUDP}); err != nil {
		record(id, expected, "start failed: "+err.Error(), "fail")
		return
	}
	if err := eng.Verify(ctx); err != nil {
		record(id, expected, "TCP forwarding failed in the udp:false session: "+err.Error(), "fail")
		return
	}
	out, err := child(ctx, 40*time.Second, "ntp", o.ntpHost)
	switch {
	case err != nil:
		record(id, expected, err.Error(), "fail")
	case out == "reply":
		record(id, expected, "SNTP reply arrived although the node has no UDP: the packet left directly", "fail")
	default:
		record(id, expected, "UDP refused ("+out+"); TCP still forwarded", "pass")
	}
}

// killSession proves the crash story: a hard-killed Core cannot clean up, so the adapter,
// its routes, the DNS setting and the WFP filters must disappear by themselves.
func killSession(ctx context.Context, o options, before snapshot) {
	const id, expected = "T09-kill", "after TerminateProcess of a connected Core, no adapter, route, DNS change or filter remains and the system resolver is real again"
	self, err := os.Executable()
	if err != nil {
		record(id, expected, err.Error(), "blocked")
		return
	}
	holder := exec.CommandContext(ctx, self, "--hold", "--allow-network-changes", "--max-seconds", "300")
	stdout, err := holder.StdoutPipe()
	if err != nil {
		record(id, expected, err.Error(), "blocked")
		return
	}
	if err := holder.Start(); err != nil {
		record(id, expected, err.Error(), "blocked")
		return
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if line := scanner.Text(); line == "READY" || strings.HasPrefix(line, "START-FAILED") || strings.HasPrefix(line, "PREFLIGHT-FAILED") {
				ready <- line
				return
			}
		}
		ready <- "EOF"
	}()
	select {
	case line := <-ready:
		if line != "READY" {
			_ = holder.Process.Kill()
			_ = holder.Wait()
			record(id, expected, "the victim did not connect: "+line, "blocked")
			return
		}
	case <-time.After(90 * time.Second):
		_ = holder.Process.Kill()
		_ = holder.Wait()
		record(id, expected, "the victim did not report READY within 90s", "blocked")
		return
	}
	time.Sleep(3 * time.Second)
	connectedDNS, _ := child(ctx, 30*time.Second, "lookup", fmt.Sprintf("al-%d.example.com", time.Now().UnixNano()))
	_ = holder.Process.Kill() // TerminateProcess: no deferred function, no signal handler runs
	_ = holder.Wait()
	time.Sleep(6 * time.Second)
	after := capture()
	problems := diff(before, after)
	real, derr := child(ctx, 30*time.Second, "lookup", "example.org")
	if derr != nil || real == "" || inFakeRange(real) || strings.Contains(real, "ERR(") {
		problems = append(problems, fmt.Sprintf("system resolver is not real after the kill: %q (%v)", real, derr))
	}
	observed := fmt.Sprintf("while connected the lookup was fake-ip=%v; after kill: %s", inFakeRange(connectedDNS), problemsText(problems))
	record(id, expected, observed, verdict(len(problems) == 0 && inFakeRange(connectedDNS)))
}

func problemsText(p []string) string {
	if len(p) == 0 {
		return "everything restored"
	}
	return strings.Join(p, "; ")
}

func platformCleanup() { cleanupProbe() }
