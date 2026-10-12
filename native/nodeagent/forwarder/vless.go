package forwarder

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// VLESS is a reference forwarder for the VLESS protocol (version 0, no flow, no
// mux): TCP (command 1) and UDP (command 2, length-prefixed datagrams). Every user
// has their own UUID; a connection is attributed to the grant that owns the UUID,
// so counting and revocation are per user by construction.
//
// It is deliberately small. It does not implement Reality, XTLS flows or multiplexing:
// a node that needs those must use a forwarder that is separately verified for the same
// guarantees (credential per user, trusted counters, kill of established flows). The
// agent reports only the protocols its forwarder enforces.
type VLESS struct {
	opts VLESSOptions
	ln   net.Listener

	mu     sync.RWMutex
	byUUID map[[16]byte]*entry
	byID   map[string]*entry

	done chan struct{}
	wg   sync.WaitGroup
}

type VLESSOptions struct {
	Listen string
	// TLS, when set, wraps the listener. Production nodes should always set it.
	TLS *tls.Config
	// AllowPrivateTargets permits loopback/private/link-local destinations. It exists for
	// tests only: a public node must not be usable to reach its own network.
	AllowPrivateTargets bool
	HandshakeTimeout    time.Duration
	IdleTimeout         time.Duration
	DialTimeout         time.Duration
	WatchInterval       time.Duration
}

func NewVLESS(opts VLESSOptions) (*VLESS, error) {
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = 10 * time.Second
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = 5 * time.Minute
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	if opts.WatchInterval <= 0 {
		opts.WatchInterval = 250 * time.Millisecond
	}
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return nil, err
	}
	if opts.TLS != nil {
		ln = tls.NewListener(ln, opts.TLS)
	}
	v := &VLESS{opts: opts, ln: ln, byUUID: map[[16]byte]*entry{}, byID: map[string]*entry{}, done: make(chan struct{})}
	v.wg.Add(2)
	go v.accept()
	go v.watch()
	return v, nil
}

func (v *VLESS) Addr() net.Addr      { return v.ln.Addr() }
func (v *VLESS) Protocols() []string { return []string{"vless"} }

func parseUUID(s string) ([16]byte, bool) {
	var out [16]byte
	raw := strings.ReplaceAll(s, "-", "")
	if len(raw) != 32 {
		return out, false
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return out, false
	}
	copy(out[:], b)
	return out, true
}

func (v *VLESS) Install(g Grant) error {
	if g.Protocol != "vless" {
		return ErrUnsupported
	}
	key, ok := parseUUID(g.Credential["uuid"])
	if !ok || len(g.Credential) != 1 {
		return fmt.Errorf("invalid vless credential")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if existing, ok := v.byID[g.ID]; ok {
		existing.deadline.Store(g.Deadline.UnixNano())
		existing.cap.Store(g.EpochBudget)
		return nil
	}
	if _, taken := v.byUUID[key]; taken {
		return ErrDuplicate
	}
	e := newEntry(g)
	v.byID[g.ID], v.byUUID[key] = e, e
	return nil
}

func (v *VLESS) Update(id string, deadline time.Time, epochBudget int64) bool {
	v.mu.RLock()
	e := v.byID[id]
	v.mu.RUnlock()
	if e == nil {
		return false
	}
	e.deadline.Store(deadline.UnixNano())
	e.cap.Store(epochBudget)
	return true
}

func (v *VLESS) Remove(id string) (Counters, bool) {
	v.mu.Lock()
	e := v.byID[id]
	if e == nil {
		v.mu.Unlock()
		return Counters{}, false
	}
	delete(v.byID, id)
	for k, candidate := range v.byUUID {
		if candidate == e {
			delete(v.byUUID, k)
		}
	}
	v.mu.Unlock()
	// New flows can no longer find the credential; now stop the established ones.
	e.mu.Lock()
	e.closed.Store(true)
	e.mu.Unlock()
	e.killAll()
	e.wg.Wait()
	return e.counters(), true
}

func (v *VLESS) Usage() map[string]Counters {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]Counters, len(v.byID))
	for id, e := range v.byID {
		out[id] = e.counters()
	}
	return out
}

func (v *VLESS) Expired(now time.Time) []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	var ids []string
	for id, e := range v.byID {
		if now.UnixNano() >= e.deadline.Load() {
			ids = append(ids, id)
		}
	}
	return ids
}

