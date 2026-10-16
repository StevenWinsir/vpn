package forwarder

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Wire contract: the pinned Mihomo transport/vless/conn.go and sing-vmess
// v0.2.5 xudp.go/mux.go. Only UDP mux sessions are accepted. Global IDs are
// parsed but never used to share sockets/credentials across carriers or grants.
const (
	xudpNew         byte = 1
	xudpKeep        byte = 2
	xudpEnd         byte = 3
	xudpAlive       byte = 4
	xudpData        byte = 1
	xudpUDP         byte = 2
	maxXUDPHeader        = 272 // fixed fields + maximum domain + optional 8-byte global ID
	maxXUDPSessions      = 64
	maxXUDPTargets       = 64 // across ALL sessions in one carrier
)

var errXUDP = errors.New("invalid or unsupported xudp frame")

type xudpFrame struct {
	id              uint16
	status, options byte
	target          request
	hasTarget       bool
	payload         []byte
}

func readXUDPFrame(r io.Reader, payload []byte) (f xudpFrame, err error) {
	var size [2]byte
	if _, err = io.ReadFull(r, size[:]); err != nil {
		return
	}
	n := int(binary.BigEndian.Uint16(size[:]))
	if n < 4 || n > maxXUDPHeader {
		return f, errXUDP
	}
	var header [maxXUDPHeader]byte
	if _, err = io.ReadFull(r, header[:n]); err != nil {
		return
	}
	f.id = binary.BigEndian.Uint16(header[:2])
	f.status, f.options = header[2], header[3]
	switch f.status {
	case xudpNew, xudpKeep:
		if f.options & ^xudpData != 0 {
			return f, errXUDP
		}
		if n > 4 {
			if header[4] != xudpUDP {
				return f, errXUDP
			}
			b := bytes.NewReader(header[5:n])
			if _, err = io.ReadFull(b, size[:]); err != nil {
				return
			}
			f.target.port = int(binary.BigEndian.Uint16(size[:]))
			var kind byte
			if kind, err = b.ReadByte(); err != nil {
				return
			}
			var addr []byte
			switch kind {
			case 1:
				addr = make([]byte, 4)
			case 3:
				addr = make([]byte, 16)
			case 2:
				var length byte
				if length, err = b.ReadByte(); err != nil {
					return
				}
				if length == 0 {
					return f, errXUDP
				}
				addr = make([]byte, int(length))
			default:
				return f, errXUDP
			}
			if _, err = io.ReadFull(b, addr); err != nil {
				return
			}
			if kind == 2 {
				f.target.host = string(addr)
			} else {
				f.target.host = net.IP(addr).String()
			}
			// Optional XUDP global ID exists on New only, and is not an authority.
			if b.Len() != 0 && !(f.status == xudpNew && b.Len() == 8) {
				return f, errXUDP
			}
			if f.target.port == 0 {
				return f, errXUDP
			}
			f.hasTarget = true
		} else if f.status == xudpNew {
			return f, errXUDP
		}
	case xudpEnd:
		if n != 4 || f.options & ^byte(2) != 0 {
			return f, errXUDP
		}
	case xudpAlive:
		if n != 4 || f.options != 0 {
			return f, errXUDP
		}
	default:
		return f, errXUDP
	}
	if f.options&xudpData != 0 {
		if _, err = io.ReadFull(r, size[:]); err != nil {
			return
		}
		n = int(binary.BigEndian.Uint16(size[:]))
		if n > len(payload) {
			return f, errXUDP
		}
		f.payload = payload[:n]
		_, err = io.ReadFull(r, f.payload)
	}
	return
}

// writeDatagram counts only accepted payload, never VLESS/mux/TLS framing.
// Counters are published before the write (a fast peer may immediately read
// Usage), then undelivered payload is refunded, including partial stream writes.
func writeDatagram(e *entry, w io.Writer, wire []byte, overhead int, counter *atomic.Int64) error {
	n := int64(len(wire) - overhead)
	if !e.admitDatagram(n) {
		return errDenied
	}
	counter.Add(n)
	written, err := w.Write(wire)
	delivered := int64(max(0, min(len(wire), written)-overhead))
	e.refund(n - delivered)
	counter.Add(delivered - n)
	if err == nil && written != len(wire) {
		err = io.ErrShortWrite
	}
	return err
}

var errXUDPPeerStopped = errors.New("xudp target reader stopped")

type xudpPeer struct {
	conn    net.Conn
	mu      sync.Mutex // serialize retirement with payload admission/write
	stopped atomic.Bool
	done    chan struct{}
}

func (p *xudpPeer) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped.Store(true)
	_ = p.conn.Close()
}

func (p *xudpPeer) write(e *entry, payload []byte, idle time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped.Load() {
		return errXUDPPeerStopped // no payload admitted or written; safe to redial
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(idle))
	return writeDatagram(e, p.conn, payload, 0, &e.up)
}

type xudpSession struct {
	target request
	peers  map[string]*xudpPeer
}

// A carrier is registered before parsing any frames or dialing. Remove closes
// its TCP stream, cancels pending DNS/dials, closes all UDP sockets and waits for
// its parser and readers; no late socket or counter can escape final settlement.
type xudpCarrier struct {
	v       *VLESS
	e       *entry
	conn    net.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	peers   map[net.Conn]struct{}
	writeMu sync.Mutex
	wg      sync.WaitGroup
}

func (x *xudpCarrier) Close() error {
	x.cancel()
	_ = x.conn.Close()
	x.mu.Lock()
	x.closed = true
	for c := range x.peers {
		_ = c.Close()
	}
	x.mu.Unlock()
	return nil
}
func (x *xudpCarrier) stopPeer(p *xudpPeer) {
	p.close()
	<-p.done
}

