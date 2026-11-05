package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"sync"
	"time"
)

// relay is the data-plane stand-in of handoff_fix.md 2.2: a TCP forwarder that seals and opens
// every 16 KiB with AES-GCM, so it costs CPU the way an AEAD proxy does. Started under
// `taskpolicy -c utility` it reproduces the pre-fix macOS Core (all threads clamped to 20);
// started plainly it is the fixed one. R3-01 uses the pair to prove that the measurement
// catches F1. It is loopback test plumbing, not a proxy: it only forwards to --to.
//
// rateMbps > 0 paces the whole relay to that rate. Unpaced on loopback it forwards tens of
// Gbit/s and is then CPU-bound by itself, so a busy machine takes a fair share from it at any
// priority; paced well above a real node's rate it needs only part of the CPU, like the Core.
func runRelay(listen, to string, rateMbps float64) int {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Print(err)
		return 2
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		log.Print(err)
		return 2
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	var p *pacer
	if rateMbps > 0 {
		p = &pacer{bytesPerSecond: rateMbps * 1e6 / 8}
	}
	log.Printf("stand-in relay %s -> %s (pid %d, rate %v Mbit/s, 0 = unpaced)", ln.Addr(), to, os.Getpid(), rateMbps)
	for {
		client, err := ln.Accept()
		if err != nil {
			log.Print(err)
			return 1
		}
		go func() {
			defer client.Close()
			upstream, err := net.DialTimeout("tcp", to, 5*time.Second)
			if err != nil {
				return
			}
			defer upstream.Close()
			go func() { _ = aeadCopy(upstream, client, aead, nil); upstream.Close() }()
			_ = aeadCopy(client, upstream, aead, p)
		}()
	}
}

// pacer spreads the relay's bytes evenly over time (shared by all its connections).
type pacer struct {
	mu             sync.Mutex
	bytesPerSecond float64
	next           time.Time
}

func (p *pacer) wait(n int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	p.next = p.next.Add(time.Duration(float64(n) / p.bytesPerSecond * float64(time.Second)))
	d := p.next.Sub(now)
	p.mu.Unlock()
	if d > time.Millisecond {
		time.Sleep(d)
	}
}

func aeadCopy(dst io.Writer, src io.Reader, aead cipher.AEAD, p *pacer) error {
	buf := make([]byte, 16<<10)
	nonce := make([]byte, aead.NonceSize())
	sealed := make([]byte, 0, len(buf)+aead.Overhead())
	var counter uint64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			counter++
			for i := 0; i < 8; i++ {
				nonce[i] = byte(counter >> (8 * i))
			}
			sealed = aead.Seal(sealed[:0], nonce, buf[:n], nil)
			plain, openErr := aead.Open(buf[:0], nonce, sealed, nil)
			if openErr != nil {
				return openErr
			}
			p.wait(len(plain))
			if _, werr := dst.Write(plain); werr != nil {
				return werr
			}
		}
		if err != nil {
			return err
		}
	}
}

// runBurn is one contention process: a busy loop per thread it was asked for, ending on its
// own after d so a killed parent cannot leave it spinning.
func runBurn(d time.Duration, threads int) int {
	if threads < 1 {
		threads = 1
	}
	runtime.GOMAXPROCS(threads)
	deadline := time.Now().Add(d)
	done := make(chan struct{})
	for i := 0; i < threads; i++ {
		go func() {
			x := uint64(1)
			for time.Now().Before(deadline) {
				for j := 0; j < 1<<16; j++ {
					x = x*6364136223846793005 + 1442695040888963407
				}
			}
			if x == 0 {
				println(x)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < threads; i++ {
		<-done
	}
	return 0
}
