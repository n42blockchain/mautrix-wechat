package config

import (
	"os"
	"strings"
	"testing"
)

// validMultiTenantConfig returns a minimal valid multi-tenant PadPro configuration.
func validMultiTenantConfig() *Config {
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
			PadPro: PadProProviderConfig{
				Enabled:     true,
				MultiTenant: true,
				Nodes: []PadProNodeConfig{
					{
						ID:          "node-01",
						APIEndpoint: "http://10.0.1.1:1239",
						AuthKey:     "key01",
						Enabled:     true,
					},
					{
						ID:          "node-02",
						APIEndpoint: "http://10.0.1.2:1239",
						AuthKey:     "key02",
						Enabled:     true,
					},
				},
			},
		},
	}
}

func TestValidate_MultiTenant_ValidConfig(t *testing.T) {
	cfg := validMultiTenantConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate multi-tenant config: %v", err)
	}
}

func TestValidate_MultiTenant_DefaultMaxUsersPerNode(t *testing.T) {
	cfg := validMultiTenantConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.Providers.PadPro.MaxUsersPerNode != 10 {
		t.Errorf("expected default max_users_per_node 10, got %d", cfg.Providers.PadPro.MaxUsersPerNode)
	}
}

func TestValidate_MultiTenant_CustomMaxUsersPerNode(t *testing.T) {
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.MaxUsersPerNode = 5
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.Providers.PadPro.MaxUsersPerNode != 5 {
		t.Errorf("custom max_users_per_node overwritten: %d", cfg.Providers.PadPro.MaxUsersPerNode)
	}
}

func TestValidate_MultiTenant_NodeMaxUsersDefault(t *testing.T) {
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.MaxUsersPerNode = 8
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	for i, node := range cfg.Providers.PadPro.Nodes {
		if node.MaxUsers != 8 {
			t.Errorf("node[%d].MaxUsers: got %d, want 8", i, node.MaxUsers)
		}
	}
}

func TestValidate_MultiTenant_NodeMaxUsersOverride(t *testing.T) {
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.Nodes[0].MaxUsers = 15
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.Providers.PadPro.Nodes[0].MaxUsers != 15 {
		t.Errorf("node[0].MaxUsers override lost: %d", cfg.Providers.PadPro.Nodes[0].MaxUsers)
	}
}

func TestValidate_MultiTenant_NoNodes(t *testing.T) {
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.Nodes = nil

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for multi-tenant with no nodes")
	}
	if !strings.Contains(err.Error(), "at least 1 enabled node") {
		t.Errorf("error should mention enabled node: %v", err)
	}
}

func TestValidate_MultiTenant_AllNodesDisabled(t *testing.T) {
	cfg := validMultiTenantConfig()
	for i := range cfg.Providers.PadPro.Nodes {
		cfg.Providers.PadPro.Nodes[i].Enabled = false
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when all nodes disabled")
	}
	if !strings.Contains(err.Error(), "at least 1 enabled node") {
		t.Errorf("error should mention enabled node: %v", err)
	}
}

func TestValidate_MultiTenant_NodeMissingID(t *testing.T) {
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.Nodes[0].ID = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing node ID")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error should mention id: %v", err)
	}
}

func TestValidate_MultiTenant_DuplicateNodeID(t *testing.T) {
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.Nodes[1].ID = "node-01" // same as first

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for duplicate node ID")
	}
	if !strings.Contains(err.Error(), "duplicated") {
		t.Errorf("error should mention duplicated: %v", err)
	}
}

func TestValidate_MultiTenant_NodeMissingEndpoint(t *testing.T) {
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.Nodes[0].APIEndpoint = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing node api_endpoint")
	}
	if !strings.Contains(err.Error(), "api_endpoint is required") {
		t.Errorf("error should mention api_endpoint: %v", err)
	}
}

func TestValidate_MultiTenant_NodeMissingAuthKey(t *testing.T) {
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.Nodes[0].AuthKey = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing node auth_key")
	}
	if !strings.Contains(err.Error(), "auth_key is required") {
		t.Errorf("error should mention auth_key: %v", err)
	}
}

