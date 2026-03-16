package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/n42/mautrix-wechat/internal/config"
	"github.com/n42/mautrix-wechat/internal/database"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// Bridge is the main entry point that ties all components together.
type Bridge struct {
	Config *config.Config
	DB     *database.Database
	Log    *slog.Logger

	Puppets         *PuppetManager
	EventRouter     *EventRouter
	ASHandler       *ASHandler
	Provider        wechat.Provider
	ProviderManager *ProviderManager
	Metrics         *Metrics
	Crypto          CryptoHelper

	// Multi-tenant fields
	SessionManager *SessionManager
	NodePool       *NodePool

	httpServer      *http.Server
	httpListener    net.Listener
	metricsServer   *http.Server
	metricsListener net.Listener
	mu              sync.Mutex
	running         bool
}

// New creates a new Bridge instance from the given configuration.
func New(cfg *config.Config, log *slog.Logger) (*Bridge, error) {
	b := &Bridge{
		Config: cfg,
		Log:    log,
	}

	// Initialize database
	db, err := database.New(cfg.Database.Type, cfg.Database.URI, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	if err != nil {
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	b.DB = db

	return b, nil
}

// Start initializes all components and starts the bridge.
func (b *Bridge) Start(ctx context.Context) (err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return fmt.Errorf("bridge is already running")
	}

	defer func() {
		if err != nil {
			b.cleanupStartupLocked()
		}
	}()

	b.Log.Info("starting mautrix-wechat bridge")

	// Initialize metrics
	b.Metrics = NewMetrics()

	// Run database migrations
	if err := b.DB.RunMigrations(ctx); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	b.Log.Info("database migrations complete")

	// Check if we're in multi-tenant mode
	multiTenant := b.Config.Providers.PadPro.Enabled && b.Config.Providers.PadPro.MultiTenant

	// Select and initialize provider(s) — skip in multi-tenant mode
	if !multiTenant {
		if b.Config.Providers.Failover.Enabled {
			if err := b.initProviderManager(ctx); err != nil {
				return fmt.Errorf("init provider manager: %w", err)
			}
		} else {
			provider, err := b.selectProvider()
			if err != nil {
				return fmt.Errorf("select provider: %w", err)
			}
			b.Provider = provider
		}
	}

	// Initialize puppet manager (MatrixClient will be set once we have a real implementation)
	b.Puppets = NewPuppetManager(
		b.Config.Homeserver.Domain,
		b.Config.Bridge.UsernameTemplate,
		b.Config.Bridge.DisplaynameTemplate,
		b.DB.User,
		nil, // MatrixClient — injected later or via a stub
	)

	// Initialize crypto helper
	b.Crypto = NewCryptoHelper(
		b.Log.With("component", "crypto"),
		b.Config.Bridge.Encryption,
		nil, // CryptoStore — injected when E2EE store is available
		nil, // MatrixClient
		fmt.Sprintf("@%s:%s", b.Config.AppService.Bot.Username, b.Config.Homeserver.Domain),
	)
	if err := b.Crypto.Init(ctx); err != nil {
		b.Log.Warn("crypto helper initialization failed, E2EE disabled", "error", err)
		b.Crypto = &noopCryptoHelper{}
	}

	// Initialize event router with metrics and crypto
	b.EventRouter = NewEventRouter(EventRouterConfig{
		Log:          b.Log.With("component", "event_router"),
		Puppets:      b.Puppets,
		Processor:    &defaultMessageProcessor{},
		Provider:     b.Provider, // nil in multi-tenant mode (per-user providers via SessionManager)
		Rooms:        b.DB.RoomMapping,
		Messages:     b.DB.MessageMapping,
		BridgeUsers:  b.DB.BridgeUser,
		GroupMembers: b.DB.GroupMember,
		MatrixClient: nil, // Injected when real client available
		Crypto:       b.Crypto,
		Metrics:      b.Metrics,
		MultiTenant:  multiTenant,
	})

	if multiTenant {
		// === Multi-tenant initialization ===

		// 1. Create NodePool from configured nodes
		b.NodePool = NewNodePool(
			b.Config.Providers.PadPro.Nodes,
			b.DB,
			b.Log.With("component", "node_pool"),
		)

		// 2. Drop stale assignments that cannot be restored after a restart.
		cleaned, err := b.DB.NodeAssignment.DeleteExceptLoginState(ctx, int(wechat.LoginStateLoggedIn))
		if err != nil {
			return fmt.Errorf("cleanup stale node assignments: %w", err)
		}
		if cleaned > 0 {
			b.Log.Info("cleaned stale node assignments", "count", cleaned)
		}

		// 3. Restore user counts from DB
		if err := b.NodePool.LoadAssignments(ctx); err != nil {
			return fmt.Errorf("load node assignments: %w", err)
		}

		// 4. Start health check loop
		go b.NodePool.HealthCheckLoop(ctx, 30*time.Second)

		// 5. Create SessionManager (needs EventRouter; EventRouter needs SessionManager — resolve via setter)
		b.SessionManager = NewSessionManager(
			b.NodePool,
			b.DB,
			b.Config.Providers.PadPro.RiskControl,
			b.EventRouter,
			b.Config.Logging.MinLevel,
			b.Log.With("component", "session_manager"),
		)

		// 6. Inject SessionManager back into EventRouter
		b.EventRouter.SetSessionManager(b.SessionManager)

		// 7. Restore previously logged-in sessions
		if err := b.SessionManager.RestoreSessions(ctx); err != nil {
			b.Log.Error("failed to restore sessions (non-fatal)", "error", err)
		}

		b.Log.Info("multi-tenant mode initialized",
			"nodes", len(b.Config.Providers.PadPro.Nodes),
			"restored_sessions", b.SessionManager.SessionCount())

	} else {
		// === Single-provider initialization (unchanged) ===

		// Initialize provider with event router as message handler
		if b.ProviderManager != nil {
			b.ProviderManager.SetHandler(b.EventRouter)
			b.ProviderManager.SetOnSwitch(func(newProvider wechat.Provider) {
				b.Provider = newProvider
				b.EventRouter.SetProvider(newProvider)
				b.Log.Info("active provider switched",
					"name", newProvider.Name(), "tier", newProvider.Tier())
			})
		} else {
			providerCfg := b.buildProviderConfig()
			if err := b.Provider.Init(providerCfg, b.EventRouter); err != nil {
				return fmt.Errorf("initialize provider %s: %w", b.Provider.Name(), err)
			}
		}
	}

	// Initialize AS handler
	b.ASHandler = NewASHandler(
		b.Log.With("component", "as_handler"),
		b.Config.AppService.HSToken,
		b.EventRouter,
	)

	// Start HTTP server for AS API
	listenAddr := fmt.Sprintf("%s:%d", b.Config.AppService.Hostname, b.Config.AppService.Port)
	b.httpServer = &http.Server{
		Addr:         listenAddr,
		Handler:      b.ASHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if err := b.prepareHTTPServer(); err != nil {
		return fmt.Errorf("bind AS HTTP server: %w", err)
	}

	// Start metrics HTTP server (if enabled)
	if b.Config.Metrics.Enabled {
		if err := b.prepareMetricsServer(); err != nil {
			return fmt.Errorf("bind metrics server: %w", err)
		}
	}

	// Start provider(s) — only in non-multi-tenant mode
	if !multiTenant {
		if b.ProviderManager != nil {
			if err := b.ProviderManager.Start(ctx); err != nil {
				return fmt.Errorf("start provider manager: %w", err)
			}
			b.Provider = b.ProviderManager.Active()
			b.Log.Info("provider manager started",
				"active", b.ProviderManager.ActiveName(),
				"tier", b.ProviderManager.ActiveTier(),
				"providers", b.ProviderManager.ProviderCount())
		} else {
			if err := b.Provider.Start(ctx); err != nil {
				return fmt.Errorf("start provider %s: %w", b.Provider.Name(), err)
			}
			b.Metrics.SetConnected(b.Provider.IsRunning() && b.Provider.GetLoginState() == wechat.LoginStateLoggedIn)
			b.Metrics.SetLoginState(int(b.Provider.GetLoginState()))
			b.Log.Info("provider started", "name", b.Provider.Name(), "tier", b.Provider.Tier())
		}
	}

	b.running = true
	b.servePreparedServers()
	b.Log.Info("mautrix-wechat bridge started successfully")

	return nil
}

// Stop gracefully shuts down all bridge components.
func (b *Bridge) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		return nil
	}

	b.Log.Info("stopping mautrix-wechat bridge")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop metrics server
	if b.metricsServer != nil {
		if err := b.metricsServer.Shutdown(shutdownCtx); err != nil {
			b.Log.Error("metrics server shutdown error", "error", err)
		}
		b.metricsServer = nil
		b.metricsListener = nil
	}

	// Stop HTTP server
	if b.httpServer != nil {
		if err := b.httpServer.Shutdown(shutdownCtx); err != nil {
			b.Log.Error("HTTP server shutdown error", "error", err)
		}
		b.httpServer = nil
		b.httpListener = nil
	}

	// Stop multi-tenant components
	if b.SessionManager != nil {
		b.SessionManager.StopAll()
	}
	if b.NodePool != nil {
		b.NodePool.Stop()
	}

	// Stop provider(s) — single-provider mode
	if b.ProviderManager != nil {
		if err := b.ProviderManager.Stop(); err != nil {
			b.Log.Error("provider manager stop error", "error", err)
		}
	} else if b.Provider != nil {
		if err := b.Provider.Stop(); err != nil {
			b.Log.Error("provider stop error", "error", err)
		}
	}

	// Close crypto helper
	if b.Crypto != nil {
		if err := b.Crypto.Close(); err != nil {
			b.Log.Error("crypto close error", "error", err)
		}
	}

	// Close database
	if b.DB != nil {
		if err := b.DB.Close(); err != nil {
			b.Log.Error("database close error", "error", err)
		}
	}

	b.running = false
	b.Log.Info("mautrix-wechat bridge stopped")

	return nil
}

