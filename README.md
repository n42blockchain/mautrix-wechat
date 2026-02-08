# mautrix-wechat

A Matrix-WeChat bridge that enables bidirectional messaging between [Matrix](https://matrix.org/) and [WeChat](https://www.wechat.com/). Built as a Matrix [Application Service](https://spec.matrix.org/latest/application-service-api/), it supports multiple WeChat access methods with automatic failover.

## Features

- **Multi-provider architecture** — three interchangeable WeChat access methods with tiered priority
- **Automatic failover** — health monitoring with seamless provider switching and recovery promotion
- **Rich message support** — text, image, voice, video, file, location, link cards, emoji, mini-app
- **Group bridging** — group chat sync, member management, @mentions, announcements
- **Contact sync** — friend list, avatars, remarks, friend request acceptance
- **End-to-end encryption** — optional Matrix E2EE (Olm/Megolm) for encrypted rooms
- **Risk control** — anti-ban protection for iPad protocol (rate limiting, random delays, daily quotas)
- **Voice conversion** — automatic SILK-to-OGG transcoding for voice messages
- **Prometheus metrics** — request counts, latency histograms, connection state, error rates
- **Grafana dashboards** — pre-built dashboard for monitoring bridge health
- **Alerting** — Prometheus alert rules for disconnection, failure spikes, login errors, high latency
- **Docker deployment** — production-ready Compose stack with health checks and secret management

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
│  ┌───────────────────────▼───────────────────────────────┐  │
│  │              Provider Manager (Failover)              │  │
│  │  ┌──────────┐   ┌──────────┐   ┌──────────┐          │  │
│  │  │  WeCom   │   │   iPad   │   │  PC Hook │          │  │
│  │  │ (Tier 1) │   │ (Tier 2) │   │ (Tier 3) │          │  │
│  │  └──────────┘   └──────────┘   └──────────┘          │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                              │
│  ┌────────────┐  ┌────────────┐  ┌────────────────────────┐ │
│  │ Encryption │  │  Metrics   │  │     Database (PG)      │ │
│  │ (Olm/Megolm) │  (Prometheus) │  │ users, rooms, messages │ │
│  └────────────┘  └────────────┘  └────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

## Providers

| Provider | Tier | Protocol | Platform | Stability | Use Case |
|----------|------|----------|----------|-----------|----------|
| **WeCom** | 1 | Official API | Cloud | Highest | Enterprise/internal messaging |
| **iPad** | 2 | GeWeChat (iPad protocol) | Docker | High | Personal WeChat (recommended) |
| **PC Hook** | 3 | WeChatFerry RPC | Windows | Medium | Development/testing |

### WeCom (Tier 1)

Uses the [WeCom (Enterprise WeChat)](https://work.weixin.qq.com/) official API. Most stable but limited to enterprise scenarios. Requires a WeCom corp account with an application configured for callback events.

### iPad Protocol (Tier 2)

Uses the [GeWeChat](https://gewechat.com/) iPad protocol service. Recommended for personal WeChat bridging. Includes built-in risk control:

- New account silence period (configurable days)
- Daily message/group/friend request quotas
- Minimum message interval with optional random delay
- Automatic reconnection with exponential backoff

### PC Hook (Tier 3)

Uses [WeChatFerry](https://github.com/lich0821/WeChatFerry) via TCP JSON-RPC. Requires a Windows host with WeChat desktop injected. Recommended for development only.

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
  ipad:
    enabled: true
    api_endpoint: "http://gewechat:2531/api"
    callback_url: "http://mautrix-wechat:29352/callback"
    callback_port: 29352
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

# With iPad protocol (GeWeChat)
docker compose --profile ipad up -d

# With monitoring (Prometheus + Grafana)
docker compose --profile monitoring up -d

# All services
docker compose --profile ipad --profile monitoring up -d
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

#### iPad (GeWeChat)

| Key | Type | Description |
|-----|------|-------------|
| `providers.ipad.enabled` | bool | Enable iPad protocol |
| `providers.ipad.api_endpoint` | string | GeWeChat HTTP API URL |
| `providers.ipad.api_token` | string | GeWeChat API token |
| `providers.ipad.callback_url` | string | Callback URL for receiving messages |
| `providers.ipad.callback_port` | int | Callback HTTP server port |
| `providers.ipad.risk_control.max_messages_per_day` | int | Daily message quota (default 500) |
| `providers.ipad.risk_control.message_interval_ms` | int | Min interval between messages (default 1000) |
| `providers.ipad.risk_control.random_delay` | bool | Add random delay to intervals |

#### WeCom

| Key | Type | Description |
|-----|------|-------------|
| `providers.wecom.enabled` | bool | Enable WeCom provider |
| `providers.wecom.corp_id` | string | WeCom Corp ID |
| `providers.wecom.app_secret` | string | WeCom App Secret |
| `providers.wecom.agent_id` | int | WeCom Agent ID |
| `providers.wecom.callback.token` | string | Callback verification token |
| `providers.wecom.callback.aes_key` | string | Callback AES encryption key |

#### PC Hook

| Key | Type | Description |
|-----|------|-------------|
| `providers.pchook.enabled` | bool | Enable PC Hook provider |
| `providers.pchook.rpc_endpoint` | string | WeChatFerry RPC endpoint (e.g. `tcp://host:19088`) |

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
│   │   ├── event_router.go    # Matrix ↔ WeChat event dispatch
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
│       ├── ipad/              # iPad protocol (GeWeChat) provider
│       │   ├── provider.go    # Provider implementation
│       │   ├── callback.go    # Webhook callback handler
│       │   ├── reconnect.go   # Auto-reconnection with backoff
│       │   ├── riskcontrol.go # Anti-ban rate limiting
│       │   └── voiceconv.go   # SILK voice transcoding
│       ├── pchook/            # PC Hook (WeChatFerry) provider
│       │   ├── provider.go    # Provider implementation
│       │   ├── rpcclient.go   # TCP JSON-RPC client
│       │   └── message.go     # Message serialization
│       └── wecom/             # WeCom (Enterprise WeChat) provider
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
| Location | `m.location` | WeChat → Matrix |
| Link/Article | `m.text` (with URL) | WeChat → Matrix |
| Emoji/Sticker | `m.image` | Both |
| Contact Card | `m.text` (formatted) | WeChat → Matrix |
| Mini Program | `m.text` (with URL) | WeChat → Matrix |
| Revoke | `m.room.redaction` | Both |
| System | `m.notice` | WeChat → Matrix |

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
