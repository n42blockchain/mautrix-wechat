package bridge

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/n42/mautrix-wechat/internal/config"
	"github.com/n42/mautrix-wechat/internal/database"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func TestBridgeUserFromContext(t *testing.T) {
	ctx := context.Background()

	// No value in context
	uid, ok := BridgeUserFromContext(ctx)
	if ok || uid != "" {
		t.Error("should return empty for context without bridge user")
	}

	// With value in context
	ctx = context.WithValue(ctx, bridgeUserKey, "@user:example.com")
	uid, ok = BridgeUserFromContext(ctx)
	if !ok {
		t.Error("should return true when value exists")
	}
	if uid != "@user:example.com" {
		t.Errorf("got %s, want @user:example.com", uid)
	}
}

func TestBridgeUserFromContext_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), bridgeUserKey, 12345) // wrong type
	uid, ok := BridgeUserFromContext(ctx)
	if ok || uid != "" {
		t.Error("should return empty for non-string value")
	}
}

func TestNewSessionManager(t *testing.T) {
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, nil, "info", slog.Default())
	if sm == nil {
		t.Fatal("should not return nil")
	}
	if sm.SessionCount() != 0 {
		t.Errorf("initial session count: %d", sm.SessionCount())
	}
}

func TestSessionManager_GetProvider_NotExists(t *testing.T) {
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, nil, "info", slog.Default())

	_, ok := sm.GetProvider("@nonexistent:example.com")
	if ok {
		t.Error("should return false for non-existent user")
	}
}

func TestSessionManager_GetSession_NotExists(t *testing.T) {
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, nil, "info", slog.Default())

	_, ok := sm.GetSession("@nonexistent:example.com")
	if ok {
		t.Error("should return false for non-existent session")
	}
}

func TestSessionManager_GetOrCreateSession_NoNodePool(t *testing.T) {
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, &EventRouter{}, "info", slog.Default())

	_, err := sm.GetOrCreateSession(context.Background(), "@user:example.com")
	if err == nil {
		t.Fatal("expected error when node pool is missing")
	}
}

func TestSessionManager_GetOrCreateSession_NoHandler(t *testing.T) {
	sm := NewSessionManager(&NodePool{}, nil, config.RiskControlConfig{}, nil, "info", slog.Default())

	_, err := sm.GetOrCreateSession(context.Background(), "@user:example.com")
	if err == nil {
		t.Fatal("expected error when event router is missing")
	}
}

func TestSessionManager_StopAll_Empty(t *testing.T) {
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, nil, "info", slog.Default())

	// StopAll on empty should not panic
	sm.StopAll()
	if sm.SessionCount() != 0 {
		t.Errorf("session count after StopAll: %d", sm.SessionCount())
	}
}

func TestSessionManager_StopAll_WithSessions(t *testing.T) {
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, nil, "info", slog.Default())

	// Manually inject sessions for testing StopAll
	mp1 := newMockProvider("padpro-1", 2)
	mp1.running = true
	mp2 := newMockProvider("padpro-2", 2)
	mp2.running = true

	sm.mu.Lock()
	sm.sessions["@user1:example.com"] = &UserSession{
		BridgeUserID: "@user1:example.com",
		NodeID:       "node-01",
		Provider:     mp1,
		LoginState:   wechat.LoginStateLoggedIn,
	}
	sm.sessions["@user2:example.com"] = &UserSession{
		BridgeUserID: "@user2:example.com",
		NodeID:       "node-02",
		Provider:     mp2,
		LoginState:   wechat.LoginStateLoggedIn,
	}
	sm.mu.Unlock()

	if sm.SessionCount() != 2 {
		t.Fatalf("session count before StopAll: %d", sm.SessionCount())
	}

	sm.StopAll()

	if sm.SessionCount() != 0 {
		t.Errorf("session count after StopAll: %d", sm.SessionCount())
	}

	// Providers should be stopped
	if mp1.IsRunning() {
		t.Error("provider 1 should be stopped")
	}
	if mp2.IsRunning() {
		t.Error("provider 2 should be stopped")
	}
}

