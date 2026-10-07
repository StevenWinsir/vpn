# macOS managed TUN and WebRTC regression

## Scope and root causes

This change is based on main `7dfaa8fdc613374b6ec7b25108a373cf168b2ad4`. It reuses reviewed network/lifecycle work from the closed, unmerged PR #4, without its unrelated node deletion or catalog watcher changes. No frontend, backend, database schema, production account, or node credential changes are required.

The source audit found two independent paths, and the first native CI run exposed a third:

1. The managed profile parser intentionally discarded server-supplied listener and `tun` settings, while `managedProfileEngine.Start` only opened the loopback HTTP/SOCKS mixed listener. A system proxy is not packet routing; successful HTTPS and DNS tests did not prove that browser UDP was captured.
2. The pinned Mihomo commit `70f0570405c3c2c47bb113b88db95006d239b346` skips a matching rule when its selected adapter cannot support UDP, then chooses DIRECT after the last rule. A profile ending in `MATCH,VPN` could therefore expose STUN even after UDP reached Mihomo.
3. Darwin source-bound sockets can bypass unscoped TUN routes. The first [native CI run](https://github.com/StevenWinsir/vpn/actions/runs/37575227764/job/112642509527) passed ordinary IPv4/IPv6 TUN round trips but failed the physical-source-bound UDP cases. A route-only fix was therefore insufficient; the follow-up adds an actual PF egress guard rather than removing that acceptance requirement.

A new loopback regression reproduces the second problem on the unchanged main implementation: valid STUN Binding Requests reach their destinations over DIRECT for both IPv4 and IPv6, with both an HTTP node and a Shadowsocks node configured with `udp: false`. The two explicit DIRECT positive controls pass; the four forbidden-direct cases fail before the fix.

## Runtime policy

The connected-state PF guard rejects ordinary-user TCP/UDP attempting to leave outside the owned utun, including source-bound WebRTC/STUN and TCP-based fallback. Loopback is exempt. Normal UDP through TUN remains available; the privileged Core's physical proxy/control-plane sockets are allowed. Root processes are trusted and are not contained by this policy.

On macOS, the managed engine owns a gVisor TUN, automatic physical-outbound interface binding, TCP/UDP DNS interception, and IPv4/IPv6 routes. Paired `/1` routes cover both default address families without attempting to replace an existing Darwin `/0` route. The client enforces rule mode and dual-stack DNS locally; administrators cannot accidentally disable this protection by sending listener overrides. External mixed/controller listeners are still not exposed.

`MATCH,REJECT` is appended only after the server catalog's strict `MATCH,VPN` shape validation. UDP-capable nodes continue to carry WebRTC/QUIC; UDP-incapable nodes fail closed instead of falling through to DIRECT. The fix does not block a small list of common STUN ports or disable browser WebRTC.

TUN acquisition is part of the existing authorized, metered runtime transition. A failed permission check, route preflight, TUN creation, or mixed-port bind cannot report a connected state or leave a system-proxy-only fallback. Stop, account revocation, and configuration cleanup release the owned TUN. Failed cleanup retains ownership and prevents opening a second TUN. Competing VPN routes are detected without deleting or stealing them.

The management API uses Mihomo's physical-outbound dialer and proxy-server resolver on macOS so that login/heartbeat/quota reporting cannot recursively enter the client's own tunnel. This control-plane traffic is intentionally separate from the user's metered proxy traffic.

PF acquisition and cleanup are owned by the same runtime as TUN. Partial acquisition returns its resources to the caller for rollback; failed rollback retains ownership and prevents a second connection. The guard uses a dedicated anchor and a reference-counted `pfctl -E`/`-X` pair. It never globally disables PF, flushes another application's states/rules, or replaces NAT/options. Cleanup removes a temporary root filter hook only when it is still exactly the hook this instance installed.

Compatibility is deliberately conservative: an empty root filter or the standard `com.apple/*` hook may be used; unrecognized filter policies, overriding anchors, and pre-existing PF states cause connection failure rather than silently weakening protection. Existing PF states are checked because they can bypass newly loaded filter rules. Users of additional firewalls or Internet Sharing must verify this compatibility before release. The Core stores only its cleanup lease in root-owned `0700` `/var/run/flclash-managed-pf`, with `0600`, non-symlink, single-link files. Reconnect recovers a valid abandoned lease only after the saved PID is proven absent; a live or reused PID is not guessed from its process name.

## Authorization and UI

The macOS account panel offers an explicit **Authorize macOS TUN and restart Core** repair action while disconnected. It uses the existing macOS authorization mechanism, then always goes through the single Core lifecycle owner. An already-setuid executable does not retroactively elevate a process that was launched earlier. This authorization permits the Core to manage both TUN and the PF guard. The Core checks its actual effective UID before opening TUN; file permissions or process-name heuristics are not treated as proof of protection.

Restart requires a fresh login. Remembered credentials remain in the existing credential store; the action does not copy live tokens into a new process. The UI awaits successful Core initialization before publishing readiness, reports initialization crashes, and retains sanitized TUN/start failure codes across otherwise healthy account heartbeats and settlement. Server entitlement revocation still takes precedence over a local diagnostic.

## Automated validation

Ordinary tests do not alter the developer machine's network or request administrator privileges:

```sh
cd FlClash/core
CGO_ENABLED=0 go test -tags=with_gvisor -count=1 ./...
CGO_ENABLED=0 go vet -tags=with_gvisor ./...
go test -race -count=1 ./managed
```

The tests cover TUN policy, route coverage/conflicts, failed acquisition/cleanup/port binding, canceled startup, entitlement/error precedence, and real dual-stack STUN datagrams with forbidden DIRECT fallback. A separate encrypted loopback Shadowsocks peer proves that all three supported AEAD algorithms can return a STUN response through the selected proxy and increment the existing proxy traffic counters. Its mapped address is synthetic; it does not claim to test a commercial node's public exit IP.

Flutter tests cover authorization-button behavior, non-secret error rendering, initialization acknowledgement, repeated/failed restarts, account isolation, and existing account/quota transitions. The workflow retains the existing full Flutter coverage gate and native bundle/signature checks.

Only the disposable GitHub macOS runner enables the explicitly gated real-network tests. These create a real utun, prove normal IPv4/IPv6 round trips from both privileged and ordinary-user processes, and require actual PF UDP rejection counters to increase for ordinary-user sockets bound to an available physical source address. A timeout alone cannot pass the negative test. They also verify privileged physical egress, abandoned-owner lease recovery, legacy default-route and competing-VPN collisions, and reconnect after cleanup. They run both under sudo and with non-root real UID/root effective UID to match the installed app's setuid launch. Another native Flutter test exercises the authorization/restart UI against the real Core IPC. Privileged tests must not run on the normal development machine or alongside a user's active VPN.

## Manual M4 browser acceptance before release

Use a newly built app from this branch in a test environment; do not overwrite the existing source checkout or installed app before review/merge.

1. Record the unproxied public IPv4/IPv6, quit other VPNs, then use the app's authorization action and log in again. Select a known UDP-capable SS node and connect. Check the account page does not show a TUN error. A node with UDP disabled must not silently become a direct UDP path.
2. Start a fresh browser session (or close existing WebRTC peer connections). Compare HTTPS exit IP, DNS results, and WebRTC ICE candidates in each supported browser. Public server-reflexive/relay candidates must not expose the recorded ISP IPv4/IPv6. Exercise calls or other UDP traffic too, so that an entirely broken WebRTC path is not mistaken for success. Confirm the selected node's metered usage increases.
3. Repeat after switching nodes, disconnect/reconnect, sleep/wake, and Ethernet/Wi-Fi changes. Test IPv6 on an IPv6-capable network. Reject permission, occupy the mixed port, and leave another VPN's routes active: each must visibly fail rather than claim protection. Verify disconnect restores only the routes owned by this client.
4. Recheck subscription expiry/quota exhaustion and configuration refresh. They must stop the authorized proxy and cannot silently reuse an old node/session.

## Explicit boundaries

Passing a DNS test is not a WebRTC test. Automated local fixtures, native build success, and TUN routing tests do not establish the public exit address seen by an external leak-test site. Final browser/node verification must be recorded separately.

This is a connected-state TCP/UDP egress guard, **not a complete, all-protocol or root-process kill switch**. Normal disconnect removes the owned guard and restores normal routing. Unexpected Core termination can leave ordinary-user traffic blocked until the next protected connection recovers the dead owner's lease; this fail-safe behavior must be included in support and crash testing. An external privileged application changing PF or its states after connection is outside this patch's guarantee.

Packet filtering does not hide addresses that browser JavaScript already learns through local ICE `host` candidates, including private/link-local or native public IPv6 addresses. Public host candidates require particular attention in browser acceptance and must not be called harmless merely because they are not `srflx` candidates. This patch does not modify browser privacy policies or interface enumeration. A stronger complete-product guarantee still requires browser compatibility testing and an independently reviewed Network Extension/privileged-helper design.

The existing node credentials and traffic reports remain part of the current product design. This patch does not make client-reported accounting authoritative, prevent a device owner from inspecting locally usable credentials, or constitute a full commercial-security audit.
