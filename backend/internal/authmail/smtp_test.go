package authmail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"mime"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testPKI issues a private CA and leaf certificates for the controlled SMTP server.
type testPKI struct {
	pool   *x509.CertPool
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	serial int64
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "authmail test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return &testPKI{pool: pool, ca: ca, caKey: key, serial: 1}
}

func (p *testPKI) leaf(t *testing.T, names ...string) tls.Certificate {
	t.Helper()
	p.serial++
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(p.serial), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// smtpServer is a minimal, controllable ESMTP server for adapter tests.
type smtpServer struct {
	ln            net.Listener
	tls           *tls.Config
	implicitTLS   bool
	offerStartTLS bool
	silent        bool // accept TCP, never answer (timeout case)
	rcptCode      int  // 0 = accept
	user, pass    string

	mu            *sync.Mutex
	messages      [][]byte
	authPlaintext bool
	authAttempts  int
}

func startSMTP(t *testing.T, s *smtpServer) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	s.mu = &sync.Mutex{}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func (s *smtpServer) serve(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	if s.silent {
		time.Sleep(5 * time.Second)
		return
	}
	conn := raw
	secure := false
	if s.implicitTLS {
		tc := tls.Server(raw, s.tls)
		if tc.Handshake() != nil {
			return
		}
		conn, secure = tc, true
	}
	tp := textproto.NewConn(conn)
	reply := func(line string) { _ = tp.PrintfLine("%s", line) }
	reply("220 authmail-test ESMTP")
	authed := false
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO", "HELO":
			lines := []string{"250-authmail-test", "250-8BITMIME", "250-AUTH PLAIN LOGIN"}
			if s.offerStartTLS && !secure {
				lines = append(lines, "250-STARTTLS")
			}
			lines = append(lines, "250 SMTPUTF8")
			for _, l := range lines {
				reply(l)
			}
		case "STARTTLS":
			if !s.offerStartTLS || secure {
				reply("502 5.5.1 not supported")
				continue
			}
			reply("220 2.0.0 ready")
			tc := tls.Server(raw, s.tls)
			if tc.Handshake() != nil {
				return
			}
			conn, secure = tc, true
			tp = textproto.NewConn(conn)
		case "AUTH":
			s.mu.Lock()
			s.authAttempts++
			if !secure {
				s.authPlaintext = true
			}
			s.mu.Unlock()
			fields := strings.Fields(line)
			var user, pass string
			switch strings.ToUpper(fields[1]) {
			case "PLAIN":
				resp := ""
				if len(fields) > 2 {
					resp = fields[2]
				} else {
					reply("334 ")
					resp, _ = tp.ReadLine()
				}
				decoded, _ := base64.StdEncoding.DecodeString(resp)
				parts := strings.Split(string(decoded), "\x00")
				if len(parts) == 3 {
					user, pass = parts[1], parts[2]
				}
			case "LOGIN":
				reply("334 VXNlcm5hbWU6")
				u, _ := tp.ReadLine()
				reply("334 UGFzc3dvcmQ6")
				p, _ := tp.ReadLine()
				du, _ := base64.StdEncoding.DecodeString(u)
				dp, _ := base64.StdEncoding.DecodeString(p)
				user, pass = string(du), string(dp)
			}
			if user == s.user && pass == s.pass {
				authed = true
				reply("235 2.7.0 authenticated")
			} else {
				reply("535 5.7.8 authentication failed")
			}
		case "MAIL":
			if !authed {
				reply("530 5.7.0 authentication required")
				continue
			}
			reply("250 2.1.0 ok")
		case "RCPT":
			if s.rcptCode != 0 {
				reply(strconv.Itoa(s.rcptCode) + " recipient refused")
				continue
			}
			reply("250 2.1.5 ok")
		case "DATA":
			reply("354 go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.messages = append(s.messages, data)
			s.mu.Unlock()
			reply("250 2.0.0 queued")
		case "RSET", "NOOP":
			reply("250 2.0.0 ok")
		case "QUIT":
			reply("221 2.0.0 bye")
			return
		default:
			reply("502 5.5.2 unknown")
		}
	}
}

func testMessage() Message {
	r, _ := Render(TemplateRegisterCode, mailPayload{To: "receiver@qq.com", Code: "012345", Minutes: 10}, "https://app.example.com")
	return Message{To: "receiver@qq.com", MessageID: "job-1@qq.com", Rendered: r}
}

