package bridge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// BalancerStrategy defines how requests are distributed across providers.
type BalancerStrategy int

const (
	// StrategyRoundRobin distributes requests evenly across healthy providers.
	StrategyRoundRobin BalancerStrategy = iota

	// StrategySticky routes all messages for the same chat to the same provider.
	StrategySticky

	// StrategyPrimary uses a single primary provider, others are standby only.
	StrategyPrimary
)

// BalancerConfig configures the load balancer.
type BalancerConfig struct {
	Strategy BalancerStrategy
	Log      *slog.Logger
}

// providerSlot represents a provider in the load balancer pool.
type providerSlot struct {
	provider wechat.Provider
	healthy  bool
	sent     atomic.Int64
	failed   atomic.Int64
}

// ProviderBalancer distributes requests across multiple active providers.
type ProviderBalancer struct {
	mu       sync.RWMutex
	log      *slog.Logger
	strategy BalancerStrategy

	slots   []*providerSlot
	counter atomic.Uint64

	// Sticky routing: chatID → slot index
	stickyMap sync.Map
}

// NewProviderBalancer creates a new load balancer.
func NewProviderBalancer(cfg BalancerConfig) *ProviderBalancer {
	return &ProviderBalancer{
		log:      cfg.Log,
		strategy: cfg.Strategy,
		slots:    nil,
	}
}

// AddProvider adds a provider to the balancer pool.
func (lb *ProviderBalancer) AddProvider(p wechat.Provider) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	lb.slots = append(lb.slots, &providerSlot{
		provider: p,
		healthy:  true,
	})
}

// RemoveProvider removes a provider from the pool.
func (lb *ProviderBalancer) RemoveProvider(name string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	for i, slot := range lb.slots {
		if slot.provider.Name() == name {
			lb.slots = append(lb.slots[:i], lb.slots[i+1:]...)
			return
		}
	}
}

// SetHealthy marks a provider as healthy or unhealthy.
func (lb *ProviderBalancer) SetHealthy(name string, healthy bool) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	for _, slot := range lb.slots {
		if slot.provider.Name() == name {
			slot.healthy = healthy
			return
		}
	}
}

// HealthyCount returns the number of healthy providers.
func (lb *ProviderBalancer) HealthyCount() int {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	count := 0
	for _, slot := range lb.slots {
		if slot.healthy {
			count++
		}
	}
	return count
}

// PoolSize returns the total number of providers in the pool.
func (lb *ProviderBalancer) PoolSize() int {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return len(lb.slots)
}

// select picks a provider based on the configured strategy.
func (lb *ProviderBalancer) selectProvider(chatID string) (wechat.Provider, error) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	healthy := lb.healthySlots()
	if len(healthy) == 0 {
		return nil, fmt.Errorf("no healthy providers available")
	}

	switch lb.strategy {
	case StrategySticky:
		return lb.selectSticky(chatID, healthy)
	case StrategyPrimary:
		return healthy[0].provider, nil
	default: // RoundRobin
		return lb.selectRoundRobin(healthy)
	}
}

// selectRoundRobin picks the next healthy provider in rotation.
func (lb *ProviderBalancer) selectRoundRobin(healthy []*providerSlot) (wechat.Provider, error) {
	idx := lb.counter.Add(1) % uint64(len(healthy))
	return healthy[idx].provider, nil
}

// selectSticky routes by chatID, falling back to round-robin for new chats.
func (lb *ProviderBalancer) selectSticky(chatID string, healthy []*providerSlot) (wechat.Provider, error) {
	if chatID != "" {
		if cached, ok := lb.stickyMap.Load(chatID); ok {
			name, _ := cached.(string)
			for _, slot := range healthy {
				if slot.provider.Name() == name {
					return slot.provider, nil
				}
			}
			// Previous sticky provider is unhealthy, reassign
			lb.stickyMap.Delete(chatID)
		}
	}

	// Assign to least-loaded healthy provider
	best := healthy[0]
	for _, slot := range healthy[1:] {
		if slot.sent.Load() < best.sent.Load() {
			best = slot
		}
	}

	if chatID != "" {
		lb.stickyMap.Store(chatID, best.provider.Name())
	}
	return best.provider, nil
}

// healthySlots returns only healthy provider slots.
func (lb *ProviderBalancer) healthySlots() []*providerSlot {
	result := make([]*providerSlot, 0, len(lb.slots))
	for _, slot := range lb.slots {
		if slot.healthy && slot.provider.IsRunning() {
			result = append(result, slot)
		}
	}
	return result
}

// recordSend updates send statistics for a provider.
func (lb *ProviderBalancer) recordSend(name string, success bool) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	for _, slot := range lb.slots {
		if slot.provider.Name() == name {
			if success {
				slot.sent.Add(1)
			} else {
				slot.failed.Add(1)
			}
			return
		}
	}
}

// GetStats returns per-provider statistics.
func (lb *ProviderBalancer) GetStats() map[string]map[string]int64 {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	stats := make(map[string]map[string]int64)
	for _, slot := range lb.slots {
		stats[slot.provider.Name()] = map[string]int64{
			"sent":    slot.sent.Load(),
			"failed":  slot.failed.Load(),
			"healthy": boolToInt64(slot.healthy),
		}
	}
	return stats
}

// ClearSticky removes all sticky routing entries.
func (lb *ProviderBalancer) ClearSticky() {
	lb.stickyMap.Range(func(key, _ interface{}) bool {
		lb.stickyMap.Delete(key)
		return true
	})
}

// === Convenience send methods ===
// These route through the balancer, picking the right provider.

// SendText sends a text message through the balanced provider pool.
func (lb *ProviderBalancer) SendText(ctx context.Context, toUser string, text string) (string, error) {
	p, err := lb.selectProvider(toUser)
	if err != nil {
		return "", err
	}

	msgID, err := p.SendText(ctx, toUser, text)
	lb.recordSend(p.Name(), err == nil)
	return msgID, err
}

// SendImage sends an image through the balanced provider pool.
func (lb *ProviderBalancer) SendImage(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	p, err := lb.selectProvider(toUser)
	if err != nil {
		return "", err
	}

	msgID, err := p.SendImage(ctx, toUser, data, filename)
	lb.recordSend(p.Name(), err == nil)
	return msgID, err
}

// SendFile sends a file through the balanced provider pool.
func (lb *ProviderBalancer) SendFile(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	p, err := lb.selectProvider(toUser)
	if err != nil {
		return "", err
	}

	msgID, err := p.SendFile(ctx, toUser, data, filename)
	lb.recordSend(p.Name(), err == nil)
	return msgID, err
}

// RevokeMessage revokes a message through the appropriate provider.
func (lb *ProviderBalancer) RevokeMessage(ctx context.Context, msgID string, toUser string) error {
	p, err := lb.selectProvider(toUser)
	if err != nil {
		return err
	}

	return p.RevokeMessage(ctx, msgID, toUser)
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
