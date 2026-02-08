package bridge

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/n42/mautrix-wechat/internal/config"
)

var testEncLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

// --- In-memory CryptoStore for testing ---

type memoryCryptoStore struct {
	deviceID         string
	olmAccount       []byte
	megolmSessions   map[string][]byte // key: "roomID|senderKey|sessionID"
	outboundSessions map[string][]byte // key: roomID
	encryptedRooms   map[string]bool
}

func newMemoryCryptoStore() *memoryCryptoStore {
	return &memoryCryptoStore{
		megolmSessions:   make(map[string][]byte),
		outboundSessions: make(map[string][]byte),
		encryptedRooms:   make(map[string]bool),
	}
}

func (s *memoryCryptoStore) GetDeviceID(_ context.Context) (string, error) {
	return s.deviceID, nil
}

func (s *memoryCryptoStore) SetDeviceID(_ context.Context, id string) error {
	s.deviceID = id
	return nil
}

func (s *memoryCryptoStore) GetPickleKey(_ context.Context) (string, error) {
	return "test_pickle_key", nil
}

func (s *memoryCryptoStore) PutOlmAccount(_ context.Context, data []byte) error {
	s.olmAccount = data
	return nil
}

func (s *memoryCryptoStore) GetOlmAccount(_ context.Context) ([]byte, error) {
	return s.olmAccount, nil
}

func (s *memoryCryptoStore) PutMegolmSession(_ context.Context, roomID, senderKey, sessionID string, data []byte) error {
	key := roomID + "|" + senderKey + "|" + sessionID
	s.megolmSessions[key] = data
	return nil
}

func (s *memoryCryptoStore) GetMegolmSession(_ context.Context, roomID, senderKey, sessionID string) ([]byte, error) {
	key := roomID + "|" + senderKey + "|" + sessionID
	return s.megolmSessions[key], nil
}

func (s *memoryCryptoStore) PutOutboundSession(_ context.Context, roomID string, data []byte) error {
	s.outboundSessions[roomID] = data
	return nil
}

func (s *memoryCryptoStore) GetOutboundSession(_ context.Context, roomID string) ([]byte, error) {
	return s.outboundSessions[roomID], nil
}

func (s *memoryCryptoStore) IsRoomEncrypted(_ context.Context, roomID string) (bool, error) {
	return s.encryptedRooms[roomID], nil
}

func (s *memoryCryptoStore) SetRoomEncrypted(_ context.Context, roomID string) error {
	s.encryptedRooms[roomID] = true
	return nil
}

// --- Tests ---

func TestNoopCryptoHelper_PassThrough(t *testing.T) {
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: false}, nil, nil, "")

	ctx := context.Background()

	if err := helper.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	content := map[string]interface{}{"body": "hello"}
	eventType, result, err := helper.Encrypt(ctx, "!room:test", "m.room.message", content)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if eventType != "m.room.message" {
		t.Fatalf("event type should be unchanged: %s", eventType)
	}
	if result["body"] != "hello" {
		t.Fatalf("content should be unchanged")
	}

	if helper.IsEncrypted(ctx, "!room:test") {
		t.Fatal("noop helper should never report encrypted")
	}

	_, _, err = helper.Decrypt(ctx, "!room:test", content)
	if err == nil {
		t.Fatal("noop helper decrypt should return error")
	}

	if err := helper.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestNoopCryptoHelper_MemberEvents(t *testing.T) {
	helper := &noopCryptoHelper{}
	ctx := context.Background()

	if err := helper.HandleMemberEvent(ctx, "!room:test", "@user:test", "join"); err != nil {
		t.Fatalf("handle member: %v", err)
	}
	if err := helper.ShareKeysWithUser(ctx, "!room:test", "@user:test"); err != nil {
		t.Fatalf("share keys: %v", err)
	}
	if err := helper.SetEncryptionForRoom(ctx, "!room:test"); err != nil {
		t.Fatalf("set encryption: %v", err)
	}
}

func TestBridgeCryptoHelper_Init(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{
		Allow:   true,
		Default: true,
	}, store, nil, "@bot:test")

	ctx := context.Background()
	if err := helper.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Should have generated a device ID
	if store.deviceID == "" {
		t.Fatal("device ID should be generated")
	}
	if len(store.deviceID) < 10 {
		t.Fatalf("device ID too short: %s", store.deviceID)
	}

	if err := helper.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestBridgeCryptoHelper_InitExistingDevice(t *testing.T) {
	store := newMemoryCryptoStore()
	store.deviceID = "EXISTING_DEVICE_123"

	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	if err := helper.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	if store.deviceID != "EXISTING_DEVICE_123" {
		t.Fatalf("should keep existing device ID: %s", store.deviceID)
	}
}

