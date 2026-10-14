package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// childFlag re-executes the harness as an ordinary application. On Windows the harness is
// the Core stand-in and the TUN's firewall rules exempt that process, so traffic that must
// ride the tunnel (and be metered) comes from a separate child, like any user program.
const childFlag = "--child"

// tokenEnv carries the sink token to the child without putting it on a command line.
const tokenEnv = "ASTERLINK_BILL_SINK_TOKEN"

type transferResult struct {
	Upload   int64  `json:"upload_bytes"`
	Download int64  `json:"download_bytes"`
	Millis   int64  `json:"millis"`
	Error    string `json:"error,omitempty"`
}

// transferFunc moves exactly upload bytes up and download bytes down through whatever path
// the operating system gives an ordinary program.
type transferFunc func(ctx context.Context, upload, download int64) (transferResult, error)

// childTransfer runs a transfer in a child process and parses its single JSON line.
func childTransfer(sinkBase string) transferFunc {
	return func(ctx context.Context, upload, download int64) (transferResult, error) {
		exe, err := probeExecutable()
		if err != nil {
			return transferResult{}, err
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, exe, childFlag, "transfer", sinkBase, strconv.FormatInt(upload, 10), strconv.FormatInt(download, 10)).Output()
		var result transferResult
		if jsonErr := json.Unmarshal(bytes.TrimSpace(out), &result); jsonErr != nil {
			return transferResult{}, fmt.Errorf("transfer child gave no result (%v, %v)", err, jsonErr)
		}
		if result.Error != "" {
			return result, fmt.Errorf("transfer: %s", result.Error)
		}
		return result, err
	}
}

// runChild is the body of the child process. It prints one JSON line and exits.
func runChild(args []string) int {
	if len(args) != 4 || args[0] != "transfer" {
		return 2
	}
	up, err1 := strconv.ParseInt(args[2], 10, 64)
	down, err2 := strconv.ParseInt(args[3], 10, 64)
	if err1 != nil || err2 != nil || up < 0 || down < 0 {
		return 2
	}
	result := doTransfer(context.Background(), args[1], os.Getenv(tokenEnv), up, down)
	_ = json.NewEncoder(os.Stdout).Encode(result)
	if result.Error != "" {
		return 1
	}
	return 0
}

// zeros produces n bytes without allocating them.
type zeros struct{ left int64 }

func (z *zeros) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > z.left {
		n = z.left
	}
	clear(p[:n])
	z.left -= n
	return int(n), nil
}

func doTransfer(ctx context.Context, base, token string, upload, download int64) transferResult {
	started := time.Now()
	result := transferResult{}
	// No proxy setting and no interface binding: the same path as any application.
	client := &http.Client{Timeout: 15 * time.Minute, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	query := func(extra url.Values) string {
		extra.Set("t", token)
		return extra.Encode()
	}
	if upload > 0 {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/up?"+query(url.Values{}), &zeros{left: upload})
		if err != nil {
			result.Error = err.Error()
			return result
		}
		request.ContentLength = upload
		request.Header.Set("Content-Type", "application/octet-stream")
		response, err := client.Do(request)
		if err != nil {
			result.Error = "upload: " + redact(err.Error(), token)
			return result
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			result.Error = fmt.Sprintf("upload: status %d", response.StatusCode)
			return result
		}
		result.Upload = upload
	}
	if download > 0 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/down?"+query(url.Values{"bytes": {strconv.FormatInt(download, 10)}}), nil)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		response, err := client.Do(request)
		if err != nil {
			result.Error = "download: " + redact(err.Error(), token)
			return result
		}
		got, copyErr := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		result.Download = got
		switch {
		case response.StatusCode != http.StatusOK:
			result.Error = fmt.Sprintf("download: status %d", response.StatusCode)
		case copyErr != nil:
			result.Error = "download: " + redact(copyErr.Error(), token)
		case got != download:
			result.Error = fmt.Sprintf("download: got %d of %d bytes", got, download)
		}
	}
	result.Millis = time.Since(started).Milliseconds()
	return result
}

// redact keeps the sink token out of error text (Go includes the URL in client errors).
func redact(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "<token>")
}

type sinkStats struct {
	DownSent   int64 `json:"down_sent"`
	UpReceived int64 `json:"up_received"`
	Requests   int64 `json:"requests"`
}

// fetchSinkStats asks the sink for its totals. It is called while no tunnel exists, so the
// request never touches the ledger being measured.
func fetchSinkStats(ctx context.Context, base, token string) (sinkStats, error) {
	var stats sinkStats
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/stats?t="+url.QueryEscape(token), nil)
	if err != nil {
		return stats, err
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return stats, fmt.Errorf("sink unreachable: %s", redact(err.Error(), token))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return stats, fmt.Errorf("sink answered %d", response.StatusCode)
	}
	return stats, json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&stats)
}
