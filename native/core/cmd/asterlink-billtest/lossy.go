package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

// lossyTransport sits under the Core's real API client. When armed it lets one /traffic
// request reach the real server, throws the reply away and reports a network failure: the
// exact shape of a lost acknowledgement (B02). It also records every /traffic body so the
// test can prove that the retry carries the same sequence and the same counters.
type lossyTransport struct {
	next http.RoundTripper

	mu      sync.Mutex
	armed   bool
	bodies  []string
	dropped int // index into bodies of the request whose reply was discarded, -1 if none
}

func newLossy() *lossyTransport { return &lossyTransport{dropped: -1} }

func (l *lossyTransport) wrap(next http.RoundTripper) http.RoundTripper {
	l.next = next
	return l
}

// dropNext arms exactly one lost acknowledgement.
func (l *lossyTransport) dropNext() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.armed, l.dropped = true, -1
}

// trafficBodies returns the recorded bodies and the index of the dropped one.
func (l *lossyTransport) trafficBodies() ([]string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.bodies...), l.dropped
}

var errLostAck = errors.New("acknowledgement lost (acceptance harness)")

func (l *lossyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(request.URL.Path, "/traffic") {
		return l.next.RoundTrip(request)
	}
	var body []byte
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
		request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(body))
	}
	l.mu.Lock()
	l.bodies = append(l.bodies, string(body))
	index := len(l.bodies) - 1
	drop := l.armed
	if drop {
		l.armed, l.dropped = false, index
	}
	l.mu.Unlock()
	response, err := l.next.RoundTrip(request)
	if drop && err == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, errLostAck
	}
	return response, err
}
