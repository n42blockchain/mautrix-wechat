package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validMinimalConfig returns a minimal valid configuration for testing.
func validMinimalConfig() *Config {
	return &Config{
		Homeserver: HomeserverConfig{
			Address: "https://m.example.com",
			Domain:  "example.com",
		},
		AppService: AppServiceConfig{
			ASToken: "as_token_abc",
			HSToken: "hs_token_xyz",
		},
		Database: DatabaseConfig{
			URI: "postgres://localhost/test",
		},
		Providers: ProvidersConfig{
			WeCom: WeComProviderConfig{
				Enabled:   true,
				CorpID:    "corp123",
				AppSecret: "secret456",
			},
		},
	}
}

func TestValidate_MinimalValid(t *testing.T) {
	cfg := validMinimalConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate minimal config: %v", err)
	}
}

func TestValidate_Defaults(t *testing.T) {
	cfg := validMinimalConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// AppService defaults
	if cfg.AppService.Port != 29350 {
		t.Errorf("expected default port 29350, got %d", cfg.AppService.Port)
	}
	if cfg.AppService.ID != "wechat" {
		t.Errorf("expected default ID 'wechat', got %s", cfg.AppService.ID)
	}
	if cfg.AppService.Bot.Username != "wechatbot" {
		t.Errorf("expected default bot username 'wechatbot', got %s", cfg.AppService.Bot.Username)
	}

	// Database defaults
	if cfg.Database.Type != "postgres" {
		t.Errorf("expected default db type 'postgres', got %s", cfg.Database.Type)
	}
	if cfg.Database.MaxOpenConns != 20 {
		t.Errorf("expected default max_open_conns 20, got %d", cfg.Database.MaxOpenConns)
	}
	if cfg.Database.MaxIdleConns != 5 {
		t.Errorf("expected default max_idle_conns 5, got %d", cfg.Database.MaxIdleConns)
	}

	// Bridge defaults
	if cfg.Bridge.UsernameTemplate != "wechat_{{.}}" {
		t.Errorf("expected default username template, got %s", cfg.Bridge.UsernameTemplate)
	}
	if cfg.Bridge.DisplaynameTemplate != "{{.Nickname}} (WeChat)" {
		t.Errorf("expected default displayname template, got %s", cfg.Bridge.DisplaynameTemplate)
	}
	if cfg.Bridge.RateLimit.MessagesPerMinute != 30 {
		t.Errorf("expected default messages_per_minute 30, got %d", cfg.Bridge.RateLimit.MessagesPerMinute)
	}
	if cfg.Bridge.RateLimit.MediaPerMinute != 10 {
		t.Errorf("expected default media_per_minute 10, got %d", cfg.Bridge.RateLimit.MediaPerMinute)
	}
	if cfg.Bridge.RateLimit.APICallsPerMinute != 60 {
		t.Errorf("expected default api_calls_per_minute 60, got %d", cfg.Bridge.RateLimit.APICallsPerMinute)
	}
	if cfg.Bridge.Media.MaxFileSize != 100*1024*1024 {
		t.Errorf("expected default max_file_size 100MB, got %d", cfg.Bridge.Media.MaxFileSize)
	}
	if cfg.Bridge.Media.ImageQuality != 90 {
		t.Errorf("expected default image_quality 90, got %d", cfg.Bridge.Media.ImageQuality)
	}
	if cfg.Bridge.MessageHandling.MaxMessageAge != 300 {
		t.Errorf("expected default max_message_age 300, got %d", cfg.Bridge.MessageHandling.MaxMessageAge)
	}

	// Logging defaults
	if cfg.Logging.MinLevel != "info" {
		t.Errorf("expected default min_level 'info', got %s", cfg.Logging.MinLevel)
	}

	// Metrics defaults
	if cfg.Metrics.Listen != "0.0.0.0:9110" {
		t.Errorf("expected default metrics listen '0.0.0.0:9110', got %s", cfg.Metrics.Listen)
	}
}

func TestValidate_CustomValuesNotOverwritten(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.AppService.Port = 12345
	cfg.AppService.ID = "custom_id"
	cfg.AppService.Bot.Username = "custom_bot"
	cfg.Database.Type = "sqlite"
	cfg.Database.MaxOpenConns = 50
	cfg.Bridge.UsernameTemplate = "wx_{{.}}"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if cfg.AppService.Port != 12345 {
		t.Errorf("custom port overwritten: %d", cfg.AppService.Port)
	}
	if cfg.AppService.ID != "custom_id" {
		t.Errorf("custom ID overwritten: %s", cfg.AppService.ID)
	}
	if cfg.AppService.Bot.Username != "custom_bot" {
		t.Errorf("custom bot username overwritten: %s", cfg.AppService.Bot.Username)
	}
	if cfg.Database.Type != "sqlite" {
		t.Errorf("custom db type overwritten: %s", cfg.Database.Type)
	}
	if cfg.Database.MaxOpenConns != 50 {
		t.Errorf("custom max_open_conns overwritten: %d", cfg.Database.MaxOpenConns)
	}
	if cfg.Bridge.UsernameTemplate != "wx_{{.}}" {
		t.Errorf("custom username template overwritten: %s", cfg.Bridge.UsernameTemplate)
	}
}

