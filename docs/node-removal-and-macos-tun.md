# Node removal and macOS TUN privacy changes

## Deployment order

Deploy the Go API and Next.js frontend, then distribute a newly built macOS app from the same revision. Existing apps do not acquire the new watch or TUN behavior merely by restarting an old binary. No database migration is required.

`DELETE /api/v1/admin/nodes/:id` requires an active administrator, the existing same-origin/CSRF checks, and JSON containing the current node `version`. A stale version returns 409 and must be reviewed before retrying. Deletion removes the node and encrypted configuration in one transaction and writes `node.deleted` to the audit trail. Historical reports and their original rate snapshots remain intact. Repeated deletion returns 404.

## Online synchronization and accounting

The authenticated `GET /api/v1/client/nodes?revision=<sha256>` endpoint exposes entitlement-filtered metadata only. A watch waits up to five seconds, wakes on a committed mutation in its API process, and checks PostgreSQL every second to observe changes made through another API replica. The app pauses one second between successful watches and backs off after network errors. This is normally second-scale synchronization, not an unconditional real-time delivery guarantee.

Metadata watches do not renew authorization leases, update traffic counters, or replace the existing 60-second reporting cadence. Upon a changed catalog the client stops the old runtime, drains it, settles its final cumulative report using the old node/rate binding, and requests the current server configuration. It does not automatically connect the replacement. Removing the last eligible node clears the managed configuration. Deleting an unselected node also changes the catalog hash and invalidates the old configuration. Responses from an earlier account, configuration, or runtime transition cannot overwrite current state. Offline clients remain subject to the existing 90-second authorization lease; old clients retain their older heartbeat-based synchronization behavior.

This endpoint is a bounded long poll with database polling, not a cross-region messaging system. Load-test watch concurrency, API limits, database pool size, and reverse-proxy timeouts before scaling it to a large commercial fleet. A watch must be allowed to wait at least five seconds plus transport overhead.

## macOS connection policy

The earlier managed path started a loopback mixed proxy and enabled system proxy settings. That does not route arbitrary UDP, including WebRTC STUN. New managed macOS connections additionally require an actual privileged TUN listener before the runtime can report connected. The account screen provides an explicit authorization action using the project's existing macOS privilege mechanism. Core restart invalidates the local login; sign in again afterward. Refusing authorization leaves the proxy disconnected.

The runtime owns the TUN listener and its cleanup. It captures IPv4 and IPv6 with automatic routes, automatic outbound-interface detection, the gVisor stack, and UDP/TCP DNS interception. User DNS respects the selected proxy rule; proxy/control-plane bootstrap resolution is intentionally separate. ICMP direct forwarding is disabled. A final `MATCH,REJECT` guard prevents Mihomo from falling through to direct UDP when the selected proxy does not support UDP. A node without working UDP may therefore cause WebRTC calls to fail instead of exposing the user's direct IP.

TUN creation errors are not treated as successful system-proxy-only connections. Failed cleanup retains ownership and blocks a second listener. The shared proxy credentials supplied by the server are still present inside a trusted client's memory; hiding the YAML is not cryptographic secrecy against the device owner.

## What tests do and do not establish

Automated regressions cover administrator deletion/CSRF/concurrency, metadata synchronization, stale responses, tail-accounting preservation, TUN lifecycle failures, and actual loopback UDP rejection with a working positive control. The macOS CI job additionally opens a real `utun` interface and sends UDP in both address families through the managed data-plane wrapper. That test installs only two documentation/test destination routes, not default routes, and does not connect to an external proxy or STUN service.

A green build or isolated `utun` test is **not** proof that every browser/network combination passes a public WebRTC leak test. Before release, on the M4 Mac with the newly built app, record the browser/version, physical-interface public IPv4/IPv6, proxy exit addresses, and ICE candidate types (`host`, `srflx`, `relay`). Test a UDP-capable node, a TCP-only node, IPv4-only and dual-stack access, Wi-Fi/Ethernet changes, sleep/wake, denied authorization, node removal, quota exhaustion, and plan expiry. Check browser-visible public candidates and physical-interface packet capture, not just an HTTP IP page. Do not publish captured credentials or unredacted personal IPs in CI artifacts.

This patch is **not a system-wide kill switch**. After disconnect/crash, normal OS networking can resume. TUN routing also does not by itself hide browser-reported host candidates or guarantee interception of sockets explicitly bound to a physical interface. Stronger commercial guarantees require a separate reviewed network-extension/firewall and browser-policy design, including IPv6 and crash recovery. Deleting an API catalog entry cannot revoke previously copied shared credentials on a third-party proxy server: revoke or rotate those credentials at the actual node, or use enforceable per-user server authorization.