// Run starts the bridge and blocks until a shutdown signal is received.
func (b *Bridge) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := b.Start(ctx); err != nil {
		return err
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	b.Log.Info("received shutdown signal", "signal", sig)

	return b.Stop()
}

// enabledProviders returns the provider entries in tier priority order.
func (b *Bridge) enabledProviders() []struct {
	name    string
	enabled bool
} {
	// Emit deprecation warning for iPad (GeWeChat) provider
	if b.Config.Providers.IPad.Enabled {
		b.Log.Warn("iPad (GeWeChat) provider is DEPRECATED: GeWeChat was archived on 2025-05-03 due to WeChat legal enforcement. Migrate to PadPro provider.")
	}

	return []struct {
		name    string
		enabled bool
	}{
		{"wecom", b.Config.Providers.WeCom.Enabled},
		{"padpro", b.Config.Providers.PadPro.Enabled},
		{"ipad", b.Config.Providers.IPad.Enabled},
		{"pchook", b.Config.Providers.PCHook.Enabled},
	}
}

// selectProvider chooses the highest-priority enabled provider.
func (b *Bridge) selectProvider() (wechat.Provider, error) {
	for _, pn := range b.enabledProviders() {
		if !pn.enabled {
			continue
		}

		p, err := wechat.DefaultRegistry.Create(pn.name)
		if err != nil {
			b.Log.Warn("provider registered but creation failed",
				"name", pn.name, "error", err)
			continue
		}

		b.Log.Info("selected provider", "name", pn.name, "tier", p.Tier())
		return p, nil
	}

	return nil, fmt.Errorf("no enabled provider is registered; available: %v", wechat.DefaultRegistry.List())
}

