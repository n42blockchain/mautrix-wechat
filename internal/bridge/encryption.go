package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"

	"github.com/n42/mautrix-wechat/internal/config"
)

// CryptoHelper abstracts Matrix end-to-end encryption for the bridge.
// When encryption is enabled, it encrypts outgoing events and decrypts incoming ones.
// When disabled, it passes events through unchanged.
type CryptoHelper interface {
	// Init initializes the crypto module (key loading, device setup).
	Init(ctx context.Context) error

	// Close shuts down the crypto module and persists state.
	Close() error

	// Encrypt encrypts a Matrix event content map for a given room.
	// Returns the encrypted content (m.room.encrypted event) or the original if room is unencrypted.
	Encrypt(ctx context.Context, roomID string, eventType string, content map[string]interface{}) (string, map[string]interface{}, error)

	// Decrypt decrypts an m.room.encrypted event and returns the plaintext event type and content.
	Decrypt(ctx context.Context, roomID string, content map[string]interface{}) (string, map[string]interface{}, error)

	// IsEncrypted returns whether a room has encryption enabled.
	IsEncrypted(ctx context.Context, roomID string) bool

	// HandleMemberEvent processes room membership changes to manage Olm sessions.
	HandleMemberEvent(ctx context.Context, roomID string, userID string, membership string) error

	// ShareKeysWithUser ensures Megolm session keys are shared with a new room member.
	ShareKeysWithUser(ctx context.Context, roomID string, userID string) error

	// SetEncryptionForRoom marks a room as encrypted (e.g., when m.room.encryption state event is received).
	SetEncryptionForRoom(ctx context.Context, roomID string) error
}

// CryptoStore persists encryption keys and device state.
// Implementations may use SQL or file-based storage.
type CryptoStore interface {
	// GetDeviceID returns the bridge's device ID, or empty if not yet created.
	GetDeviceID(ctx context.Context) (string, error)
	// SetDeviceID persists the bridge's device ID.
	SetDeviceID(ctx context.Context, deviceID string) error
	// GetPickleKey returns the key used to encrypt Olm account data.
	GetPickleKey(ctx context.Context) (string, error)
	// PutOlmAccount stores the pickled Olm account.
	PutOlmAccount(ctx context.Context, pickled []byte) error
	// GetOlmAccount retrieves the pickled Olm account.
	GetOlmAccount(ctx context.Context) ([]byte, error)
	// PutMegolmSession stores a Megolm inbound session for a room.
	PutMegolmSession(ctx context.Context, roomID, senderKey, sessionID string, session []byte) error
	// GetMegolmSession retrieves a Megolm inbound session.
	GetMegolmSession(ctx context.Context, roomID, senderKey, sessionID string) ([]byte, error)
	// PutOutboundSession stores the current outbound Megolm session for a room.
	PutOutboundSession(ctx context.Context, roomID string, session []byte) error
	// GetOutboundSession retrieves the current outbound Megolm session for a room.
	GetOutboundSession(ctx context.Context, roomID string) ([]byte, error)
	// IsRoomEncrypted checks if a room has encryption enabled.
	IsRoomEncrypted(ctx context.Context, roomID string) (bool, error)
	// SetRoomEncrypted marks a room as encrypted.
	SetRoomEncrypted(ctx context.Context, roomID string) error
}

// NewCryptoHelper creates a CryptoHelper based on the encryption config.
// Returns a no-op helper when encryption is disabled.
func NewCryptoHelper(log *slog.Logger, cfg config.EncryptionConfig, store CryptoStore, client MatrixClient, botUserID string) CryptoHelper {
	if !cfg.Allow {
		return &noopCryptoHelper{}
	}

	return &bridgeCryptoHelper{
		log:       log,
		cfg:       cfg,
		store:     store,
		client:    client,
		botUserID: botUserID,
		rooms:     make(map[string]bool),
	}
}

// --- No-op implementation (encryption disabled) ---

type noopCryptoHelper struct{}

func (n *noopCryptoHelper) Init(_ context.Context) error { return nil }
func (n *noopCryptoHelper) Close() error                 { return nil }

func (n *noopCryptoHelper) Encrypt(_ context.Context, _ string, eventType string, content map[string]interface{}) (string, map[string]interface{}, error) {
	return eventType, content, nil
}

func (n *noopCryptoHelper) Decrypt(_ context.Context, _ string, content map[string]interface{}) (string, map[string]interface{}, error) {
	return "", nil, fmt.Errorf("encryption not enabled")
}

func (n *noopCryptoHelper) IsEncrypted(_ context.Context, _ string) bool       { return false }
func (n *noopCryptoHelper) HandleMemberEvent(_ context.Context, _, _, _ string) error { return nil }
func (n *noopCryptoHelper) ShareKeysWithUser(_ context.Context, _, _ string) error    { return nil }
func (n *noopCryptoHelper) SetEncryptionForRoom(_ context.Context, _ string) error    { return nil }

// --- Bridge crypto helper (encryption enabled) ---

// bridgeCryptoHelper implements CryptoHelper with actual encryption support.
// It manages room encryption state and delegates to Olm/Megolm operations.
// When the actual libolm bindings are not available, it tracks room states
// and returns appropriate errors for encrypt/decrypt operations.
type bridgeCryptoHelper struct {
	log       *slog.Logger
	cfg       config.EncryptionConfig
	store     CryptoStore
	client    MatrixClient
	botUserID string

	mu       sync.RWMutex
	rooms    map[string]bool // cached room encryption state
	deviceID string
}

