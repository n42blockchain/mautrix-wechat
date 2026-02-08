package ipad

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

var testReconnectLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

func TestReconnector_NewDefaults(t *testing.T) {
	r := NewReconnector(ReconnectorConfig{
		Log: testReconnectLog,
	})

	if r.heartbeatInterval != 30*time.Second {
		t.Fatalf("heartbeatInterval: %v", r.heartbeatInterval)
	}
	if r.maxBackoff != 5*time.Minute {
		t.Fatalf("maxBackoff: %v", r.maxBackoff)
	}
	if r.baseBackoff != 2*time.Second {
		t.Fatalf("baseBackoff: %v", r.baseBackoff)
	}
	if r.state != stateDisconnected {
		t.Fatalf("initial state: %v", r.state)
	}
}

func TestReconnector_MarkConnected(t *testing.T) {
	r := NewReconnector(ReconnectorConfig{
		Log: testReconnectLog,
	})

	r.MarkConnected()

	if !r.IsConnected() {
		t.Fatal("should be connected")
	}
	if r.reconnectCount != 0 {
		t.Fatalf("reconnectCount: %d", r.reconnectCount)
	}
	if r.lastConnected.IsZero() {
		t.Fatal("lastConnected should be set")
	}
}

func TestReconnector_MarkDisconnected(t *testing.T) {
	var disconnectedCalled atomic.Bool

	r := NewReconnector(ReconnectorConfig{
		Log: testReconnectLog,
		OnDisconnected: func() {
			disconnectedCalled.Store(true)
		},
	})

	// First mark connected, then disconnect
	r.MarkConnected()
	r.MarkDisconnected()

	if r.IsConnected() {
		t.Fatal("should be disconnected")
	}

	// Wait briefly for async callback
	time.Sleep(50 * time.Millisecond)

	if !disconnectedCalled.Load() {
		t.Fatal("OnDisconnected should have been called")
	}
}

func TestReconnector_MarkDisconnectedNoop(t *testing.T) {
	// Marking disconnected when already disconnected should be a no-op
	r := NewReconnector(ReconnectorConfig{
		Log: testReconnectLog,
		OnDisconnected: func() {
			// Should not be called
		},
	})

	// Already disconnected at creation, so this should not trigger OnDisconnected
	r.MarkDisconnected()

	if r.IsConnected() {
		t.Fatal("should be disconnected")
	}
}

func TestReconnector_Stop(t *testing.T) {
	r := NewReconnector(ReconnectorConfig{
		Log:               testReconnectLog,
		HeartbeatInterval: 100 * time.Millisecond,
	})

	r.Start()
	r.Stop()

	// Double stop should be safe
	r.Stop()

	if r.state != stateStopped {
		t.Fatalf("state: %v", r.state)
	}
}

func TestReconnector_Stats(t *testing.T) {
	r := NewReconnector(ReconnectorConfig{
		Log: testReconnectLog,
	})

	r.MarkConnected()
	stats := r.Stats()

	if !stats.Connected {
		t.Fatal("should be connected")
	}
	if stats.ReconnectCount != 0 {
		t.Fatalf("reconnectCount: %d", stats.ReconnectCount)
	}
	if stats.LastConnected.IsZero() {
		t.Fatal("lastConnected should be set")
	}
}

func TestReconnector_CalculateBackoff(t *testing.T) {
	r := NewReconnector(ReconnectorConfig{
		Log:         testReconnectLog,
		BaseBackoff: 1 * time.Second,
		MaxBackoff:  30 * time.Second,
	})

	// Attempt 0: ~1s (base)
	b0 := r.calculateBackoff(0)
	if b0 < 500*time.Millisecond || b0 > 2*time.Second {
		t.Fatalf("attempt 0 backoff out of range: %v", b0)
	}

	// Attempt 1: ~2s
	b1 := r.calculateBackoff(1)
	if b1 < 1*time.Second || b1 > 4*time.Second {
		t.Fatalf("attempt 1 backoff out of range: %v", b1)
	}

	// Attempt 2: ~4s
	b2 := r.calculateBackoff(2)
	if b2 < 2*time.Second || b2 > 8*time.Second {
		t.Fatalf("attempt 2 backoff out of range: %v", b2)
	}

	// Attempt 10: should be capped at maxBackoff (30s * jitter)
	b10 := r.calculateBackoff(10)
	if b10 > 45*time.Second { // maxBackoff * 1.25 jitter
		t.Fatalf("attempt 10 backoff should be capped, got: %v", b10)
	}
}

