package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/n42/mautrix-wechat/internal/bridge"
	"github.com/n42/mautrix-wechat/internal/config"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	genConfig := flag.Bool("generate-config", false, "Generate example config and exit")
	genReg := flag.Bool("generate-registration", false, "Generate appservice registration YAML and exit")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mautrix-wechat %s (commit: %s, built: %s)\n", version, commit, buildDate)
		os.Exit(0)
	}

	if *genConfig {
		fmt.Print(exampleConfig)
		os.Exit(0)
	}

	log := newLogger(slog.LevelInfo)
	slog.SetDefault(log)

	log.Info("mautrix-wechat starting",
		"version", version, "commit", commit, "build_date", buildDate)

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err, "path", *configPath)
		os.Exit(1)
	}

	logLevel, err := parseLogLevel(cfg.Logging.MinLevel)
	if err != nil {
		log.Error("invalid logging.min_level", "error", err, "value", cfg.Logging.MinLevel)
		os.Exit(1)
	}
	log = newLogger(logLevel)
	slog.SetDefault(log)

	if *genReg {
		fmt.Print(cfg.GenerateRegistration())
		os.Exit(0)
	}

	// Create and run bridge
	b, err := bridge.New(cfg, log)
	if err != nil {
		log.Error("failed to create bridge", "error", err)
		os.Exit(1)
	}

	if err := b.Run(); err != nil {
		log.Error("bridge error", "error", err)
		os.Exit(1)
	}
}

func newLogger(level slog.Level) *slog.Logger {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(handler)
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", value)
	}
}

const exampleConfig = `# mautrix-wechat configuration
# See https://github.com/n42/mautrix-wechat for documentation

homeserver:
  address: https://m.si46.world
  domain: m.si46.world

appservice:
  address: http://localhost:29350
  hostname: 0.0.0.0
  port: 29350
  id: wechat
  bot:
    username: wechatbot
    displayname: WeChat Bridge Bot
    avatar: ""
  as_token: "CHANGE_ME_AS_TOKEN"
  hs_token: "CHANGE_ME_HS_TOKEN"
  ephemeral_events: true

database:
  type: postgres
  uri: "postgres://mautrix_wechat:password@localhost:5432/mautrix_wechat?sslmode=require"
  max_open_conns: 20
  max_idle_conns: 5

bridge:
  permissions:
    "*": relay
    "m.si46.world": user
    "@admin:m.si46.world": admin
  username_template: "wechat_{{.}}"
  displayname_template: "{{.Nickname}} (WeChat)"
  message_handling:
    max_message_age: 300
    delivery_receipts: true
    send_read_receipts: true
    sync_direct_chat_list: true
  encryption:
    allow: true
    default: false
    require: false
    appservice: false
    pickle_key: ""
  rate_limit:
    messages_per_minute: 30
    media_per_minute: 10
    api_calls_per_minute: 60
  media:
    max_file_size: 104857600
    voice_converter: silk2ogg
    image_quality: 90
    video_thumbnail: true

providers:
  wecom:
    enabled: false
    corp_id: "YOUR_CORP_ID"
    app_secret: "YOUR_APP_SECRET"
    agent_id: 1000001
    callback:
      token: "YOUR_CALLBACK_TOKEN"
      aes_key: "YOUR_AES_KEY"
      port: 29351

  # Recommended: WeChatPadPro (Pad protocol, successor to GeWeChat)
  # Docker image: registry.cn-hangzhou.aliyuncs.com/wechatpad/wechatpadpro:v0.11
  # Requires: MySQL 8.0 + Redis 6
  padpro:
    enabled: true
    api_endpoint: "http://wechatpadpro:1239"
    auth_key: "YOUR_PADPRO_AUTH_KEY"
    # ws_endpoint: ""  # Optional, derived from api_endpoint if empty
    # webhook_url: "http://bridge:29353/callback"  # Optional webhook callback
    callback_port: 29353
    risk_control:
      new_account_silence_days: 3
      max_messages_per_day: 500
      max_groups_per_day: 10
      max_friends_per_day: 20
      message_interval_ms: 1000
      random_delay: true

    # Multi-tenant mode: each user logs in with their own WeChat account
    # distributed across multiple PadPro server nodes.
    # When enabled, api_endpoint and auth_key above are ignored;
    # each node provides its own endpoint and auth_key.
    multi_tenant: false
    max_users_per_node: 10
    # nodes:
    #   - id: "node-01"
    #     api_endpoint: "http://10.0.1.1:1239"
    #     auth_key: "NODE_01_AUTH_KEY"
    #     # ws_endpoint: ""
    #     max_users: 10
    #     enabled: true
    #   - id: "node-02"
    #     api_endpoint: "http://10.0.1.2:1239"
    #     auth_key: "NODE_02_AUTH_KEY"
    #     max_users: 10
    #     enabled: true

  # DEPRECATED: GeWeChat was archived on 2025-05-03. Migrate to padpro.
  ipad:
    enabled: false
    api_endpoint: "http://gewechat:2531/api"
    api_token: "YOUR_GEWECHAT_TOKEN"
    callback_url: "http://bridge:29352/callback"
    callback_port: 29352
    risk_control:
      new_account_silence_days: 3
      max_messages_per_day: 500
      max_groups_per_day: 10
      max_friends_per_day: 20
      message_interval_ms: 1000
      random_delay: true

  pchook:
    enabled: false
    rpc_endpoint: "tcp://windows-host:19088"
    wechat_version: "3.9.12.17"

logging:
  min_level: info
  writers:
    - type: stdout
      format: pretty
    - type: file
      format: json
      filename: ./logs/mautrix-wechat.log
      max_size: 100
      max_backups: 7
      compress: true

metrics:
  enabled: true
  listen: 0.0.0.0:9110
`
