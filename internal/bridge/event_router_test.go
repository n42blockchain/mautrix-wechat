package bridge

import (
	"context"
	"log/slog"
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func TestNewEventRouter_DefaultCrypto(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	if er.crypto == nil {
		t.Fatal("crypto should default to noopCryptoHelper")
	}

	// Verify it's a noop
	ctx := context.Background()
	if er.crypto.IsEncrypted(ctx, "!room:example.com") {
		t.Error("noop crypto should report rooms as unencrypted")
	}
}

func TestNewEventRouter_WithCrypto(t *testing.T) {
	pm := newTestPuppetManager()
	noop := &noopCryptoHelper{}
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
		Crypto:  noop,
	})

	if er.crypto != noop {
		t.Error("should use provided crypto helper")
	}
}

func TestEventRouter_SetProvider(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	if er.provider != nil {
		t.Error("provider should initially be nil")
	}

	mp := &mockProvider{name: "test", running: true}
	er.SetProvider(mp)

	if er.provider != mp {
		t.Error("SetProvider should update the provider")
	}
}

func TestEventRouter_HandleMatrixEvent_IgnorePuppet(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	evt := &MatrixEvent{
		Sender: "@wechat_wxid_test:example.com",
		Type:   "m.room.message",
		RoomID: "!room:example.com",
	}

	// Should return nil (ignored) since sender is a puppet
	err := er.HandleMatrixEvent(ctx, evt)
	if err != nil {
		t.Errorf("expected nil for puppet sender, got: %v", err)
	}
}

func TestEventRouter_HandleMatrixEvent_UnsupportedType(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
		Rooms:   nil, // will cause error for mapped rooms, but unsupported types are handled differently
	})

	ctx := context.Background()
	evt := &MatrixEvent{
		Sender: "@real_user:example.com",
		Type:   "m.custom.event",
		RoomID: "!room:example.com",
	}

	// HandleMatrixEvent first looks up the room, which requires rooms store.
	// Since rooms is nil, it will error at room lookup.
	err := er.HandleMatrixEvent(ctx, evt)
	if err == nil {
		t.Error("expected error when rooms store is nil")
	}
}

func TestEventRouter_OnMessage_NilDependencies(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	msg := &wechat.Message{
		MsgID:    "msg1",
		Type:     wechat.MsgText,
		FromUser: "wxid_sender",
		Content:  "hello",
	}

	// With nil db/bridgeUsers, should error gracefully without panic
	err := er.OnMessage(ctx, msg)
	if err == nil {
		t.Error("expected error when dependencies are nil")
	}
}

func TestEventRouter_OnLoginEvent(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	evt := &wechat.LoginEvent{
		State: wechat.LoginStateLoggedIn,
	}

	err := er.OnLoginEvent(ctx, evt)
	if err != nil {
		t.Errorf("OnLoginEvent should not error: %v", err)
	}
}

func TestEventRouter_OnPresence_NilMatrixClient(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	err := er.OnPresence(ctx, "wxid_test", true)
	if err != nil {
		t.Errorf("OnPresence with nil matrixClient should return nil: %v", err)
	}
}

func TestEventRouter_OnTyping_NilMatrixClient(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	err := er.OnTyping(ctx, "wxid_test", "wxid_chat")
	if err != nil {
		t.Errorf("OnTyping with nil matrixClient should return nil: %v", err)
	}
}

func TestEventRouter_OnRevoke_NilMatrixClient(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	err := er.OnRevoke(ctx, "msg1", "message revoked")
	if err != nil {
		t.Errorf("OnRevoke with nil matrixClient should return nil: %v", err)
	}
}

func TestEventRouter_BackfillRoom_Empty(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	err := er.BackfillRoom(ctx, nil, nil)
	if err != nil {
		t.Errorf("BackfillRoom with nil messages should return nil: %v", err)
	}
}

func TestEventRouter_BackfillRoom_NilProcessor(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	msgs := []*wechat.Message{{MsgID: "m1"}}
	err := er.BackfillRoom(ctx, nil, msgs)
	if err == nil {
		t.Error("BackfillRoom with nil processor should return error")
	}
}

func TestEventRouter_OnGroupMemberUpdate_NilFields(t *testing.T) {
	pm := newTestPuppetManager()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
	})

	ctx := context.Background()
	// bridgeUsers is nil, so findBridgeUser will fail early
	err := er.OnGroupMemberUpdate(ctx, "group1", nil)
	if err != nil {
		t.Errorf("should return nil with nil bridgeUsers: %v", err)
	}
}

func TestEventRouter_MetricsRecording(t *testing.T) {
	pm := newTestPuppetManager()
	metrics := NewMetrics()
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: pm,
		Metrics: metrics,
	})

	// Verify metrics reference is stored
	if er.metrics != metrics {
		t.Error("metrics should be stored in EventRouter")
	}
}