func TestBridgeCryptoHelper_SetAndCheckEncryption(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	helper.Init(ctx)

	if helper.IsEncrypted(ctx, "!room:test") {
		t.Fatal("room should not be encrypted initially")
	}

	if err := helper.SetEncryptionForRoom(ctx, "!room:test"); err != nil {
		t.Fatalf("set encryption: %v", err)
	}

	if !helper.IsEncrypted(ctx, "!room:test") {
		t.Fatal("room should be encrypted after setting")
	}

	// Verify persisted to store
	if !store.encryptedRooms["!room:test"] {
		t.Fatal("encryption state should be persisted to store")
	}
}

func TestBridgeCryptoHelper_EncryptUnencryptedRoom(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	helper.Init(ctx)

	content := map[string]interface{}{"body": "hello"}
	eventType, result, err := helper.Encrypt(ctx, "!unencrypted:test", "m.room.message", content)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if eventType != "m.room.message" {
		t.Fatalf("should pass through for unencrypted room: %s", eventType)
	}
	if result["body"] != "hello" {
		t.Fatal("content should be unchanged for unencrypted room")
	}
}

func TestBridgeCryptoHelper_EncryptEncryptedRoom_NoSession(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	helper.Init(ctx)
	helper.SetEncryptionForRoom(ctx, "!encrypted:test")

	// No outbound session exists, should fall back to unencrypted
	content := map[string]interface{}{"body": "hello"}
	eventType, _, err := helper.Encrypt(ctx, "!encrypted:test", "m.room.message", content)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Should pass through since no Megolm session exists
	if eventType != "m.room.message" {
		t.Fatalf("should fall back when no session: %s", eventType)
	}
}

func TestBridgeCryptoHelper_DecryptUnsupportedAlgorithm(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	helper.Init(ctx)

	content := map[string]interface{}{
		"algorithm": "m.unsupported.v1",
	}
	_, _, err := helper.Decrypt(ctx, "!room:test", content)
	if err == nil {
		t.Fatal("should error for unsupported algorithm")
	}
}

func TestBridgeCryptoHelper_DecryptMissingFields(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	helper.Init(ctx)

	content := map[string]interface{}{
		"algorithm": "m.megolm.v1.aes-sha2",
		// Missing sender_key, session_id, ciphertext
	}
	_, _, err := helper.Decrypt(ctx, "!room:test", content)
	if err == nil {
		t.Fatal("should error for missing fields")
	}
}

func TestBridgeCryptoHelper_DecryptUnknownSession(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	helper.Init(ctx)

	content := map[string]interface{}{
		"algorithm":  "m.megolm.v1.aes-sha2",
		"sender_key": "abc123",
		"session_id": "sess456",
		"ciphertext": "encrypted_data",
	}
	_, _, err := helper.Decrypt(ctx, "!room:test", content)
	if err == nil {
		t.Fatal("should error for unknown session")
	}
}

func TestBridgeCryptoHelper_HandleMemberJoin(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	helper.Init(ctx)

	// No error for unencrypted room
	if err := helper.HandleMemberEvent(ctx, "!room:test", "@user:test", "join"); err != nil {
		t.Fatalf("handle member join: %v", err)
	}

	// After setting encryption
	helper.SetEncryptionForRoom(ctx, "!room:test")
	if err := helper.HandleMemberEvent(ctx, "!room:test", "@user:test", "join"); err != nil {
		t.Fatalf("handle member join in encrypted room: %v", err)
	}
}

func TestBridgeCryptoHelper_HandleMemberLeave(t *testing.T) {
	store := newMemoryCryptoStore()
	helper := NewCryptoHelper(testEncLog, config.EncryptionConfig{Allow: true}, store, nil, "@bot:test")

	ctx := context.Background()
	helper.Init(ctx)
	helper.SetEncryptionForRoom(ctx, "!room:test")

	// Leave should not error
	if err := helper.HandleMemberEvent(ctx, "!room:test", "@user:test", "leave"); err != nil {
		t.Fatalf("handle member leave: %v", err)
	}
}

func TestGenerateDeviceID(t *testing.T) {
	id1 := generateDeviceID()
	id2 := generateDeviceID()

	if id1 == "" {
		t.Fatal("device ID should not be empty")
	}
	if id1 == id2 {
		t.Fatal("device IDs should be unique")
	}
	if len(id1) < 20 {
		t.Fatalf("device ID too short: %s", id1)
	}
}
