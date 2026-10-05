# Development acceptance: administrator catalog, macOS and weighted ledger

Executed on the developer's macOS host on 2026-10-05 UTC. This records observed local results; it is not a production readiness certificate or a statement about future GitHub CI runs.

## Observed passing checks

| Check | Result and scope |
| --- | --- |
| Backend | `scripts/test-native.py` passed using a private PostgreSQL cluster, including Go race tests, authorization, idempotency, quota/expiry, catalog versions, administrator audits and a database-verified read-only readiness transaction. Fixture-only tests also run through their corresponding drivers. |
| Next.js | Lint, webpack production build and TypeScript checks passed. The production same-origin API proxy suite passed 12 tests with an isolated Chrome profile, including login/cookies, CSRF, routing, timeouts, large profile responses and bounded chunked upstream bodies. |
| Catalog chain | Real browser administrator imports/edits nodes; non-admin receives HTTP 403. Actual Gin/PostgreSQL catalog is consumed by managed Core, real Mihomo TCP traffic is measured and weighted segments settle correctly. |
| Flutter | Full suite: 1,950 tests passed. Generated-code-excluded coverage: 18,142 / 22,898 lines, 79.23%; all existing per-group floors passed. |
| Core | CGO-disabled full wrapper tests and vet passed; managed package race tests passed without whole-Core coverage instrumentation. |
| Shadowsocks | Six real loopback encrypted TCP tests passed: correct and incorrect password cases for AES-128-GCM, AES-256-GCM and ChaCha20-IETF-Poly1305. Correct credentials round-trip the actual payload; incorrect ones cannot reach the owned destination. |
| Proxy ownership | Standalone proxy package: 38 tests passed, including durable ownership journals, competing changes, recovery, symlink rejection and OS-lock release after fixture SIGKILL. Analyzer passed. |
| Build hooks | Standalone setup-hooks package: 47 tests passed; analyzer passed. This includes a filtered native-hook environment, explicit client `.env`, endpoint validation and cache-input changes. |
| Real macOS application | Bundled, first-run and reopen phases passed in the actual Flutter/Rust IPC/Go application with isolated Gin/PostgreSQL. Each phase confirmed normal App and Core exit. Actual cumulative traffic, a minute report, final settlement and refused/expired entitlements were exercised. |
| Keychain | Real macOS native Keychain writes and readback passed. Remembered credentials survive normal App exit/reopen but do not authorize or auto-connect; explicit logout removes the saved entry. A random fixture API scope isolates this from the developer's ordinary saved login. |
| Normal macOS package | Clean `lib/main.dart` debug build passed. Final Core build metadata confirmed `http://127.0.0.1:8080/api/v1/client` was embedded from client `.env` with the shell variable absent, and no `managed_acceptance` tag was present. The whole App passed strict nested signature verification. This is a local development package, not a notarized release. |
| Migration/restore | A separate private SCRAM PostgreSQL fixture passed old web-schema upgrade, repeated migration preservation, real browser flows, concurrent shared-balance reports, full custom-format backup/restore of all ten tables, baseline-binary rollback without down-migration and forward restoration. |

## Concrete metering evidence

The catalog fixture observed 1,785 upload bytes and 1,755 download bytes on its first segment: 3,540 actual bytes at 500 permille created 1,770,000 ledger units, equivalent to 1,770 bytes of quota. The next segment transferred another 3,540 bytes at 1000 permille. Totals became 7,080 actual bytes and 5,310,000 ledger units, equivalent to 5,310 bytes of quota.

The evidence is explicitly `metering_source: client_reported`, `node_authoritative: false`. These counters exclude neither direction and do not substitute display/reset counters for the monotonic billing stream. Local controlled proxy endpoints were used; no public commercial node was declared tested by this evidence.

## Evidence retained locally

Evidence is intentionally excluded from Git because logs, browser traces and database snapshots may contain sensitive fixture or operational state. Relevant paths under the project:

- `artifacts/review-20261004/backend-postgres.log` and the corresponding JSON result.
- `artifacts/review-20261004/flutter-full-bounded.log` and the corresponding JSON result.
- `artifacts/review-20261004/catalog-final/summary.json` and `traffic-evidence.json`.
- `artifacts/review-20261004/macos-e2e/summary.json`, `result-first.json` and `result-reopen.json`.
- `artifacts/review-20261004/macos-dotenv-build.json` and its build log.
- `artifacts/review-20261004/release-rehearsal/summary.json`.

The artifact directory label is retained from task start; execution timestamps in the files are authoritative. Failed intermediate attempts are not discarded or counted as passes: the initial full Flutter attempt exhausted the host's default file-descriptor limit; the successful rerun used a process-local limit of 4096 and concurrency two. A first browser test attempt required selecting the locally installed Chrome. A proxy subpackage attempt lacked `dart` on PATH; the full rerun passed after configuring the SDK PATH. An incremental switch from the integration target to the normal App yielded a stale nested App.framework signature, so final packaging must use a clean normal build and strict signature verification, not mere compilation success.

## Development database preparation

The explicitly authorized development schema was backed up before additive migration. The first backup attempt correctly refused a PostgreSQL-16 client against a PostgreSQL-18 server and stopped before modifying the database. A compatible libpq client then completed the backup and preparation. A separate administrator was created transactionally with an audit entry; the existing customer account, password, role and subscription were not reset or promoted.

The post-migration read-only doctor found no missing tables, one active administrator, a valid node key, catalog/test access enabled and an eligible test customer. It found zero configured nodes, so `managed_development_ready` remained false. No sample public nodes were silently imported. Administrator credentials and pre-migration backups remain in the private ignored `.runtime` directory, not in this report or Git.

## Explicitly outside the verified scope

This work does not claim authoritative node-side metering, customer-bound proxy credential revocation, all-device macOS TUN, DNS/IPv6/UDP leak protection, live public-node availability/latency, production database TLS, real payment/refund processing, Developer ID signing/notarization, or native release acceptance on other platforms. The macOS end-to-end driver isolates the system-proxy command adapter instead of changing the developer's real network settings. A development ad-hoc signature, even when valid, is not a notarized distribution identity.

The configured development API was also started temporarily without auto-migration. The newly created administrator completed real login, authorized empty-catalog reading and logout; no customer credentials were required or changed. The temporary API process was stopped afterwards.

Production configuration rejects client-reported catalog/private-file proxy delivery. Removing that guard without deploying and verifying node ingress enforcement would reintroduce an unsafe commercial quota claim. See [node and metering boundaries](admin-node-metering.md) and the README for the remaining requirements and manual real-node setup.