func TestReconnector_ReconnectSuccess(t *testing.T) {
	var reconnectAttempts atomic.Int32
	var connectedCalled atomic.Bool

	r := NewReconnector(ReconnectorConfig{
		Log:               testReconnectLog,
		HeartbeatInterval: 50 * time.Millisecond,
		BaseBackoff:       10 * time.Millisecond,
		MaxBackoff:        100 * time.Millisecond,
		CheckAlive: func(ctx context.Context) bool {
			return false // Simulate connection loss
		},
		DoReconnect: func(ctx context.Context) error {
			reconnectAttempts.Add(1)
			return nil // Succeed immediately
		},
		OnConnected: func() {
			connectedCalled.Store(true)
		},
	})

	r.MarkConnected()
	r.Start()
	defer r.Stop()

	// Wait for heartbeat to detect disconnection and reconnect
	time.Sleep(300 * time.Millisecond)

	if reconnectAttempts.Load() == 0 {
		t.Fatal("reconnect should have been attempted")
	}
	if !connectedCalled.Load() {
		t.Fatal("OnConnected should have been called")
	}
}

func TestReconnector_ReconnectWithRetry(t *testing.T) {
	var attempts atomic.Int32
	var reconnected atomic.Bool

	r := NewReconnector(ReconnectorConfig{
		Log:               testReconnectLog,
		HeartbeatInterval: 2 * time.Second, // Long interval so heartbeat doesn't re-trigger during test
		BaseBackoff:       5 * time.Millisecond,
		MaxBackoff:        20 * time.Millisecond,
		CheckAlive: func(ctx context.Context) bool {
			// Return true once we've reconnected, false before that
			return reconnected.Load()
		},
		DoReconnect: func(ctx context.Context) error {
			n := attempts.Add(1)
			if n < 3 {
				return fmt.Errorf("reconnect failed, attempt %d", n)
			}
			reconnected.Store(true)
			return nil // Succeed on 3rd attempt
		},
	})

	r.MarkConnected()
	// Manually trigger disconnection and reconnect instead of relying on heartbeat
	r.MarkDisconnected()
	go r.reconnectWithBackoff()

	// Wait for retries
	time.Sleep(500 * time.Millisecond)

	if attempts.Load() < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts.Load())
	}
	if !r.IsConnected() {
		t.Fatal("should be connected after successful reconnect")
	}
}

func TestSessionData_Marshal(t *testing.T) {
	sd := &SessionData{
		UserID:    "wxid_test123",
		Nickname:  "TestUser",
		AvatarURL: "https://example.com/avatar.jpg",
		LoginTime: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Token:     "test_token_abc",
		DeviceID:  "device_001",
		AppID:     "app_001",
	}

	data, err := sd.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	sd2, err := UnmarshalSessionData(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if sd2.UserID != sd.UserID {
		t.Fatalf("UserID: %s", sd2.UserID)
	}
	if sd2.Nickname != sd.Nickname {
		t.Fatalf("Nickname: %s", sd2.Nickname)
	}
	if sd2.Token != sd.Token {
		t.Fatalf("Token: %s", sd2.Token)
	}
	if sd2.DeviceID != sd.DeviceID {
		t.Fatalf("DeviceID: %s", sd2.DeviceID)
	}
	if !sd2.LoginTime.Equal(sd.LoginTime) {
		t.Fatalf("LoginTime: %v", sd2.LoginTime)
	}
}

func TestSessionData_UnmarshalInvalid(t *testing.T) {
	_, err := UnmarshalSessionData([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSessionData_EmptyFields(t *testing.T) {
	sd := &SessionData{
		UserID: "wxid_minimal",
	}

	data, err := sd.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Token, DeviceID, AppID should be omitted when empty
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if _, ok := raw["token"]; ok {
		t.Fatal("empty token should be omitted")
	}
	if _, ok := raw["device_id"]; ok {
		t.Fatal("empty device_id should be omitted")
	}
	if _, ok := raw["app_id"]; ok {
		t.Fatal("empty app_id should be omitted")
	}
}
