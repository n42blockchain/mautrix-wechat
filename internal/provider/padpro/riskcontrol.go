package padpro

import (
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// RiskControl enforces anti-ban policies for PadPro (iPad protocol) accounts.
// It tracks daily operation counters and enforces delays between messages
// to reduce behavioral anomaly detection risk.
//
// Controlled dimensions:
//   - Message frequency: daily cap + minimum interval between sends
//   - Group operations: daily cap on create/invite/remove
//   - Friend operations: daily cap on accept/add
//   - Media operations: daily cap on image/video/voice/file sends
//   - New account silence: block all operations for N days after first login
//   - Random jitter: randomize delays to avoid fixed-interval patterns
type RiskControl struct {
	mu sync.Mutex

	// Configuration
	maxMessagesPerDay     int
	maxGroupsPerDay       int
	maxFriendsPerDay      int
	maxMediaPerDay        int
	messageIntervalMs     int
	randomDelay           bool
	newAccountSilenceDays int
	accountCreatedAt      time.Time

	// Daily counters (reset at midnight)
	counterDate  time.Time
	messageCount int
	groupCount   int
	friendCount  int
	mediaCount   int

	lastMessageAt time.Time
}

// NewRiskControl creates a new risk control engine from provider config.
// Reads settings from cfg.Extra map (populated by bridge.go from config.yaml).
func NewRiskControl(cfg *wechat.ProviderConfig) *RiskControl {
	rc := &RiskControl{
		maxMessagesPerDay:     parseIntOr(cfg.Extra, "max_messages_per_day", 500),
		maxGroupsPerDay:       parseIntOr(cfg.Extra, "max_groups_per_day", 10),
		maxFriendsPerDay:      parseIntOr(cfg.Extra, "max_friends_per_day", 20),
		maxMediaPerDay:        parseIntOr(cfg.Extra, "max_media_per_day", 100),
		messageIntervalMs:     parseIntOr(cfg.Extra, "message_interval_ms", 1000),
		newAccountSilenceDays: parseIntOr(cfg.Extra, "new_account_silence_days", 3),
		randomDelay:           cfg.Extra["random_delay"] == "true",
		counterDate:           today(),
		// Default: assume old account (created 1 year ago)
		accountCreatedAt: time.Now().AddDate(-1, 0, 0),
	}
	return rc
}

// SetAccountCreatedAt updates the account creation timestamp.
// Call this after login when the actual creation date is known.
func (rc *RiskControl) SetAccountCreatedAt(t time.Time) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.accountCreatedAt = t
}

// CheckMessage checks if sending a message is allowed and returns the
// required delay before sending. Returns (delay, allowed).
// This covers text messages only — media messages should use CheckMedia.
func (rc *RiskControl) CheckMessage() (time.Duration, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.resetIfNewDay()

	if rc.isInSilencePeriod() {
		return 0, false
	}

	if rc.messageCount >= rc.maxMessagesPerDay {
		return 0, false
	}

	delay := rc.calculateDelay()
	rc.messageCount++
	rc.lastMessageAt = time.Now()

	return delay, true
}

// CheckMedia checks if sending a media message (image/video/voice/file) is allowed.
// Media messages count toward both the message counter and the media counter.
func (rc *RiskControl) CheckMedia() (time.Duration, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.resetIfNewDay()

	if rc.isInSilencePeriod() {
		return 0, false
	}

	if rc.messageCount >= rc.maxMessagesPerDay {
		return 0, false
	}

	if rc.mediaCount >= rc.maxMediaPerDay {
		return 0, false
	}

	delay := rc.calculateDelay()
	rc.messageCount++
	rc.mediaCount++
	rc.lastMessageAt = time.Now()

	return delay, true
}

// CheckGroupOperation checks if a group operation (create/invite/remove) is allowed.
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

// CheckFriendOperation checks if a friend operation (accept/add) is allowed.
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

// GetStats returns current daily counters for monitoring/health checks.
func (rc *RiskControl) GetStats() (messages, media, groups, friends int) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.resetIfNewDay()
	return rc.messageCount, rc.mediaCount, rc.groupCount, rc.friendCount
}

// IsInSilencePeriod returns whether the account is in its new-account silence window.
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

// StatsString returns a human-readable summary for logging.
func (rc *RiskControl) StatsString() string {
	msgs, media, groups, friends := rc.GetStats()
	return fmt.Sprintf("messages=%d/%d media=%d/%d groups=%d/%d friends=%d/%d",
		msgs, rc.maxMessagesPerDay,
		media, rc.maxMediaPerDay,
		groups, rc.maxGroupsPerDay,
		friends, rc.maxFriendsPerDay,
	)
}

// --- internal helpers ---

func (rc *RiskControl) isInSilencePeriod() bool {
	silenceEnd := rc.accountCreatedAt.AddDate(0, 0, rc.newAccountSilenceDays)
	return time.Now().Before(silenceEnd)
}

func (rc *RiskControl) calculateDelay() time.Duration {
	baseDelay := time.Duration(rc.messageIntervalMs) * time.Millisecond

	elapsed := time.Since(rc.lastMessageAt)
	if elapsed >= baseDelay {
		if rc.randomDelay {
			// Add small random jitter (0-500ms) even when enough time has passed
			return time.Duration(rand.Intn(500)) * time.Millisecond
		}
		return 0
	}

	remaining := baseDelay - elapsed

	if rc.randomDelay {
		// Jitter: 50%-150% of the remaining interval
		jitter := float64(remaining) * (0.5 + rand.Float64())
		return time.Duration(jitter)
	}

	return remaining
}

func (rc *RiskControl) resetIfNewDay() {
	t := today()
	if !t.Equal(rc.counterDate) {
		rc.counterDate = t
		rc.messageCount = 0
		rc.mediaCount = 0
		rc.groupCount = 0
		rc.friendCount = 0
	}
}

func today() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

func parseIntOr(m map[string]string, key string, defaultVal int) int {
	if v, ok := m[key]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}
