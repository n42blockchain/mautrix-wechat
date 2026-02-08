package bridge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// mockProvider implements wechat.Provider for testing failover logic.
type mockProvider struct {
	mu         sync.RWMutex
	name       string
	tier       int
	running    bool
	loginState wechat.LoginState
	startErr   error
	failCount  int
}

func newMockProvider(name string, tier int) *mockProvider {
	return &mockProvider{
		name:       name,
		tier:       tier,
		loginState: wechat.LoginStateLoggedIn,
	}
}

func (m *mockProvider) Init(_ *wechat.ProviderConfig, _ wechat.MessageHandler) error { return nil }

func (m *mockProvider) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.startErr != nil {
		return m.startErr
	}
	m.running = true
	return nil
}

func (m *mockProvider) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = false
	return nil
}

func (m *mockProvider) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Tier() int    { return m.tier }
func (m *mockProvider) Capabilities() wechat.Capability {
	return wechat.Capability{SendText: true, ReceiveMessage: true}
}

func (m *mockProvider) Login(_ context.Context) error   { return nil }
func (m *mockProvider) Logout(_ context.Context) error  { return nil }
func (m *mockProvider) GetLoginState() wechat.LoginState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loginState
}
func (m *mockProvider) GetSelf() *wechat.ContactInfo {
	return &wechat.ContactInfo{UserID: m.name}
}

func (m *mockProvider) SendText(_ context.Context, _ string, _ string) (string, error) {
	return "msg_" + m.name, nil
}
func (m *mockProvider) SendImage(_ context.Context, _ string, _ io.Reader, _ string) (string, error) {
	return "", nil
}
func (m *mockProvider) SendVideo(_ context.Context, _ string, _ io.Reader, _ string, _ io.Reader) (string, error) {
	return "", nil
}
func (m *mockProvider) SendVoice(_ context.Context, _ string, _ io.Reader, _ int) (string, error) {
	return "", nil
}
func (m *mockProvider) SendFile(_ context.Context, _ string, _ io.Reader, _ string) (string, error) {
	return "", nil
}
func (m *mockProvider) SendLocation(_ context.Context, _ string, _ *wechat.LocationInfo) (string, error) {
	return "", nil
}
func (m *mockProvider) SendLink(_ context.Context, _ string, _ *wechat.LinkCardInfo) (string, error) {
	return "", nil
}
func (m *mockProvider) RevokeMessage(_ context.Context, _ string, _ string) error { return nil }
func (m *mockProvider) GetContactList(_ context.Context) ([]*wechat.ContactInfo, error) {
	return nil, nil
}
func (m *mockProvider) GetContactInfo(_ context.Context, _ string) (*wechat.ContactInfo, error) {
	return nil, nil
}
func (m *mockProvider) GetUserAvatar(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}
func (m *mockProvider) AcceptFriendRequest(_ context.Context, _ string) error    { return nil }
func (m *mockProvider) SetContactRemark(_ context.Context, _, _ string) error    { return nil }
func (m *mockProvider) GetGroupList(_ context.Context) ([]*wechat.ContactInfo, error) {
	return nil, nil
}
func (m *mockProvider) GetGroupMembers(_ context.Context, _ string) ([]*wechat.GroupMember, error) {
	return nil, nil
}
func (m *mockProvider) GetGroupInfo(_ context.Context, _ string) (*wechat.ContactInfo, error) {
	return nil, nil
}
func (m *mockProvider) CreateGroup(_ context.Context, _ string, _ []string) (string, error) {
	return "", nil
}
func (m *mockProvider) InviteToGroup(_ context.Context, _ string, _ []string) error { return nil }
func (m *mockProvider) RemoveFromGroup(_ context.Context, _ string, _ []string) error {
	return nil
}
func (m *mockProvider) SetGroupName(_ context.Context, _, _ string) error         { return nil }
func (m *mockProvider) SetGroupAnnouncement(_ context.Context, _, _ string) error { return nil }
func (m *mockProvider) LeaveGroup(_ context.Context, _ string) error              { return nil }
func (m *mockProvider) DownloadMedia(_ context.Context, _ *wechat.Message) (io.ReadCloser, string, error) {
	return nil, "", nil
}

