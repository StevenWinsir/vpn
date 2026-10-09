//go:build !cgo

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
	SS "github.com/metacubex/mihomo/transport/shadowsocks/core"
	"github.com/metacubex/mihomo/transport/socks5"
)

// Exercise the same restricted SS AEAD types accepted by the admin catalog.
// These are real loopback encrypted TCP transfers, not commercial node tests.
func TestManagedShadowsocksAEADTransport(t *testing.T) {
	for _, algorithm := range []string{"aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
		for _, accepted := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/correct-password=%t", algorithm, accepted), func(t *testing.T) {
				payload := bytes.Repeat([]byte("private-loopback-payload\n"), 2048)
				echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer r.Body.Close()
					data, err := io.ReadAll(io.LimitReader(r.Body, int64(len(payload)+1)))
					if err != nil || !bytes.Equal(data, payload) {
						http.Error(w, "invalid fixture payload", 400)
						return
					}
					w.Header().Set("Connection", "close")
					_, _ = w.Write(data)
				}))
				defer echo.Close()
				listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
				cipher, err := SS.PickCipher(algorithm, nil, "isolated-ss-fixture-password")
				if err != nil {
					t.Fatal(err)
				}
				served := make(chan error, 1)
				go func() {
					raw, err := listener.Accept()
					if err != nil {
						served <- err
						return
					}
					defer raw.Close()
					_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
					conn := cipher.StreamConn(raw)
					destination, err := socks5.ReadAddr(conn, make([]byte, socks5.MaxAddrLen))
					if err != nil {
						served <- err
						return
					}
					// Refuse any destination other than this owned loopback fixture.
					if destination.String() != echo.Listener.Addr().String() {
						served <- fmt.Errorf("unexpected fixture destination")
						return
					}
					remote, err := net.DialTimeout("tcp", destination.String(), time.Second)
					if err != nil {
						served <- err
						return
					}
					defer remote.Close()
					_ = remote.SetDeadline(time.Now().Add(5 * time.Second))
					done := make(chan struct{})
					go func() { _, _ = io.Copy(remote, conn); _ = remote.Close(); close(done) }()
					_, err = io.Copy(conn, remote)
					_ = conn.Close()
					<-done
					served <- err
				}()
				password := "isolated-ss-fixture-password"
				if !accepted {
					password = "rejected-fixture-password"
				}
				proxy, err := adapter.ParseProxy(map[string]any{
					"name": "SS fixture", "type": "ss", "server": "127.0.0.1",
					"port": listener.Addr().(*net.TCPAddr).Port, "cipher": algorithm,
					"password": password, "udp": true,
				})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				connection, err := proxy.DialContext(ctx, &C.Metadata{
					NetWork: C.TCP, DstIP: netip.MustParseAddr("127.0.0.1"),
					DstPort: uint16(echo.Listener.Addr().(*net.TCPAddr).Port),
				})
				if err != nil {
					t.Fatal(err)
				}
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				request, _ := http.NewRequest(http.MethodPost, echo.URL, bytes.NewReader(payload))
				request.Close = true
				err = request.Write(connection)
				var response *http.Response
				if err == nil {
					response, err = http.ReadResponse(bufio.NewReader(connection), request)
				}
				if accepted {
					if err != nil {
						_ = connection.Close()
						t.Fatal(err)
					}
					data, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if readErr != nil || response.StatusCode != 200 || !bytes.Equal(data, payload) {
						_ = connection.Close()
						t.Fatal("encrypted Shadowsocks payload did not round-trip")
					}
				} else if err == nil {
					_ = response.Body.Close()
					_ = connection.Close()
					t.Fatal("wrong SS password reached the destination")
				}
				_ = connection.Close()
				select {
				case serverErr := <-served:
					if accepted && serverErr != nil {
						t.Fatal(serverErr)
					}
					if !accepted && serverErr == nil {
						t.Fatal("server accepted wrong password")
					}
				case <-ctx.Done():
					t.Fatal("fixture did not shut down")
				}
			})
		}
	}
}