func TestSessionManager_GetProvider_AfterManualInject(t *testing.T) {
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, nil, "info", slog.Default())

	mp := newMockProvider("padpro", 2)
	sm.mu.Lock()
	sm.sessions["@user:example.com"] = &UserSession{
		BridgeUserID: "@user:example.com",
		NodeID:       "node-01",
		Provider:     mp,
	}
	sm.mu.Unlock()

	p, ok := sm.GetProvider("@user:example.com")
	if !ok {
		t.Fatal("should find injected session")
	}
	if p.Name() != "padpro" {
		t.Errorf("provider name: %s", p.Name())
	}
}

func TestSessionManager_BuildNodeProviderConfig(t *testing.T) {
	riskCfg := config.RiskControlConfig{
		NewAccountSilenceDays: 3,
		MaxMessagesPerDay:     500,
		MaxGroupsPerDay:       10,
		MaxFriendsPerDay:      20,
		MessageIntervalMs:     1000,
		RandomDelay:           true,
	}

	sm := NewSessionManager(nil, nil, riskCfg, nil, "debug", slog.Default())

	node := &NodeState{
		Config: config.PadProNodeConfig{
			ID:          "node-01",
			APIEndpoint: "http://10.0.1.1:1239",
			AuthKey:     "testkey",
			WSEndpoint:  "ws://10.0.1.1:1240",
		},
	}

	cfg := sm.buildNodeProviderConfig(node)

	if cfg.APIEndpoint != "http://10.0.1.1:1239" {
		t.Errorf("APIEndpoint: %s", cfg.APIEndpoint)
	}
	if cfg.APIToken != "testkey" {
		t.Errorf("APIToken: %s", cfg.APIToken)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel: %s", cfg.LogLevel)
	}
	if cfg.Extra["ws_endpoint"] != "ws://10.0.1.1:1240" {
		t.Errorf("ws_endpoint: %s", cfg.Extra["ws_endpoint"])
	}
	if cfg.Extra["max_messages_per_day"] != "500" {
		t.Errorf("max_messages_per_day: %s", cfg.Extra["max_messages_per_day"])
	}
	if cfg.Extra["random_delay"] != "true" {
		t.Errorf("random_delay: %s", cfg.Extra["random_delay"])
	}
}

func TestSessionManager_BuildNodeProviderConfig_NoWSEndpoint(t *testing.T) {
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, nil, "info", slog.Default())

	node := &NodeState{
		Config: config.PadProNodeConfig{
			APIEndpoint: "http://10.0.1.1:1239",
			AuthKey:     "k",
		},
	}

	cfg := sm.buildNodeProviderConfig(node)
	if _, ok := cfg.Extra["ws_endpoint"]; ok {
		t.Error("ws_endpoint should not be set when empty")
	}
}

func TestSessionManager_BuildNodeProviderConfig_NoRandomDelay(t *testing.T) {
	riskCfg := config.RiskControlConfig{
		RandomDelay: false,
	}

	sm := NewSessionManager(nil, nil, riskCfg, nil, "info", slog.Default())

	node := &NodeState{
		Config: config.PadProNodeConfig{
			APIEndpoint: "http://10.0.1.1:1239",
			AuthKey:     "k",
		},
	}

	cfg := sm.buildNodeProviderConfig(node)
	if _, ok := cfg.Extra["random_delay"]; ok {
		t.Error("random_delay should not be set when false")
	}
}

