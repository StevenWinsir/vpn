//go:build managed_acceptance && !android

package main

import (
	"core/managed"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
)

type acceptanceTransport struct{ direct *http.Transport }

func (t acceptanceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	target, err := url.Parse(os.Getenv("FLCLASH_ACCEPTANCE_API"))
	if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || target.Port() == "" || target.User != nil || target.Path != "" || target.RawQuery != "" || target.Fragment != "" {
		return nil, errors.New("isolated acceptance API required")
	}
	if r.URL.Scheme != "https" || r.URL.Host != "demo.hyshentou.cn" {
		return nil, errors.New("unexpected acceptance source")
	}
	if _, _, err = net.SplitHostPort(target.Host); err != nil {
		return nil, errors.New("invalid acceptance port")
	}
	copy := r.Clone(r.Context())
	copy.URL.Scheme, copy.URL.Host = target.Scheme, target.Host
	copy.Host = target.Host
	return t.direct.RoundTrip(copy)
}

func (t acceptanceTransport) CloseIdleConnections() { t.direct.CloseIdleConnections() }

func init() {
	managedAccount = managed.NewCoordinator(func() http.RoundTripper {
		return acceptanceTransport{managed.NewTransport(nil)}
	}, func() {
		handleStopListener()
		handleCloseConnections()
		stopManagedPlatform()
	}, managedProfiles, managedTotals)
}
