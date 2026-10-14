package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"

	"asterlink/core/internal/apiclient"
	"asterlink/core/internal/engine"
	"asterlink/core/internal/managed"
)

// runGrants wires the round-7 scenarios to the real API, sink and (with --tun) the production Manager and engine.
func runGrants(ctx context.Context, cfg config, opt grantOptions, raceAt, output string, lossy *lossyTransport, eng *engine.Mihomo) int {
	rec := &recorder{}
	base, err := apiclient.ParseBase(cfg.apiBase, cfg.channel)
	if err != nil {
		fail("BLOCKED: " + err.Error())
	}
	if raceAt != "" {
		if opt.raceAt, err = time.Parse(time.RFC3339, raceAt); err != nil {
			fail("BLOCKED: --race-at must be RFC 3339: " + err.Error())
		}
	}
	sink, err := destinationOf(cfg.sinkBase)
	if err != nil {
		fail("BLOCKED: " + err.Error())
	}
	var udp *destination
	if opt.udpSink != "" {
		host, port, splitErr := net.SplitHostPort(opt.udpSink)
		number, numErr := strconv.Atoi(port)
		if splitErr != nil || numErr != nil || host == "" {
			fail("BLOCKED: --udp-sink must be host:port")
		}
		udp = &destination{host, number}
	}
	token := os.Getenv(tokenEnv)
	api := apiclient.New(base)
	trace := newLossy()
	api.WrapTransport(trace.wrap)
	g := &grantRun{
		cfg: cfg, opt: opt, rec: rec, api: api, trace: trace, sink: sink, udp: udp, token: token, now: time.Now,
		stats: func(ctx context.Context) (sinkStats, error) { return fetchSinkStats(ctx, cfg.sinkBase, token) },
	}
	var manager *managed.Manager
	if opt.tun {
		manager = managed.New(managed.Options{
			CoreVersion: "billtest", Engine: eng,
			NewClient: func(base *url.URL) *apiclient.Client {
				client := apiclient.New(base)
				client.WrapTransport(lossy.wrap)
				return client
			},
		})
		g.h = &harness{cfg: cfg, m: manager, lossy: lossy, rec: rec, xfer: childTransfer(cfg.sinkBase), stats: g.stats}
	}
	g.run(ctx)
	code := finish(output, rec, cfg)
	if manager != nil {
		stop, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		manager.Shutdown(stop)
		cancel()
		_ = eng.Stop()
		cleanupProbe()
	}
	fmt.Println("grants run finished")
	return code
}
