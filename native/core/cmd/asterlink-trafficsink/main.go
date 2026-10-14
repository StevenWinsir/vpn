// Command asterlink-trafficsink is the controlled traffic target for the billing
// acceptance (handoff_app_windows.md section 10, round 5, cases B01-B03). It serves a body
// of an exact size and swallows an upload of any size, so the number of payload bytes that
// crossed the paid node is known independently of the client's own counters.
//
// Run it on a machine the test node can reach (not on the machine under test):
//
//	ASTERLINK_SINK_TOKEN=<random> asterlink-trafficsink --listen :8088
//
// Every request needs the token (query "t"), compared in constant time. Plain HTTP is
// deliberate: the bytes on the wire are then the payload plus HTTP framing, which keeps the
// reconciliation honest. Do not leave it running; it will happily send bytes to anyone with
// the token.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

const chunk = 64 << 10

type sink struct {
	token    string
	maxBytes int64

	downSent, upReceived, requests atomic.Int64
}

func newSink(token string, maxBytes int64) *sink { return &sink{token: token, maxBytes: maxBytes} }

func (s *sink) authorized(r *http.Request) bool {
	return len(s.token) >= 16 && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(s.token)) == 1
}

func (s *sink) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		n, err := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64)
		if err != nil || n < 0 || n > s.maxBytes {
			http.Error(w, "bytes out of range", http.StatusBadRequest)
			return
		}
		s.requests.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		w.Header().Set("Cache-Control", "no-store")
		block := make([]byte, chunk)
		for i := range block {
			block[i] = byte('A' + i%26)
		}
		for sent := int64(0); sent < n; {
			part := min(int64(chunk), n-sent)
			written, err := w.Write(block[:part])
			s.downSent.Add(int64(written))
			if err != nil {
				return
			}
			sent += int64(written)
		}
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) || r.Method != http.MethodPost {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.requests.Add(1)
		got, err := io.Copy(io.Discard, io.LimitReader(r.Body, s.maxBytes+1))
		s.upReceived.Add(got)
		if err != nil || got > s.maxBytes {
			http.Error(w, "upload failed", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"received": got})
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"down_sent": s.downSent.Load(), "up_received": s.upReceived.Load(), "requests": s.requests.Load()})
	})
	return mux
}

func main() {
	listen := flag.String("listen", ":8088", "address to listen on")
	maxMiB := flag.Int64("max-mib", 512, "largest single download or upload accepted, in MiB")
	flag.Parse()
	token := os.Getenv("ASTERLINK_SINK_TOKEN")
	if len(token) < 16 {
		fmt.Fprintln(os.Stderr, "the sink needs its access environment variable (see the file header): a random value of at least 16 characters")
		os.Exit(3)
	}
	server := &http.Server{Addr: *listen, Handler: newSink(token, *maxMiB<<20).handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	log.Printf("asterlink-trafficsink listening on %s (max %d MiB per request)", *listen, *maxMiB)
	log.Fatal(server.ListenAndServe())
}
