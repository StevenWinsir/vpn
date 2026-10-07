//go:build darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const managedPFDirectory = "/var/run/flclash-managed-pf"
const managedPFLabel = "flclash_managed_egress"

var managedPFDevicePattern = regexp.MustCompile(`^utun[0-9]+$`)
var managedPFTokenPattern = regexp.MustCompile(`(?m)^Token\s*:\s*([0-9]+)\s*$`)

// A source-bound WebRTC socket can bypass Darwin's unscoped TUN routes. The
// connected-state PF guard rejects unprivileged TCP/UDP outside our utun.
// The privileged Core's proxy/control-plane sockets retain physical egress.
// We never flush another application's rules, states, or PF enable reference.
type managedPFLease struct {
	PID      int    `json:"pid"`
	Anchor   string `json:"anchor"`
	Token    string `json:"token"`
	RootRule string `json:"root_rule"`
}

type managedPFEgressGuard struct {
	lease managedPFLease
	path  string
}

func managedPFControl(ctx context.Context, input string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/sbin/pfctl", args...)
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		// pfctl can print connection addresses; never propagate its raw output.
		return "", fmt.Errorf("pfctl %s failed: %w", args[0], err)
	}
	if len(args) == 1 && args[0] == "-E" {
		return stdout.String() + "\n" + stderr.String(), nil
	}
	return stdout.String(), nil
}

func managedPFRules(device string) (string, error) {
	if !managedPFDevicePattern.MatchString(device) {
		return "", errors.New("invalid managed utun device")
	}
	var rules strings.Builder
	for _, family := range []struct{ name, loopback, suffix string }{{"inet", "127.0.0.0/8", "4"}, {"inet6", "::1", "6"}} {
		for _, protocol := range []string{"tcp", "udp"} {
			fmt.Fprintf(&rules, "block drop out quick on ! %s %s proto %s from any to ! %s user != 0 label %q\n", device, family.name, protocol, family.loopback, managedPFLabel+"_"+protocol+family.suffix)
		}
	}
	return rules.String(), nil
}

func managedPFNormalizeRules(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func managedPFToken(text string) (string, error) {
	match := managedPFTokenPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", errors.New("PF did not acknowledge an enable reference")
	}
	if _, err := strconv.ParseUint(match[1], 10, 64); err != nil {
		return "", errors.New("invalid PF enable reference")
	}
	return match[1], nil
}