func TestSMTPAdapterTransportSecurity(t *testing.T) {
	pki := newTestPKI(t)
	good := &tls.Config{Certificates: []tls.Certificate{pki.leaf(t, "localhost")}, MinVersion: tls.VersionTLS12}
	wrongName := &tls.Config{Certificates: []tls.Certificate{pki.leaf(t, "mail.other.invalid")}}
	otherPKI := newTestPKI(t)
	untrusted := &tls.Config{Certificates: []tls.Certificate{otherPKI.leaf(t, "localhost")}}
	oldTLS := &tls.Config{Certificates: []tls.Certificate{pki.leaf(t, "localhost")}, MaxVersion: tls.VersionTLS11, MinVersion: tls.VersionTLS10}

	cases := []struct {
		name      string
		server    smtpServer
		implicit  bool
		pass      string
		wantOK    bool
		wantClass string
		temporary bool
	}{
		{name: "implicit TLS 465 style", server: smtpServer{tls: good, implicitTLS: true}, implicit: true, wantOK: true},
		{name: "mandatory STARTTLS 587 style", server: smtpServer{tls: good, offerStartTLS: true}, wantOK: true},
		{name: "untrusted certificate", server: smtpServer{tls: untrusted, implicitTLS: true}, implicit: true, wantClass: "tls_verification"},
		{name: "host name mismatch", server: smtpServer{tls: wrongName, implicitTLS: true}, implicit: true, wantClass: "tls_verification"},
		{name: "STARTTLS untrusted", server: smtpServer{tls: untrusted, offerStartTLS: true}, wantClass: "tls_verification"},
		{name: "no STARTTLS offered", server: smtpServer{tls: good}, wantClass: "starttls_unavailable"},
		{name: "TLS 1.1 only", server: smtpServer{tls: oldTLS, implicitTLS: true}, implicit: true, wantClass: "tls_error"},
		{name: "authentication failure", server: smtpServer{tls: good, implicitTLS: true}, implicit: true, pass: "wrong-code", wantClass: "smtp_auth"},
		{name: "recipient permanently refused", server: smtpServer{tls: good, implicitTLS: true, rcptCode: 550}, implicit: true, wantClass: "smtp_send"},
		{name: "recipient temporarily refused", server: smtpServer{tls: good, implicitTLS: true, rcptCode: 451}, implicit: true, wantClass: "smtp_send", temporary: true},
		{name: "server never answers", server: smtpServer{silent: true}, implicit: false, wantClass: "timeout", temporary: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := tc.server
			server.user, server.pass = "sender@qq.com", "test-authorization-code"
			port := startSMTP(t, &server)
			pass := server.pass
			if tc.pass != "" {
				pass = tc.pass
			}
			mailer := &SMTPMailer{Host: "localhost", Port: port, ImplicitTLS: tc.implicit, Username: server.user, Password: pass, FromName: "AsterLink VPN 星链", Timeout: 2 * time.Second, RootCAs: pki.pool}
			started := time.Now()
			err := mailer.Send(context.Background(), testMessage())
			if time.Since(started) > 4*time.Second {
				t.Fatal("send not bounded by its timeout")
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			if server.authPlaintext {
				t.Fatal("credentials were sent over a plaintext connection")
			}
			if tc.wantOK {
				if err != nil || len(server.messages) != 1 {
					t.Fatalf("send failed: %v", err)
				}
				checkDeliveredMessage(t, server.messages[0])
				return
			}
			var delivery *DeliveryError
			if !errors.As(err, &delivery) || delivery.Class != tc.wantClass || delivery.Temporary != tc.temporary {
				t.Fatalf("got %#v (%v), want class %s temporary=%v", delivery, err, tc.wantClass, tc.temporary)
			}
			if len(server.messages) != 0 {
				t.Fatal("message delivered despite failure")
			}
			if strings.Contains(err.Error(), pass) || strings.Contains(err.Error(), "receiver@qq.com") {
				t.Fatal("delivery error leaked credentials or the recipient")
			}
		})
	}
}

func checkDeliveredMessage(t *testing.T, raw []byte) {
	t.Helper()
	msg, parts := parseMIME(t, raw)
	from, err := msg.Header.AddressList("From")
	if err != nil || len(from) != 1 || from[0].Name != "AsterLink VPN 星链" || from[0].Address != "sender@qq.com" {
		t.Fatalf("From header wrong: %v %v", from, err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "AsterLink VPN 注册验证码" {
		t.Fatalf("subject not decoded: %q", subject)
	}
	if msg.Header.Get("Message-Id") != "<job-1@qq.com>" {
		t.Fatalf("Message-ID wrong: %q", msg.Header.Get("Message-Id"))
	}
	if !strings.Contains(parts["text/plain"], "012345") || !strings.Contains(parts["text/html"], "012345") || !strings.Contains(parts["text/plain"], "验证码") {
		t.Fatalf("plain/html alternatives missing: %v", parts)
	}
	if strings.Contains(string(raw), "\nBcc:") {
		t.Fatal("unexpected header")
	}
}

func TestSMTPFromNameHeaderInjectionRefused(t *testing.T) {
	pki := newTestPKI(t)
	server := smtpServer{tls: &tls.Config{Certificates: []tls.Certificate{pki.leaf(t, "localhost")}}, implicitTLS: true, user: "sender@qq.com", pass: "p"}
	port := startSMTP(t, &server)
	mailer := &SMTPMailer{Host: "localhost", Port: port, ImplicitTLS: true, Username: server.user, Password: "p", FromName: "A\r\nBcc: victim@qq.com", Timeout: 2 * time.Second, RootCAs: pki.pool}
	err := mailer.Send(context.Background(), testMessage())
	server.mu.Lock()
	defer server.mu.Unlock()
	for _, m := range server.messages {
		reader := textproto.NewReader(bufio.NewReader(strings.NewReader(string(m))))
		header, _ := reader.ReadMIMEHeader()
		if header.Get("Bcc") != "" {
			t.Fatal("display name injected a header")
		}
	}
	_ = err // either refused or safely encoded; both are acceptable
}