// buildProviderConfig builds a ProviderConfig from the bridge configuration.
// Used in non-failover mode; delegates to buildProviderConfigFor with the active provider name.
func (b *Bridge) buildProviderConfig() *wechat.ProviderConfig {
	// Find the first enabled provider name
	for _, pn := range b.enabledProviders() {
		if pn.enabled {
			return b.buildProviderConfigFor(pn.name)
		}
	}

	// Fallback (should not reach here — validation ensures at least one provider)
	return &wechat.ProviderConfig{
		LogLevel: b.Config.Logging.MinLevel,
		Extra:    make(map[string]string),
	}
}

func (b *Bridge) prepareHTTPServer() error {
	listener, err := net.Listen("tcp", b.httpServer.Addr)
	if err != nil {
		return err
	}
	b.httpListener = listener
	return nil
}

// prepareMetricsServer binds the dedicated Prometheus/health HTTP server.
func (b *Bridge) prepareMetricsServer() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", b.Metrics.Handler())
	mux.HandleFunc("/health", b.handleHealth)

	b.metricsServer = &http.Server{
		Addr:         b.Config.Metrics.Listen,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	listener, err := net.Listen("tcp", b.Config.Metrics.Listen)
	if err != nil {
		return err
	}
	b.metricsListener = listener

	return nil
}

func (b *Bridge) servePreparedServers() {
	if b.httpServer != nil && b.httpListener != nil {
		b.serveServer("AS HTTP server", b.httpServer, b.httpListener)
	}
	if b.metricsServer != nil && b.metricsListener != nil {
		b.serveServer("metrics server", b.metricsServer, b.metricsListener)
	}
}

