package wechat

import (
	"context"
	"io"
	"testing"
)

func TestRegistry_RegisterAndCreate(t *testing.T) {
	r := NewRegistry()

	err := r.Register("mock", func() Provider {
		return &mockProvider{name: "mock"}
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	names := r.List()
	if len(names) != 1 || names[0] != "mock" {
		t.Fatalf("list: %v", names)
	}

	if !r.Has("mock") {
		t.Fatal("Has(mock) = false")
	}
	if r.Has("nonexistent") {
		t.Fatal("Has(nonexistent) = true")
	}

	p, err := r.Create("mock")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Name() != "mock" {
		t.Fatalf("name: %s", p.Name())
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := NewRegistry()

	factory := func() Provider { return &mockProvider{name: "dup"} }
	r.Register("dup", factory)

	err := r.Register("dup", factory)
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestRegistry_CreateUnknown(t *testing.T) {
	r := NewRegistry()

	_, err := r.Create("unknown")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRegistry_ListSorted(t *testing.T) {
	r := NewRegistry()

	factory := func() Provider { return &mockProvider{} }
	r.Register("charlie", factory)
	r.Register("alpha", factory)
	r.Register("bravo", factory)

	names := r.List()
	if len(names) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(names))
	}
	if names[0] != "alpha" || names[1] != "bravo" || names[2] != "charlie" {
		t.Fatalf("not sorted: %v", names)
	}
}

// mockProvider implements the Provider interface for testing the registry.
type mockProvider struct {
	name string
}

func (m *mockProvider) Init(_ *ProviderConfig, _ MessageHandler) error { return nil }
func (m *mockProvider) Start(_ context.Context) error                  { return nil }
func (m *mockProvider) Stop() error                                     { return nil }
func (m *mockProvider) IsRunning() bool                                 { return false }
func (m *mockProvider) Name() string                                    { return m.name }
func (m *mockProvider) Tier() int                                       { return 99 }
func (m *mockProvider) Capabilities() Capability                        { return Capability{} }
func (m *mockProvider) Login(_ context.Context) error                   { return nil }
func (m *mockProvider) Logout(_ context.Context) error                  { return nil }
func (m *mockProvider) GetLoginState() LoginState                       { return LoginStateLoggedOut }
func (m *mockProvider) GetSelf() *ContactInfo                           { return nil }

func (m *mockProvider) SendText(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
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
func (m *mockProvider) SendLocation(_ context.Context, _ string, _ *LocationInfo) (string, error) {
	return "", nil
}
func (m *mockProvider) SendLink(_ context.Context, _ string, _ *LinkCardInfo) (string, error) {
	return "", nil
}
func (m *mockProvider) RevokeMessage(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *mockProvider) GetContactList(_ context.Context) ([]*ContactInfo, error) {
	return nil, nil
}
func (m *mockProvider) GetContactInfo(_ context.Context, _ string) (*ContactInfo, error) {
	return nil, nil
}
func (m *mockProvider) GetUserAvatar(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}
func (m *mockProvider) AcceptFriendRequest(_ context.Context, _ string) error {
	return nil
}
func (m *mockProvider) SetContactRemark(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *mockProvider) GetGroupList(_ context.Context) ([]*ContactInfo, error) {
	return nil, nil
}
func (m *mockProvider) GetGroupMembers(_ context.Context, _ string) ([]*GroupMember, error) {
	return nil, nil
}
func (m *mockProvider) GetGroupInfo(_ context.Context, _ string) (*ContactInfo, error) {
	return nil, nil
}
func (m *mockProvider) CreateGroup(_ context.Context, _ string, _ []string) (string, error) {
	return "", nil
}
func (m *mockProvider) InviteToGroup(_ context.Context, _ string, _ []string) error {
	return nil
}
func (m *mockProvider) RemoveFromGroup(_ context.Context, _ string, _ []string) error {
	return nil
}
func (m *mockProvider) SetGroupName(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *mockProvider) SetGroupAnnouncement(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *mockProvider) LeaveGroup(_ context.Context, _ string) error {
	return nil
}
func (m *mockProvider) DownloadMedia(_ context.Context, _ *Message) (io.ReadCloser, string, error) {
	return nil, "", nil
}
