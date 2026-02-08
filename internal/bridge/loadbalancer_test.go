package bridge

import (
	"context"
	"log/slog"
	"testing"
)

func TestProviderBalancer_AddRemove(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategyRoundRobin,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	p2 := newMockProvider("ipad", 2)
	p2.running = true

	lb.AddProvider(p1)
	lb.AddProvider(p2)

	if lb.PoolSize() != 2 {
		t.Fatalf("pool size: %d", lb.PoolSize())
	}

	lb.RemoveProvider("wecom")
	if lb.PoolSize() != 1 {
		t.Fatalf("pool size after remove: %d", lb.PoolSize())
	}
}

func TestProviderBalancer_RoundRobin(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategyRoundRobin,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	p2 := newMockProvider("ipad", 2)
	p2.running = true

	lb.AddProvider(p1)
	lb.AddProvider(p2)

	// Call multiple times — should alternate
	names := make(map[string]int)
	for i := 0; i < 10; i++ {
		p, err := lb.selectProvider("")
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		names[p.Name()]++
	}

	if names["wecom"] == 0 || names["ipad"] == 0 {
		t.Errorf("round robin distribution: wecom=%d, ipad=%d", names["wecom"], names["ipad"])
	}
}

func TestProviderBalancer_Sticky(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategySticky,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	p2 := newMockProvider("ipad", 2)
	p2.running = true

	lb.AddProvider(p1)
	lb.AddProvider(p2)

	// Same chatID should always get same provider
	first, _ := lb.selectProvider("chat_123")
	for i := 0; i < 5; i++ {
		p, _ := lb.selectProvider("chat_123")
		if p.Name() != first.Name() {
			t.Errorf("sticky routing broken: got %s, want %s", p.Name(), first.Name())
		}
	}

	// Different chatID might get different provider
	// (at least shouldn't fail)
	_, err := lb.selectProvider("chat_456")
	if err != nil {
		t.Fatalf("second chat: %v", err)
	}
}

func TestProviderBalancer_StickyReassign(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategySticky,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	p2 := newMockProvider("ipad", 2)
	p2.running = true

	lb.AddProvider(p1)
	lb.AddProvider(p2)

	// Assign chat to a provider
	first, _ := lb.selectProvider("chat_123")

	// Make that provider unhealthy
	lb.SetHealthy(first.Name(), false)

	// Should reassign to remaining healthy provider
	p, err := lb.selectProvider("chat_123")
	if err != nil {
		t.Fatalf("select after unhealthy: %v", err)
	}
	if p.Name() == first.Name() {
		t.Error("should have been reassigned to different provider")
	}
}

func TestProviderBalancer_Primary(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategyPrimary,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	p2 := newMockProvider("ipad", 2)
	p2.running = true

	lb.AddProvider(p1)
	lb.AddProvider(p2)

	// Always returns first healthy provider
	for i := 0; i < 5; i++ {
		p, _ := lb.selectProvider("")
		if p.Name() != "wecom" {
			t.Errorf("primary strategy should always return first: got %s", p.Name())
		}
	}
}

func TestProviderBalancer_NoHealthy(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategyRoundRobin,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = false // not running

	lb.AddProvider(p1)

	_, err := lb.selectProvider("")
	if err == nil {
		t.Error("should fail with no healthy providers")
	}
}

func TestProviderBalancer_HealthCount(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategyRoundRobin,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	p2 := newMockProvider("ipad", 2)
	p2.running = true

	lb.AddProvider(p1)
	lb.AddProvider(p2)

	if lb.HealthyCount() != 2 {
		t.Fatalf("healthy: %d", lb.HealthyCount())
	}

	lb.SetHealthy("wecom", false)
	if lb.HealthyCount() != 1 {
		t.Fatalf("healthy after unhealthy: %d", lb.HealthyCount())
	}
}

func TestProviderBalancer_SendText(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategyRoundRobin,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	lb.AddProvider(p1)

	msgID, err := lb.SendText(context.Background(), "user123", "hello")
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if msgID != "msg_wecom" {
		t.Errorf("msgID: %s", msgID)
	}

	// Check stats
	stats := lb.GetStats()
	if stats["wecom"]["sent"] != 1 {
		t.Errorf("sent count: %d", stats["wecom"]["sent"])
	}
}

func TestProviderBalancer_GetStats(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategyRoundRobin,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	lb.AddProvider(p1)

	stats := lb.GetStats()
	if len(stats) != 1 {
		t.Fatalf("stats count: %d", len(stats))
	}
	if stats["wecom"]["healthy"] != 1 {
		t.Errorf("healthy: %d", stats["wecom"]["healthy"])
	}
	if stats["wecom"]["sent"] != 0 {
		t.Errorf("sent: %d", stats["wecom"]["sent"])
	}
}

func TestProviderBalancer_ClearSticky(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategySticky,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	p2 := newMockProvider("ipad", 2)
	p2.running = true

	lb.AddProvider(p1)
	lb.AddProvider(p2)

	// Create a sticky assignment
	lb.selectProvider("chat_123")

	// Clear sticky
	lb.ClearSticky()

	// Verify the sticky map is empty (no assertion needed — just shouldn't panic)
	_, err := lb.selectProvider("chat_123")
	if err != nil {
		t.Fatalf("select after clear: %v", err)
	}
}

func TestProviderBalancer_RecordSend(t *testing.T) {
	lb := NewProviderBalancer(BalancerConfig{
		Strategy: StrategyRoundRobin,
		Log:      slog.Default(),
	})

	p1 := newMockProvider("wecom", 1)
	p1.running = true
	lb.AddProvider(p1)

	lb.recordSend("wecom", true)
	lb.recordSend("wecom", true)
	lb.recordSend("wecom", false)

	stats := lb.GetStats()
	if stats["wecom"]["sent"] != 2 {
		t.Errorf("sent: %d", stats["wecom"]["sent"])
	}
	if stats["wecom"]["failed"] != 1 {
		t.Errorf("failed: %d", stats["wecom"]["failed"])
	}
}

func TestBoolToInt64(t *testing.T) {
	if boolToInt64(true) != 1 {
		t.Error("true should be 1")
	}
	if boolToInt64(false) != 0 {
		t.Error("false should be 0")
	}
}
