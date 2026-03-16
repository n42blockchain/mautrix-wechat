package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/n42/mautrix-wechat/internal/config"
	"github.com/n42/mautrix-wechat/internal/database"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// bridgeUserKey is the context key for passing bridge user ID through the message handler chain.
type bridgeUserKeyType struct{}

var bridgeUserKey = bridgeUserKeyType{}

// BridgeUserFromContext extracts the bridge user ID from a context.
func BridgeUserFromContext(ctx context.Context) (string, bool) {
	uid, ok := ctx.Value(bridgeUserKey).(string)
	return uid, ok
}

// UserSession represents an active WeChat session for a single bridge user.
type UserSession struct {
	BridgeUserID string
	NodeID       string
	Provider     wechat.Provider
	LoginState   wechat.LoginState
}

// SessionManager manages per-user WeChat provider sessions in multi-tenant mode.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*UserSession // matrix_user_id → session

	nodePool *NodePool
	db       *database.Database
	riskCfg  config.RiskControlConfig
	handler  *EventRouter
	log      *slog.Logger
	logLevel string

	providerFactory func() (wechat.Provider, error)
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(
	nodePool *NodePool,
	db *database.Database,
	riskCfg config.RiskControlConfig,
	handler *EventRouter,
	logLevel string,
	log *slog.Logger,
) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*UserSession),
		nodePool: nodePool,
		db:       db,
		riskCfg:  riskCfg,
		handler:  handler,
		logLevel: logLevel,
		log:      log,
		providerFactory: func() (wechat.Provider, error) {
			return wechat.DefaultRegistry.Create("padpro")
		},
	}
}

// GetOrCreateSession returns an existing session or creates a new one for the bridge user.
func (sm *SessionManager) GetOrCreateSession(ctx context.Context, bridgeUserID string) (*UserSession, error) {
	if sm.nodePool == nil {
		return nil, fmt.Errorf("node pool not initialized")
	}
	if sm.handler == nil {
		return nil, fmt.Errorf("event router not initialized")
	}

	// Fast path: check in-memory map
	sm.mu.RLock()
	if session, ok := sm.sessions[bridgeUserID]; ok {
		sm.mu.RUnlock()
		return session, nil
	}
	sm.mu.RUnlock()

	// Slow path: create new session
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double-check after acquiring write lock
	if session, ok := sm.sessions[bridgeUserID]; ok {
		return session, nil
	}

	// Assign a node
	node, err := sm.nodePool.AssignNode(ctx, bridgeUserID)
	if err != nil {
		return nil, fmt.Errorf("assign node for %s: %w", bridgeUserID, err)
	}

	// Create provider instance
	provider, err := sm.providerFactory()
	if err != nil {
		_ = sm.nodePool.ReleaseNode(ctx, bridgeUserID)
		return nil, fmt.Errorf("create padpro provider for %s: %w", bridgeUserID, err)
	}

	// Build per-node config
	providerCfg := sm.buildNodeProviderConfig(node)

	// Create a handler wrapper that injects bridge user ID into context
	wrappedHandler := &userMessageHandler{
		bridgeUserID: bridgeUserID,
		inner:        sm.handler,
	}

	// Initialize provider
	if err := provider.Init(providerCfg, wrappedHandler); err != nil {
		_ = sm.nodePool.ReleaseNode(ctx, bridgeUserID)
		return nil, fmt.Errorf("init provider for %s: %w", bridgeUserID, err)
	}

	// Start provider
	if err := provider.Start(ctx); err != nil {
		_ = provider.Stop()
		_ = sm.nodePool.ReleaseNode(ctx, bridgeUserID)
		return nil, fmt.Errorf("start provider for %s: %w", bridgeUserID, err)
	}

	session := &UserSession{
		BridgeUserID: bridgeUserID,
		NodeID:       node.Config.ID,
		Provider:     provider,
		LoginState:   wechat.LoginStateLoggedOut,
	}
	sm.sessions[bridgeUserID] = session

	sm.log.Info("created session",
		"bridge_user", bridgeUserID, "node", node.Config.ID)
	return session, nil
}

// GetProvider returns the provider for a given bridge user, if a session exists.
func (sm *SessionManager) GetProvider(bridgeUserID string) (wechat.Provider, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[bridgeUserID]
	if !ok {
		return nil, false
	}
	return session.Provider, true
}

// GetSession returns the session for a given bridge user, if it exists.
func (sm *SessionManager) GetSession(bridgeUserID string) (*UserSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[bridgeUserID]
	return session, ok
}

