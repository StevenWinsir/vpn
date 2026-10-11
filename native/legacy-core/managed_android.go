//go:build android && cgo

package main

import (
	"core/managed"
	"net/http"
	"sync/atomic"
	"syscall"
)

var managedDNSServers atomic.Pointer[[]string]

func managedProtect(_ string, _ string, connection syscall.RawConn) error {
	handler := activeTunHandler.Load()
	if handler == nil {
		return nil
	}
	var protected error
	if err := connection.Control(func(fd uintptr) { protected = handler.handleProtect(int(fd)) }); err != nil {
		return err
	}
	return protected
}

func newManagedTransport() http.RoundTripper {
	return managed.NewTransportWithDNS(managedProtect, func() []string {
		if servers := managedDNSServers.Load(); servers != nil {
			return *servers
		}
		return nil
	})
}

func stopManagedPlatform() { handleStopTun() }