// Only the parser owns session maps. Readers retire their sockets independently;
// prune their references on the next frame so a long-lived carrier stays bounded.
func (x *xudpCarrier) prunePeers(sessions map[uint16]*xudpSession) {
	for _, s := range sessions {
		for address, p := range s.peers {
			if p.stopped.Load() {
				x.stopPeer(p)
				delete(s.peers, address)
			}
		}
	}
}

func (v *VLESS) xudp(e *entry, c net.Conn, br *bufio.Reader) {
	ctx, cancel := context.WithCancel(context.Background())
	x := &xudpCarrier{v: v, e: e, conn: c, ctx: ctx, cancel: cancel, peers: make(map[net.Conn]struct{})}
	defer cancel()
	if !e.track(x) {
		return
	}
	defer e.untrack(x)
	defer func() { x.Close(); x.wg.Wait() }()
	if !e.live(time.Now()) {
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(v.opts.IdleTimeout))
	if _, err := c.Write([]byte{0, 0}); err != nil {
		return
	}
	sessions := make(map[uint16]*xudpSession)
	payload := make([]byte, 65535)
	for {
		_ = c.SetReadDeadline(time.Now().Add(v.opts.IdleTimeout))
		f, err := readXUDPFrame(br, payload)
		if err != nil || !e.live(time.Now()) {
			return
		}
		x.prunePeers(sessions)
		s := sessions[f.id]
		switch f.status {
		case xudpNew:
			if s != nil || len(sessions) >= maxXUDPSessions {
				return
			}
			s = &xudpSession{target: f.target, peers: make(map[string]*xudpPeer)}
			sessions[f.id] = s
		case xudpKeep:
			if s == nil {
				return
			}
		case xudpEnd:
			if s == nil {
				return
			}
			for _, p := range s.peers {
				x.stopPeer(p)
			}
			delete(sessions, f.id)
			continue
		case xudpAlive:
			continue
		}
		if f.options&xudpData == 0 {
			continue
		}
		target := s.target
		if f.hasTarget {
			target = f.target
		}
		address := net.JoinHostPort(target.host, strconv.Itoa(target.port))
		for {
			p := s.peers[address]
			if p == nil {
				p, err = x.openPeer(f.id, address)
				if err != nil {
					return
				}
				s.peers[address] = p
			}
			err = p.write(e, f.payload, v.opts.IdleTimeout)
			if err == errXUDPPeerStopped {
				// Retirement raced the lookup. Nothing was sent or billed, so
				// join this reader and retry through the guarded dial path.
				x.stopPeer(p)
				delete(s.peers, address)
				continue
			}
			if err != nil {
				return
			}
			break
		}
	}
}

func (x *xudpCarrier) openPeer(id uint16, address string) (*xudpPeer, error) {
	x.mu.Lock()
	full := x.closed || len(x.peers) >= maxXUDPTargets
	x.mu.Unlock()
	if full {
		return nil, errDenied
	}
	// A connected UDP socket pins the checked DNS answer and filters replies by
	// peer. Every New/Keep target, including retargets, goes through the guard.
	pc, err := x.v.dialer().DialContext(x.ctx, "udp", address)
	if err != nil {
		return nil, err
	}
	x.mu.Lock()
	if x.closed || !x.e.live(time.Now()) {
		x.mu.Unlock()
		pc.Close()
		return nil, errDenied
	}
	x.peers[pc] = struct{}{}
	x.mu.Unlock()
	p := &xudpPeer{conn: pc, done: make(chan struct{})}
	x.wg.Add(1)
	go func() {
		defer x.wg.Done()
		defer func() {
			p.close()
			x.mu.Lock()
			delete(x.peers, pc)
			x.mu.Unlock()
			close(p.done)
		}()
		b := make([]byte, 65535)
		for {
			_ = pc.SetReadDeadline(time.Now().Add(x.v.opts.IdleTimeout))
			n, err := pc.Read(b)
			if err != nil {
				// No reply from this target is not a carrier failure. Retire
				// only its socket/reader; later data may reopen the same target.
				// Unexpected errors retain the existing fail-closed behavior.
				var ne net.Error
				if !p.stopped.Load() && !(errors.As(err, &ne) && ne.Timeout()) {
					x.Close()
				}
				return
			}
			if p.stopped.Load() {
				return
			}
			if err = x.reply(id, pc.RemoteAddr().(*net.UDPAddr), b[:n]); err != nil {
				x.Close()
				return
			}
		}
	}()
	return p, nil
}

func (x *xudpCarrier) reply(id uint16, from *net.UDPAddr, payload []byte) error {
	// Keep + explicit source address: changing a destination must not mislabel
	// delayed replies from the previous target as replies from the current one.
	h := binary.BigEndian.AppendUint16(nil, id)
	h = append(h, xudpKeep, xudpData, xudpUDP)
	h = binary.BigEndian.AppendUint16(h, uint16(from.Port))
	if ip := from.IP.To4(); ip != nil {
		h = append(append(h, 1), ip...)
	} else {
		h = append(append(h, 3), from.IP.To16()...)
	}
	wire := binary.BigEndian.AppendUint16(nil, uint16(len(h)))
	wire = append(wire, h...)
	wire = binary.BigEndian.AppendUint16(wire, uint16(len(payload)))
	overhead := len(wire)
	wire = append(wire, payload...)
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	_ = x.conn.SetWriteDeadline(time.Now().Add(x.v.opts.IdleTimeout))
	return writeDatagram(x.e, x.conn, wire, overhead, &x.e.down)
}
