# mautrix-wechat Full Review Plan

## Scope
- Repository: `mautrix-wechat`
- Goal: complete code review, safe dependency upgrades, feature completion, and test completion
- Current special context: there is an existing uncommitted multi-tenant PadPro change set. Review and fixes must preserve that work and audit it first.

## Baseline
- Repo state at review start: dirty worktree with multi-tenant changes in `cmd/mautrix-wechat`, `internal/bridge`, `internal/config`, and `internal/database`
- Direct dependencies before upgrade:
  - `github.com/gorilla/websocket v1.5.3`
  - `github.com/lib/pq v1.11.1`
  - `gopkg.in/yaml.v3 v3.0.1`
- Tooling baseline:
  - `go test ./...` passes
  - `go test -race ./...` passes
  - `go vet ./...` passes
- Coverage snapshot before fixes:
  - `internal/bridge` 46.9%
  - `internal/config` 98.4%
  - `internal/message` 90.6%
  - `internal/provider/ipad` 40.1%
  - `internal/provider/pchook` 21.9%
  - `internal/provider/wecom` 26.0%
  - `pkg/wechat` 52.2%
  - `internal/database` no tests
  - `internal/provider/padpro` no tests

## Review Strategy
1. Lock down high-risk multi-tenant correctness first.
2. Close missing state and persistence flows before adding surface features.
3. Fill test gaps for every bug fix and for low-coverage core packages.
4. Upgrade only low-risk dependencies in this pass.
5. Keep fallback behavior explicit; do not silently route cross-user traffic.

## Phase Plan

### Phase 0: Inventory and Baseline
- [x] Record full file inventory by module in `docs/MAUTRIX_WECHAT_FILE_INVENTORY_2026-03-16.md`
- [x] Record baseline test, race, vet, and dependency status
- [x] Refresh inventory after all code changes

### Phase 1: Multi-Tenant Safety and Session Integrity
- Audit `NodePool`, `SessionManager`, `EventRouter`, config validation, and DB migration
- [x] Fix cross-tenant fallback behavior when bridge-user context is missing
- [x] Fix login-state persistence so restart restore can work
- [x] Handle stale or incomplete node assignments after restart
- [x] Add regression tests for:
  - context-required multi-tenant routing
  - login event persistence behavior
  - stale assignment cleanup
  - node/session recovery edge cases

### Phase 2: Database and Bridge Core
- Audit all stores under `internal/database`
- [x] Add first tests for `node_store.go`
- [x] Review `bridge.go` startup lifecycle around multi-tenant recovery
- [x] Fix nil-store and startup cleanup correctness issues found in `bridge`/`node_pool`/`session_manager`
- [x] Audit entry logging path and make `logging.min_level` effective
- [x] Fix `PuppetManager` nil-matrix-client crash path for first-contact creation
- [x] Harden `ASHandler` against uninitialized router/puppet manager state
- [x] Raise `internal/database` coverage with store-level `sqlmock` tests
- [x] Verify remaining room/message/bridge user stores against full bridge flows
- [x] Review `as_handler.go`, `puppet.go`, metrics, encryption, failover, and load balancer for secondary issues

### Phase 3: Provider Surfaces
- Review all provider packages in risk order:
  1. `internal/provider/padpro`
  2. `internal/provider/pchook`
  3. `internal/provider/ipad`
  4. `internal/provider/wecom`
- Focus on auth state, reconnect, callback parsing, media download/upload, and timeout behavior
- [x] Add first tests for `internal/provider/padpro`
- [x] Harden PadPro webhook/websocket handlers for nil handler safety
- [x] Remove PadPro websocket auth key leakage from connection logs
- [x] Raise provider coverage for `padpro` thin client wrappers and `pchook` lifecycle/RPC paths
- [x] Raise provider coverage for `wecom` lifecycle, contacts, groups, messages, and media flows
- [x] Review remaining provider packages for secondary lifecycle issues

### Phase 4: Message and Matrix Pipeline
- Review `internal/message` conversion logic and `internal/bridge/processor.go`
- Audit reply, mention, revoke, media, and encryption paths
- Verify room creation and puppet/profile sync behavior
- [x] Add regression tests where bridge/database/provider fixes touch message flow
- [x] Fix nil message-store and nil matrix-client crash paths in reply/revoke/media conversion flows
- [x] Review remaining room creation and media-send constraints

### Phase 5: Dependencies and Tooling
- [x] Upgrade safe dependencies
- [x] Run `go mod tidy`
- [x] Re-run `go list -m -u all`
- [x] Verify no new warnings or unexpected transitive churn

