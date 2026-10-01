package main

import (
	"context"
	"core/managed"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

type retainedPacketTunnel struct {
	C.Tunnel
	packet C.UDPPacket
}

func (t *retainedPacketTunnel) HandleUDPPacket(packet C.UDPPacket, _ *C.Metadata) {
	t.packet = packet
}

type recyclablePacket struct {
	C.UDPPacket
	data   []byte
	drops  atomic.Int64
	writes atomic.Int64
}

func (p *recyclablePacket) Data() []byte { return p.data }
func (p *recyclablePacket) Drop() {
	p.drops.Add(1)
	clear(p.data)
}
func (p *recyclablePacket) WriteBack(data []byte, _ net.Addr) (int, error) {
	p.writes.Add(1)
	return len(data), nil
}

func TestManagedStopPreservesConsumerOwnedUDPBuffer(t *testing.T) {
	base := &retainedPacketTunnel{}
	plane := newManagedTrafficTunnel(base)
	t.Cleanup(func() {
		managedPlanes.Lock()
		delete(managedPlanes.items, plane)
		managedPlanes.Unlock()
	})
	packet := &recyclablePacket{data: []byte("pending upload")}
	plane.HandleUDPPacket(packet, nil)
	plane.stop()
	if packet.drops.Load() != 0 || string(base.packet.Data()) != "pending upload" {
		t.Fatal("stop recycled a buffer still owned by the upstream consumer")
	}
	if _, err := base.packet.WriteBack([]byte("late response"), nil); err != net.ErrClosed || packet.writes.Load() != 0 {
		t.Fatal("stopped tunnel allowed UDP response")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := (&managedProfileEngine{}).Drain(ctx); managed.PublicError(err) != "traffic_unconfirmed" {
		t.Fatalf("drain declared an outstanding packet settled: %v", err)
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() { defer workers.Done(); base.packet.Drop() }()
	}
	workers.Wait()
	if packet.drops.Load() != 1 || plane.packets != 0 {
		t.Fatal("consumer release did not decrement exactly once")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (&managedProfileEngine{}).Drain(ctx); err != nil {
		t.Fatalf("drain did not finish after consumer release: %v", err)
	}
	late := &recyclablePacket{data: []byte("rejected")}
	plane.HandleUDPPacket(late, nil)
	if late.drops.Load() != 1 || plane.packets != 0 {
		t.Fatal("stopped tunnel retained a new packet")
	}
}
