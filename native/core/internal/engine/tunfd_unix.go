//go:build !windows

package engine

import "syscall"

// ownedFD is the VPN descriptor the Android host handed over (VpnService.establish +
// detachFd). The Core owns it from then on and must close it exactly once: while it is open
// the VPN interface exists, so a leaked descriptor would keep every app's traffic in a
// tunnel nobody reads, and a second close could hit an unrelated file that reused the
// number. Ownership moves to Mihomo's listener when it starts (sing-tun wraps the number in
// an os.File and closes it with the listener); a start that fails before that point leaves
// it with the Core. release decides by identity, not by bookkeeping: the number is closed
// only if it still refers to the very file that was adopted. Nothing else in the process
// opens a TUN, so a reused number is always a different file. It is a separate file so the
// rule is tested on every Unix host.
type ownedFD struct {
	fd       int
	dev, ino uint64
	set      bool
}

func adoptFD(fd int) (ownedFD, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return ownedFD{}, err
	}
	return ownedFD{fd: fd, dev: uint64(st.Dev), ino: st.Ino, set: true}, nil
}

// open reports whether the number still names the adopted file.
func (o ownedFD) open() bool {
	if !o.set {
		return false
	}
	var st syscall.Stat_t
	return syscall.Fstat(o.fd, &st) == nil && uint64(st.Dev) == o.dev && st.Ino == o.ino
}

// release closes the descriptor unless its owner already did, and forgets it.
func (o *ownedFD) release() {
	if o.open() {
		_ = syscall.Close(o.fd)
	}
	*o = ownedFD{}
}
