# mautrix-wechat File Inventory

Total tracked source/config/ops/test/doc files reviewed in this inventory: 102

## Entry, Build, Ops, and Docs
- `cmd/mautrix-wechat/main.go`
- `cmd/mautrix-wechat/main_test.go`
- `Dockerfile`
- `docker-compose.yml`
- `README.md`
- `LICENSE`
- `go.mod`
- `go.sum`
- `scripts/setup.sh`
- `docs/MAUTRIX_WECHAT_FILE_INVENTORY_2026-03-16.md`
- `docs/MAUTRIX_WECHAT_FULL_REVIEW_PLAN_2026-03-16.md`
- `configs/alerts.yml`
- `configs/prometheus.yml`
- `grafana/dashboards/mautrix-wechat.json`
- `grafana/provisioning/dashboards/provider.yml`

## Core Bridge
- `internal/bridge/as_handler.go`
- `internal/bridge/as_handler_test.go`
- `internal/bridge/bridge.go`
- `internal/bridge/encryption.go`
- `internal/bridge/encryption_test.go`
- `internal/bridge/event_router.go`
- `internal/bridge/event_router_test.go`
- `internal/bridge/event_router_multitenant_test.go`
- `internal/bridge/failover.go`
- `internal/bridge/failover_test.go`
- `internal/bridge/loadbalancer.go`
- `internal/bridge/loadbalancer_test.go`
- `internal/bridge/metrics.go`
- `internal/bridge/metrics_test.go`
- `internal/bridge/node_pool.go`
- `internal/bridge/node_pool_test.go`
- `internal/bridge/processor.go`
- `internal/bridge/processor_test.go`
- `internal/bridge/puppet.go`
- `internal/bridge/puppet_test.go`
- `internal/bridge/session_manager.go`
- `internal/bridge/session_manager_test.go`

## Configuration
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/config/config_multitenant_test.go`

## Database and Migrations
- `internal/database/bridge_user_store.go`
- `internal/database/bridge_user_store_test.go`
- `internal/database/database.go`
- `internal/database/message_store.go`
- `internal/database/message_store_test.go`
- `internal/database/migrations/0001_initial_schema.sql`
- `internal/database/migrations/0002_multi_tenant.sql`
- `internal/database/node_store.go`
- `internal/database/node_store_test.go`
- `internal/database/other_stores.go`
- `internal/database/other_stores_test.go`
- `internal/database/room_store.go`
- `internal/database/room_store_test.go`
- `internal/database/user_store.go`
- `internal/database/user_store_test.go`

## Message Conversion
- `internal/message/mention.go`
- `internal/message/mention_test.go`
- `internal/message/processor.go`
- `internal/message/processor_test.go`

## Provider: PadPro
- `internal/provider/padpro/callback.go`
- `internal/provider/padpro/callback_test.go`
- `internal/provider/padpro/channels.go`
- `internal/provider/padpro/client.go`
- `internal/provider/padpro/client_test.go`
- `internal/provider/padpro/convert.go`
- `internal/provider/padpro/convert_test.go`
- `internal/provider/padpro/moments.go`
- `internal/provider/padpro/provider.go`
- `internal/provider/padpro/provider_test.go`
- `internal/provider/padpro/riskcontrol.go`
- `internal/provider/padpro/types.go`
- `internal/provider/padpro/websocket.go`
- `internal/provider/padpro/websocket_test.go`

## Provider: iPad
- `internal/provider/ipad/callback.go`
- `internal/provider/ipad/callback_test.go`
- `internal/provider/ipad/provider.go`
- `internal/provider/ipad/provider_test.go`
- `internal/provider/ipad/reconnect.go`
- `internal/provider/ipad/reconnect_test.go`
- `internal/provider/ipad/riskcontrol.go`
- `internal/provider/ipad/riskcontrol_test.go`
- `internal/provider/ipad/voiceconv.go`
- `internal/provider/ipad/voiceconv_test.go`

## Provider: PC Hook
- `internal/provider/pchook/integration_test.go`
- `internal/provider/pchook/message.go`
- `internal/provider/pchook/provider.go`
- `internal/provider/pchook/provider_test.go`
- `internal/provider/pchook/rpcclient.go`

## Provider: WeCom
- `internal/provider/wecom/callback.go`
- `internal/provider/wecom/callback_test.go`
- `internal/provider/wecom/client.go`
- `internal/provider/wecom/client_test.go`
- `internal/provider/wecom/contact.go`
- `internal/provider/wecom/crypto.go`
- `internal/provider/wecom/crypto_test.go`
- `internal/provider/wecom/message.go`
- `internal/provider/wecom/provider.go`
- `internal/provider/wecom/provider_test.go`

## Shared WeChat Package
- `pkg/wechat/provider.go`
- `pkg/wechat/registry.go`
- `pkg/wechat/registry_test.go`
- `pkg/wechat/types.go`

## Recommended Review Order
1. `internal/config`
2. `internal/database/migrations`
3. `internal/database/node_store.go`
4. `internal/bridge/node_pool.go`
5. `internal/bridge/session_manager.go`
6. `internal/bridge/event_router.go`
7. `internal/bridge/bridge.go`
8. Remaining `internal/database`
9. `internal/provider/padpro`
10. Remaining providers
11. `internal/message`
12. Entry/docs/tooling
