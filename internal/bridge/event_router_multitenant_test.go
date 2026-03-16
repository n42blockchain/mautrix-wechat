package bridge

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/n42/mautrix-wechat/internal/config"
	"github.com/n42/mautrix-wechat/internal/database"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func TestEventRouter_MultiTenantFields(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
	})

	if !er.multiTenant {
		t.Error("multiTenant should be true")
	}
	if er.sessionManager != nil {
		t.Error("sessionManager should be nil until SetSessionManager called")
	}
}

func TestEventRouter_SetSessionManager(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: newTestPuppetManager(),
	})

	if er.multiTenant {
		t.Error("should initially not be multi-tenant")
	}

	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, er, "info", slog.Default())
	er.SetSessionManager(sm)

	if !er.multiTenant {
		t.Error("SetSessionManager should enable multiTenant")
	}
	if er.sessionManager != sm {
		t.Error("sessionManager not set correctly")
	}
}

func TestEventRouter_GetProviderForRoom_SingleMode(t *testing.T) {
	mp := newMockProvider("padpro", 2)
	er := NewEventRouter(EventRouterConfig{
		Log:      slog.Default(),
		Puppets:  newTestPuppetManager(),
		Provider: mp,
	})

	room := &database.RoomMapping{
		WeChatChatID: "group1",
		BridgeUser:   "@user:example.com",
	}

	ctx := context.Background()
	p, err := er.getProviderForRoom(ctx, room)
	if err != nil {
		t.Fatalf("getProviderForRoom error: %v", err)
	}
	if p != mp {
		t.Error("should return the single provider")
	}
}

func TestEventRouter_GetProviderForRoom_MultiTenantMode(t *testing.T) {
	mp := newMockProvider("padpro", 2)
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
	})

	// Create SessionManager with an injected session
	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, er, "info", slog.Default())
	sm.mu.Lock()
	sm.sessions["@user:example.com"] = &UserSession{
		BridgeUserID: "@user:example.com",
		NodeID:       "node-01",
		Provider:     mp,
	}
	sm.mu.Unlock()

	er.SetSessionManager(sm)

	room := &database.RoomMapping{
		WeChatChatID: "group1",
		BridgeUser:   "@user:example.com",
	}

	ctx := context.Background()
	p, err := er.getProviderForRoom(ctx, room)
	if err != nil {
		t.Fatalf("getProviderForRoom error: %v", err)
	}
	if p != mp {
		t.Error("should return the per-user provider")
	}
}

func TestEventRouter_GetProviderForRoom_MultiTenantNoSession(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
	})

	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, er, "info", slog.Default())
	er.SetSessionManager(sm)

	room := &database.RoomMapping{
		WeChatChatID: "group1",
		BridgeUser:   "@unknown:example.com",
	}

	ctx := context.Background()
	_, err := er.getProviderForRoom(ctx, room)
	if err == nil {
		t.Error("should error when no session exists for user")
	}
}

func TestEventRouter_GetProviderForUser_SingleMode(t *testing.T) {
	mp := newMockProvider("padpro", 2)
	er := NewEventRouter(EventRouterConfig{
		Log:      slog.Default(),
		Puppets:  newTestPuppetManager(),
		Provider: mp,
	})

	ctx := context.Background()
	p, err := er.getProviderForUser(ctx, "@user:example.com")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if p != mp {
		t.Error("should return default provider in single mode")
	}
}

func TestEventRouter_GetProviderForUser_MultiTenantNoManager(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
	})
	// sessionManager is nil

	ctx := context.Background()
	_, err := er.getProviderForUser(ctx, "@user:example.com")
	if err == nil {
		t.Error("should error when sessionManager is nil")
	}
}

func TestEventRouter_GetProviderForContext_SingleMode(t *testing.T) {
	mp := newMockProvider("padpro", 2)
	er := NewEventRouter(EventRouterConfig{
		Log:      slog.Default(),
		Puppets:  newTestPuppetManager(),
		Provider: mp,
	})

	ctx := context.Background()
	p, err := er.getProviderForContext(ctx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if p != mp {
		t.Error("should return default provider")
	}
}

func TestEventRouter_GetProviderForContext_MultiTenantWithContext(t *testing.T) {
	mp := newMockProvider("padpro", 2)
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
	})

	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, er, "info", slog.Default())
	sm.mu.Lock()
	sm.sessions["@user:example.com"] = &UserSession{
		Provider: mp,
	}
	sm.mu.Unlock()
	er.SetSessionManager(sm)

	ctx := context.WithValue(context.Background(), bridgeUserKey, "@user:example.com")
	p, err := er.getProviderForContext(ctx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if p != mp {
		t.Error("should return per-user provider from context")
	}
}

