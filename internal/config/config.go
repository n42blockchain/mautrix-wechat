package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration for mautrix-wechat.
type Config struct {
	Homeserver HomeserverConfig `yaml:"homeserver"`
	AppService AppServiceConfig `yaml:"appservice"`
	Database   DatabaseConfig   `yaml:"database"`
	Bridge     BridgeConfig     `yaml:"bridge"`
	Providers  ProvidersConfig  `yaml:"providers"`
	Logging    LoggingConfig    `yaml:"logging"`
	Metrics    MetricsConfig    `yaml:"metrics"`
}

// HomeserverConfig contains Matrix homeserver connection settings.
type HomeserverConfig struct {
	Address string `yaml:"address"`
	Domain  string `yaml:"domain"`
}

// AppServiceConfig contains application service settings.
type AppServiceConfig struct {
	Address         string    `yaml:"address"`
	Hostname        string    `yaml:"hostname"`
	Port            int       `yaml:"port"`
	ID              string    `yaml:"id"`
	Bot             BotConfig `yaml:"bot"`
	ASToken         string    `yaml:"as_token"`
	HSToken         string    `yaml:"hs_token"`
	EphemeralEvents bool      `yaml:"ephemeral_events"`
}

// BotConfig contains the bridge bot user settings.
type BotConfig struct {
	Username    string `yaml:"username"`
	Displayname string `yaml:"displayname"`
	Avatar      string `yaml:"avatar"`
}