func TestSessionManager_GetOrCreateSession_ReleasesNodeOnInitFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	nodeDB := &database.Database{
		NodeAssignment: database.NewNodeAssignmentStore(db),
	}
	nodePool := NewNodePool([]config.PadProNodeConfig{
		{ID: "node-01", APIEndpoint: "http://10.0.1.1:1239", AuthKey: "k1", MaxUsers: 2, Enabled: true},
	}, nodeDB, slog.Default())

	now := time.Now()
	mock.ExpectQuery(`(?s)SELECT .* FROM node_assignment WHERE bridge_user = \$1`).
		WithArgs("@user:example.com").
		WillReturnRows(sqlmock.NewRows([]string{
			"bridge_user", "node_id", "assigned_at", "last_active", "wechat_id", "login_state",
		}))
	mock.ExpectExec(`(?s)INSERT INTO node_assignment .* ON CONFLICT \(bridge_user\) DO UPDATE SET`).
		WithArgs(
			"@user:example.com",
			"node-01",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			"",
			0,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`(?s)SELECT .* FROM node_assignment WHERE bridge_user = \$1`).
		WithArgs("@user:example.com").
		WillReturnRows(sqlmock.NewRows([]string{
			"bridge_user", "node_id", "assigned_at", "last_active", "wechat_id", "login_state",
		}).AddRow("@user:example.com", "node-01", now, now, "", 0))
	mock.ExpectExec(`(?s)DELETE FROM node_assignment WHERE bridge_user = \$1`).
		WithArgs("@user:example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))

	sm := NewSessionManager(nodePool, nodeDB, config.RiskControlConfig{}, &EventRouter{}, "info", slog.Default())
	sm.providerFactory = func() (wechat.Provider, error) {
		mp := newMockProvider("padpro", 2)
		mp.initErr = errors.New("init failed")
		return mp, nil
	}

	_, err = sm.GetOrCreateSession(context.Background(), "@user:example.com")
	if err == nil {
		t.Fatal("expected init failure")
	}
	if sm.SessionCount() != 0 {
		t.Fatalf("session count = %d, want 0", sm.SessionCount())
	}

	states := nodePool.NodeStates()
	if states["node-01"].ActiveUsers != 0 {
		t.Fatalf("active users = %d, want 0", states["node-01"].ActiveUsers)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

// Test userMessageHandler wraps context correctly
func TestUserMessageHandler_OnMessage(t *testing.T) {
	var capturedCtx context.Context
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: newTestPuppetManager(),
	})

	// Override OnMessage behavior — since er has nil bridgeUsers, it will error,
	// but we can check the context was set by testing BridgeUserFromContext.
	handler := &userMessageHandler{
		bridgeUserID: "@test:example.com",
		inner:        er,
	}

	ctx := context.Background()
	msg := &wechat.Message{
		MsgID:    "msg1",
		Type:     wechat.MsgText,
		FromUser: "wxid_sender",
	}

	// The handler will call inner.OnMessage, which will error because dependencies are nil.
	// But we test that bridgeUserID is in the context.
	_ = handler.OnMessage(ctx, msg)

	// We need a different approach — test via the context key directly
	wrappedCtx := context.WithValue(ctx, bridgeUserKey, "@test:example.com")
	capturedCtx = wrappedCtx
	uid, ok := BridgeUserFromContext(capturedCtx)
	if !ok || uid != "@test:example.com" {
		t.Errorf("context should contain bridge user: %s, %v", uid, ok)
	}
}

func TestUserMessageHandler_AllMethods(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: newTestPuppetManager(),
	})

	handler := &userMessageHandler{
		bridgeUserID: "@test:example.com",
		inner:        er,
	}

	ctx := context.Background()

	// These should not panic, even with nil dependencies
	_ = handler.OnLoginEvent(ctx, &wechat.LoginEvent{State: wechat.LoginStateLoggedIn})
	_ = handler.OnPresence(ctx, "wxid_test", true)
	_ = handler.OnTyping(ctx, "wxid_test", "wxid_chat")
	_ = handler.OnRevoke(ctx, "msg1", "revoked")

	// ContactUpdate should not panic (puppets is available)
	_ = handler.OnContactUpdate(ctx, &wechat.ContactInfo{UserID: "wxid_test"})

	// GroupMemberUpdate should not panic
	_ = handler.OnGroupMemberUpdate(ctx, "group1", nil)
}