func (v *VLESS) Close() error {
	select {
	case <-v.done:
		return nil
	default:
		close(v.done)
	}
	err := v.ln.Close()
	v.mu.RLock()
	entries := make([]*entry, 0, len(v.byID))
	for _, e := range v.byID {
		entries = append(entries, e)
	}
	v.mu.RUnlock()
	for _, e := range entries {
		e.mu.Lock()
		e.closed.Store(true)
		e.mu.Unlock()
		e.killAll()
	}
	v.wg.Wait()
	return err
}

// watch closes the flows of grants that ran out of time or bytes. Data flowing through
// a flow is checked per chunk; this covers idle flows and keeps expiry independent of
// traffic.
func (v *VLESS) watch() {
	defer v.wg.Done()
	ticker := time.NewTicker(v.opts.WatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-v.done:
			return
		case now := <-ticker.C:
			v.mu.RLock()
			var dead []*entry
			for _, e := range v.byID {
				if !e.live(now) {
					dead = append(dead, e)
				}
			}
			v.mu.RUnlock()
			for _, e := range dead {
				e.killAll()
			}
		}
	}
}

func (v *VLESS) accept() {
	defer v.wg.Done()
	for {
		c, err := v.ln.Accept()
		if err != nil {
			select {
			case <-v.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		v.wg.Add(1)
		go func() {
			defer v.wg.Done()
			v.serve(c)
		}()
	}
}

func (v *VLESS) lookup(key [16]byte) *entry {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.byUUID[key]
}

type request struct {
	command byte
	host    string
	port    int
}

func readRequest(br *bufio.Reader) (key [16]byte, req request, err error) {
	head := make([]byte, 18)
	if _, err = io.ReadFull(br, head); err != nil {
		return
	}
	if head[0] != 0 {
		err = errors.New("unsupported vless version")
		return
	}
	copy(key[:], head[1:17])
	if _, err = io.CopyN(io.Discard, br, int64(head[17])); err != nil {
		return
	}
	body := make([]byte, 4)
	if _, err = io.ReadFull(br, body[:3]); err != nil {
		return
	}
	req.command = body[0]
	req.port = int(binary.BigEndian.Uint16(body[1:3]))
	kind, err := br.ReadByte()
	if err != nil {
		return
	}
	switch kind {
	case 1:
		ip := make([]byte, 4)
		if _, err = io.ReadFull(br, ip); err == nil {
			req.host = net.IP(ip).String()
		}
	case 3:
		ip := make([]byte, 16)
		if _, err = io.ReadFull(br, ip); err == nil {
			req.host = net.IP(ip).String()
		}
	case 2:
		var n byte
		if n, err = br.ReadByte(); err != nil || n == 0 {
			err = errors.New("empty domain")
			return
		}
		name := make([]byte, n)
		if _, err = io.ReadFull(br, name); err == nil {
			req.host = string(name)
		}
	default:
		err = errors.New("unknown address type")
	}
	return
}

func (v *VLESS) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(v.opts.HandshakeTimeout))
	br := bufio.NewReaderSize(c, 32<<10)
	key, req, err := readRequest(br)
	if err != nil {
		return
	}
	e := v.lookup(key)
	if e == nil || !e.live(time.Now()) {
		return // unknown, revoked, expired or out of bytes: no response at all
	}
	_ = c.SetReadDeadline(time.Time{})
	switch req.command {
	case 1:
		v.tcp(e, c, br, req)
	case 2:
		v.udp(e, c, br, req)
	}
}

func isPrivate(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1]&0xc0 == 64 || v4[0] == 0 || v4[0] >= 240
	}
	return false
}

// guard rejects forbidden destinations at connect time, after name resolution, so a
// hostile DNS answer cannot turn the node into a door to its own network.
func (v *VLESS) dialer() *net.Dialer {
	return &net.Dialer{Timeout: v.opts.DialTimeout, Control: func(network, address string, _ syscall.RawConn) error {
		if v.opts.AllowPrivateTargets {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return errors.New("bad destination")
		}
		if ip := net.ParseIP(host); ip == nil || isPrivate(ip) {
			return errors.New("destination not allowed")
		}
		return nil
	}}
}