// LoginUser creates a session (if needed) and triggers the login flow.
func (sm *SessionManager) LoginUser(ctx context.Context, bridgeUserID string) error {
	session, err := sm.GetOrCreateSession(ctx, bridgeUserID)
	if err != nil {
		return err
	}

	if err := session.Provider.Login(ctx); err != nil {
		return fmt.Errorf("login for %s: %w", bridgeUserID, err)
	}

	session.LoginState = wechat.LoginStateQRCode

	// Update DB login state
	if err := sm.db.NodeAssignment.UpdateLoginState(ctx, bridgeUserID, int(wechat.LoginStateQRCode), ""); err != nil {
		sm.log.Error("failed to update login state", "error", err, "bridge_user", bridgeUserID)
	}

	return nil
}

// LogoutUser stops a user's provider and releases their node assignment.
func (sm *SessionManager) LogoutUser(ctx context.Context, bridgeUserID string) error {
	sm.mu.Lock()
	session, ok := sm.sessions[bridgeUserID]
	if !ok {
		sm.mu.Unlock()
		return nil
	}
	delete(sm.sessions, bridgeUserID)
	sm.mu.Unlock()

	// Stop the provider
	if err := session.Provider.Logout(ctx); err != nil {
		sm.log.Error("logout error", "error", err, "bridge_user", bridgeUserID)
	}
	if err := session.Provider.Stop(); err != nil {
		sm.log.Error("provider stop error", "error", err, "bridge_user", bridgeUserID)
	}

	// Release node
	if err := sm.nodePool.ReleaseNode(ctx, bridgeUserID); err != nil {
		sm.log.Error("release node error", "error", err, "bridge_user", bridgeUserID)
	}

	sm.log.Info("logged out user", "bridge_user", bridgeUserID, "node", session.NodeID)
	return nil
}

// RestoreSessions rebuilds provider sessions from the database for all logged-in users.
// Called during bridge startup.
func (sm *SessionManager) RestoreSessions(ctx context.Context) error {
	assignments, err := sm.db.NodeAssignment.GetAllByLoginState(ctx, int(wechat.LoginStateLoggedIn))
	if err != nil {
		return fmt.Errorf("get logged-in assignments: %w", err)
	}

	if len(assignments) == 0 {
		sm.log.Info("no sessions to restore")
		return nil
	}

	sm.log.Info("restoring sessions", "count", len(assignments))

	for _, a := range assignments {
		node, ok := sm.nodePool.GetNode(a.NodeID)
		if !ok {
			sm.log.Warn("skipping restore: node not found",
				"bridge_user", a.BridgeUser, "node", a.NodeID)
			continue
		}

		provider, err := sm.providerFactory()
		if err != nil {
			sm.log.Error("skipping restore: provider creation failed",
				"error", err, "bridge_user", a.BridgeUser)
			sm.restoreFailure(ctx, a.BridgeUser, "provider creation failed")
			continue
		}

		providerCfg := sm.buildNodeProviderConfig(node)
		wrappedHandler := &userMessageHandler{
			bridgeUserID: a.BridgeUser,
			inner:        sm.handler,
		}

		if err := provider.Init(providerCfg, wrappedHandler); err != nil {
			sm.log.Error("skipping restore: provider init failed",
				"error", err, "bridge_user", a.BridgeUser)
			sm.restoreFailure(ctx, a.BridgeUser, "provider init failed")
			continue
		}

		if err := provider.Start(ctx); err != nil {
			sm.log.Error("skipping restore: provider start failed",
				"error", err, "bridge_user", a.BridgeUser)
			_ = provider.Stop()
			sm.restoreFailure(ctx, a.BridgeUser, "provider start failed")
			continue
		}

		sm.mu.Lock()
		sm.sessions[a.BridgeUser] = &UserSession{
			BridgeUserID: a.BridgeUser,
			NodeID:       a.NodeID,
			Provider:     provider,
			LoginState:   wechat.LoginStateLoggedIn,
		}
		sm.mu.Unlock()

		sm.log.Info("restored session",
			"bridge_user", a.BridgeUser, "node", a.NodeID)
	}

	return nil
}

// StopAll stops all active provider sessions. Called during bridge shutdown.
func (sm *SessionManager) StopAll() {
	sm.mu.Lock()
	sessions := make(map[string]*UserSession, len(sm.sessions))
	for k, v := range sm.sessions {
		sessions[k] = v
	}
	sm.sessions = make(map[string]*UserSession)
	sm.mu.Unlock()

	for userID, session := range sessions {
		if err := session.Provider.Stop(); err != nil {
			sm.log.Error("error stopping provider during shutdown",
				"error", err, "bridge_user", userID)
		}
	}

	sm.log.Info("stopped all sessions", "count", len(sessions))
}

