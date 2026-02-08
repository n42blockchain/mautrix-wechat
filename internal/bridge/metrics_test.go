package bridge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetrics_NewMetrics(t *testing.T) {
	m := NewMetrics()
	if m == nil {
		t.Fatal("NewMetrics should not return nil")
	}
	if m.startTime.IsZero() {
		t.Fatal("startTime should be set")
	}
	if m.wechatToMatrixLatency == nil || m.matrixToWechatLatency == nil {
		t.Fatal("histograms should be initialized")
	}
}

func TestMetrics_Counters(t *testing.T) {
	m := NewMetrics()

	m.IncrMessagesReceived()
	m.IncrMessagesReceived()
	m.IncrMessagesSent()
	m.IncrMessagesFailed()
	m.IncrMediaUploaded()
	m.IncrMediaDownloaded()
	m.IncrProviderErrors()
	m.IncrEncryptionErrors()
	m.IncrRiskControlBlocked()
	m.IncrReconnectAttempts()
	m.IncrReconnectSuccesses()
	m.IncrLoginAttempts()
	m.IncrLoginSuccesses()
	m.IncrLoginFailures()
	m.IncrPuppetsCreated()
	m.IncrRoomsCreated()

	if m.messagesReceived.Load() != 2 {
		t.Fatalf("messagesReceived: %d", m.messagesReceived.Load())
	}
	if m.messagesSent.Load() != 1 {
		t.Fatalf("messagesSent: %d", m.messagesSent.Load())
	}
	if m.messagesFailed.Load() != 1 {
		t.Fatalf("messagesFailed: %d", m.messagesFailed.Load())
	}
	if m.providerErrors.Load() != 1 {
		t.Fatalf("providerErrors: %d", m.providerErrors.Load())
	}
	if m.encryptionErrors.Load() != 1 {
		t.Fatalf("encryptionErrors: %d", m.encryptionErrors.Load())
	}
	if m.riskControlBlocked.Load() != 1 {
		t.Fatalf("riskControlBlocked: %d", m.riskControlBlocked.Load())
	}
	if m.reconnectAttempts.Load() != 1 {
		t.Fatalf("reconnectAttempts: %d", m.reconnectAttempts.Load())
	}
}

func TestMetrics_Gauges(t *testing.T) {
	m := NewMetrics()

	m.SetActiveUsers(5)
	if m.activeUsers.Load() != 5 {
		t.Fatalf("activeUsers: %d", m.activeUsers.Load())
	}

	m.SetConnected(true)
	if m.connectedState.Load() != 1 {
		t.Fatal("should be connected")
	}

	m.SetConnected(false)
	if m.connectedState.Load() != 0 {
		t.Fatal("should be disconnected")
	}

	m.SetLoginState(3)
	if m.loginState.Load() != 3 {
		t.Fatalf("loginState: %d", m.loginState.Load())
	}
}

func TestMetrics_MessagesByType(t *testing.T) {
	m := NewMetrics()

	m.IncrMessagesByType("wechat_to_matrix", "text")
	m.IncrMessagesByType("wechat_to_matrix", "text")
	m.IncrMessagesByType("wechat_to_matrix", "image")
	m.IncrMessagesByType("matrix_to_wechat", "text")

	var count int
	m.messagesByType.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	if count != 3 {
		t.Fatalf("expected 3 type keys, got %d", count)
	}
}

func TestMetrics_LatencyHistogram(t *testing.T) {
	m := NewMetrics()

	m.ObserveWeChatToMatrixLatency(10 * time.Millisecond)
	m.ObserveWeChatToMatrixLatency(50 * time.Millisecond)
	m.ObserveWeChatToMatrixLatency(200 * time.Millisecond)
	m.ObserveWeChatToMatrixLatency(1 * time.Second)

	m.ObserveMatrixToWeChatLatency(5 * time.Millisecond)

	if m.wechatToMatrixLatency.total != 4 {
		t.Fatalf("wechat_to_matrix total: %d", m.wechatToMatrixLatency.total)
	}
	if m.matrixToWechatLatency.total != 1 {
		t.Fatalf("matrix_to_wechat total: %d", m.matrixToWechatLatency.total)
	}
}

func TestMetrics_HealthStatus(t *testing.T) {
	m := NewMetrics()

	m.SetConnected(true)
	m.SetLoginState(3)
	m.IncrMessagesReceived()

	status := m.HealthStatus()

	if !status["connected"].(bool) {
		t.Fatal("should be connected")
	}
	if status["login_state"].(int64) != 3 {
		t.Fatalf("login_state: %v", status["login_state"])
	}
	if status["uptime_secs"].(float64) <= 0 {
		t.Fatal("uptime should be positive")
	}

	msgs := status["messages"].(map[string]int64)
	if msgs["received"] != 1 {
		t.Fatalf("received: %d", msgs["received"])
	}
}