func (b *Bridge) serveServer(name string, server *http.Server, listener net.Listener) {
	go func() {
		b.Log.Info(name+" listening", "addr", listener.Addr().String())
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			b.Log.Error(name+" error", "error", err)
		}
	}()
}

func (b *Bridge) cleanupStartupLocked() {
	if b.metricsListener != nil {
		if err := b.metricsListener.Close(); err != nil {
			b.Log.Error("metrics listener cleanup error", "error", err)
		}
		b.metricsListener = nil
	}
	b.metricsServer = nil

	if b.httpListener != nil {
		if err := b.httpListener.Close(); err != nil {
			b.Log.Error("HTTP listener cleanup error", "error", err)
		}
		b.httpListener = nil
	}
	b.httpServer = nil

	if b.SessionManager != nil {
		b.SessionManager.StopAll()
	}
	if b.NodePool != nil {
		b.NodePool.Stop()
	}

	if b.ProviderManager != nil {
		if stopErr := b.ProviderManager.Stop(); stopErr != nil {
			b.Log.Error("provider manager cleanup error", "error", stopErr)
		}
	} else if b.Provider != nil && b.Provider.IsRunning() {
		if stopErr := b.Provider.Stop(); stopErr != nil {
			b.Log.Error("provider cleanup error", "error", stopErr)
		}
	}

	if b.Crypto != nil {
		if closeErr := b.Crypto.Close(); closeErr != nil {
			b.Log.Error("crypto cleanup error", "error", closeErr)
		}
	}

	if b.Metrics != nil {
		b.Metrics.SetConnected(false)
		b.Metrics.SetLoginState(int(wechat.LoginStateLoggedOut))
	}

	b.running = false
}