// SessionCount returns the number of active sessions.
func (sm *SessionManager) SessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// UpdateSessionLoginState updates the in-memory login state for an active session.
func (sm *SessionManager) UpdateSessionLoginState(bridgeUserID string, state wechat.LoginState) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[bridgeUserID]; ok {
		session.LoginState = state
	}
}

// buildNodeProviderConfig creates a ProviderConfig for a specific node.
func (sm *SessionManager) buildNodeProviderConfig(node *NodeState) *wechat.ProviderConfig {
	cfg := &wechat.ProviderConfig{
		LogLevel:    sm.logLevel,
		APIEndpoint: node.Config.APIEndpoint,
		APIToken:    node.Config.AuthKey,
		Extra:       make(map[string]string),
	}

	if node.Config.WSEndpoint != "" {
		cfg.Extra["ws_endpoint"] = node.Config.WSEndpoint
	}

	// Apply risk control settings
	rc := sm.riskCfg
	cfg.Extra["max_messages_per_day"] = fmt.Sprintf("%d", rc.MaxMessagesPerDay)
	cfg.Extra["max_groups_per_day"] = fmt.Sprintf("%d", rc.MaxGroupsPerDay)
	cfg.Extra["max_friends_per_day"] = fmt.Sprintf("%d", rc.MaxFriendsPerDay)
	cfg.Extra["message_interval_ms"] = fmt.Sprintf("%d", rc.MessageIntervalMs)
	cfg.Extra["new_account_silence_days"] = fmt.Sprintf("%d", rc.NewAccountSilenceDays)
	if rc.RandomDelay {
		cfg.Extra["random_delay"] = "true"
	}

	return cfg
}

func (sm *SessionManager) restoreFailure(ctx context.Context, bridgeUserID string, reason string) {
	if sm.db == nil || sm.db.NodeAssignment == nil {
		return
	}
	if err := sm.db.NodeAssignment.UpdateLoginState(ctx, bridgeUserID, int(wechat.LoginStateError), ""); err != nil {
		sm.log.Error("failed to mark restore failure",
			"error", err, "bridge_user", bridgeUserID, "reason", reason)
	}
	if sm.nodePool != nil {
		if err := sm.nodePool.ReleaseNode(ctx, bridgeUserID); err != nil {
			sm.log.Error("failed to release node after restore failure",
				"error", err, "bridge_user", bridgeUserID, "reason", reason)
		}
	}
}

// === userMessageHandler: per-user wrapper around EventRouter ===

// userMessageHandler wraps the EventRouter to inject bridge user ID into the context
// for every callback from a per-user provider instance.
type userMessageHandler struct {
	bridgeUserID string
	inner        *EventRouter
}

func (h *userMessageHandler) OnMessage(ctx context.Context, msg *wechat.Message) error {
	ctx = context.WithValue(ctx, bridgeUserKey, h.bridgeUserID)
	return h.inner.OnMessage(ctx, msg)
}

func (h *userMessageHandler) OnLoginEvent(ctx context.Context, evt *wechat.LoginEvent) error {
	ctx = context.WithValue(ctx, bridgeUserKey, h.bridgeUserID)
	return h.inner.OnLoginEvent(ctx, evt)
}

func (h *userMessageHandler) OnContactUpdate(ctx context.Context, contact *wechat.ContactInfo) error {
	ctx = context.WithValue(ctx, bridgeUserKey, h.bridgeUserID)
	return h.inner.OnContactUpdate(ctx, contact)
}

func (h *userMessageHandler) OnGroupMemberUpdate(ctx context.Context, groupID string, members []*wechat.GroupMember) error {
	ctx = context.WithValue(ctx, bridgeUserKey, h.bridgeUserID)
	return h.inner.OnGroupMemberUpdate(ctx, groupID, members)
}

func (h *userMessageHandler) OnPresence(ctx context.Context, userID string, online bool) error {
	ctx = context.WithValue(ctx, bridgeUserKey, h.bridgeUserID)
	return h.inner.OnPresence(ctx, userID, online)
}

func (h *userMessageHandler) OnTyping(ctx context.Context, userID string, chatID string) error {
	ctx = context.WithValue(ctx, bridgeUserKey, h.bridgeUserID)
	return h.inner.OnTyping(ctx, userID, chatID)
}

func (h *userMessageHandler) OnRevoke(ctx context.Context, msgID string, replaceTip string) error {
	ctx = context.WithValue(ctx, bridgeUserKey, h.bridgeUserID)
	return h.inner.OnRevoke(ctx, msgID, replaceTip)
}