func TestMetrics_PrometheusHandler(t *testing.T) {
	m := NewMetrics()

	m.IncrMessagesReceived()
	m.IncrMessagesSent()
	m.IncrMessagesFailed()
	m.SetConnected(true)
	m.SetLoginState(3)
	m.SetActiveUsers(2)
	m.ObserveWeChatToMatrixLatency(50 * time.Millisecond)
	m.IncrMessagesByType("wechat_to_matrix", "text")

	handler := m.Handler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	// Verify content type
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("content-type: %s", ct)
	}

	// Check key metrics are present
	checks := []string{
		"mautrix_wechat_uptime_seconds",
		"mautrix_wechat_connected 1",
		"mautrix_wechat_login_state 3",
		"mautrix_wechat_messages_received_total 1",
		"mautrix_wechat_messages_sent_total 1",
		"mautrix_wechat_messages_failed_total 1",
		"mautrix_wechat_active_users 2",
		"mautrix_wechat_wechat_to_matrix_latency_seconds_bucket",
		"mautrix_wechat_wechat_to_matrix_latency_seconds_sum",
		"mautrix_wechat_wechat_to_matrix_latency_seconds_count 1",
		"mautrix_wechat_messages_by_type_total",
		"wechat_to_matrix",
	}

	for _, check := range checks {
		if !strings.Contains(text, check) {
			t.Errorf("missing metric: %s\n\nFull output:\n%s", check, text)
		}
	}
}

func TestMetrics_PrometheusHandler_EmptyHistogram(t *testing.T) {
	m := NewMetrics()

	handler := m.Handler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Result().Body)
	text := string(body)

	// Empty histogram should still have buckets with 0 counts
	if !strings.Contains(text, "mautrix_wechat_wechat_to_matrix_latency_seconds_count 0") {
		t.Errorf("empty histogram should have count 0:\n%s", text)
	}
}

func TestHistogram_Observe(t *testing.T) {
	h := newHistogram([]float64{0.1, 0.5, 1.0})

	h.observe(0.05) // fits in 0.1 bucket
	h.observe(0.3)  // fits in 0.5 bucket
	h.observe(0.8)  // fits in 1.0 bucket
	h.observe(2.0)  // exceeds all buckets

	if h.total != 4 {
		t.Fatalf("total: %d", h.total)
	}
	if h.counts[0] != 1 { // <= 0.1
		t.Fatalf("bucket[0.1]: %d", h.counts[0])
	}
	if h.counts[1] != 2 { // <= 0.5
		t.Fatalf("bucket[0.5]: %d", h.counts[1])
	}
	if h.counts[2] != 3 { // <= 1.0
		t.Fatalf("bucket[1.0]: %d", h.counts[2])
	}
}

func TestHistogram_CumulativeBuckets(t *testing.T) {
	h := newHistogram([]float64{0.1, 0.5, 1.0})

	// Add a value that fits all buckets
	h.observe(0.01)

	if h.counts[0] != 1 {
		t.Fatalf("0.01 should be in 0.1 bucket: %d", h.counts[0])
	}
	if h.counts[1] != 1 {
		t.Fatalf("0.01 should be in 0.5 bucket: %d", h.counts[1])
	}
	if h.counts[2] != 1 {
		t.Fatalf("0.01 should be in 1.0 bucket: %d", h.counts[2])
	}
}

func TestSplitTypeKey(t *testing.T) {
	tests := []struct {
		key       string
		direction string
		msgType   string
	}{
		{"wechat_to_matrix:text", "wechat_to_matrix", "text"},
		{"matrix_to_wechat:image", "matrix_to_wechat", "image"},
		{"no_colon", "no_colon", "unknown"},
	}

	for _, tt := range tests {
		d, m := splitTypeKey(tt.key)
		if d != tt.direction || m != tt.msgType {
			t.Errorf("splitTypeKey(%q) = (%q, %q), want (%q, %q)", tt.key, d, m, tt.direction, tt.msgType)
		}
	}
}

func TestFormatFloat(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "0"},
		{1.0, "1.0"},
		{0.5, "0.5"},
		{123.0, "123.0"},
	}

	for _, tt := range tests {
		result := formatFloat(tt.input)
		if result != tt.expected {
			t.Errorf("formatFloat(%v) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