func TestValidate_MissingHomeserverAddress(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Homeserver.Address = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing homeserver address")
	}
	if !strings.Contains(err.Error(), "homeserver.address") {
		t.Errorf("error should mention homeserver.address: %v", err)
	}
}

func TestValidate_MissingHomeserverDomain(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Homeserver.Domain = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing homeserver domain")
	}
	if !strings.Contains(err.Error(), "homeserver.domain") {
		t.Errorf("error should mention homeserver.domain: %v", err)
	}
}

func TestValidate_MissingASToken(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.AppService.ASToken = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing as_token")
	}
	if !strings.Contains(err.Error(), "as_token") {
		t.Errorf("error should mention as_token: %v", err)
	}
}

func TestValidate_MissingHSToken(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.AppService.HSToken = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing hs_token")
	}
	if !strings.Contains(err.Error(), "hs_token") {
		t.Errorf("error should mention hs_token: %v", err)
	}
}

func TestValidate_MissingDatabaseURI(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Database.URI = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing database uri")
	}
	if !strings.Contains(err.Error(), "database.uri") {
		t.Errorf("error should mention database.uri: %v", err)
	}
}

func TestValidate_NoProviderEnabled(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.WeCom.Enabled = false

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when no provider is enabled")
	}
	if !strings.Contains(err.Error(), "at least one provider") {
		t.Errorf("error should mention provider requirement: %v", err)
	}
}

func TestValidate_WeComMissingCorpID(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.WeCom.CorpID = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing wecom corp_id")
	}
	if !strings.Contains(err.Error(), "corp_id") {
		t.Errorf("error should mention corp_id: %v", err)
	}
}

func TestValidate_WeComMissingAppSecret(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.WeCom.AppSecret = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing wecom app_secret")
	}
	if !strings.Contains(err.Error(), "app_secret") {
		t.Errorf("error should mention app_secret: %v", err)
	}
}

func TestValidate_IPadMissingAPIEndpoint(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.IPad = IPadProviderConfig{
		Enabled: true,
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing ipad api_endpoint")
	}
	if !strings.Contains(err.Error(), "api_endpoint") {
		t.Errorf("error should mention api_endpoint: %v", err)
	}
}

