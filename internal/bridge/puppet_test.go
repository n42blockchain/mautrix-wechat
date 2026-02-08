package bridge

import (
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func newTestPuppetManager() *PuppetManager {
	return NewPuppetManager(
		"example.com",
		"wechat_{{.}}",
		"{{.Nickname}} (WeChat)",
		nil,
		nil,
	)
}

func TestPuppetManager_WechatIDToMatrixID(t *testing.T) {
	pm := newTestPuppetManager()

	tests := []struct {
		wechatID string
		expected string
	}{
		{"wxid_abc123", "@wechat_wxid_abc123:example.com"},
		{"user01", "@wechat_user01:example.com"},
		{"", "@wechat_:example.com"},
	}

	for _, tc := range tests {
		result := pm.wechatIDToMatrixID(tc.wechatID)
		if result != tc.expected {
			t.Errorf("wechatIDToMatrixID(%q) = %q, want %q", tc.wechatID, result, tc.expected)
		}
	}
}

func TestPuppetManager_MatrixIDToWeChatID(t *testing.T) {
	pm := newTestPuppetManager()

	tests := []struct {
		matrixID string
		expected string
	}{
		{"@wechat_wxid_abc123:example.com", "wxid_abc123"},
		{"@wechat_user01:example.com", "user01"},
		{"@other_user:example.com", ""},
		{"@wechat_test:other.com", ""},
		{"invalid", ""},
		{"", ""},
	}

	for _, tc := range tests {
		result := pm.matrixIDToWeChatID(tc.matrixID)
		if result != tc.expected {
			t.Errorf("matrixIDToWeChatID(%q) = %q, want %q", tc.matrixID, result, tc.expected)
		}
	}
}

func TestPuppetManager_IsPuppet(t *testing.T) {
	pm := newTestPuppetManager()

	tests := []struct {
		matrixID string
		expected bool
	}{
		{"@wechat_wxid_abc:example.com", true},
		{"@wechat_user:example.com", true},
		{"@other:example.com", false},
		{"@wechat_test:other.com", false},
		{"", false},
	}

	for _, tc := range tests {
		result := pm.IsPuppet(tc.matrixID)
		if result != tc.expected {
			t.Errorf("IsPuppet(%q) = %v, want %v", tc.matrixID, result, tc.expected)
		}
	}
}

func TestPuppetManager_FormatDisplayName(t *testing.T) {
	pm := newTestPuppetManager()

	tests := []struct {
		nickname string
		remark   string
		expected string
	}{
		{"Alice", "", "Alice (WeChat)"},
		{"Bob", "Bobby", "Bobby (WeChat)"},
		{"", "", " (WeChat)"},
		{"Test User", "", "Test User (WeChat)"},
	}

	for _, tc := range tests {
		contact := &wechat.ContactInfo{
			Nickname: tc.nickname,
			Remark:   tc.remark,
		}
		result := pm.formatDisplayName(contact)
		if result != tc.expected {
			t.Errorf("formatDisplayName(nick=%q, remark=%q) = %q, want %q",
				tc.nickname, tc.remark, result, tc.expected)
		}
	}
}

func TestPuppetManager_CustomTemplate(t *testing.T) {
	pm := NewPuppetManager(
		"m.si46.world",
		"wx_{{.}}",
		"{{.Nickname}}",
		nil,
		nil,
	)

	// Test custom username template
	matrixID := pm.wechatIDToMatrixID("wxid_test")
	if matrixID != "@wx_wxid_test:m.si46.world" {
		t.Errorf("custom template matrixID = %q", matrixID)
	}

	// Test reverse
	wechatID := pm.matrixIDToWeChatID("@wx_wxid_test:m.si46.world")
	if wechatID != "wxid_test" {
		t.Errorf("custom template wechatID = %q", wechatID)
	}

	// IsPuppet with custom template
	if !pm.IsPuppet("@wx_user123:m.si46.world") {
		t.Error("should recognize puppet with custom template")
	}
	if pm.IsPuppet("@wechat_user123:m.si46.world") {
		t.Error("should not recognize old template with custom template")
	}

	// Custom displayname template
	contact := &wechat.ContactInfo{Nickname: "TestUser"}
	name := pm.formatDisplayName(contact)
	if name != "TestUser" {
		t.Errorf("custom displayname template = %q", name)
	}
}

func TestPuppetManager_RoundTripIDConversion(t *testing.T) {
	pm := newTestPuppetManager()

	wechatIDs := []string{"wxid_abc123", "user01", "wxid_very_long_id_12345"}

	for _, id := range wechatIDs {
		matrixID := pm.wechatIDToMatrixID(id)
		recovered := pm.matrixIDToWeChatID(matrixID)
		if recovered != id {
			t.Errorf("round-trip failed: %q -> %q -> %q", id, matrixID, recovered)
		}
	}
}

func TestPuppetManager_NewPuppetManager(t *testing.T) {
	pm := newTestPuppetManager()

	if pm.domain != "example.com" {
		t.Errorf("domain: %s", pm.domain)
	}
	if pm.template != "wechat_{{.}}" {
		t.Errorf("template: %s", pm.template)
	}
	if pm.dnTempl != "{{.Nickname}} (WeChat)" {
		t.Errorf("dnTempl: %s", pm.dnTempl)
	}
	if pm.puppets == nil {
		t.Error("puppets map should be initialized")
	}
}
