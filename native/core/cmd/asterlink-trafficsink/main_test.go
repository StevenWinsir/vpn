package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const token = "0123456789abcdef-token"

func TestDownloadHasTheExactRequestedSize(t *testing.T) {
	s := newSink(token, 10<<20)
	server := httptest.NewServer(s.handler())
	defer server.Close()
	var total int64
	for _, size := range []int64{0, 1, 65536, 65537, 3<<20 + 5} {
		response, err := http.Get(server.URL + "/down?bytes=" + strconv.FormatInt(size, 10) + "&t=" + token)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if got != size || response.ContentLength != size {
			t.Fatalf("asked %d, body %d, content-length %d", size, got, response.ContentLength)
		}
		total += size
	}
	if s.downSent.Load() != total {
		t.Fatalf("down counter %d, want %d", s.downSent.Load(), total)
	}
}

func TestUploadIsCountedAndReported(t *testing.T) {
	s := newSink(token, 10<<20)
	server := httptest.NewServer(s.handler())
	defer server.Close()
	response, err := http.Post(server.URL+"/up?t="+token, "application/octet-stream", bytes.NewReader(make([]byte, 1234567)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), `"received":1234567`) || s.upReceived.Load() != 1234567 {
		t.Fatalf("%s / %d", body, s.upReceived.Load())
	}
}

func TestTokenAndLimitsAreEnforced(t *testing.T) {
	s := newSink(token, 1<<20)
	server := httptest.NewServer(s.handler())
	defer server.Close()
	for _, url := range []string{"/down?bytes=10", "/down?bytes=10&t=wrong", "/stats", "/stats?t=", "/down?bytes=2097152&t=" + token, "/down?bytes=-1&t=" + token, "/down?t=" + token} {
		response, err := http.Get(server.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatalf("%s was served", url)
		}
	}
	response, err := http.Post(server.URL+"/up?t="+token, "text/plain", bytes.NewReader(make([]byte, 2<<20)))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized upload status %d", response.StatusCode)
	}
	// A short token is never acceptable, even when it matches.
	short := httptest.NewServer(newSink("short", 1<<20).handler())
	defer short.Close()
	response, err = http.Get(short.URL + "/stats?t=short")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("a short token was accepted")
	}
}

func TestUDPEchoAnswersOnlyTheTokenHolder(t *testing.T) {
	s := newSink(token, 1<<20)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go s.serveUDP(pc)
	client, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ask := func(payload string) (string, bool) {
		_, _ = client.Write([]byte(payload))
		_ = client.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		buf := make([]byte, 128)
		n, err := client.Read(buf)
		return string(buf[:n]), err == nil
	}
	if back, ok := ask("t=" + token + ";hello"); !ok || back != "t="+token+";hello" {
		t.Fatalf("token holder got %q, %v", back, ok)
	}
	if _, ok := ask("hello"); ok {
		t.Fatal("a datagram without the token was answered")
	}
	if _, ok := ask("t=wrong-token-wrong-token;hello"); ok {
		t.Fatal("a datagram with the wrong token was answered")
	}
	if s.udpEchoed.Load() != 1 {
		t.Fatalf("echo counter %d", s.udpEchoed.Load())
	}
}