func TestEventRouter_GetProviderForContext_MultiTenantNoContext(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
	})

	sm := NewSessionManager(nil, nil, config.RiskControlConfig{}, er, "info", slog.Default())
	er.SetSessionManager(sm)

	// No bridge user in context should error instead of silently falling back.
	ctx := context.Background()
	_, err := er.getProviderForContext(ctx)
	if err == nil {
		t.Fatal("expected error when multi-tenant context is missing")
	}
}

func TestEventRouter_FindBridgeUser_MultiTenantMissingContext(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
	})

	_, err := er.findBridgeUser(context.Background())
	if err == nil {
		t.Fatal("expected error when multi-tenant context is missing")
	}
}

func TestEventRouter_FindBridgeUser_MultiTenantFromContext(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
		BridgeUsers: nil, // will cause error when trying to fetch from store
	})

	// Multi-tenant with context user but nil bridgeUsers store → error
	ctx := context.WithValue(context.Background(), bridgeUserKey, "@user:example.com")
	_, err := er.findBridgeUser(ctx)
	if err == nil {
		t.Error("should error when bridgeUsers store is nil")
	}
}

func TestEventRouter_FindBridgeUser_SingleModeFallback(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:     slog.Default(),
		Puppets: newTestPuppetManager(),
	})

	// Single mode, no bridge users store
	ctx := context.Background()
	_, err := er.findBridgeUser(ctx)
	if err == nil {
		t.Error("should error when bridgeUsers store is nil")
	}
}

func TestEventRouter_OnMessage_MultiTenantContextPropagation(t *testing.T) {
	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		MultiTenant: true,
	})

	// Context with bridge user
	ctx := context.WithValue(context.Background(), bridgeUserKey, "@user:example.com")
	msg := &wechat.Message{
		MsgID:    "msg1",
		Type:     wechat.MsgText,
		FromUser: "wxid_sender",
	}

	// Will error because bridgeUsers is nil, but should not panic
	err := er.OnMessage(ctx, msg)
	if err == nil {
		t.Error("should error with nil dependencies")
	}
}

func TestEventRouter_OnLoginEvent_MultiTenantMissingContext(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		BridgeUsers: database.NewBridgeUserStore(db),
		MultiTenant: true,
	})

	err = er.OnLoginEvent(context.Background(), &wechat.LoginEvent{State: wechat.LoginStateQRCode})
	if err == nil {
		t.Fatal("expected error when multi-tenant login event has no context")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected context error, got %v", err)
	}
}

func TestEventRouter_OnLoginEvent_MultiTenantPersistsState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	bridgeUsers := database.NewBridgeUserStore(db)
	nodeAssignments := database.NewNodeAssignmentStore(db)
	dbWrap := &database.Database{
		BridgeUser:     bridgeUsers,
		NodeAssignment: nodeAssignments,
	}

	er := NewEventRouter(EventRouterConfig{
		Log:         slog.Default(),
		Puppets:     newTestPuppetManager(),
		BridgeUsers: bridgeUsers,
		MultiTenant: true,
	})
	sm := NewSessionManager(nil, dbWrap, config.RiskControlConfig{}, er, "info", slog.Default())
	sm.mu.Lock()
	sm.sessions["@user:example.com"] = &UserSession{
		BridgeUserID: "@user:example.com",
		LoginState:   wechat.LoginStateQRCode,
	}
	sm.mu.Unlock()
	er.SetSessionManager(sm)

	mock.ExpectQuery(`(?s)SELECT .* FROM bridge_user WHERE matrix_user_id = \$1`).
		WithArgs("@user:example.com").
		WillReturnRows(sqlmock.NewRows([]string{
			"matrix_user_id", "wechat_id", "provider_type", "login_state",
			"management_room", "space_room", "last_login", "created_at",
		}))
	mock.ExpectExec(`(?s)INSERT INTO bridge_user .* ON CONFLICT \(matrix_user_id\) DO UPDATE SET`).
		WithArgs(
			"@user:example.com",
			"wxid_test",
			"padpro",
			int(wechat.LoginStateLoggedIn),
			"",
			"",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE node_assignment\s+SET login_state = \$1, wechat_id = \$2, last_active = NOW\(\)\s+WHERE bridge_user = \$3`).
		WithArgs(int(wechat.LoginStateLoggedIn), "wxid_test", "@user:example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))

	ctx := context.WithValue(context.Background(), bridgeUserKey, "@user:example.com")
	err = er.OnLoginEvent(ctx, &wechat.LoginEvent{
		State:  wechat.LoginStateLoggedIn,
		UserID: "wxid_test",
		Name:   "Tester",
	})
	if err != nil {
		t.Fatalf("OnLoginEvent error: %v", err)
	}

	session, ok := sm.GetSession("@user:example.com")
	if !ok {
		t.Fatal("session should still exist")
	}
	if session.LoginState != wechat.LoginStateLoggedIn {
		t.Fatalf("session login state = %v, want logged in", session.LoginState)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
