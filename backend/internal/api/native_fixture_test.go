package api

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type nativeLoopbackFixture struct {
	profile       string
	proxyAddress  string
	targetAddress string
	request       []byte
}

func startNativeLoopbackFixture(t *testing.T, profile string) nativeLoopbackFixture {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fixture" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil || len(body) > 4096 {
			http.Error(w, "invalid fixture payload", 400)
			return
		}
		_, _ = w.Write(append([]byte("loopback-fixture:"), body...))
	}))
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		target.Close()
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(target.Listener.Addr().String())
	if err != nil {
		listener.Close()
		target.Close()
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		listener.Close()
		target.Close()
		t.Fatal(err)
	}
	request := []byte{5, 1, 0, 1, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(request[8:], uint16(port))
	var workers sync.WaitGroup
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func(connection net.Conn) {
				defer workers.Done()
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
				var greeting [3]byte
				if _, err := io.ReadFull(connection, greeting[:]); err != nil || greeting != [3]byte{5, 1, 0} {
					return
				}
				if _, err := connection.Write([]byte{5, 0}); err != nil {
					return
				}
				var connect [10]byte
				if _, err := io.ReadFull(connection, connect[:]); err != nil {
					return
				}
				if !bytes.Equal(connect[:], request) {
					_, _ = connection.Write([]byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				upstream, err := net.DialTimeout("tcp4", target.Listener.Addr().String(), time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				_ = upstream.SetDeadline(time.Now().Add(3 * time.Second))
				if _, err := connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
					return
				}
				uploaded := make(chan struct{})
				go func() { defer close(uploaded); _, _ = io.Copy(upstream, connection) }()
				_, _ = io.Copy(connection, upstream)
				_ = connection.Close()
				_ = upstream.Close()
				<-uploaded
			}(connection)
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-accepted; workers.Wait(); target.Close() })
	_, proxyPort, _ := net.SplitHostPort(listener.Addr().String())
	return nativeLoopbackFixture{profile: strings.Replace(profile, "port: 18080", "port: "+proxyPort, 1), proxyAddress: listener.Addr().String(), targetAddress: target.Listener.Addr().String(), request: request}
}

func TestNativeControlledLoopbackProxy(t *testing.T) {
	var config struct {
		YAML string `json:"yaml"`
	}
	if err := json.Unmarshal(readNativeContract(t)["vip_ready"].Response, &config); err != nil {
		t.Fatal(err)
	}
	fixture := startNativeLoopbackFixture(t, config.YAML)
	for _, allowed := range []bool{true, false} {
		connection, err := net.DialTimeout("tcp4", fixture.proxyAddress, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := connection.Write([]byte{5, 1, 0}); err != nil {
				t.Fatal(err)
			}
			var greeting [2]byte
			if _, err := io.ReadFull(connection, greeting[:]); err != nil || greeting != [2]byte{5, 0} {
				t.Fatal("fixture negotiation failed")
			}
			request := bytes.Clone(fixture.request)
			if !allowed {
				copy(request[4:8], []byte{192, 0, 2, 1})
			}
			if _, err := connection.Write(request); err != nil {
				t.Fatal(err)
			}
			var result [10]byte
			if _, err := io.ReadFull(connection, result[:]); err != nil {
				t.Fatal(err)
			}
			if !allowed {
				if result[1] != 2 {
					t.Fatal("fixture allowed a non-test target")
				}
				return
			}
			if result[1] != 0 {
				t.Fatal("fixture rejected its test target")
			}
			req, err := http.NewRequest("POST", "http://"+fixture.targetAddress+"/fixture", strings.NewReader("known-upload"))
			if err != nil {
				t.Fatal(err)
			}
			req.Close = true
			if err = req.Write(connection); err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(bufio.NewReader(connection), req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
			if err != nil || response.StatusCode != 200 || string(body) != "loopback-fixture:known-upload" {
				t.Fatal("fixture upload/download did not reach the controlled target")
			}
		}()
	}
}