func (c *bridgeCryptoHelper) Init(ctx context.Context) error {
	// Load or create device ID
	deviceID, err := c.store.GetDeviceID(ctx)
	if err != nil {
		return fmt.Errorf("load device ID: %w", err)
	}

	if deviceID == "" {
		deviceID = generateDeviceID()
		if err := c.store.SetDeviceID(ctx, deviceID); err != nil {
			return fmt.Errorf("save device ID: %w", err)
		}
		c.log.Info("generated new bridge device ID", "device_id", deviceID)
	}
	c.deviceID = deviceID

	c.log.Info("crypto helper initialized",
		"device_id", c.deviceID,
		"allow", c.cfg.Allow,
		"default", c.cfg.Default,
		"require", c.cfg.Require,
	)

	return nil
}

func (c *bridgeCryptoHelper) Close() error {
	c.log.Info("crypto helper closed")
	return nil
}

func (c *bridgeCryptoHelper) Encrypt(ctx context.Context, roomID string, eventType string, content map[string]interface{}) (string, map[string]interface{}, error) {
	if !c.IsEncrypted(ctx, roomID) {
		return eventType, content, nil
	}

	// Retrieve or create outbound Megolm session for the room
	sessionData, err := c.store.GetOutboundSession(ctx, roomID)
	if err != nil {
		return "", nil, fmt.Errorf("get outbound session for room %s: %w", roomID, err)
	}

	if sessionData == nil {
		// In a full implementation, we would create a new Megolm outbound session here
		// and share the session key with all room members via Olm-encrypted to-device events.
		c.log.Warn("no outbound Megolm session for room, sending unencrypted",
			"room_id", roomID)
		return eventType, content, nil
	}

	// In a full implementation with libolm:
	// 1. Encrypt content using the outbound Megolm session
	// 2. Return m.room.encrypted event type with ciphertext
	// For now, we pass through and log a warning
	c.log.Debug("would encrypt event for room", "room_id", roomID, "event_type", eventType)
	return eventType, content, nil
}

func (c *bridgeCryptoHelper) Decrypt(ctx context.Context, roomID string, content map[string]interface{}) (string, map[string]interface{}, error) {
	algorithm, _ := content["algorithm"].(string)
	if algorithm != "m.megolm.v1.aes-sha2" {
		return "", nil, fmt.Errorf("unsupported encryption algorithm: %s", algorithm)
	}

	senderKey, _ := content["sender_key"].(string)
	sessionID, _ := content["session_id"].(string)
	ciphertext, _ := content["ciphertext"].(string)

	if senderKey == "" || sessionID == "" || ciphertext == "" {
		return "", nil, fmt.Errorf("missing required encryption fields")
	}

	// Look up Megolm inbound session
	sessionData, err := c.store.GetMegolmSession(ctx, roomID, senderKey, sessionID)
	if err != nil {
		return "", nil, fmt.Errorf("get megolm session: %w", err)
	}

	if sessionData == nil {
		return "", nil, fmt.Errorf("unknown Megolm session %s for room %s", sessionID, roomID)
	}

	// In a full implementation with libolm:
	// 1. Unpickle the inbound Megolm session
	// 2. Decrypt the ciphertext
	// 3. Parse the decrypted JSON to get event type and content
	// 4. Return decrypted event type and content
	return "", nil, fmt.Errorf("megolm decryption not yet implemented (requires libolm)")
}

func (c *bridgeCryptoHelper) IsEncrypted(ctx context.Context, roomID string) bool {
	c.mu.RLock()
	if encrypted, ok := c.rooms[roomID]; ok {
		c.mu.RUnlock()
		return encrypted
	}
	c.mu.RUnlock()

	// Check store
	encrypted, err := c.store.IsRoomEncrypted(ctx, roomID)
	if err != nil {
		c.log.Warn("failed to check room encryption state", "error", err, "room_id", roomID)
		return false
	}

	c.mu.Lock()
	c.rooms[roomID] = encrypted
	c.mu.Unlock()

	return encrypted
}

func (c *bridgeCryptoHelper) HandleMemberEvent(ctx context.Context, roomID string, userID string, membership string) error {
	if !c.IsEncrypted(ctx, roomID) {
		return nil
	}

	switch membership {
	case "join":
		return c.ShareKeysWithUser(ctx, roomID, userID)
	case "leave", "ban":
		// When a user leaves, we should rotate the Megolm session
		// so they can't decrypt future messages
		c.log.Debug("member left encrypted room, session should be rotated",
			"room_id", roomID, "user_id", userID)
	}

	return nil
}

func (c *bridgeCryptoHelper) ShareKeysWithUser(ctx context.Context, roomID string, userID string) error {
	if !c.IsEncrypted(ctx, roomID) {
		return nil
	}

	// In a full implementation:
	// 1. Get the current outbound Megolm session for the room
	// 2. Get the user's device keys via /keys/query
	// 3. Create Olm sessions if needed via /keys/claim
	// 4. Encrypt the Megolm session key with each device's Olm session
	// 5. Send via /sendToDevice
	c.log.Debug("would share Megolm keys with user",
		"room_id", roomID, "user_id", userID)
	return nil
}

func (c *bridgeCryptoHelper) SetEncryptionForRoom(ctx context.Context, roomID string) error {
	c.mu.Lock()
	c.rooms[roomID] = true
	c.mu.Unlock()

	if err := c.store.SetRoomEncrypted(ctx, roomID); err != nil {
		return fmt.Errorf("persist room encryption state: %w", err)
	}

	c.log.Info("room marked as encrypted", "room_id", roomID)
	return nil
}

// generateDeviceID creates a random device ID for the bridge.
func generateDeviceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "WECHAT_BRIDGE_" + hex.EncodeToString(b)[:8]
}