func managedPFEnsureDirectory() error {
	if err := os.Mkdir(managedPFDirectory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(managedPFDirectory)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != 0 {
		return errors.New("unsafe PF lease directory")
	}
	return nil
}

func (g *managedPFEgressGuard) save(create bool) error {
	flags := os.O_WRONLY | syscall.O_NOFOLLOW
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(g.path, flags, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != 0 || stat.Nlink != 1 || info.Mode().Perm() != 0600 {
		return errors.New("unsafe PF lease file")
	}
	data, err := json.Marshal(g.lease)
	if err != nil {
		return err
	}
	if _, err := file.WriteAt(data, 0); err != nil {
		return err
	}
	if err := file.Truncate(int64(len(data))); err != nil {
		return err
	}
	return file.Sync()
}

func managedPFValidLease(lease managedPFLease) bool {
	if lease.PID <= 0 || lease.Anchor != "com.apple/000.FlClash."+strconv.Itoa(lease.PID) {
		return false
	}
	if lease.Token != "" {
		if _, err := strconv.ParseUint(lease.Token, 10, 64); err != nil {
			return false
		}
	}
	return lease.RootRule == "" || lease.RootRule == fmt.Sprintf("anchor %q all", lease.Anchor)
}

func recoverManagedPFLeases(ctx context.Context) error {
	entries, err := os.ReadDir(managedPFDirectory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil || entry.Name() != strconv.Itoa(pid)+".json" || pid <= 0 {
			return errors.New("unrecognized PF lease entry")
		}
		file, err := os.OpenFile(filepath.Join(managedPFDirectory, entry.Name()), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		info, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return statErr
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || stat.Uid != 0 || stat.Nlink != 1 || info.Mode().Perm() != 0600 || info.Size() > 4096 {
			file.Close()
			return errors.New("unsafe saved PF lease")
		}
		var lease managedPFLease
		decoder := json.NewDecoder(io.LimitReader(file, 4096))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&lease)
		file.Close()
		if err != nil || !managedPFValidLease(lease) || lease.PID != pid {
			return errors.New("invalid saved PF lease")
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			// A live or reused PID is never assumed to be ours based on its name.
			return errors.New("another managed PF owner is still active")
		}
		guard := &managedPFEgressGuard{lease: lease, path: filepath.Join(managedPFDirectory, entry.Name())}
		if err := guard.close(ctx); err != nil {
			return err
		}
	}
	return nil
}

func newManagedEgressGuard(ctx context.Context, device string) (io.Closer, error) {
	rules, err := managedPFRules(device)
	if err != nil {
		return nil, err
	}
	if err := managedPFEnsureDirectory(); err != nil {
		return nil, err
	}
	if err := recoverManagedPFLeases(ctx); err != nil {
		return nil, err
	}
	root, err := managedPFControl(ctx, "", "-s", "rules")
	if err != nil {
		return nil, err
	}
	g := &managedPFEgressGuard{lease: managedPFLease{PID: os.Getpid()}}
	g.lease.Anchor = "com.apple/000.FlClash." + strconv.Itoa(g.lease.PID)
	g.path = filepath.Join(managedPFDirectory, strconv.Itoa(g.lease.PID)+".json")
	switch managedPFNormalizeRules(root) {
	case "":
		// Only an empty filter ruleset may acquire our temporary root hook.
		// -R affects filter rules only; NAT/rdr/options are not replaced.
		g.lease.RootRule = fmt.Sprintf("anchor %q all", g.lease.Anchor)
	case `anchor "com.apple/*" all`:
		anchors, err := managedPFControl(ctx, "", "-a", "com.apple", "-s", "Anchors")
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(g.lease.Anchor, "com.apple/")
		for _, anchor := range strings.Fields(anchors) {
			anchor = strings.TrimPrefix(anchor, "com.apple/")
			if anchor < name {
				previous, err := managedPFControl(ctx, "", "-a", "com.apple/"+anchor, "-s", "rules")
				if err != nil || strings.TrimSpace(previous) != "" {
					return nil, errors.New("an earlier PF anchor could override the egress guard")
				}
			}
		}
	default:
		return nil, errors.New("existing PF filter policy cannot safely host the egress guard")
	}
	if _, err := managedPFControl(ctx, rules, "-n", "-a", g.lease.Anchor, "-f", "-"); err != nil {
		return nil, err
	}
	if err := g.save(true); err != nil {
		return nil, err
	}
	// Return ownership even on partial failure. The runtime rolls it back and
	// retains failed cleanup ownership instead of reporting a protected state.
	if _, err := managedPFControl(ctx, rules, "-a", g.lease.Anchor, "-f", "-"); err != nil {
		return g, err
	}
	if g.lease.RootRule != "" {
		if _, err := managedPFControl(ctx, g.lease.RootRule+"\n", "-R", "-f", "-"); err != nil {
			return g, err
		}
	}
	output, err := managedPFControl(ctx, "", "-E")
	if err != nil {
		return g, err
	}
	g.lease.Token, err = managedPFToken(output)
	if err != nil {
		return g, err
	}
	if err := g.save(false); err != nil {
		return g, err
	}
	// Existing states are evaluated before filter rules. Never silently accept
	// their bypass, and never globally flush someone else's active connections.
	states, err := managedPFControl(ctx, "", "-s", "states")
	if err != nil || strings.TrimSpace(states) != "" {
		return g, errors.New("existing PF states prevent verified egress protection")
	}
	return g, nil
}

func (g *managedPFEgressGuard) Close() error {
	return g.close(context.Background())
}

func (g *managedPFEgressGuard) close(ctx context.Context) error {
	if g.path == "" {
		return nil
	}
	if _, err := managedPFControl(ctx, "", "-a", g.lease.Anchor, "-F", "rules"); err != nil {
		return err
	}
	if g.lease.RootRule != "" {
		root, err := managedPFControl(ctx, "", "-s", "rules")
		if err != nil {
			return err
		}
		if managedPFNormalizeRules(root) == g.lease.RootRule {
			if _, err := managedPFControl(ctx, "", "-R", "-f", "-"); err != nil {
				return err
			}
		}
	}
	if g.lease.Token != "" {
		if _, err := managedPFControl(ctx, "", "-X", g.lease.Token); err != nil {
			return err
		}
		g.lease.Token = ""
		if err := g.save(false); err != nil {
			return err
		}
	}
	if err := os.Remove(g.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	g.path = ""
	return nil
}

func managedPFRejectedPackets(ctx context.Context, guard *managedPFEgressGuard, label string) (uint64, error) {
	output, err := managedPFControl(ctx, "", "-a", guard.lease.Anchor, "-s", "labels")
	if err != nil {
		return 0, err
	}
	var total uint64
	found := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] != label {
			continue
		}
		packets, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return 0, err
		}
		found = true
		total += packets
	}
	if !found {
		return 0, errors.New("PF egress guard has no observable counter")
	}
	return total, nil
}
