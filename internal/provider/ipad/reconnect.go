package ipad

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"sync"
	"time"
)

// Reconnector handles automatic reconnection with exponential backoff.
// It monitors the connection via heartbeats and triggers reconnection
// when the connection drops.
type Reconnector struct {
	mu     sync.Mutex
	log    *slog.Logger
	state  reconnectState
	stopCh chan struct{}

	// Configuration
	heartbeatInterval time.Duration
	maxBackoff        time.Duration
	baseBackoff       time.Duration

	// Callbacks
	checkAlive  func(ctx context.Context) bool
	doReconnect func(ctx context.Context) error
	onConnected func()
	onDisconnected func()

	// Stats
	reconnectCount int
	lastConnected  time.Time
	lastDisconnected time.Time
}

type reconnectState int

const (
	stateConnected    reconnectState = iota
	stateDisconnected
	stateReconnecting
	stateStopped
)

// ReconnectorConfig holds configuration for the reconnector.
type ReconnectorConfig struct {
	Log               *slog.Logger
	HeartbeatInterval time.Duration // default: 30s
	MaxBackoff        time.Duration // default: 5min
	BaseBackoff       time.Duration // default: 2s

	// CheckAlive is called periodically to verify connection health.
	CheckAlive func(ctx context.Context) bool
	// DoReconnect is called to attempt reconnection.
	DoReconnect func(ctx context.Context) error
	// OnConnected is called when connection is established/restored.
	OnConnected func()
	// OnDisconnected is called when connection is lost.
	OnDisconnected func()
}

// NewReconnector creates a new reconnector.
func NewReconnector(cfg ReconnectorConfig) *Reconnector {
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 5 * time.Minute
	}
	if cfg.BaseBackoff == 0 {
		cfg.BaseBackoff = 2 * time.Second
	}

	return &Reconnector{
		log:               cfg.Log,
		state:             stateDisconnected,
		stopCh:            make(chan struct{}),
		heartbeatInterval: cfg.HeartbeatInterval,
		maxBackoff:        cfg.MaxBackoff,
		baseBackoff:       cfg.BaseBackoff,
		checkAlive:        cfg.CheckAlive,
		doReconnect:       cfg.DoReconnect,
		onConnected:       cfg.OnConnected,
		onDisconnected:    cfg.OnDisconnected,
	}
}

// Start begins the heartbeat monitoring loop.
func (r *Reconnector) Start() {
	go r.heartbeatLoop()
}

// Stop stops the reconnector.
func (r *Reconnector) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state == stateStopped {
		return
	}
	r.state = stateStopped
	close(r.stopCh)
}

// MarkConnected marks the connection as established.
func (r *Reconnector) MarkConnected() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.state = stateConnected
	r.lastConnected = time.Now()
	r.reconnectCount = 0
}

// MarkDisconnected marks the connection as lost.
func (r *Reconnector) MarkDisconnected() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state == stateConnected {
		r.state = stateDisconnected
		r.lastDisconnected = time.Now()
		if r.onDisconnected != nil {
			go r.onDisconnected()
		}
	}
}

// IsConnected returns whether the connection is active.
func (r *Reconnector) IsConnected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state == stateConnected
}

// Stats returns reconnection statistics.
func (r *Reconnector) Stats() ReconnectStats {
	r.mu.Lock()
	defer r.mu.Unlock()

	return ReconnectStats{
		Connected:        r.state == stateConnected,
		ReconnectCount:   r.reconnectCount,
		LastConnected:    r.lastConnected,
		LastDisconnected: r.lastDisconnected,
	}
}

// ReconnectStats holds reconnection statistics.
type ReconnectStats struct {
	Connected        bool
	ReconnectCount   int
	LastConnected    time.Time
	LastDisconnected time.Time
}

// heartbeatLoop periodically checks connection health and triggers reconnection.
func (r *Reconnector) heartbeatLoop() {
	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.checkAndReconnect()
		}
	}
}

// checkAndReconnect checks the connection and reconnects if needed.
func (r *Reconnector) checkAndReconnect() {
	r.mu.Lock()
	state := r.state
	r.mu.Unlock()

	if state == stateStopped || state == stateReconnecting {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if state == stateConnected {
		if r.checkAlive != nil && !r.checkAlive(ctx) {
			r.log.Warn("heartbeat check failed, connection lost")
			r.MarkDisconnected()
			go r.reconnectWithBackoff()
		}
		return
	}

	// stateDisconnected — attempt reconnect
	go r.reconnectWithBackoff()
}

// reconnectWithBackoff attempts to reconnect with exponential backoff.
func (r *Reconnector) reconnectWithBackoff() {
	r.mu.Lock()
	if r.state == stateReconnecting || r.state == stateStopped {
		r.mu.Unlock()
		return
	}
	r.state = stateReconnecting
	attempt := r.reconnectCount
	r.mu.Unlock()

	for {
		select {
		case <-r.stopCh:
			return
		default:
		}

		// Calculate backoff duration
		backoff := r.calculateBackoff(attempt)
		r.log.Info("attempting reconnection",
			"attempt", attempt+1, "backoff", backoff)

		// Wait for backoff
		select {
		case <-r.stopCh:
			return
		case <-time.After(backoff):
		}

		// Attempt reconnection
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := r.doReconnect(ctx)
		cancel()

		if err == nil {
			r.log.Info("reconnection successful", "attempt", attempt+1)
			r.mu.Lock()
			r.state = stateConnected
			r.lastConnected = time.Now()
			r.reconnectCount = attempt + 1
			r.mu.Unlock()

			if r.onConnected != nil {
				r.onConnected()
			}
			return
		}

		r.log.Error("reconnection failed",
			"attempt", attempt+1, "error", err)
		attempt++

		r.mu.Lock()
		r.reconnectCount = attempt
		r.mu.Unlock()
	}
}

// calculateBackoff returns the backoff duration for the given attempt.
func (r *Reconnector) calculateBackoff(attempt int) time.Duration {
	backoff := float64(r.baseBackoff) * math.Pow(2, float64(attempt))
	if backoff > float64(r.maxBackoff) {
		backoff = float64(r.maxBackoff)
	}
	// Add jitter: 75%-125% of calculated backoff
	jitter := 0.75 + 0.5*float64(time.Now().UnixNano()%1000)/1000.0
	return time.Duration(backoff * jitter)
}

// SessionData holds serializable session state for persistence across restarts.
type SessionData struct {
	UserID    string    `json:"user_id"`
	Nickname  string    `json:"nickname"`
	AvatarURL string    `json:"avatar_url"`
	LoginTime time.Time `json:"login_time"`
	Token     string    `json:"token,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	AppID     string    `json:"app_id,omitempty"`
}

// Marshal serializes session data to JSON.
func (s *SessionData) Marshal() ([]byte, error) {
	return json.Marshal(s)
}

// UnmarshalSessionData deserializes session data from JSON.
func UnmarshalSessionData(data []byte) (*SessionData, error) {
	var s SessionData
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