func TestValidate_MultiTenant_DisabledNodeSkipsValidation(t *testing.T) {
	cfg := validMultiTenantConfig()
	// Add a disabled node with missing fields — should not cause error
	cfg.Providers.PadPro.Nodes = append(cfg.Providers.PadPro.Nodes, PadProNodeConfig{
		ID:      "",
		Enabled: false,
	})

	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled node with missing fields should be skipped: %v", err)
	}
}

func TestValidate_MultiTenant_NoAPIEndpointRequired(t *testing.T) {
	// In multi-tenant mode, top-level api_endpoint is not required
	cfg := validMultiTenantConfig()
	cfg.Providers.PadPro.APIEndpoint = ""
	cfg.Providers.PadPro.AuthKey = ""

	if err := cfg.Validate(); err != nil {
		t.Fatalf("multi-tenant should not require top-level api_endpoint: %v", err)
	}
}

func TestValidate_MultiTenant_RiskControlDefaults(t *testing.T) {
	cfg := validMultiTenantConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	rc := cfg.Providers.PadPro.RiskControl
	if rc.NewAccountSilenceDays != 3 {
		t.Errorf("risk control default: NewAccountSilenceDays = %d, want 3", rc.NewAccountSilenceDays)
	}
	if rc.MaxMessagesPerDay != 500 {
		t.Errorf("risk control default: MaxMessagesPerDay = %d, want 500", rc.MaxMessagesPerDay)
	}
}

func TestValidate_SinglePadPro_RequiresEndpoint(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Providers.WeCom.Enabled = false
	cfg.Providers.PadPro = PadProProviderConfig{
		Enabled:     true,
		MultiTenant: false,
		// Missing APIEndpoint and AuthKey
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing api_endpoint in single mode")
	}
	if !strings.Contains(err.Error(), "api_endpoint is required") {
		t.Errorf("error should mention api_endpoint: %v", err)
	}
}

func TestValidate_MultiTenant_YAMLRoundTrip(t *testing.T) {
	yamlData := `
homeserver:
  address: https://m.example.com
  domain: example.com
appservice:
  as_token: "test_as"
  hs_token: "test_hs"
database:
  uri: "postgres://localhost/test"
providers:
  padpro:
    enabled: true
    multi_tenant: true
    max_users_per_node: 5
    nodes:
      - id: "node-01"
        api_endpoint: "http://10.0.1.1:1239"
        auth_key: "k1"
        enabled: true
      - id: "node-02"
        api_endpoint: "http://10.0.1.2:1239"
        auth_key: "k2"
        ws_endpoint: "ws://10.0.1.2:1240"
        max_users: 8
        enabled: true
`
	dir := t.TempDir()
	path := dir + "/config.yaml"
	if err := writeTestFile(path, yamlData); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load multi-tenant config: %v", err)
	}

	if !cfg.Providers.PadPro.MultiTenant {
		t.Error("multi_tenant should be true")
	}
	if cfg.Providers.PadPro.MaxUsersPerNode != 5 {
		t.Errorf("max_users_per_node: %d", cfg.Providers.PadPro.MaxUsersPerNode)
	}
	if len(cfg.Providers.PadPro.Nodes) != 2 {
		t.Fatalf("nodes count: %d", len(cfg.Providers.PadPro.Nodes))
	}
	if cfg.Providers.PadPro.Nodes[0].ID != "node-01" {
		t.Errorf("node[0].ID: %s", cfg.Providers.PadPro.Nodes[0].ID)
	}
	if cfg.Providers.PadPro.Nodes[1].WSEndpoint != "ws://10.0.1.2:1240" {
		t.Errorf("node[1].WSEndpoint: %s", cfg.Providers.PadPro.Nodes[1].WSEndpoint)
	}
	if cfg.Providers.PadPro.Nodes[1].MaxUsers != 8 {
		t.Errorf("node[1].MaxUsers: %d", cfg.Providers.PadPro.Nodes[1].MaxUsers)
	}
	// node[0].MaxUsers should have been filled with global default
	if cfg.Providers.PadPro.Nodes[0].MaxUsers != 5 {
		t.Errorf("node[0].MaxUsers should default to global: %d", cfg.Providers.PadPro.Nodes[0].MaxUsers)
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