### Phase 6: Final Verification
- [x] Run:
  - `go test ./...`
  - `go test -race ./...`
  - `go test -cover ./...`
  - `go vet ./...`
- [x] Summarize fixed issues, residual risks, and next candidates for deeper provider work

## Initial Findings Queue
- High: multi-tenant `findBridgeUser()` falls back to the first logged-in bridge user when context is missing, which can misroute incoming events across users.
- High: `OnLoginEvent()` currently only logs and does not persist bridge user or assignment login state, so restart restore is incomplete.
- Medium: startup counts all stored node assignments, including sessions that are not restorable, which can leak capacity after restart.
- Medium: `internal/database` has no tests, including the new multi-tenant store and migration.
- Medium: `internal/provider/padpro` has no tests despite now being the core path for multi-tenant mode.
- Medium: `pchook.Provider.Start()` can deadlock because it calls `checkLoginStatus()` while holding `p.mu`.
- Medium: `wecom.Provider.Start()` can deadlock because it calls `fetchSelfInfo()` while holding `p.mu`.

## Execution Log
- 2026-03-16 11: Review started from the existing multi-tenant delta already present in the worktree.
- 2026-03-16 11: Baseline checks completed: `go test ./...`, `go test -race ./...`, and `go vet ./...` passed.
- 2026-03-16 11: Dependency scan completed: only low-risk direct patch upgrade available is `github.com/lib/pq v1.11.1 -> v1.11.2`.
- 2026-03-16 11: Phase 1 in progress.
- 2026-03-16 14: Fixed multi-tenant context routing so missing bridge-user context no longer falls back to another logged-in user.
- 2026-03-16 14: Implemented login-event persistence to `bridge_user` and `node_assignment`, and kept in-memory session state aligned.
- 2026-03-16 14: Added startup cleanup for non-restorable node assignments to prevent leaked node capacity after restart.
- 2026-03-16 14: Added database and PadPro tests: `internal/database/node_store_test.go`, `internal/provider/padpro/provider_test.go`.
- 2026-03-16 14: Fixed bootstrap logging so `logging.min_level` applies after config load and propagates via `slog.SetDefault`.
- 2026-03-16 14: Safe dependency upgrades applied: `github.com/lib/pq v1.11.2`, `github.com/DATA-DOG/go-sqlmock v1.5.2`.
- 2026-03-16 15: Fixed `PuppetManager` nil `intent` crash path and added regression coverage in `internal/bridge/puppet_test.go`.
- 2026-03-16 15: Hardened `ASHandler` to return `503` instead of panicking when router or puppet manager is not initialized.
- 2026-03-16 15: Verification completed: `go test ./...`, `go test -race ./...`, `go test -cover ./...`, and `go vet ./...` all passed.
- 2026-03-16 16: Fixed `SessionManager` node-capacity leak on provider init/start failure and added rollback coverage.
- 2026-03-16 16: Hardened `EventRouter` reply/revoke/backfill paths against missing message-store dependencies.
- 2026-03-16 16: Hardened `internal/message` media upload conversion against nil Matrix client.
- 2026-03-16 16: Added PadPro callback/websocket tests and removed websocket auth key leakage from logs.
- 2026-03-16 17: Added `sqlmock` coverage for bridge-user, user, room, message, and other database stores; `internal/database` targeted coverage increased to 53.8%.
- 2026-03-16 17: Added PadPro client/convert tests; `internal/provider/padpro` targeted coverage increased to 28.4%.
- 2026-03-16 17: Added PC Hook integration tests, fixed `Provider.Start()` deadlock by avoiding recursive `p.mu` locking, and increased `internal/provider/pchook` targeted coverage to 78.3%.
- 2026-03-16 18: Added WeCom provider lifecycle/contact/group/message/media tests, fixed `Provider.Start()` deadlock by avoiding recursive `p.mu` locking, and increased `internal/provider/wecom` targeted coverage to 72.5%.
- 2026-03-16 18: Fixed WeChat revoke lookup to use the latest stored mapping when Matrix room ID is unavailable, so revoke callbacks can redact the correct Matrix event.
- 2026-03-16 18: Fixed `ProviderManager.Stop()` to clear active provider state and mark metrics disconnected/logged out, preventing stale active-provider reporting after shutdown.
- 2026-03-16 18: Fixed `ProviderBalancer` revoke routing to remember the original sending provider, so revoke requests follow the same provider even after later pool rotation.
- 2026-03-16 18: Fixed `ipad` and `padpro` restart behavior by recreating closed stop channels before `Start()`, restoring login polling and WebSocket loops after restart.
- 2026-03-16 19: Hardened `ipad`, `padpro`, and `pchook` nil-handler lifecycle edges, including callback 503 behavior and send-only startup without inbound sync.
- 2026-03-16 19: Changed Matrix -> WeChat unsupported media sends from silent success to explicit errors, preventing invisible image/file drops during bridge send attempts.
- 2026-03-16 19: Final verification completed again after all fixes: `go test ./...`, `go test -cover ./...`, `go test -race ./...`, and `go vet ./...` all passed.
- 2026-03-16 20: Added `MatrixClient.DownloadMedia()` and implemented Matrix -> WeChat image/file/video/voice forwarding end-to-end, including thumbnail and duration handling where Matrix metadata is available.
- 2026-03-16 20: Added regression coverage for downloaded MXC media, provider send payloads, video thumbnails, and voice duration normalization.
- 2026-03-16 20: Verification completed again after full media forwarding support: `go test ./...`, `go test -cover ./...`, `go test -race ./...`, and `go vet ./...` all passed.
- 2026-03-16 21: Fixed `padpro` video/file send paths to actually include encoded media payloads; previously both methods ignored the provided bytes and could report success without sending real content.
- 2026-03-16 21: Fixed `ipad` video send to surface thumbnail read failures instead of silently dropping the thumbnail, and added send-path regression coverage for image/file/voice payloads.
- 2026-03-16 21: Fixed `ipad` reconnector restart/race behavior by binding heartbeat/reconnect goroutines to the start-time stop channel, so provider restarts no longer race on `stopCh`.
- 2026-03-16 21: Fixed `ipad` media download handling to reject non-2xx HTTP responses and fall back to `application/octet-stream` when the server omits `Content-Type`.
- 2026-03-16 21: Fixed `padpro` WebSocket/login goroutines and `ipad` login polling to bind to the start-time stop channel, avoiding restart-time races on shared `stopCh` state.
- 2026-03-16 22: Fixed `ipad` and `padpro` avatar downloads to reject non-2xx HTTP responses instead of accepting error pages as image payloads.
- 2026-03-16 22: Taught `padpro.DownloadMedia()` to reuse embedded `MediaData` before attempting a CDN fetch, reducing failures when upstream media URLs are missing or expired.
- 2026-03-16 22: Fixed `wecom` avatar downloads to reject non-2xx HTTP responses too, so avatar fetch semantics now match `ipad` and `padpro`.
- 2026-03-16 22: Completed `pchook` location parsing by extracting `x/y` coordinates and attribute-form `label/poiname`, then added reply-path regression tests for link and location XML parsing.
- 2026-03-16 22: Covered `pchook` logout and group-info lifecycle paths in the RPC-backed integration suite, raising `internal/provider/pchook` coverage to `81.4%`.
- 2026-03-16 22: Re-ran `go test ./...`, `go test -cover ./...`, `go test -race ./...`, and `go vet ./...` successfully after the provider/media hardening pass.