// DatabaseConfig contains database connection settings.
type DatabaseConfig struct {
	Type         string `yaml:"type"`
	URI          string `yaml:"uri"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

// BridgeConfig contains bridge-specific settings.
type BridgeConfig struct {
	Permissions         map[string]string     `yaml:"permissions"`
	UsernameTemplate    string                `yaml:"username_template"`
	DisplaynameTemplate string                `yaml:"displayname_template"`
	MessageHandling     MessageHandlingConfig `yaml:"message_handling"`
	Encryption          EncryptionConfig      `yaml:"encryption"`
	RateLimit           RateLimitConfig       `yaml:"rate_limit"`
	Media               MediaConfig           `yaml:"media"`
}

// MessageHandlingConfig controls message processing behavior.
type MessageHandlingConfig struct {
	MaxMessageAge    int  `yaml:"max_message_age"`
	DeliveryReceipts bool `yaml:"delivery_receipts"`
	SendReadReceipts bool `yaml:"send_read_receipts"`
	SyncDirectChat   bool `yaml:"sync_direct_chat_list"`
}

// EncryptionConfig controls end-to-end encryption settings.
type EncryptionConfig struct {
	Allow      bool   `yaml:"allow"`
	Default    bool   `yaml:"default"`
	Require    bool   `yaml:"require"`
	AppService bool   `yaml:"appservice"`
	PickleKey  string `yaml:"pickle_key"`
}

// RateLimitConfig controls rate limiting for outgoing messages.
type RateLimitConfig struct {
	MessagesPerMinute int `yaml:"messages_per_minute"`
	MediaPerMinute    int `yaml:"media_per_minute"`
	APICallsPerMinute int `yaml:"api_calls_per_minute"`
}

// MediaConfig controls media processing settings.
type MediaConfig struct {
	MaxFileSize    int64  `yaml:"max_file_size"`
	VoiceConverter string `yaml:"voice_converter"`
	ImageQuality   int    `yaml:"image_quality"`
	VideoThumbnail bool   `yaml:"video_thumbnail"`
}

// ProvidersConfig holds configuration for all provider types.
type ProvidersConfig struct {
	WeCom    WeComProviderConfig  `yaml:"wecom"`
	IPad     IPadProviderConfig   `yaml:"ipad"`
	PCHook   PCHookProviderConfig `yaml:"pchook"`
	Failover FailoverConfig       `yaml:"failover"`
}

// FailoverConfig controls automatic provider failover and recovery.
type FailoverConfig struct {
	Enabled               bool `yaml:"enabled"`
	HealthCheckIntervalS  int  `yaml:"health_check_interval_s"`
	FailureThreshold      int  `yaml:"failure_threshold"`
	RecoveryCheckIntervalS int `yaml:"recovery_check_interval_s"`
	RecoveryThreshold     int  `yaml:"recovery_threshold"`
}

// WeComProviderConfig holds WeCom (enterprise WeChat) settings.
type WeComProviderConfig struct {
	Enabled   bool                   `yaml:"enabled"`
	CorpID    string                 `yaml:"corp_id"`
	AppSecret string                 `yaml:"app_secret"`
	AgentID   int                    `yaml:"agent_id"`
	Callback  WeComCallbackConfig    `yaml:"callback"`
}

// WeComCallbackConfig holds WeCom callback verification settings.
type WeComCallbackConfig struct {
	Token  string `yaml:"token"`
	AESKey string `yaml:"aes_key"`
	Port   int    `yaml:"port"`
}

// IPadProviderConfig holds iPad protocol (GeWeChat) settings.
type IPadProviderConfig struct {
	Enabled      bool              `yaml:"enabled"`
	APIEndpoint  string            `yaml:"api_endpoint"`
	APIToken     string            `yaml:"api_token"`
	CallbackURL  string            `yaml:"callback_url"`
	CallbackPort int               `yaml:"callback_port"`
	RiskControl  RiskControlConfig `yaml:"risk_control"`
}

// RiskControlConfig holds anti-ban risk control settings for the iPad protocol.
type RiskControlConfig struct {
	NewAccountSilenceDays int  `yaml:"new_account_silence_days"`
	MaxMessagesPerDay     int  `yaml:"max_messages_per_day"`
	MaxGroupsPerDay       int  `yaml:"max_groups_per_day"`
	MaxFriendsPerDay      int  `yaml:"max_friends_per_day"`
	MessageIntervalMs     int  `yaml:"message_interval_ms"`
	RandomDelay           bool `yaml:"random_delay"`
}

// PCHookProviderConfig holds PC Hook settings.
type PCHookProviderConfig struct {
	Enabled       bool   `yaml:"enabled"`
	RPCEndpoint   string `yaml:"rpc_endpoint"`
	WeChatVersion string `yaml:"wechat_version"`
}

// LoggingConfig controls logging output.
type LoggingConfig struct {
	MinLevel string         `yaml:"min_level"`
	Writers  []LoggerWriter `yaml:"writers"`
}

// LoggerWriter describes a single log output target.
type LoggerWriter struct {
	Type       string `yaml:"type"`
	Format     string `yaml:"format"`
	Filename   string `yaml:"filename,omitempty"`
	MaxSize    int    `yaml:"max_size,omitempty"`
	MaxBackups int    `yaml:"max_backups,omitempty"`
	Compress   bool   `yaml:"compress,omitempty"`
}

// MetricsConfig controls Prometheus metrics exposure.
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// Load reads and parses a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	// Expand environment variables
	data = []byte(os.ExpandEnv(string(data)))

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// Validate checks that the configuration is valid and sets defaults.
func (c *Config) Validate() error {
	if c.Homeserver.Address == "" {
		return fmt.Errorf("homeserver.address is required")
	}
	if c.Homeserver.Domain == "" {
		return fmt.Errorf("homeserver.domain is required")
	}
	if c.AppService.Port == 0 {
		c.AppService.Port = 29350
	}
	if c.AppService.ID == "" {
		c.AppService.ID = "wechat"
	}
	if c.AppService.Bot.Username == "" {
		c.AppService.Bot.Username = "wechatbot"
	}
	if c.AppService.ASToken == "" {
		return fmt.Errorf("appservice.as_token is required")
	}
	if c.AppService.HSToken == "" {
		return fmt.Errorf("appservice.hs_token is required")
	}
	if c.Database.URI == "" {
		return fmt.Errorf("database.uri is required")
	}
	if c.Database.Type == "" {
		c.Database.Type = "postgres"
	}
	if c.Database.MaxOpenConns == 0 {
		c.Database.MaxOpenConns = 20
	}
	if c.Database.MaxIdleConns == 0 {
		c.Database.MaxIdleConns = 5
	}

	// Bridge defaults
	if c.Bridge.UsernameTemplate == "" {
		c.Bridge.UsernameTemplate = "wechat_{{.}}"
	}
	if c.Bridge.DisplaynameTemplate == "" {
		c.Bridge.DisplaynameTemplate = "{{.Nickname}} (WeChat)"
	}
	if c.Bridge.RateLimit.MessagesPerMinute == 0 {
		c.Bridge.RateLimit.MessagesPerMinute = 30
	}
	if c.Bridge.RateLimit.MediaPerMinute == 0 {
		c.Bridge.RateLimit.MediaPerMinute = 10
	}
	if c.Bridge.RateLimit.APICallsPerMinute == 0 {
		c.Bridge.RateLimit.APICallsPerMinute = 60
	}
	if c.Bridge.Media.MaxFileSize == 0 {
		c.Bridge.Media.MaxFileSize = 100 * 1024 * 1024 // 100MB
	}
	if c.Bridge.Media.ImageQuality == 0 {
		c.Bridge.Media.ImageQuality = 90
	}
	if c.Bridge.MessageHandling.MaxMessageAge == 0 {
		c.Bridge.MessageHandling.MaxMessageAge = 300
	}

	// iPad risk control defaults
	if c.Providers.IPad.Enabled {
		rc := &c.Providers.IPad.RiskControl
		if rc.NewAccountSilenceDays == 0 {
			rc.NewAccountSilenceDays = 3
		}
		if rc.MaxMessagesPerDay == 0 {
			rc.MaxMessagesPerDay = 500
		}
		if rc.MaxGroupsPerDay == 0 {
			rc.MaxGroupsPerDay = 10
		}
		if rc.MaxFriendsPerDay == 0 {
			rc.MaxFriendsPerDay = 20
		}
		if rc.MessageIntervalMs == 0 {
			rc.MessageIntervalMs = 1000
		}
	}

	// Failover defaults
	if c.Providers.Failover.Enabled {
		fo := &c.Providers.Failover
		if fo.HealthCheckIntervalS == 0 {
			fo.HealthCheckIntervalS = 30
		}
		if fo.FailureThreshold == 0 {
			fo.FailureThreshold = 3
		}
		if fo.RecoveryCheckIntervalS == 0 {
			fo.RecoveryCheckIntervalS = 120
		}
		if fo.RecoveryThreshold == 0 {
			fo.RecoveryThreshold = 3
		}
	}

	// Logging defaults
	if c.Logging.MinLevel == "" {
		c.Logging.MinLevel = "info"
	}

	// Metrics defaults
	if c.Metrics.Listen == "" {
		c.Metrics.Listen = "0.0.0.0:9110"
	}

	// Ensure at least one provider is enabled
	if !c.Providers.WeCom.Enabled && !c.Providers.IPad.Enabled && !c.Providers.PCHook.Enabled {
		return fmt.Errorf("at least one provider must be enabled")
	}

	// Per-provider required field validation
	if c.Providers.WeCom.Enabled {
		if c.Providers.WeCom.CorpID == "" {
			return fmt.Errorf("providers.wecom.corp_id is required when wecom is enabled")
		}
		if c.Providers.WeCom.AppSecret == "" {
			return fmt.Errorf("providers.wecom.app_secret is required when wecom is enabled")
		}
	}
	if c.Providers.IPad.Enabled {
		if c.Providers.IPad.APIEndpoint == "" {
			return fmt.Errorf("providers.ipad.api_endpoint is required when ipad is enabled")
		}
	}

	return nil
}

// GenerateRegistration creates a Matrix appservice registration YAML.
func (c *Config) GenerateRegistration() string {
	return fmt.Sprintf(`id: %s
url: %s
as_token: %s
hs_token: %s
sender_localpart: %s
namespaces:
  users:
    - exclusive: true
      regex: '@wechat_.+:%s'
  aliases: []
  rooms: []
rate_limited: false
de.sorunome.msc2409.push_ephemeral: %t
push_ephemeral: %t
`,
		c.AppService.ID,
		c.AppService.Address,
		c.AppService.ASToken,
		c.AppService.HSToken,
		c.AppService.Bot.Username,
		regexEscape(c.Homeserver.Domain),
		c.AppService.EphemeralEvents,
		c.AppService.EphemeralEvents,
	)
}

func regexEscape(s string) string {
	return regexp.QuoteMeta(s)
}