func TestValidate_IPadRiskControlDefaults(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.IPad = IPadProviderConfig{
		Enabled:     true,
		APIEndpoint: "http://localhost:2531",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	rc := cfg.Providers.IPad.RiskControl
	if rc.NewAccountSilenceDays != 3 {
		t.Errorf("expected default new_account_silence_days 3, got %d", rc.NewAccountSilenceDays)
	}
	if rc.MaxMessagesPerDay != 500 {
		t.Errorf("expected default max_messages_per_day 500, got %d", rc.MaxMessagesPerDay)
	}
	if rc.MaxGroupsPerDay != 10 {
		t.Errorf("expected default max_groups_per_day 10, got %d", rc.MaxGroupsPerDay)
	}
	if rc.MaxFriendsPerDay != 20 {
		t.Errorf("expected default max_friends_per_day 20, got %d", rc.MaxFriendsPerDay)
	}
	if rc.MessageIntervalMs != 1000 {
		t.Errorf("expected default message_interval_ms 1000, got %d", rc.MessageIntervalMs)
	}
}

func TestValidate_FailoverDefaults(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.Failover = FailoverConfig{
		Enabled: true,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	fo := cfg.Providers.Failover
	if fo.HealthCheckIntervalS != 30 {
		t.Errorf("expected default health_check_interval_s 30, got %d", fo.HealthCheckIntervalS)
	}
	if fo.FailureThreshold != 3 {
		t.Errorf("expected default failure_threshold 3, got %d", fo.FailureThreshold)
	}
	if fo.RecoveryCheckIntervalS != 120 {
		t.Errorf("expected default recovery_check_interval_s 120, got %d", fo.RecoveryCheckIntervalS)
	}
	if fo.RecoveryThreshold != 3 {
		t.Errorf("expected default recovery_threshold 3, got %d", fo.RecoveryThreshold)
	}
}

func TestValidate_MultipleProvidersEnabled(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.IPad = IPadProviderConfig{
		Enabled:     true,
		APIEndpoint: "http://localhost:2531",
	}
	cfg.Providers.PCHook = PCHookProviderConfig{
		Enabled: true,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate multiple providers: %v", err)
	}
}

func TestValidate_OnlyIPadEnabled(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.WeCom.Enabled = false
	cfg.Providers.IPad = IPadProviderConfig{
		Enabled:     true,
		APIEndpoint: "http://localhost:2531",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate ipad-only config: %v", err)
	}
}

func TestValidate_OnlyPCHookEnabled(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.WeCom.Enabled = false
	cfg.Providers.PCHook = PCHookProviderConfig{
		Enabled: true,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate pchook-only config: %v", err)
	}
}

func TestGenerateRegistration(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.AppService.Address = "http://localhost:29350"
	cfg.AppService.ID = "wechat"
	cfg.AppService.Bot.Username = "wechatbot"
	cfg.AppService.ASToken = "as_token_test"
	cfg.AppService.HSToken = "hs_token_test"
	cfg.AppService.EphemeralEvents = true
	cfg.Homeserver.Domain = "example.com"

	reg := cfg.GenerateRegistration()

	checks := []struct {
		name     string
		contains string
	}{
		{"id", "id: wechat"},
		{"url", "url: http://localhost:29350"},
		{"as_token", "as_token: as_token_test"},
		{"hs_token", "hs_token: hs_token_test"},
		{"sender_localpart", "sender_localpart: wechatbot"},
		{"user regex", "@wechat_.+:example\\.com"},
		{"ephemeral", "push_ephemeral: true"},
	}

	for _, c := range checks {
		if !strings.Contains(reg, c.contains) {
			t.Errorf("registration missing %s: expected to contain %q", c.name, c.contains)
		}
	}
}

func TestGenerateRegistration_DomainEscaped(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Homeserver.Domain = "m.si46.world"
	cfg.AppService.Address = "http://localhost:29350"

	reg := cfg.GenerateRegistration()

	if !strings.Contains(reg, `m\.si46\.world`) {
		t.Error("domain dots should be escaped in regex")
	}
}

func TestRegexEscape(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com", `example\.com`},
		{"nodots", "nodots"},
		{"a.b.c", `a\.b\.c`},
		{"", ""},
	}

	for _, tc := range tests {
		result := regexEscape(tc.input)
		if result != tc.expected {
			t.Errorf("regexEscape(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("{{invalid yaml"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_ValidationError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	os.WriteFile(path, []byte("{}"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty config")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
homeserver:
  address: https://m.example.com
  domain: example.com
appservice:
  as_token: "test_as_token"
  hs_token: "test_hs_token"
database:
  uri: "postgres://localhost/test"
providers:
  wecom:
    enabled: true
    corp_id: "corp123"
    app_secret: "secret456"
`
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load valid config: %v", err)
	}

	if cfg.Homeserver.Address != "https://m.example.com" {
		t.Errorf("homeserver address: %s", cfg.Homeserver.Address)
	}
	if cfg.Providers.WeCom.CorpID != "corp123" {
		t.Errorf("wecom corp_id: %s", cfg.Providers.WeCom.CorpID)
	}
}

func TestLoad_EnvVarExpansion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	t.Setenv("TEST_HS_ADDR", "https://matrix.example.com")
	t.Setenv("TEST_AS_TOKEN", "env_as_token")
	t.Setenv("TEST_HS_TOKEN", "env_hs_token")
	t.Setenv("TEST_DB_URI", "postgres://localhost/testdb")
	t.Setenv("TEST_CORP_ID", "env_corp")
	t.Setenv("TEST_SECRET", "env_secret")

	content := `
homeserver:
  address: $TEST_HS_ADDR
  domain: example.com
appservice:
  as_token: $TEST_AS_TOKEN
  hs_token: $TEST_HS_TOKEN
database:
  uri: $TEST_DB_URI
providers:
  wecom:
    enabled: true
    corp_id: $TEST_CORP_ID
    app_secret: $TEST_SECRET
`
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config with env vars: %v", err)
	}

	if cfg.Homeserver.Address != "https://matrix.example.com" {
		t.Errorf("env var not expanded for homeserver.address: %s", cfg.Homeserver.Address)
	}
	if cfg.AppService.ASToken != "env_as_token" {
		t.Errorf("env var not expanded for as_token: %s", cfg.AppService.ASToken)
	}
	if cfg.Database.URI != "postgres://localhost/testdb" {
		t.Errorf("env var not expanded for db uri: %s", cfg.Database.URI)
	}
}
