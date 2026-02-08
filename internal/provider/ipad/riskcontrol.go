package ipad

import (
	"math/rand"
	"sync"
	"time"
)

// RiskControl enforces anti-ban policies for iPad protocol accounts.
// It tracks daily counters and enforces delays between operations
// to mimic natural human behavior.
type RiskControl struct {
	mu sync.Mutex

	// Configuration
	newAccountSilenceDays int
	maxMessagesPerDay     int
	maxGroupsPerDay       int
	maxFriendsPerDay      int
	messageIntervalMs     int
	randomDelay           bool
	accountCreatedAt      time.Time

	// Daily counters (reset at midnight)
	counterDate   time.Time
	messageCount  int
	groupCount    int
	friendCount   int
	lastMessageAt time.Time
}

// RiskControlConfig holds risk control configuration.
type RiskControlConfig struct {
	NewAccountSilenceDays int
	MaxMessagesPerDay     int
	MaxGroupsPerDay       int
	MaxFriendsPerDay      int
	MessageIntervalMs     int
	RandomDelay           bool
	AccountCreatedAt      time.Time
}

// NewRiskControl creates a new risk control engine.
func NewRiskControl(cfg RiskControlConfig) *RiskControl {
	if cfg.MaxMessagesPerDay == 0 {
		cfg.MaxMessagesPerDay = 500
	}
	if cfg.MaxGroupsPerDay == 0 {
		cfg.MaxGroupsPerDay = 10
	}
	if cfg.MaxFriendsPerDay == 0 {
		cfg.MaxFriendsPerDay = 20
	}
	if cfg.MessageIntervalMs == 0 {
		cfg.MessageIntervalMs = 1000
	}
	if cfg.NewAccountSilenceDays == 0 {
		cfg.NewAccountSilenceDays = 3
	}
	if cfg.AccountCreatedAt.IsZero() {
		// Default: assume old account
		cfg.AccountCreatedAt = time.Now().AddDate(-1, 0, 0)
	}

	return &RiskControl{
		newAccountSilenceDays: cfg.NewAccountSilenceDays,
		maxMessagesPerDay:     cfg.MaxMessagesPerDay,
		maxGroupsPerDay:       cfg.MaxGroupsPerDay,
		maxFriendsPerDay:      cfg.MaxFriendsPerDay,
		messageIntervalMs:     cfg.MessageIntervalMs,
		randomDelay:           cfg.RandomDelay,
		accountCreatedAt:      cfg.AccountCreatedAt,
		counterDate:           today(),
	}
}

// CheckMessage checks if sending a message is allowed and returns the
// required delay before sending. Returns (delay, allowed).
func (rc *RiskControl) CheckMessage() (time.Duration, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.resetIfNewDay()

	// Check silence period for new accounts
	if rc.isInSilencePeriod() {
		return 0, false
	}

	// Check daily limit
	if rc.messageCount >= rc.maxMessagesPerDay {
		return 0, false
	}

	// Calculate delay
	delay := rc.calculateDelay()

	rc.messageCount++
	rc.lastMessageAt = time.Now()

	return delay, true
}

// CheckGroupOperation checks if a group operation is allowed.
func (rc *RiskControl) CheckGroupOperation() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.resetIfNewDay()

	if rc.isInSilencePeriod() {
		return false
	}

	if rc.groupCount >= rc.maxGroupsPerDay {
		return false
	}

	rc.groupCount++
	return true
}

// CheckFriendOperation checks if a friend operation is allowed.
func (rc *RiskControl) CheckFriendOperation() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.resetIfNewDay()

	if rc.isInSilencePeriod() {
		return false
	}

	if rc.friendCount >= rc.maxFriendsPerDay {
		return false
	}

	rc.friendCount++
	return true
}

// GetStats returns current daily counters.
func (rc *RiskControl) GetStats() (messages, groups, friends int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.resetIfNewDay()

	return rc.messageCount, rc.groupCount, rc.friendCount
}

// IsInSilencePeriod returns whether the account is in its new-account silence period.
func (rc *RiskControl) IsInSilencePeriod() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.isInSilencePeriod()
}

// RemainingMessages returns how many messages can still be sent today.
func (rc *RiskControl) RemainingMessages() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.resetIfNewDay()

	remaining := rc.maxMessagesPerDay - rc.messageCount
	if remaining < 0 {
		return 0
	}
	return remaining
}

// isInSilencePeriod checks if we're within the new account silence window.
// Must be called with rc.mu held.
func (rc *RiskControl) isInSilencePeriod() bool {
	silenceEnd := rc.accountCreatedAt.AddDate(0, 0, rc.newAccountSilenceDays)
	return time.Now().Before(silenceEnd)
}

// calculateDelay returns the delay before the next message.
// Must be called with rc.mu held.
func (rc *RiskControl) calculateDelay() time.Duration {
	baseDelay := time.Duration(rc.messageIntervalMs) * time.Millisecond

	elapsed := time.Since(rc.lastMessageAt)
	if elapsed >= baseDelay {
		// Already waited long enough
		if rc.randomDelay {
			// Add small random jitter (0-500ms)
			return time.Duration(rand.Intn(500)) * time.Millisecond
		}
		return 0
	}

	remaining := baseDelay - elapsed

	if rc.randomDelay {
		// Add random jitter: 50%-150% of the remaining interval
		jitter := float64(remaining) * (0.5 + rand.Float64())
		return time.Duration(jitter)
	}

	return remaining
}

// resetIfNewDay resets daily counters if the date has changed.
// Must be called with rc.mu held.
func (rc *RiskControl) resetIfNewDay() {
	t := today()
	if !t.Equal(rc.counterDate) {
		rc.counterDate = t
		rc.messageCount = 0
		rc.groupCount = 0
		rc.friendCount = 0
	}
}

func today() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}