// handleHealth serves a comprehensive JSON health check response.
func (b *Bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := b.Metrics.HealthStatus()
	if b.Provider != nil {
		status["connected"] = b.Provider.IsRunning() && b.Provider.GetLoginState() == wechat.LoginStateLoggedIn
		status["login_state"] = int64(b.Provider.GetLoginState())
		status["provider"] = b.Provider.Name()
		status["provider_running"] = b.Provider.IsRunning()
	} else {
		status["provider"] = "none"
		status["provider_running"] = false
	}

	// Include multi-tenant node pool info if available
	if b.NodePool != nil {
		status["multi_tenant"] = true
		nodeStates := b.NodePool.NodeStates()
		nodeInfos := make([]map[string]interface{}, 0, len(nodeStates))
		for id, ns := range nodeStates {
			nodeInfos = append(nodeInfos, map[string]interface{}{
				"id":           id,
				"healthy":      ns.Healthy,
				"active_users": ns.ActiveUsers,
				"max_users":    ns.Config.MaxUsers,
				"endpoint":     ns.Config.APIEndpoint,
			})
		}
		status["nodes"] = nodeInfos
		if b.SessionManager != nil {
			status["active_sessions"] = b.SessionManager.SessionCount()
		}
	}

	// Include failover info if available
	if b.ProviderManager != nil {
		status["failover_enabled"] = true
		status["active_provider"] = b.ProviderManager.ActiveName()
		status["active_tier"] = b.ProviderManager.ActiveTier()
		status["provider_count"] = b.ProviderManager.ProviderCount()

		states := b.ProviderManager.GetProviderStates()
		providerInfos := make([]map[string]interface{}, len(states))
		for i, ps := range states {
			providerInfos[i] = map[string]interface{}{
				"name":   ps.Provider.Name(),
				"tier":   ps.Provider.Tier(),
				"active": ps.Active,
				"fails":  ps.ConsecutiveFails,
				"checks": ps.TotalChecks,
			}
		}
		status["providers"] = providerInfos
	}

	w.Header().Set("Content-Type", "application/json")

	connected, _ := status["connected"].(bool)
	if !connected {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	data, err := json.Marshal(status)
	if err != nil {
		b.Log.Error("failed to marshal health status", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

// initProviderManager creates and populates the ProviderManager with all enabled providers.
func (b *Bridge) initProviderManager(ctx context.Context) error {
	foCfg := b.Config.Providers.Failover
	failoverCfg := FailoverConfig{
		Enabled:               true,
		HealthCheckInterval:   time.Duration(foCfg.HealthCheckIntervalS) * time.Second,
		FailureThreshold:      foCfg.FailureThreshold,
		RecoveryCheckInterval: time.Duration(foCfg.RecoveryCheckIntervalS) * time.Second,
		RecoveryThreshold:     foCfg.RecoveryThreshold,
	}

	pm := NewProviderManager(
		b.Log.With("component", "provider_manager"),
		failoverCfg,
		b.Metrics,
	)

	// Register enabled providers in tier order
	registered := 0
	for _, entry := range b.enabledProviders() {
		if !entry.enabled {
			continue
		}

		p, err := wechat.DefaultRegistry.Create(entry.name)
		if err != nil {
			b.Log.Warn("provider creation failed",
				"name", entry.name, "error", err)
			continue
		}

		cfg := b.buildProviderConfigFor(entry.name)
		pm.AddProvider(p, cfg)
		registered++

		b.Log.Info("registered provider for failover",
			"name", entry.name, "tier", p.Tier())
	}

	if registered == 0 {
		return fmt.Errorf("no providers could be registered")
	}

	b.ProviderManager = pm
	return nil
}

// buildProviderConfigFor builds a ProviderConfig for a specific provider.
func (b *Bridge) buildProviderConfigFor(name string) *wechat.ProviderConfig {
	cfg := &wechat.ProviderConfig{
		LogLevel: b.Config.Logging.MinLevel,
		Extra:    make(map[string]string),
	}

	switch name {
	case "wecom":
		cfg.CorpID = b.Config.Providers.WeCom.CorpID
		cfg.AppSecret = b.Config.Providers.WeCom.AppSecret
		cfg.AgentID = b.Config.Providers.WeCom.AgentID
		cfg.Token = b.Config.Providers.WeCom.Callback.Token
		cfg.AESKey = b.Config.Providers.WeCom.Callback.AESKey
	case "padpro":
		cfg.APIEndpoint = b.Config.Providers.PadPro.APIEndpoint
		cfg.APIToken = b.Config.Providers.PadPro.AuthKey // Used as ?key= query parameter
		if b.Config.Providers.PadPro.WSEndpoint != "" {
			cfg.Extra["ws_endpoint"] = b.Config.Providers.PadPro.WSEndpoint
		}
		if b.Config.Providers.PadPro.WebhookURL != "" {
			cfg.Extra["webhook_url"] = b.Config.Providers.PadPro.WebhookURL
		}
		if b.Config.Providers.PadPro.CallbackPort > 0 {
			cfg.Extra["callback_port"] = fmt.Sprintf("%d", b.Config.Providers.PadPro.CallbackPort)
		}
		// Pass risk control settings via Extra
		rc := b.Config.Providers.PadPro.RiskControl
		cfg.Extra["max_messages_per_day"] = fmt.Sprintf("%d", rc.MaxMessagesPerDay)
		cfg.Extra["max_groups_per_day"] = fmt.Sprintf("%d", rc.MaxGroupsPerDay)
		cfg.Extra["max_friends_per_day"] = fmt.Sprintf("%d", rc.MaxFriendsPerDay)
		cfg.Extra["message_interval_ms"] = fmt.Sprintf("%d", rc.MessageIntervalMs)
		cfg.Extra["new_account_silence_days"] = fmt.Sprintf("%d", rc.NewAccountSilenceDays)
		if rc.RandomDelay {
			cfg.Extra["random_delay"] = "true"
		}
	case "ipad":
		cfg.APIEndpoint = b.Config.Providers.IPad.APIEndpoint
		cfg.APIToken = b.Config.Providers.IPad.APIToken
		cfg.CallbackURL = b.Config.Providers.IPad.CallbackURL
		// Pass risk control settings via Extra
		rc := b.Config.Providers.IPad.RiskControl
		cfg.Extra["max_messages_per_day"] = fmt.Sprintf("%d", rc.MaxMessagesPerDay)
		cfg.Extra["max_groups_per_day"] = fmt.Sprintf("%d", rc.MaxGroupsPerDay)
		cfg.Extra["max_friends_per_day"] = fmt.Sprintf("%d", rc.MaxFriendsPerDay)
		cfg.Extra["message_interval_ms"] = fmt.Sprintf("%d", rc.MessageIntervalMs)
		cfg.Extra["new_account_silence_days"] = fmt.Sprintf("%d", rc.NewAccountSilenceDays)
		if rc.RandomDelay {
			cfg.Extra["random_delay"] = "true"
		}
	case "pchook":
		cfg.RPCPort = 19088
		if b.Config.Providers.PCHook.RPCEndpoint != "" {
			cfg.Extra["rpc_endpoint"] = b.Config.Providers.PCHook.RPCEndpoint
		}
	}

	return cfg
}