func (v *VLESS) tcp(e *entry, c net.Conn, br *bufio.Reader, req request) {
	ctx, cancel := context.WithTimeout(context.Background(), v.opts.DialTimeout)
	defer cancel()
	dst, err := v.dialer().DialContext(ctx, "tcp", net.JoinHostPort(req.host, strconv.Itoa(req.port)))
	if err != nil {
		return
	}
	defer dst.Close()
	if !e.track(c) {
		return
	}
	defer e.untrack(c)
	if !e.track(dst) {
		return
	}
	defer e.untrack(dst)
	if !e.live(time.Now()) {
		return
	}
	if _, err = c.Write([]byte{0, 0}); err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		v.pipe(e, dst, br, c, &e.up)
	}()
	go func() {
		defer wg.Done()
		v.pipe(e, c, dst, dst, &e.down)
	}()
	wg.Wait()
}

type halfCloser interface{ CloseWrite() error }

// pipe copies one direction and then shuts the write side so the peer sees the end.
func (v *VLESS) pipe(e *entry, dst io.Writer, src io.Reader, srcConn net.Conn, counter interface{ Add(int64) int64 }) {
	err := v.copyCounted(e, dst, src, srcConn, counter)
	if err != nil && !errors.Is(err, io.EOF) {
		if closer, ok := dst.(io.Closer); ok {
			_ = closer.Close()
		}
		_ = srcConn.Close()
		return
	}
	if hc, ok := dst.(halfCloser); ok {
		_ = hc.CloseWrite()
	}
}

var errDenied = errors.New("grant no longer allows this traffic")

func (v *VLESS) copyCounted(e *entry, dst io.Writer, src io.Reader, srcConn net.Conn, counter interface{ Add(int64) int64 }) error {
	buf := make([]byte, 32<<10)
	for {
		_ = srcConn.SetReadDeadline(time.Now().Add(v.opts.IdleTimeout))
		n, rerr := src.Read(buf)
		if n > 0 {
			if !e.live(time.Now()) {
				return errDenied
			}
			allowed := e.admit(int64(n))
			if allowed == 0 {
				return errDenied
			}
			w, werr := dst.Write(buf[:allowed])
			e.refund(allowed - int64(w))
			counter.Add(int64(w))
			if werr != nil {
				return werr
			}
			if allowed < int64(n) {
				return errDenied
			}
		}
		if rerr != nil {
			return rerr
		}
	}
}

func (v *VLESS) udp(e *entry, c net.Conn, br *bufio.Reader, req request) {
	ctx, cancel := context.WithTimeout(context.Background(), v.opts.DialTimeout)
	defer cancel()
	pc, err := v.dialer().DialContext(ctx, "udp", net.JoinHostPort(req.host, strconv.Itoa(req.port)))
	if err != nil {
		return
	}
	defer pc.Close()
	if !e.track(c) {
		return
	}
	defer e.untrack(c)
	if !e.track(pc) {
		return
	}
	defer e.untrack(pc)
	if _, err = c.Write([]byte{0, 0}); err != nil {
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // client -> target
		defer wg.Done()
		defer pc.Close()
		var length [2]byte
		payload := make([]byte, 65535)
		for {
			_ = c.SetReadDeadline(time.Now().Add(v.opts.IdleTimeout))
			if _, err := io.ReadFull(br, length[:]); err != nil {
				return
			}
			n := int(binary.BigEndian.Uint16(length[:]))
			if _, err := io.ReadFull(br, payload[:n]); err != nil {
				return
			}
			if !e.live(time.Now()) || e.admit(int64(n)) < int64(n) {
				return
			}
			w, err := pc.Write(payload[:n])
			e.refund(int64(n - w))
			e.up.Add(int64(w))
			if err != nil {
				return
			}
		}
	}()
	go func() { // target -> client
		defer wg.Done()
		defer c.Close()
		buf := make([]byte, 2+65535)
		for {
			_ = pc.SetReadDeadline(time.Now().Add(v.opts.IdleTimeout))
			n, err := pc.Read(buf[2:])
			if err != nil {
				return
			}
			if !e.live(time.Now()) || e.admit(int64(n)) < int64(n) {
				return
			}
			binary.BigEndian.PutUint16(buf[:2], uint16(n))
			if _, err := c.Write(buf[:2+n]); err != nil {
				e.refund(int64(n))
				return
			}
			e.down.Add(int64(n))
		}
	}()
	wg.Wait()
}
