package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// FailoverConfig controls the provider failover behavior.
type FailoverConfig struct {
	// Enabled turns on automatic failover.
	Enabled bool `yaml:"enabled"`

	// HealthCheckInterval is how often to check provider health.
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`

	// FailureThreshold is the number of consecutive failures before failover.
	FailureThreshold int `yaml:"failure_threshold"`

	// RecoveryCheckInterval is how often to check if a higher-tier provider recovered.
	RecoveryCheckInterval time.Duration `yaml:"recovery_check_interval"`

	// RecoveryThreshold is the number of consecutive successes before promoting back.
	RecoveryThreshold int `yaml:"recovery_threshold"`
}

// DefaultFailoverConfig returns sensible defaults.
func DefaultFailoverConfig() FailoverConfig {
	return FailoverConfig{
		Enabled:               false,
		HealthCheckInterval:   30 * time.Second,
		FailureThreshold:      3,
		RecoveryCheckInterval: 2 * time.Minute,
		RecoveryThreshold:     3,
	}
}

// ProviderState tracks the health of a single provider.
type ProviderState struct {
	Provider          wechat.Provider
	Config            *wechat.ProviderConfig
	ConsecutiveFails  int
	ConsecutiveOK     int
	LastCheckTime     time.Time
	LastFailTime      time.Time
	LastSuccessTime   time.Time
	TotalChecks       int64
	TotalFailures     int64
	FailoverCount     int64
	Active            bool
}

// FailoverEvent records a failover occurrence for audit/metrics.
type FailoverEvent struct {
	Timestamp   time.Time
	FromName    string
	ToName      string
	FromTier    int
	ToTier      int
	Reason      string
}

// ProviderSwitchCallback is called when the active provider changes.
// The bridge uses this to update its own Provider reference.
type ProviderSwitchCallback func(newProvider wechat.Provider)

// ProviderManager manages multiple providers with health monitoring and failover.
type ProviderManager struct {
	mu       sync.RWMutex
	log      *slog.Logger
	cfg      FailoverConfig
	handler  wechat.MessageHandler
	metrics  *Metrics

	// Providers sorted by tier (ascending — lower tier = higher priority)
	providers []*ProviderState

	// Currently active provider
	activeIdx int

	// Callback when active provider switches
	onSwitch ProviderSwitchCallback

	// Failover history
	events    []FailoverEvent
	eventsMu  sync.Mutex

	stopCh   chan struct{}
	running  bool
}

// NewProviderManager creates a new ProviderManager.
func NewProviderManager(log *slog.Logger, cfg FailoverConfig, metrics *Metrics) *ProviderManager {
	return &ProviderManager{
		log:       log,
		cfg:       cfg,
		metrics:   metrics,
		providers: nil,
		activeIdx: -1,
		events:    nil,
		stopCh:    make(chan struct{}),
	}
}

// AddProvider registers a provider for failover management.
// Providers are maintained in tier order (lowest tier = highest priority).
func (pm *ProviderManager) AddProvider(p wechat.Provider, cfg *wechat.ProviderConfig) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	state := &ProviderState{
		Provider: p,
		Config:   cfg,
	}

	pm.providers = append(pm.providers, state)

	// Sort by tier
	sort.Slice(pm.providers, func(i, j int) bool {
		return pm.providers[i].Provider.Tier() < pm.providers[j].Provider.Tier()
	})
}

// SetHandler sets the message handler for all providers.
func (pm *ProviderManager) SetHandler(handler wechat.MessageHandler) {
	pm.handler = handler
}

// SetOnSwitch registers a callback that fires when the active provider changes.
func (pm *ProviderManager) SetOnSwitch(cb ProviderSwitchCallback) {
	pm.onSwitch = cb
}

// Start initializes and starts the highest-priority available provider.
func (pm *ProviderManager) Start(ctx context.Context) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.running {
		return nil
	}

	if len(pm.providers) == 0 {
		return fmt.Errorf("no providers registered")
	}

	// Re-create stopCh in case of restart after Stop
	pm.stopCh = make(chan struct{})

	// Initialize all providers (handler must be set before Start)
	for _, ps := range pm.providers {
		if err := ps.Provider.Init(ps.Config, pm.handler); err != nil {
			pm.log.Warn("provider init failed, skipping",
				"name", ps.Provider.Name(),
				"tier", ps.Provider.Tier(),
				"error", err)
			continue
		}
	}

	// Start the highest-priority provider
	if err := pm.activateBestProvider(ctx); err != nil {
		return fmt.Errorf("no provider could be started: %w", err)
	}

	pm.running = true

	if pm.metrics != nil {
		pm.metrics.SetConnected(true)
	}

	// Start health check loop if failover is enabled
	if pm.cfg.Enabled {
		go pm.healthCheckLoop()
	}

	return nil
}

// Stop shuts down all providers and the health check loop.
func (pm *ProviderManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if !pm.running {
		return nil
	}

	close(pm.stopCh)
	pm.running = false

	// Stop all running providers
	for _, ps := range pm.providers {
		if ps.Active {
			if err := ps.Provider.Stop(); err != nil {
				pm.log.Error("provider stop failed",
					"name", ps.Provider.Name(), "error", err)
			}
			ps.Active = false
		}
	}

	return nil
}

// Active returns the currently active provider, or nil if none.
func (pm *ProviderManager) Active() wechat.Provider {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.activeIdx < 0 || pm.activeIdx >= len(pm.providers) {
		return nil
	}
	return pm.providers[pm.activeIdx].Provider
}

// ActiveName returns the name of the active provider.
func (pm *ProviderManager) ActiveName() string {
	p := pm.Active()
	if p == nil {
		return "none"
	}
	return p.Name()
}

// ActiveTier returns the tier of the active provider.
func (pm *ProviderManager) ActiveTier() int {
	p := pm.Active()
	if p == nil {
		return 0
	}
	return p.Tier()
}

// ProviderCount returns the number of registered providers.
func (pm *ProviderManager) ProviderCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.providers)
}

// GetProviderStates returns a snapshot of all provider states.
func (pm *ProviderManager) GetProviderStates() []ProviderState {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	states := make([]ProviderState, len(pm.providers))
	for i, ps := range pm.providers {
		states[i] = *ps
	}
	return states
}

// GetFailoverHistory returns recent failover events.
func (pm *ProviderManager) GetFailoverHistory() []FailoverEvent {
	pm.eventsMu.Lock()
	defer pm.eventsMu.Unlock()

	events := make([]FailoverEvent, len(pm.events))
	copy(events, pm.events)
	return events
}

// activateBestProvider starts the highest-priority provider that can be started.
// Must be called with pm.mu held.
func (pm *ProviderManager) activateBestProvider(ctx context.Context) error {
	for i, ps := range pm.providers {
		if err := ps.Provider.Start(ctx); err != nil {
			pm.log.Warn("provider start failed",
				"name", ps.Provider.Name(),
				"tier", ps.Provider.Tier(),
				"error", err)
			continue
		}

		ps.Active = true
		pm.activeIdx = i
		pm.log.Info("activated provider",
			"name", ps.Provider.Name(),
			"tier", ps.Provider.Tier())

		if pm.metrics != nil {
			pm.metrics.SetLoginState(ps.Provider.Tier())
		}

		return nil
	}

	return fmt.Errorf("all providers failed to start")
}

// healthCheckLoop runs periodic health checks and handles failover/recovery.
func (pm *ProviderManager) healthCheckLoop() {
	healthTicker := time.NewTicker(pm.cfg.HealthCheckInterval)
	defer healthTicker.Stop()

	recoveryTicker := time.NewTicker(pm.cfg.RecoveryCheckInterval)
	defer recoveryTicker.Stop()

	for {
		select {
		case <-pm.stopCh:
			return

		case <-healthTicker.C:
			pm.checkActiveProvider()

		case <-recoveryTicker.C:
			pm.checkRecovery()
		}
	}
}

// checkActiveProvider verifies the active provider is healthy.
func (pm *ProviderManager) checkActiveProvider() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.activeIdx < 0 || pm.activeIdx >= len(pm.providers) {
		return
	}

	ps := pm.providers[pm.activeIdx]
	now := time.Now()
	ps.LastCheckTime = now
	ps.TotalChecks++

	healthy := pm.isProviderHealthy(ps)

	if healthy {
		ps.ConsecutiveFails = 0
		ps.ConsecutiveOK++
		ps.LastSuccessTime = now
	} else {
		ps.ConsecutiveFails++
		ps.ConsecutiveOK = 0
		ps.LastFailTime = now
		ps.TotalFailures++

		pm.log.Warn("active provider health check failed",
			"name", ps.Provider.Name(),
			"consecutive_fails", ps.ConsecutiveFails,
			"threshold", pm.cfg.FailureThreshold)

		if ps.ConsecutiveFails >= pm.cfg.FailureThreshold {
			pm.performFailover(ps)
		}
	}
}

// isProviderHealthy checks if a provider is operational.
func (pm *ProviderManager) isProviderHealthy(ps *ProviderState) bool {
	if !ps.Provider.IsRunning() {
		return false
	}
	if ps.Provider.GetLoginState() != wechat.LoginStateLoggedIn {
		return false
	}
	return true
}

// performFailover switches from the current provider to the next available one.
// Must be called with pm.mu held.
func (pm *ProviderManager) performFailover(failedPS *ProviderState) {
	pm.log.Error("provider failover triggered",
		"failed_provider", failedPS.Provider.Name(),
		"failed_tier", failedPS.Provider.Tier())

	// Stop the failed provider
	failedPS.Provider.Stop()
	failedPS.Active = false

	ctx := context.Background()

	// Try each subsequent provider in tier order
	for i := pm.activeIdx + 1; i < len(pm.providers); i++ {
		ps := pm.providers[i]
		if err := ps.Provider.Start(ctx); err != nil {
			pm.log.Warn("failover candidate failed to start",
				"name", ps.Provider.Name(), "error", err)
			continue
		}

		ps.Active = true
		ps.ConsecutiveFails = 0
		ps.ConsecutiveOK = 1

		event := FailoverEvent{
			Timestamp: time.Now(),
			FromName:  failedPS.Provider.Name(),
			ToName:    ps.Provider.Name(),
			FromTier:  failedPS.Provider.Tier(),
			ToTier:    ps.Provider.Tier(),
			Reason:    fmt.Sprintf("health check failed %d times", failedPS.ConsecutiveFails),
		}

		pm.eventsMu.Lock()
		pm.events = append(pm.events, event)
		// Keep only last 100 events
		if len(pm.events) > 100 {
			pm.events = pm.events[len(pm.events)-100:]
		}
		pm.eventsMu.Unlock()

		failedPS.FailoverCount++
		pm.activeIdx = i

		pm.log.Info("failover complete",
			"from", failedPS.Provider.Name(),
			"to", ps.Provider.Name(),
			"tier", ps.Provider.Tier())

		if pm.metrics != nil {
			pm.metrics.IncrReconnectAttempts()
			pm.metrics.IncrReconnectSuccesses()
			pm.metrics.SetConnected(true)
			pm.metrics.SetLoginState(ps.Provider.Tier())
		}

		if pm.onSwitch != nil {
			pm.onSwitch(ps.Provider)
		}

		return
	}

	pm.log.Error("all failover candidates exhausted, no active provider")
	pm.activeIdx = -1

	if pm.metrics != nil {
		pm.metrics.SetConnected(false)
	}
}

// checkRecovery probes higher-tier providers that previously failed to see if they recovered.
func (pm *ProviderManager) checkRecovery() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.activeIdx <= 0 {
		// Already on the highest priority provider, nothing to recover to
		return
	}

	// Check providers with lower tier (higher priority) than current active
	for i := 0; i < pm.activeIdx; i++ {
		ps := pm.providers[i]

		// Try to start the higher-priority provider
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := ps.Provider.Start(ctx)
		cancel()

		if err != nil {
			ps.ConsecutiveOK = 0
			continue
		}

		// Check if it's actually healthy
		if !pm.isProviderHealthy(ps) {
			ps.Provider.Stop()
			ps.ConsecutiveOK = 0
			continue
		}

		ps.ConsecutiveOK++

		if ps.ConsecutiveOK >= pm.cfg.RecoveryThreshold {
			// Promote back to this provider
			pm.performPromotion(i)
			return
		}

		// Not enough consecutive successes yet — stop it and check again later
		ps.Provider.Stop()
	}
}

// performPromotion switches from the current provider to a recovered higher-tier provider.
// Must be called with pm.mu held.
func (pm *ProviderManager) performPromotion(toIdx int) {
	oldPS := pm.providers[pm.activeIdx]
	newPS := pm.providers[toIdx]

	pm.log.Info("promoting to recovered provider",
		"from", oldPS.Provider.Name(),
		"to", newPS.Provider.Name(),
		"tier", newPS.Provider.Tier())

	// newPS is already started from checkRecovery

	// Stop old provider
	oldPS.Provider.Stop()
	oldPS.Active = false

	newPS.Active = true
	newPS.ConsecutiveFails = 0

	event := FailoverEvent{
		Timestamp: time.Now(),
		FromName:  oldPS.Provider.Name(),
		ToName:    newPS.Provider.Name(),
		FromTier:  oldPS.Provider.Tier(),
		ToTier:    newPS.Provider.Tier(),
		Reason:    "higher-tier provider recovered",
	}

	pm.eventsMu.Lock()
	pm.events = append(pm.events, event)
	if len(pm.events) > 100 {
		pm.events = pm.events[len(pm.events)-100:]
	}
	pm.eventsMu.Unlock()

	pm.activeIdx = toIdx

	if pm.metrics != nil {
		pm.metrics.IncrReconnectSuccesses()
		pm.metrics.SetConnected(true)
		pm.metrics.SetLoginState(newPS.Provider.Tier())
	}

	if pm.onSwitch != nil {
		pm.onSwitch(newPS.Provider)
	}
}

// ForceFailover manually triggers a failover to the next provider.
func (pm *ProviderManager) ForceFailover() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.activeIdx < 0 || pm.activeIdx >= len(pm.providers) {
		return fmt.Errorf("no active provider")
	}

	ps := pm.providers[pm.activeIdx]
	pm.performFailover(ps)

	if pm.activeIdx < 0 {
		return fmt.Errorf("failover failed: no available providers")
	}
	return nil
}

// ForceProvider switches to a specific provider by name.
func (pm *ProviderManager) ForceProvider(name string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for i, ps := range pm.providers {
		if ps.Provider.Name() != name {
			continue
		}

		ctx := context.Background()
		if err := ps.Provider.Start(ctx); err != nil {
			return fmt.Errorf("start provider %s: %w", name, err)
		}

		// Stop current active
		if pm.activeIdx >= 0 && pm.activeIdx < len(pm.providers) {
			old := pm.providers[pm.activeIdx]
			old.Provider.Stop()
			old.Active = false
		}

		ps.Active = true
		ps.ConsecutiveFails = 0
		pm.activeIdx = i

		pm.log.Info("manually switched provider", "name", name, "tier", ps.Provider.Tier())
		return nil
	}

	return fmt.Errorf("provider %q not found", name)
}