// --- ProviderManager Tests ---

func TestProviderManager_AddAndSort(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	// Add providers out of order
	pm.AddProvider(newMockProvider("pchook", 3), &wechat.ProviderConfig{})
	pm.AddProvider(newMockProvider("wecom", 1), &wechat.ProviderConfig{})
	pm.AddProvider(newMockProvider("ipad", 2), &wechat.ProviderConfig{})

	if pm.ProviderCount() != 3 {
		t.Fatalf("count: %d", pm.ProviderCount())
	}

	// Verify sort order
	states := pm.GetProviderStates()
	if states[0].Provider.Name() != "wecom" {
		t.Errorf("first provider: %s, want wecom", states[0].Provider.Name())
	}
	if states[1].Provider.Name() != "ipad" {
		t.Errorf("second provider: %s, want ipad", states[1].Provider.Name())
	}
	if states[2].Provider.Name() != "pchook" {
		t.Errorf("third provider: %s, want pchook", states[2].Provider.Name())
	}
}

func TestProviderManager_StartSelectsHighestPriority(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	pm.AddProvider(newMockProvider("ipad", 2), &wechat.ProviderConfig{})
	pm.AddProvider(newMockProvider("wecom", 1), &wechat.ProviderConfig{})

	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer pm.Stop()

	if pm.ActiveName() != "wecom" {
		t.Errorf("active: %s, want wecom", pm.ActiveName())
	}
	if pm.ActiveTier() != 1 {
		t.Errorf("tier: %d, want 1", pm.ActiveTier())
	}
}

func TestProviderManager_SkipsFailedProviders(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	failing := newMockProvider("wecom", 1)
	failing.startErr = fmt.Errorf("connection refused")

	pm.AddProvider(failing, &wechat.ProviderConfig{})
	pm.AddProvider(newMockProvider("ipad", 2), &wechat.ProviderConfig{})

	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer pm.Stop()

	// Should fall back to ipad since wecom failed
	if pm.ActiveName() != "ipad" {
		t.Errorf("active: %s, want ipad", pm.ActiveName())
	}
}

func TestProviderManager_AllFail(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	p1 := newMockProvider("wecom", 1)
	p1.startErr = fmt.Errorf("fail")
	p2 := newMockProvider("ipad", 2)
	p2.startErr = fmt.Errorf("fail")

	pm.AddProvider(p1, &wechat.ProviderConfig{})
	pm.AddProvider(p2, &wechat.ProviderConfig{})

	err := pm.Start(context.Background())
	if err == nil {
		t.Fatal("should fail when all providers fail")
	}
}

func TestProviderManager_ForceFailover(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	pm.AddProvider(newMockProvider("wecom", 1), &wechat.ProviderConfig{})
	pm.AddProvider(newMockProvider("ipad", 2), &wechat.ProviderConfig{})

	pm.Start(context.Background())
	defer pm.Stop()

	if pm.ActiveName() != "wecom" {
		t.Fatalf("active: %s", pm.ActiveName())
	}

	// Force failover
	if err := pm.ForceFailover(); err != nil {
		t.Fatalf("force failover: %v", err)
	}

	if pm.ActiveName() != "ipad" {
		t.Errorf("after failover: %s, want ipad", pm.ActiveName())
	}

	// Check failover history
	events := pm.GetFailoverHistory()
	if len(events) != 1 {
		t.Fatalf("events: %d", len(events))
	}
	if events[0].FromName != "wecom" || events[0].ToName != "ipad" {
		t.Errorf("event: %s -> %s", events[0].FromName, events[0].ToName)
	}
}

