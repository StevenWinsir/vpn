package managed

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestManagedTransportProtectsDNSAndTCPOnIPv4AndIPv6(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			host := "127.0.0.1"
			if network == "tcp6" {
				host = "::1"
			}
			listener, err := net.Listen(network, net.JoinHostPort(host, "0"))
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
			server.Listener = listener
			server.Start()
			defer server.Close()
			_, port, _ := net.SplitHostPort(listener.Addr().String())
			packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			dnsServer := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
				response := new(dns.Msg)
				response.SetReply(request)
				for _, question := range request.Question {
					if question.Qtype == dns.TypeA && network == "tcp4" {
						response.Answer = append(response.Answer, &dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0}, A: net.ParseIP(host)})
					}
					if question.Qtype == dns.TypeAAAA && network == "tcp6" {
						response.Answer = append(response.Answer, &dns.AAAA{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 0}, AAAA: net.ParseIP(host)})
					}
				}
				_ = w.WriteMsg(response)
			})}
			go func() { _ = dnsServer.ActivateAndServe() }()
			defer dnsServer.Shutdown()
			var dnsProtected, tcpProtected atomic.Int64
			transport := NewTransportWithDNS(func(network, _ string, _ syscall.RawConn) error {
				if strings.HasPrefix(network, "udp") {
					dnsProtected.Add(1)
				} else {
					tcpProtected.Add(1)
				}
				return nil
			}, func() []string { return []string{packet.LocalAddr().String()} })
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
			response, err := client.Get("http://managed.test.invalid:" + port)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if dnsProtected.Load() == 0 || tcpProtected.Load() == 0 {
				t.Fatal("DNS or account socket skipped protection")
			}
		})
	}
}

func TestManagedTransportFailsClosedWhenProtectionOrDNSUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("unprotected request reached server") }))
	defer server.Close()
	transport := NewTransport(func(_, _ string, _ syscall.RawConn) error { return errors.New("protect rejected") })
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	if response, err := client.Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("protection failure ignored")
	}
	transport = NewTransportWithDNS(nil, func() []string { return nil })
	defer transport.CloseIdleConnections()
	client.Transport = transport
	if response, err := client.Get("http://managed.test.invalid"); err == nil {
		response.Body.Close()
		t.Fatal("missing network DNS used a fallback")
	}
}

func TestManagedHTTPRejectsRedirectTLSOversizeAndRawServerErrors(t *testing.T) {
	var redirected atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1) }))
	defer target.Close()
	for _, mode := range []string{"redirect", "raw_error", "oversize_status", "oversize_config", "invalid_utf8", "invalid_logout"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				case "raw_error":
					w.WriteHeader(401)
					_, _ = io.WriteString(w, `{"error":{"code":"private-token-fixture","message":"private-password-fixture"}}`)
				case "oversize_status":
					_, _ = io.WriteString(w, strings.Repeat("x", (64<<10)+1))
				case "oversize_config":
					_, _ = io.WriteString(w, strings.Repeat("x", (8<<20)+1))
				case "invalid_utf8":
					_, _ = w.Write([]byte{0xff, 0xfe})
				case "invalid_logout":
					_, _ = io.WriteString(w, `{}`)
				}
			}))
			defer server.Close()
			client, err := NewClient(server.URL, strings.Repeat("A", 43), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			switch mode {
			case "oversize_config":
				_, err = client.Config(context.Background())
			case "invalid_logout":
				err = client.Logout(context.Background())
			default:
				_, err = client.Status(context.Background())
			}
			if err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatal("unsafe response accepted or private error leaked")
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("account redirect followed")
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("untrusted TLS accepted") }))
	defer tlsServer.Close()
	client, err := NewClient(tlsServer.URL, strings.Repeat("A", 43), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Status(context.Background()); PublicError(err) != "heartbeat_unavailable" {
		t.Fatal("untrusted TLS did not fail closed")
	}
	transport := NewTransport(nil)
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("system proxy or insecure TLS enabled")
	}
}
