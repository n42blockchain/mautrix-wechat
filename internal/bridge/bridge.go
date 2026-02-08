package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	Config   *config.Config
	DB       *database.Database
	Log      *slog.Logger

	Puppets         *PuppetManager
	EventRouter     *EventRouter
	ASHandler       *ASHandler
	Provider        wechat.Provider
	ProviderManager *ProviderManager
	Metrics         *Metrics
	Crypto          CryptoHelper

	httpServer    *http.Server
	metricsServer *http.Server
	mu            sync.Mutex
	running       bool
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
func (b *Bridge) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return fmt.Errorf("bridge is already running")
	}

	b.Log.Info("starting mautrix-wechat bridge")

	// Initialize metrics
	b.Metrics = NewMetrics()

	// Run database migrations
	if err := b.DB.RunMigrations(ctx); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	b.Log.Info("database migrations complete")

	// Select and initialize provider(s)
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
		Provider:     b.Provider,
		Rooms:        b.DB.RoomMapping,
		Messages:     b.DB.MessageMapping,
		BridgeUsers:  b.DB.BridgeUser,
		GroupMembers: b.DB.GroupMember,
		MatrixClient: nil, // Injected when real client available
		Crypto:       b.Crypto,
		Metrics:      b.Metrics,
	})

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

	go func() {
		b.Log.Info("AS HTTP server listening", "addr", listenAddr)
		if err := b.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			b.Log.Error("HTTP server error", "error", err)
		}
	}()

	// Start metrics HTTP server (if enabled)
	if b.Config.Metrics.Enabled {
		b.startMetricsServer()
	}

	// Start provider(s)
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
		b.Log.Info("provider started", "name", b.Provider.Name(), "tier", b.Provider.Tier())
	}

	b.running = true
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
	}

	// Stop HTTP server
	if b.httpServer != nil {
		if err := b.httpServer.Shutdown(shutdownCtx); err != nil {
			b.Log.Error("HTTP server shutdown error", "error", err)
		}
	}

	// Stop provider(s)
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
	return []struct {
		name    string
		enabled bool
	}{
		{"wecom", b.Config.Providers.WeCom.Enabled},
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
func (b *Bridge) buildProviderConfig() *wechat.ProviderConfig {
	cfg := &wechat.ProviderConfig{
		LogLevel: b.Config.Logging.MinLevel,
		Extra:    make(map[string]string),
	}

	if b.Config.Providers.WeCom.Enabled {
		cfg.CorpID = b.Config.Providers.WeCom.CorpID
		cfg.AppSecret = b.Config.Providers.WeCom.AppSecret
		cfg.AgentID = b.Config.Providers.WeCom.AgentID
		cfg.Token = b.Config.Providers.WeCom.Callback.Token
		cfg.AESKey = b.Config.Providers.WeCom.Callback.AESKey
	}

	if b.Config.Providers.IPad.Enabled {
		cfg.APIEndpoint = b.Config.Providers.IPad.APIEndpoint
		cfg.APIToken = b.Config.Providers.IPad.APIToken
		cfg.CallbackURL = b.Config.Providers.IPad.CallbackURL
	}

	if b.Config.Providers.PCHook.Enabled {
		cfg.RPCPort = 19088 // default
	}

	return cfg
}

// startMetricsServer starts a dedicated HTTP server for Prometheus metrics and health checks.
func (b *Bridge) startMetricsServer() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", b.Metrics.Handler())
	mux.HandleFunc("/health", b.handleHealth)

	b.metricsServer = &http.Server{
		Addr:         b.Config.Metrics.Listen,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		b.Log.Info("metrics server listening", "addr", b.Config.Metrics.Listen)
		if err := b.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			b.Log.Error("metrics server error", "error", err)
		}
	}()
}

// handleHealth serves a comprehensive JSON health check response.
func (b *Bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := b.Metrics.HealthStatus()
	if b.Provider != nil {
		status["provider"] = b.Provider.Name()
		status["provider_running"] = b.Provider.IsRunning()
	} else {
		status["provider"] = "none"
		status["provider_running"] = false
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
				"name":    ps.Provider.Name(),
				"tier":    ps.Provider.Tier(),
				"active":  ps.Active,
				"fails":   ps.ConsecutiveFails,
				"checks":  ps.TotalChecks,
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