func TestProviderManager_ForceProvider(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	pm.AddProvider(newMockProvider("wecom", 1), &wechat.ProviderConfig{})
	pm.AddProvider(newMockProvider("ipad", 2), &wechat.ProviderConfig{})
	pm.AddProvider(newMockProvider("pchook", 3), &wechat.ProviderConfig{})

	pm.Start(context.Background())
	defer pm.Stop()

	// Force switch to pchook
	if err := pm.ForceProvider("pchook"); err != nil {
		t.Fatalf("force provider: %v", err)
	}

	if pm.ActiveName() != "pchook" {
		t.Errorf("active: %s, want pchook", pm.ActiveName())
	}

	// Try non-existent provider
	if err := pm.ForceProvider("nonexistent"); err == nil {
		t.Error("should fail for nonexistent provider")
	}
}

func TestProviderManager_HealthCheck(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	p1 := newMockProvider("wecom", 1)
	p2 := newMockProvider("ipad", 2)

	pm.AddProvider(p1, &wechat.ProviderConfig{})
	pm.AddProvider(p2, &wechat.ProviderConfig{})

	pm.Start(context.Background())
	defer pm.Stop()

	// Provider is healthy
	pm.checkActiveProvider()
	states := pm.GetProviderStates()
	if states[0].ConsecutiveFails != 0 {
		t.Errorf("consecutive fails: %d", states[0].ConsecutiveFails)
	}

	// Make provider unhealthy
	p1.mu.Lock()
	p1.loginState = wechat.LoginStateError
	p1.mu.Unlock()

	pm.checkActiveProvider()
	states = pm.GetProviderStates()
	if states[0].ConsecutiveFails != 1 {
		t.Errorf("consecutive fails after unhealthy: %d", states[0].ConsecutiveFails)
	}
}

func TestProviderManager_HealthCheckTriggersFailover(t *testing.T) {
	log := slog.Default()
	cfg := FailoverConfig{
		Enabled:               true,
		HealthCheckInterval:   100 * time.Millisecond,
		FailureThreshold:      2,
		RecoveryCheckInterval: 1 * time.Minute,
		RecoveryThreshold:     2,
	}
	pm := NewProviderManager(log, cfg, nil)

	p1 := newMockProvider("wecom", 1)
	p2 := newMockProvider("ipad", 2)

	pm.AddProvider(p1, &wechat.ProviderConfig{})
	pm.AddProvider(p2, &wechat.ProviderConfig{})

	pm.Start(context.Background())
	defer pm.Stop()

	// Make wecom unhealthy
	p1.mu.Lock()
	p1.loginState = wechat.LoginStateError
	p1.mu.Unlock()

	// Check multiple times to exceed threshold
	pm.checkActiveProvider() // fail 1
	pm.checkActiveProvider() // fail 2 — triggers failover

	if pm.ActiveName() != "ipad" {
		t.Errorf("should have failed over to ipad, got: %s", pm.ActiveName())
	}
}

func TestProviderManager_NoActiveProvider(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	if pm.Active() != nil {
		t.Error("should have no active provider")
	}
	if pm.ActiveName() != "none" {
		t.Errorf("name: %s", pm.ActiveName())
	}
	if pm.ActiveTier() != 0 {
		t.Errorf("tier: %d", pm.ActiveTier())
	}
}

func TestProviderManager_StopIdempotent(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	// Stop without start should be safe
	if err := pm.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	pm.AddProvider(newMockProvider("wecom", 1), &wechat.ProviderConfig{})
	pm.Start(context.Background())

	// Double stop
	pm.Stop()
	if err := pm.Stop(); err != nil {
		t.Fatalf("double stop: %v", err)
	}
}

func TestProviderManager_EmptyProviders(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	err := pm.Start(context.Background())
	if err == nil {
		t.Error("should fail with no providers")
	}
}

func TestProviderManager_ForceFailoverNoFallback(t *testing.T) {
	log := slog.Default()
	pm := NewProviderManager(log, DefaultFailoverConfig(), nil)

	pm.AddProvider(newMockProvider("wecom", 1), &wechat.ProviderConfig{})
	// Only one provider — failover should fail

	pm.Start(context.Background())
	defer pm.Stop()

	err := pm.ForceFailover()
	if err == nil {
		t.Error("should fail when no fallback provider exists")
	}
}
