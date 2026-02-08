# mautrix-wechat

A Matrix-WeChat bridge that enables bidirectional messaging between [Matrix](https://matrix.org/) and [WeChat](https://www.wechat.com/). Built as a Matrix [Application Service](https://spec.matrix.org/latest/application-service-api/), it supports multiple WeChat access methods with automatic failover.

## Features

- **Multi-provider architecture** — four interchangeable WeChat access methods with tiered priority
- **Automatic failover** — health monitoring with seamless provider switching and recovery promotion
- **Rich message support** — text, image, voice, video, file, location, link cards, emoji, mini-app
- **Group bridging** — group chat sync, member management, @mentions, announcements
- **Contact sync** — friend list, avatars, remarks, friend request acceptance
- **Moments & Channels** — partial support for Moments (朋友圈) and Channels (视频号) via select providers
- **End-to-end encryption** — optional Matrix E2EE (Olm/Megolm) for encrypted rooms
- **Risk control** — anti-ban protection for Pad protocol (rate limiting, random delays, daily quotas)
- **Voice conversion** — automatic SILK-to-OGG transcoding for voice messages
- **Prometheus metrics** — request counts, latency histograms, connection state, error rates
- **Grafana dashboards** — pre-built dashboard for monitoring bridge health
- **Alerting** — Prometheus alert rules for disconnection, failure spikes, login errors, high latency
- **Docker deployment** — production-ready Compose stack with health checks and secret management

## Ecosystem Status

> Last updated: 2026-02

| Component | Status | Notes |
|-----------|--------|-------|
| **GeWeChat** | **Archived** (2025-05) | iPad protocol service, shut down due to WeChat legal enforcement. Do NOT use for new deployments. |
| **WeChatPadPro** | **Active** | Successor to GeWeChat, Pad protocol, REST API + WebSocket, cross-platform Docker deployment. |
| **WeChatFerry** | **Active** (v39.5.2) | PC Hook via DLL injection, Windows only, supports WeChat 3.9.12.17. |
| **WeCom API** | **Stable** | Official enterprise API, most stable but enterprise-only. |
| **mautrix-go bridgev2** | **Standard** (v0.26) | All new mautrix bridges use bridgev2. Migration planned for this project. |
| **duo/matrix-wechat** | **Outdated** | Uses old mautrix-go, Agent requires WeChat 3.7.0.30 (no longer supported). |

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                     Matrix Homeserver                        │
│                    (Synapse / Conduit)                       │
└────────────────┬─────────────────────────────────────────────┘
                 │  Application Service API
                 │  (AS token / HS token)
┌────────────────▼─────────────────────────────────────────────┐
│                    mautrix-wechat Bridge                     │
│                                                              │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────────────┐│
│  │  AS Handler  │  │ Event Router │  │   Puppet Manager     ││
│  │  (HTTP API)  │──│  Matrix ↔ WC │──│  (ghost users)       ││
│  └─────────────┘  └──────┬───────┘  └──────────────────────┘│
│                          │                                   │
│  ┌───────────────────────▼───────────────────────────────────┐
│  │              Provider Manager (Failover)                  │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐ │
│  │  │  WeCom   │  │ PadPro   │  │iPad(dep.)│  │ PC Hook  │ │
│  │  │ (Tier 1) │  │ (Tier 2) │  │ (Tier 2) │  │ (Tier 3) │ │
│  │  └──────────┘  └──────────┘  └──────────┘  └──────────┘ │
│  └───────────────────────────────────────────────────────────┘
│                                                              │
│  ┌────────────┐  ┌────────────┐  ┌────────────────────────┐ │
│  │ Encryption │  │  Metrics   │  │     Database (PG)      │ │
│  │(Olm/Megolm)│  │(Prometheus)│  │ users, rooms, messages │ │
│  └────────────┘  └────────────┘  └────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

## Providers

| Provider | Tier | Protocol | Platform | Stability | Status | Use Case |
|----------|------|----------|----------|-----------|--------|----------|
| **WeCom** | 1 | Official API | Cloud | Highest | Stable | Enterprise messaging, Moments marketing |
| **PadPro** | 2 | WeChatPadPro (Pad) | Docker | High | **Recommended** | Personal WeChat bridging |
| **iPad** | 2 | GeWeChat (iPad) | Docker | High | **Deprecated** | Legacy; migrate to PadPro |
| **PC Hook** | 3 | WeChatFerry RPC | Windows | Medium | Active | Dev/testing, Channels access |

### WeCom (Tier 1) — Enterprise WeChat

Uses the [WeCom](https://work.weixin.qq.com/) official API. Most stable and legally compliant, but limited to enterprise scenarios.

**Capabilities unique to WeCom:**
- Customer Moments (客户朋友圈) — post marketing content to external contacts' Moments feed
- Customer Group management at scale
- Official message template push
- No risk of account ban (official API)

**Limitations:**
- Cannot access personal WeChat Moments or Channels
- Messages only reach WeCom users or linked external contacts
- No personal friend management

### PadPro (Tier 2) — WeChatPadPro (Recommended)

Uses [WeChatPadPro](https://github.com/WeChatPadPro/WeChatPadPro), the community successor to the archived GeWeChat project. Based on Pad protocol with REST API + WebSocket.

**Key features:**
- Cross-platform Docker deployment (Linux, macOS, Windows)
- Multi-account management
- REST API + WebSocket for real-time events
- Web management interface
- Built-in risk control inherited from bridge config

### iPad Protocol (Tier 2) — GeWeChat (Deprecated)

> **WARNING**: GeWeChat was archived on 2025-05-03 due to WeChat legal enforcement against "unauthorized access to WeChat terminal user data". This provider is retained for existing deployments only. **All new deployments should use PadPro.**

### PC Hook (Tier 3) — WeChatFerry

Uses [WeChatFerry](https://github.com/wechatferry/wechatferry) (v39.5.2) via TCP JSON-RPC. Requires a Windows host with WeChat 3.9.12.17 desktop injected.

**Capabilities unique to PC Hook:**
- Channels (视频号) content capture — can intercept shared Channels video links and metadata
- Moments (朋友圈) read access — `getFriendsCircle` API for reading the Moments feed
- Database access — direct query of local WeChat SQLite databases
- Full desktop feature parity

**Limitations:**
- Windows only
- Requires specific WeChat version (3.9.12.17)
- DLL injection carries ban risk
- Not suitable for production without careful isolation

## Moments & Channels Support Matrix

### 朋友圈 (Moments) Support

| Feature | WeCom | PadPro | iPad (dep.) | PC Hook | Notes |
|---------|:-----:|:------:|:-----------:|:-------:|-------|
| Read personal Moments | - | Partial | Partial | **Yes** | PC Hook has `getFriendsCircle` API |
| Read friend Moments | - | Partial | Partial | **Yes** | Protocol-level XML parsing required |
| Post to Moments | - | Planned | - | - | High ban risk, not recommended for automation |
| Customer Moments (企业) | **Yes** | - | - | - | WeCom official API, enterprise only |
| Like/Comment | - | - | - | Partial | PC Hook can trigger UI actions |
| Moments notifications | - | Partial | Partial | **Yes** | Real-time via message callback |

**Feasibility analysis:**
- **WeCom**: The only officially supported way to interact with Moments. Limited to "Customer Moments" (客户朋友圈) — posting marketing content to external contacts. Cannot read personal friend Moments.
- **PC Hook (WeChatFerry)**: Most complete Moments access. The `getFriendsCircle` RPC call returns the Moments feed as parsed XML. Can read text, images, videos, and links. Writing (posting/liking) is possible via UI automation but carries high ban risk.
- **PadPro/iPad**: Protocol-level support for receiving Moments update notifications. Reading individual Moments entries requires additional API calls. Posting is technically possible but strongly discouraged due to anti-automation detection.

### 视频号 (Channels / Video Accounts) Support

| Feature | WeCom | PadPro | iPad (dep.) | PC Hook | Notes |
|---------|:-----:|:------:|:-----------:|:-------:|-------|
| Receive shared Channels links | **Yes** | **Yes** | **Yes** | **Yes** | Parsed as MsgType 49 (link) |
| Play/preview Channels video | - | - | - | Partial | Requires media URL extraction |
| Channels live stream notification | - | Partial | Partial | **Yes** | Via system message parsing |
| Forward Channels content | **Yes** | **Yes** | **Yes** | **Yes** | As link card message |
| Channels profile lookup | - | - | - | Partial | Via local DB query |
| Post to Channels | - | - | - | - | Not supported by any provider |
| Channels comments | - | - | - | - | Not accessible via current protocols |

**Feasibility analysis:**
- **Channels content is fundamentally a "walled garden"** — WeChat treats Channels as a separate subsystem with its own CDN and DRM. No current protocol provides direct Channels API access.
- **What works**: All providers can receive and forward shared Channels links (they appear as Type 49 link card messages with `weixin://` deep links). The bridge converts these to Matrix `m.text` events with the video URL.
- **What doesn't work**: Direct video playback, comment interaction, and posting to Channels require the native WeChat client. These operations cannot be bridged.
- **PC Hook advantage**: WeChatFerry can intercept the local Channels cache and extract video metadata/thumbnails via SQLite database access, providing richer preview information than other providers.

### Recommendation by Use Case

| Use Case | Recommended Provider | Reason |
|----------|---------------------|--------|
| Personal chat bridging | PadPro | Best balance of features and stability |
| Enterprise messaging | WeCom | Official API, zero ban risk |
| Moments monitoring | PC Hook | Only provider with full read access |
| Channels content relay | Any (link forwarding) | All providers forward shared links |
| Development/testing | PC Hook | Full desktop feature access |
| Production deployment | WeCom + PadPro (failover) | Enterprise stability + personal coverage |

## Quick Start

### Prerequisites

- Docker & Docker Compose
- A Matrix homeserver (Synapse, Conduit, etc.)
- At least one WeChat access method configured

### 1. Clone and Setup

```bash
git clone https://github.com/n42/mautrix-wechat.git
cd mautrix-wechat
bash scripts/setup.sh
```

The setup script will:
- Create `secrets/`, `logs/`, `configs/` directories
- Generate random tokens for database password, AS token, and HS token
- Generate an example `configs/config.yaml`
- Generate `configs/registration.yaml` for your homeserver

### 2. Configure

Edit `configs/config.yaml`:

```yaml
homeserver:
  address: https://your-homeserver.example.com
  domain: your-homeserver.example.com

appservice:
  address: http://mautrix-wechat:29350
  port: 29350

database:
  type: postgres
  uri: "postgres://mautrix_wechat:PASSWORD@postgres:5432/mautrix_wechat?sslmode=require"

bridge:
  permissions:
    "*": relay
    "your-homeserver.example.com": user
    "@admin:your-homeserver.example.com": admin

providers:
  # Recommended: WeChatPadPro
  padpro:
    enabled: true
    api_endpoint: "http://wechatpadpro:8849/api"
    ws_endpoint: "ws://wechatpadpro:8849/ws"
    callback_port: 29353

  # Alternative: WeChatFerry (Windows only)
  pchook:
    enabled: false
    rpc_endpoint: "tcp://windows-host:19088"
```

### 3. Register with Homeserver

Copy `configs/registration.yaml` to your Matrix homeserver and add it to the homeserver configuration:

**Synapse** (`homeserver.yaml`):
```yaml
app_service_config_files:
  - /path/to/mautrix-wechat/registration.yaml
```

### 4. Start

```bash
# Core services only (bridge + database + redis)
docker compose up -d

# With PadPro provider (recommended)
docker compose --profile padpro up -d

# With monitoring (Prometheus + Grafana)
docker compose --profile monitoring up -d

# All services
docker compose --profile padpro --profile monitoring up -d
```

### 5. Login

Send a message to `@wechatbot:your-homeserver.example.com` in Matrix:

```
login
```

The bot will reply with a QR code. Scan it with WeChat to complete login.

## Configuration Reference

### Homeserver

| Key | Type | Description |
|-----|------|-------------|
| `homeserver.address` | string | Matrix homeserver URL |
| `homeserver.domain` | string | Matrix homeserver domain |

### AppService

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `appservice.port` | int | `29350` | AS HTTP API port |
| `appservice.id` | string | `wechat` | AppService ID |
| `appservice.bot.username` | string | `wechatbot` | Bot user localpart |
| `appservice.as_token` | string | — | Application service token |
| `appservice.hs_token` | string | — | Homeserver token |

### Bridge

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `bridge.username_template` | string | `wechat_{{.}}` | Ghost user ID template |
| `bridge.displayname_template` | string | `{{.Nickname}} (WeChat)` | Ghost display name template |
| `bridge.message_handling.max_message_age` | int | `300` | Max message age in seconds |
| `bridge.message_handling.delivery_receipts` | bool | `true` | Send delivery receipts |
| `bridge.encryption.allow` | bool | `true` | Allow E2EE |
| `bridge.encryption.default` | bool | `false` | Enable E2EE by default |
| `bridge.rate_limit.messages_per_minute` | int | `30` | Outgoing message rate limit |
| `bridge.media.max_file_size` | int | `104857600` | Max file size (bytes, default 100MB) |
| `bridge.media.voice_converter` | string | `silk2ogg` | Voice format converter |

### Providers

#### PadPro (WeChatPadPro) — Recommended

| Key | Type | Description |
|-----|------|-------------|
| `providers.padpro.enabled` | bool | Enable PadPro provider |
| `providers.padpro.api_endpoint` | string | WeChatPadPro REST API URL |
| `providers.padpro.ws_endpoint` | string | WeChatPadPro WebSocket URL |
| `providers.padpro.callback_port` | int | Callback HTTP server port |
| `providers.padpro.risk_control.*` | — | Same risk control options as iPad provider |

#### WeCom

| Key | Type | Description |
|-----|------|-------------|
| `providers.wecom.enabled` | bool | Enable WeCom provider |
| `providers.wecom.corp_id` | string | WeCom Corp ID |
| `providers.wecom.app_secret` | string | WeCom App Secret |
| `providers.wecom.agent_id` | int | WeCom Agent ID |
| `providers.wecom.callback.token` | string | Callback verification token |
| `providers.wecom.callback.aes_key` | string | Callback AES encryption key |

#### iPad (GeWeChat) — Deprecated

| Key | Type | Description |
|-----|------|-------------|
| `providers.ipad.enabled` | bool | Enable iPad protocol (**deprecated**, use padpro) |
| `providers.ipad.api_endpoint` | string | GeWeChat HTTP API URL |
| `providers.ipad.api_token` | string | GeWeChat API token |
| `providers.ipad.callback_url` | string | Callback URL for receiving messages |
| `providers.ipad.callback_port` | int | Callback HTTP server port |
| `providers.ipad.risk_control.max_messages_per_day` | int | Daily message quota (default 500) |
| `providers.ipad.risk_control.message_interval_ms` | int | Min interval between messages (default 1000) |
| `providers.ipad.risk_control.random_delay` | bool | Add random delay to intervals |

#### PC Hook

| Key | Type | Description |
|-----|------|-------------|
| `providers.pchook.enabled` | bool | Enable PC Hook provider |
| `providers.pchook.rpc_endpoint` | string | WeChatFerry RPC endpoint (e.g. `tcp://host:19088`) |
| `providers.pchook.wechat_version` | string | Target WeChat version (default `3.9.12.17`) |

#### Failover

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `providers.failover.enabled` | bool | `false` | Enable automatic failover |
| `providers.failover.health_check_interval_s` | int | `30` | Health check interval (seconds) |
| `providers.failover.failure_threshold` | int | `3` | Consecutive failures before failover |
| `providers.failover.recovery_check_interval_s` | int | `120` | Recovery probe interval (seconds) |
| `providers.failover.recovery_threshold` | int | `3` | Consecutive successes before promotion |

### Metrics & Logging

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `metrics.enabled` | bool | `true` | Expose Prometheus metrics |
| `metrics.listen` | string | `0.0.0.0:9110` | Metrics HTTP listen address |
| `logging.min_level` | string | `info` | Minimum log level |

## Monitoring

### Endpoints

| Port | Path | Description |
|------|------|-------------|
| `29350` | `/transactions/*` | Matrix AS API |
| `9110` | `/metrics` | Prometheus metrics |
| `9110` | `/health` | JSON health check |

### Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `mautrix_wechat_connected` | Gauge | Bridge connection state (0/1) |
| `mautrix_wechat_login_state` | Gauge | Current login state |
| `mautrix_wechat_messages_sent_total` | Counter | Messages sent to WeChat |
| `mautrix_wechat_messages_received_total` | Counter | Messages received from WeChat |
| `mautrix_wechat_messages_failed_total` | Counter | Failed message operations |
| `mautrix_wechat_wechat_to_matrix_latency_seconds` | Histogram | WeChat-to-Matrix bridging latency |
| `mautrix_wechat_matrix_to_wechat_latency_seconds` | Histogram | Matrix-to-WeChat bridging latency |
| `mautrix_wechat_reconnect_attempts_total` | Counter | Reconnection attempts |
| `mautrix_wechat_provider_errors_total` | Counter | Provider-level errors |
| `mautrix_wechat_risk_control_blocked_total` | Counter | Messages blocked by risk control |

### Alert Rules

Pre-configured alerts in `configs/alerts.yml`:

- **WeChatBridgeDisconnected** (critical) — bridge offline > 2 minutes
- **WeChatBridgeHighFailureRate** (warning) — >10% message failure rate
- **WeChatBridgeReconnectStorm** (warning) — >5 reconnections in 10 minutes
- **WeChatBridgeProviderErrors** (warning) — >10 provider errors in 5 minutes
- **WeChatBridgeHighLatency** (warning) — P95 latency > 2 seconds
- **WeChatBridgeLoginFailed** (critical) — login error state
- **WeChatBridgeDown** (critical) — process unresponsive

### Grafana

Access pre-built dashboards at `http://localhost:3000` (default password: `admin`).

## Project Structure

```
mautrix-wechat/
├── cmd/mautrix-wechat/       # Entry point, CLI flags, example config
├── internal/
│   ├── bridge/                # Core bridge logic
│   │   ├── bridge.go          # Main orchestrator
│   │   ├── event_router.go    # Matrix <-> WeChat event dispatch
│   │   ├── as_handler.go      # Matrix Application Service HTTP handler
│   │   ├── puppet.go          # Ghost user management
│   │   ├── encryption.go      # E2EE (Olm/Megolm) helper
│   │   ├── failover.go        # Provider health monitoring & failover
│   │   ├── loadbalancer.go    # Request load balancing
│   │   ├── metrics.go         # Prometheus metrics
│   │   └── processor.go       # Message format conversion
│   ├── config/                # YAML configuration loading & validation
│   ├── database/              # PostgreSQL stores (users, rooms, messages, etc.)
│   │   └── migrations/        # SQL migration files
│   ├── message/               # Message processing (mentions, formatting)
│   └── provider/
│       ├── padpro/            # WeChatPadPro provider (recommended)
│       │   ├── provider.go    # Provider implementation
│       │   ├── client.go      # REST API client
│       │   ├── websocket.go   # WebSocket event stream
│       │   └── callback.go    # Message callback handler
│       ├── ipad/              # iPad protocol / GeWeChat (deprecated)
│       │   ├── provider.go    # Provider implementation
│       │   ├── callback.go    # Webhook callback handler
│       │   ├── reconnect.go   # Auto-reconnection with backoff
│       │   ├── riskcontrol.go # Anti-ban rate limiting
│       │   └── voiceconv.go   # SILK voice transcoding
│       ├── pchook/            # PC Hook / WeChatFerry
│       │   ├── provider.go    # Provider implementation
│       │   ├── rpcclient.go   # TCP JSON-RPC client
│       │   └── message.go     # Message serialization
│       └── wecom/             # WeCom (Enterprise WeChat)
│           ├── provider.go    # Provider implementation
│           ├── client.go      # API client with token management
│           ├── callback.go    # Webhook callback server
│           ├── contact.go     # Contact & group management
│           ├── crypto.go      # AES-CBC callback verification
│           └── message.go     # Message sending
├── pkg/wechat/                # Public provider interface & types
│   ├── provider.go            # Provider interface definition
│   ├── types.go               # Message, Contact, Group types
│   └── registry.go            # Provider factory registry
├── configs/                   # Configuration files
│   ├── prometheus.yml         # Prometheus scrape config
│   └── alerts.yml             # Alert rules
├── grafana/                   # Grafana dashboard & provisioning
├── scripts/setup.sh           # Initial setup script
├── Dockerfile                 # Multi-stage build
└── docker-compose.yml         # Production deployment stack
```

## Development

### Build from Source

```bash
# Requires Go 1.22+
go build -o mautrix-wechat ./cmd/mautrix-wechat
```

### Run Tests

```bash
go test ./...
```

### Generate Example Config

```bash
./mautrix-wechat -generate-config > config.yaml
```

### Generate Registration

```bash
./mautrix-wechat -config config.yaml -generate-registration > registration.yaml
```

## Supported Message Types

| WeChat Type | Matrix Event | Direction |
|-------------|-------------|-----------|
| Text | `m.text` | Both |
| Image | `m.image` | Both |
| Voice | `m.audio` | Both |
| Video | `m.video` | Both |
| File | `m.file` | Both |
| Location | `m.location` | WeChat -> Matrix |
| Link/Article | `m.text` (with URL) | WeChat -> Matrix |
| Emoji/Sticker | `m.image` | Both |
| Contact Card | `m.text` (formatted) | WeChat -> Matrix |
| Mini Program | `m.text` (with URL) | WeChat -> Matrix |
| Channels video (shared) | `m.text` (with URL) | WeChat -> Matrix |
| Moments notification | `m.notice` | WeChat -> Matrix (PC Hook only) |
| Revoke | `m.room.redaction` | Both |
| System | `m.notice` | WeChat -> Matrix |

## Anti-Ban Best Practices

WeChat's anti-automation enforcement has intensified significantly (2024 Q2: +37% daily bans). Follow these guidelines:

1. **New accounts**: Enable `risk_control.new_account_silence_days: 7` (minimum 3 days)
2. **Message pacing**: Keep `message_interval_ms >= 1000` with `random_delay: true`
3. **Daily quotas**: Do not exceed `max_messages_per_day: 500` for automated accounts
4. **Device fingerprint**: Use dedicated IP addresses; avoid VPN/proxy switching
5. **Login pattern**: Do not repeatedly scan QR codes; use session persistence
6. **Multi-device**: Follow "1 primary + 3 backup" device strategy for critical accounts
7. **Protocol choice**: PadPro > PC Hook for ban resistance (Pad protocol is less detectable than DLL injection)

## Roadmap

- [ ] **bridgev2 migration** — Adopt mautrix-go bridgev2 framework (NetworkConnector/NetworkAPI pattern)
- [ ] **WeChatPadPro provider** — Full implementation with WebSocket real-time events
- [ ] **Moments bridge** — Read-only Moments feed bridging (PC Hook first, PadPro planned)
- [ ] **Channels preview** — Rich preview for shared Channels videos (thumbnail + metadata)
- [ ] **Double puppeting** — Matrix user impersonation for cleaner message display
- [ ] **Backfill** — Historical message sync on first bridge connection
- [ ] **MSC4190** — Appservice device management for improved E2EE
- [ ] **Remove GeWeChat dependency** — Complete migration off archived iPad provider

## Security

- Docker container runs as non-root user (UID 1337)
- Read-only root filesystem with tmpfs for temporary files
- Secrets managed via Docker secrets (not environment variables)
- `no-new-privileges` security option enabled
- TLS required for database connections (`sslmode=require`)
- AS/HS token authentication for Matrix API
- AES-CBC encryption for WeCom callback verification

## License

See [LICENSE](LICENSE) for details.
