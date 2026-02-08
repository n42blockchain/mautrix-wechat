package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

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

	// Set up logging
	logLevel := slog.LevelInfo
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	log := slog.New(handler)

	log.Info("mautrix-wechat starting",
		"version", version, "commit", commit, "build_date", buildDate)

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err, "path", *configPath)
		os.Exit(1)
	}

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

  ipad:
    enabled: true
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
