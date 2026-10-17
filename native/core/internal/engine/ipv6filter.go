package engine

import (
	"encoding/binary"
	"net/netip"
)

// IPv6 ingress refusal for iOS (handoff_app_ios.md 8.4). An iOS tunnel must claim ::/0,
// otherwise IPv6 leaves through the physical interface. When the selected node cannot carry
// IPv6, the system stack would still complete every IPv6 TCP handshake locally and the flow
// would die afterwards (the PR #17 "connected but no internet" failure), and Happy Eyeballs
// would never fall back. So IPv6 packets are answered before the stack sees them: a TCP
// segment gets a reset, a UDP datagram an ICMPv6 "administratively prohibited" error, and
// everything else is dropped. Apps then fail IPv6 at once and use IPv4. The functions here
// are pure (a packet in, at most one packet out) so every host tests them; the relay that
// applies them to the utun lives in ipv6relay_unix.go.

const (
	ipv6HeaderLen  = 40
	protoTCP       = 6
	protoUDP       = 17
	protoICMPv6    = 58
	tcpFlagFIN     = 0x01
	tcpFlagSYN     = 0x02
	tcpFlagRST     = 0x04
	tcpFlagACK     = 0x10
	icmpv6Unreach  = 1
	icmpv6AdminBan = 1
	// icmpv6MaxError keeps an ICMPv6 error within the IPv6 minimum MTU (RFC 4443 2.4(c)).
	icmpv6MaxError = 1280
	replyHopLimit  = 64
)

// refuseIPv6 returns the packet that refuses an inbound IPv6 packet (taken from the tunnel,
// without the utun header), or nil when the packet is to be dropped silently. It never
// answers an error, a reset, a multicast destination or an unspecified/multicast source.
func refuseIPv6(packet []byte) []byte {
	if len(packet) < ipv6HeaderLen || packet[0]>>4 != 6 {
		return nil
	}
	src := netip.AddrFrom16([16]byte(packet[8:24]))
	dst := netip.AddrFrom16([16]byte(packet[24:40]))
	if src.IsUnspecified() || src.IsMulticast() || dst.IsMulticast() || dst.IsUnspecified() {
		return nil
	}
	end := ipv6HeaderLen + int(binary.BigEndian.Uint16(packet[4:6]))
	if end > len(packet) {
		return nil // truncated
	}
	payload := packet[ipv6HeaderLen:end]
	switch packet[6] {
	case protoTCP:
		return tcpReset(src, dst, payload)
	case protoUDP:
		if len(payload) < 8 {
			return nil
		}
		return icmpv6Prohibited(src, dst, packet[:end])
	default:
		// ICMPv6 (echo, neighbour discovery, errors), extension headers, anything else.
		return nil
	}
}

// tcpReset builds the RFC 9293 3.10.7.1 reset for a segment sent to a closed port.
func tcpReset(src, dst netip.Addr, segment []byte) []byte {
	if len(segment) < 20 {
		return nil
	}
	offset := int(segment[12]>>4) * 4
	if offset < 20 || offset > len(segment) {
		return nil
	}
	flags := segment[13]
	if flags&tcpFlagRST != 0 {
		return nil
	}
	seq := binary.BigEndian.Uint32(segment[4:8])
	ack := binary.BigEndian.Uint32(segment[8:12])
	var replySeq, replyAck uint32
	replyFlags := byte(tcpFlagRST)
	if flags&tcpFlagACK != 0 {
		replySeq = ack
	} else {
		length := uint32(len(segment) - offset)
		if flags&tcpFlagSYN != 0 {
			length++
		}
		if flags&tcpFlagFIN != 0 {
			length++
		}
		replyAck = seq + length
		replyFlags |= tcpFlagACK
	}
	reply := make([]byte, ipv6HeaderLen+20)
	writeIPv6Header(reply, 20, protoTCP, dst, src)
	tcp := reply[ipv6HeaderLen:]
	copy(tcp[0:2], segment[2:4]) // source port = their destination port
	copy(tcp[2:4], segment[0:2])
	binary.BigEndian.PutUint32(tcp[4:8], replySeq)
	binary.BigEndian.PutUint32(tcp[8:12], replyAck)
	tcp[12] = 5 << 4
	tcp[13] = replyFlags
	binary.BigEndian.PutUint16(tcp[16:18], transportChecksum(dst, src, protoTCP, tcp))
	return reply
}

// icmpv6Prohibited answers a datagram with "destination unreachable: communication
// administratively prohibited", quoting as much of it as fits in the minimum MTU. The error
// comes from the address the datagram was sent to, as a refusing firewall in front of it would.
func icmpv6Prohibited(src, dst netip.Addr, invoking []byte) []byte {
	quoted := min(len(invoking), icmpv6MaxError-ipv6HeaderLen-8)
	length := 8 + quoted
	reply := make([]byte, ipv6HeaderLen+length)
	writeIPv6Header(reply, length, protoICMPv6, dst, src)
	icmp := reply[ipv6HeaderLen:]
	icmp[0], icmp[1] = icmpv6Unreach, icmpv6AdminBan
	copy(icmp[8:], invoking[:quoted])
	binary.BigEndian.PutUint16(icmp[2:4], transportChecksum(dst, src, protoICMPv6, icmp))
	return reply
}

func writeIPv6Header(b []byte, payloadLen int, next byte, src, dst netip.Addr) {
	b[0] = 6 << 4
	binary.BigEndian.PutUint16(b[4:6], uint16(payloadLen))
	b[6] = next
	b[7] = replyHopLimit
	s, d := src.As16(), dst.As16()
	copy(b[8:24], s[:])
	copy(b[24:40], d[:])
}

// transportChecksum is the RFC 8200 8.1 upper-layer checksum over the IPv6 pseudo-header.
// The checksum field inside data must be zero.
func transportChecksum(src, dst netip.Addr, next byte, data []byte) uint16 {
	var sum uint32
	add := func(b []byte) {
		for len(b) >= 2 {
			sum += uint32(binary.BigEndian.Uint16(b))
			b = b[2:]
		}
		if len(b) == 1 {
			sum += uint32(b[0]) << 8
		}
	}
	s, d := src.As16(), dst.As16()
	add(s[:])
	add(d[:])
	var lengthAndNext [8]byte
	binary.BigEndian.PutUint32(lengthAndNext[0:4], uint32(len(data)))
	lengthAndNext[7] = next
	add(lengthAndNext[:])
	add(data)
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
