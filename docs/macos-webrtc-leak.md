# macOS managed TUN and WebRTC regression

## Scope and root causes

This change is based on main `7dfaa8fdc613374b6ec7b25108a373cf168b2ad4`. It reuses reviewed network/lifecycle work from the closed, unmerged PR #4, without its unrelated node deletion or catalog watcher changes. No frontend, backend, database schema, production account, or node credential changes are required.

Two independent paths caused the problem:

1. The managed profile parser intentionally discarded server-supplied listener and `tun` settings, while `managedProfileEngine.Start` only opened the loopback HTTP/SOCKS mixed listener. A system proxy is not packet routing; successful HTTPS and DNS tests did not prove that browser UDP was captured.
2. The pinned Mihomo commit `70f0570405c3c2c47bb113b88db95006d239b346` skips a matching rule when its selected adapter cannot support UDP, then chooses DIRECT after the last rule. A profile ending in `MATCH,VPN` could therefore expose STUN even after UDP reached Mihomo.

A new loopback regression reproduces the second problem on the unchanged main implementation: valid STUN Binding Requests reach their destinations over DIRECT for both IPv4 and IPv6, with both an HTTP node and a Shadowsocks node configured with `udp: false`. The two explicit DIRECT positive controls pass; the four forbidden-direct cases fail before the fix.

## Runtime policy

On macOS, the managed engine owns a gVisor TUN, automatic physical-outbound interface binding, TCP/UDP DNS interception, and IPv4/IPv6 routes. Paired `/1` routes cover both default address families without attempting to replace an existing Darwin `/0` route. The client enforces rule mode and dual-stack DNS locally; administrators cannot accidentally disable this protection by sending listener overrides. External mixed/controller listeners are still not exposed.

`MATCH,REJECT` is appended only after the server catalog's strict `MATCH,VPN` shape validation. UDP-capable nodes continue to carry WebRTC/QUIC; UDP-incapable nodes fail closed instead of falling through to DIRECT. The fix does not block a small list of common STUN ports or disable browser WebRTC.

TUN acquisition is part of the existing authorized, metered runtime transition. A failed permission check, route preflight, TUN creation, or mixed-port bind cannot report a connected state or leave a system-proxy-only fallback. Stop, account revocation, and configuration cleanup release the owned TUN. Failed cleanup retains ownership and prevents opening a second TUN. Competing VPN routes are detected without deleting or stealing them.

The management API uses Mihomo's physical-outbound dialer and proxy-server resolver on macOS so that login/heartbeat/quota reporting cannot recursively enter the client's own tunnel. This control-plane traffic is intentionally separate from the user's metered proxy traffic.

## Authorization and UI

The macOS account panel offers an explicit **Authorize macOS TUN and restart Core** repair action while disconnected. It uses the existing macOS authorization mechanism, then always goes through the single Core lifecycle owner. An already-setuid executable does not retroactively elevate a process that was launched earlier. The Core checks its actual effective UID before opening TUN; file permissions or process-name heuristics are not treated as proof of protection.

Restart requires a fresh login. Remembered credentials remain in the existing credential store; the action does not copy live tokens into a new process. The UI awaits successful Core initialization before publishing readiness, reports initialization crashes, and retains sanitized TUN/start failure codes across otherwise healthy account heartbeats and settlement. Server entitlement revocation still takes precedence over a local diagnostic.

## Automated validation

Ordinary tests do not alter the developer machine's network or request administrator privileges:

```sh
cd FlClash/core
CGO_ENABLED=0 go test -tags=with_gvisor -count=1 ./...
CGO_ENABLED=0 go vet -tags=with_gvisor ./...
go test -race -count=1 ./managed
```

The tests cover TUN policy, route coverage/conflicts, failed acquisition/cleanup/port binding, canceled startup, entitlement/error precedence, and real dual-stack STUN datagrams with forbidden DIRECT fallback. A separate encrypted loopback Shadowsocks peer proves that all three supported AEAD cipher families can return a STUN response through the selected proxy and increment the existing proxy traffic counters. Its mapped address is synthetic; it does not claim to test a commercial node's public exit IP.

Flutter tests cover authorization-button behavior, non-secret error rendering, initialization acknowledgement, repeated/failed restarts, account isolation, and existing account/quota transitions. The workflow retains the existing full Flutter coverage gate and native bundle/signature checks.

Only the disposable GitHub macOS runner enables the explicitly gated real-network tests. These create a real utun, test IPv4/IPv6 round trips, test sockets bound to an available physical interface source address, reproduce legacy default-route and competing-VPN collisions, and reconnect after cleanup. They run both under sudo and with non-root real UID/root effective UID to match the installed app's setuid launch. Another native Flutter test exercises the authorization/restart UI against the real Core IPC. Privileged tests must not run on the normal development machine or alongside a user's active VPN.

## Manual M4 browser acceptance before release

Use a newly built app from this branch in a test environment; do not overwrite the existing source checkout or installed app before review/merge.

1. Record the unproxied public IPv4/IPv6, quit other VPNs, then use the app's authorization action and log in again. Select a known UDP-capable SS node and connect. Check the account page does not show a TUN error. A node with UDP disabled must not silently become a direct UDP path.
2. Start a fresh browser session (or close existing WebRTC peer connections). Compare HTTPS exit IP, DNS results, and WebRTC ICE candidates in each supported browser. Public server-reflexive/relay candidates must not expose the recorded ISP IPv4/IPv6. Exercise calls or other UDP traffic too, so that an entirely broken WebRTC path is not mistaken for success. Confirm the selected node's metered usage increases.
3. Repeat after switching nodes, disconnect/reconnect, sleep/wake, and Ethernet/Wi-Fi changes. Test IPv6 on an IPv6-capable network. Reject permission, occupy the mixed port, and leave another VPN's routes active: each must visibly fail rather than claim protection. Verify disconnect restores only the routes owned by this client.
4. Recheck subscription expiry/quota exhaustion and configuration refresh. They must stop the authorized proxy and cannot silently reuse an old node/session.

## Explicit boundaries

Passing a DNS test is not a WebRTC test. Automated local fixtures, native build success, and TUN routing tests do not establish the public exit address seen by an external leak-test site. Final browser/node verification must be recorded separately.

This is connected-state routing protection, **not a persistent OS kill switch**. Normal internet routing resumes when the user disconnects or the Core exits. It does not promise to hide every local ICE `host` candidate (including private/link-local addresses or mDNS names), disable browser interface enumeration, or override every externally added more-specific route or explicitly interface-scoped socket. Public host candidates on native IPv6 require particular attention in browser acceptance; do not label those harmless merely because they are not `srflx` candidates. A stronger product guarantee needs an independently designed packet-filter/Network Extension policy and browser compatibility testing, not just the `strict-route` flag.

The existing node credentials and traffic reports remain part of the current product design. This patch does not make client-reported accounting authoritative, prevent a device owner from inspecting locally usable credentials, or constitute a full commercial-security audit.
