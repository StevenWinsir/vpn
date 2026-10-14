package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
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