## Final Coverage Snapshot
- `internal/bridge` 53.2%
- `internal/config` 98.4%
- `internal/database` 54.0%
- `internal/message` 91.2%
- `internal/provider/ipad` 53.3%
- `internal/provider/padpro` 41.0%
- `internal/provider/pchook` 81.4%
- `internal/provider/wecom` 72.5%
- `pkg/wechat` 52.2%

## Residual Risks
- Matrix -> WeChat core media forwarding is now implemented for image/file/video/voice, but provider-specific transcoding, richer thumbnail formats, and non-standard Matrix media metadata still need deeper compatibility work.
- End-to-end encryption remains a partial framework only. Room tracking and key-store plumbing exist, but Megolm encrypt/decrypt still needs a real libolm-backed implementation.
- `GetLatestByWeChatMsgID()` is the safest available fallback for revoke callbacks, but it still assumes WeChat message IDs are effectively unique enough across rooms to use the most recent mapping.
- `internal/provider/padpro` and `internal/provider/ipad` improved materially but still have lower coverage than the bridge and message core because their remaining branches depend on harder-to-simulate external protocol behavior.

## Next Candidates
- Harden remaining provider-specific media compatibility around transcoding, richer thumbnail formats, and fallback behavior when Matrix metadata is incomplete.
- Raise `padpro` and the deeper `ipad` reconnect/voice-conversion paths further; they still lag the bridge core because the remaining branches are integration-heavy.
- Replace the placeholder encryption helper with a real Olm/Megolm backend when encrypted-room support becomes a product requirement.
