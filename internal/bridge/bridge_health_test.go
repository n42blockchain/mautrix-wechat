package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func TestBridgeHandleHealthUsesActualProviderState(t *testing.T) {
	metrics := NewMetrics()
	metrics.SetConnected(false)
	metrics.SetLoginState(int(wechat.LoginStateLoggedOut))

	provider := newMockProvider("wecom", 1)
	provider.running = true
	provider.loginState = wechat.LoginStateLoggedIn

	b := &Bridge{
		Metrics:  metrics,
		Provider: provider,
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	b.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var status map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal health: %v", err)
	}

	if connected, _ := status["connected"].(bool); !connected {
		t.Fatalf("connected = %v", status["connected"])
	}
	if got := int(status["login_state"].(float64)); got != int(wechat.LoginStateLoggedIn) {
		t.Fatalf("login_state = %d", got)
	}
	if status["provider"] != "wecom" {
		t.Fatalf("provider = %v", status["provider"])
	}
	if running, _ := status["provider_running"].(bool); !running {
		t.Fatalf("provider_running = %v", status["provider_running"])
	}
}

func TestBridgeHandleHealthReturns503ForActualDisconnectedProvider(t *testing.T) {
	metrics := NewMetrics()
	metrics.SetConnected(true)
	metrics.SetLoginState(int(wechat.LoginStateLoggedIn))

	provider := newMockProvider("wecom", 1)
	provider.running = true
	provider.loginState = wechat.LoginStateError

	b := &Bridge{
		Metrics:  metrics,
		Provider: provider,
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	b.handleHealth(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}

	var status map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal health: %v", err)
	}

	if connected, _ := status["connected"].(bool); connected {
		t.Fatalf("connected = %v", status["connected"])
	}
	if got := int(status["login_state"].(float64)); got != int(wechat.LoginStateError) {
		t.Fatalf("login_state = %d", got)
	}
}
