package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/n42/mautrix-wechat/internal/database"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// PuppetManager creates and manages Matrix puppet users that represent WeChat contacts.
// Each WeChat user is mapped to a virtual Matrix user like @wechat_wxid_xxx:domain.
type PuppetManager struct {
	mu       sync.RWMutex
	puppets  map[string]*Puppet // keyed by WeChat ID
	domain   string
	template string // username template, e.g. "wechat_{{.}}"
	dnTempl  string // display name template
	db       *database.UserStore
	intent   MatrixClient // bot intent for creating puppet users
}

// Puppet represents a virtual Matrix user standing in for a WeChat contact.
type Puppet struct {
	WeChatID     string
	MatrixUserID string
	Nickname     string
	AvatarURL    string
	AvatarMXC    string
	NameSet      bool
	AvatarSet    bool
}

// MatrixClient abstracts Matrix homeserver operations needed by the bridge.
// The real implementation wraps the mautrix-go client.
type MatrixClient interface {
	// EnsureRegistered registers a puppet user if not already registered.
	EnsureRegistered(ctx context.Context, userID string) error
	// SetDisplayName sets the display name for a puppet user.
	SetDisplayName(ctx context.Context, userID, name string) error
	// SetAvatarURL sets the avatar for a puppet user via MXC URI.
	SetAvatarURL(ctx context.Context, userID, mxcURI string) error
	// UploadMedia uploads media data and returns an MXC URI.
	UploadMedia(ctx context.Context, data []byte, mimeType, fileName string) (string, error)
	// SendMessage sends a Matrix event to a room on behalf of a user.
	SendMessage(ctx context.Context, roomID, senderUserID string, content interface{}) (string, error)
	// SendMessageWithTimestamp sends a Matrix event with a specified timestamp (for backfill).
	SendMessageWithTimestamp(ctx context.Context, roomID, senderUserID string, content interface{}, timestamp int64) (string, error)
	// CreateRoom creates a new Matrix room and returns the room ID.
	CreateRoom(ctx context.Context, req *CreateRoomRequest) (string, error)
	// JoinRoom makes a user join a room.
	JoinRoom(ctx context.Context, userID, roomID string) error
	// LeaveRoom makes a user leave a room.
	LeaveRoom(ctx context.Context, userID, roomID string) error
	// InviteToRoom invites a user to a room.
	InviteToRoom(ctx context.Context, roomID, userID string) error
	// KickFromRoom kicks a user from a room with an optional reason.
	KickFromRoom(ctx context.Context, roomID, userID, reason string) error
	// RedactEvent redacts (removes) a Matrix event.
	RedactEvent(ctx context.Context, roomID, eventID, reason string) error
	// SendStateEvent sends a state event to a room.
	SendStateEvent(ctx context.Context, roomID, eventType, stateKey string, content interface{}) error
	// SetRoomName sets the name of a room.
	SetRoomName(ctx context.Context, roomID, name string) error
	// SetRoomAvatar sets the avatar of a room.
	SetRoomAvatar(ctx context.Context, roomID, mxcURI string) error
	// SetRoomTopic sets the topic of a room.
	SetRoomTopic(ctx context.Context, roomID, topic string) error
	// SetTyping sends a typing indicator for a user in a room.
	SetTyping(ctx context.Context, roomID, userID string, typing bool, timeoutMs int) error
	// SetPresence sets the presence status of a user.
	SetPresence(ctx context.Context, userID string, online bool) error
	// SendReadReceipt sends a read receipt for an event.
	SendReadReceipt(ctx context.Context, roomID, eventID, userID string) error
	// CreateSpace creates a Matrix Space and returns the room ID.
	CreateSpace(ctx context.Context, req *CreateSpaceRequest) (string, error)
	// AddRoomToSpace adds a child room to a parent Space.
	AddRoomToSpace(ctx context.Context, spaceID, roomID string) error
}

// CreateRoomRequest describes a room to be created.
type CreateRoomRequest struct {
	Name        string
	Topic       string
	IsDirect    bool
	Invite      []string
	AvatarMXC   string
	IsEncrypted bool
	SpaceID     string // parent Space ID
}

// CreateSpaceRequest describes a Space to be created.
type CreateSpaceRequest struct {
	Name      string
	Topic     string
	AvatarMXC string
	Invite    []string
}

// NewPuppetManager creates a new PuppetManager.
func NewPuppetManager(domain, usernameTemplate, displaynameTemplate string, db *database.UserStore, intent MatrixClient) *PuppetManager {
	return &PuppetManager{
		puppets:  make(map[string]*Puppet),
		domain:   domain,
		template: usernameTemplate,
		dnTempl:  displaynameTemplate,
		db:       db,
		intent:   intent,
	}
}

// puppetFromDBUser creates a Puppet from a database WeChatUser record.
func puppetFromDBUser(dbUser *database.WeChatUser) *Puppet {
	return &Puppet{
		WeChatID:     dbUser.WeChatID,
		MatrixUserID: dbUser.MatrixUserID,
		Nickname:     dbUser.Nickname,
		AvatarURL:    dbUser.AvatarURL,
		AvatarMXC:    dbUser.AvatarMXC,
		NameSet:      dbUser.NameSet,
		AvatarSet:    dbUser.AvatarSet,
	}
}

// GetOrCreate returns an existing puppet or creates a new one for the given WeChat contact.
func (pm *PuppetManager) GetOrCreate(ctx context.Context, contact *wechat.ContactInfo) (*Puppet, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, ok := pm.puppets[contact.UserID]; ok {
		return p, nil
	}

	// Check database
	if pm.db == nil {
		return nil, fmt.Errorf("puppet database store not initialized")
	}
	dbUser, err := pm.db.GetByWeChatID(ctx, contact.UserID)
	if err != nil {
		return nil, fmt.Errorf("query puppet from db: %w", err)
	}

	matrixUserID := pm.wechatIDToMatrixID(contact.UserID)

	if dbUser != nil {
		p := puppetFromDBUser(dbUser)
		pm.puppets[contact.UserID] = p
		return p, nil
	}

	// Create new puppet
	if err := pm.intent.EnsureRegistered(ctx, matrixUserID); err != nil {
		return nil, fmt.Errorf("register puppet %s: %w", matrixUserID, err)
	}

	displayName := pm.formatDisplayName(contact)
	if err := pm.intent.SetDisplayName(ctx, matrixUserID, displayName); err != nil {
		return nil, fmt.Errorf("set puppet display name: %w", err)
	}

	p := &Puppet{
		WeChatID:     contact.UserID,
		MatrixUserID: matrixUserID,
		Nickname:     contact.Nickname,
		AvatarURL:    contact.AvatarURL,
		NameSet:      true,
	}

	// Save to database
	dbUser = &database.WeChatUser{
		WeChatID:     contact.UserID,
		Alias:        contact.Alias,
		Nickname:     contact.Nickname,
		AvatarURL:    contact.AvatarURL,
		Gender:       contact.Gender,
		Province:     contact.Province,
		City:         contact.City,
		Signature:    contact.Signature,
		MatrixUserID: matrixUserID,
		NameSet:      true,
	}
	if err := pm.db.Upsert(ctx, dbUser); err != nil {
		return nil, fmt.Errorf("save puppet to db: %w", err)
	}

	pm.puppets[contact.UserID] = p
	return p, nil
}

// UpdateProfile updates a puppet's display name and avatar if they have changed.
func (pm *PuppetManager) UpdateProfile(ctx context.Context, contact *wechat.ContactInfo) error {
	p, err := pm.GetOrCreate(ctx, contact)
	if err != nil {
		return err
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	changed := false

	// Update display name
	if contact.Nickname != p.Nickname {
		displayName := pm.formatDisplayName(contact)
		if err := pm.intent.SetDisplayName(ctx, p.MatrixUserID, displayName); err != nil {
			return fmt.Errorf("update puppet display name: %w", err)
		}
		p.Nickname = contact.Nickname
		p.NameSet = true
		changed = true
	}

	// Update avatar
	if contact.AvatarURL != "" && contact.AvatarURL != p.AvatarURL {
		p.AvatarURL = contact.AvatarURL
		p.AvatarSet = false
		changed = true
	}

	if changed {
		dbUser := &database.WeChatUser{
			WeChatID:     contact.UserID,
			Alias:        contact.Alias,
			Nickname:     contact.Nickname,
			AvatarURL:    contact.AvatarURL,
			AvatarMXC:    p.AvatarMXC,
			MatrixUserID: p.MatrixUserID,
			NameSet:      p.NameSet,
			AvatarSet:    p.AvatarSet,
		}
		if err := pm.db.Upsert(ctx, dbUser); err != nil {
			return fmt.Errorf("update puppet in db: %w", err)
		}
	}

	return nil
}

// GetByWeChatID returns a puppet by WeChat ID, loading from DB if needed.
func (pm *PuppetManager) GetByWeChatID(ctx context.Context, wechatID string) (*Puppet, error) {
	pm.mu.RLock()
	if p, ok := pm.puppets[wechatID]; ok {
		pm.mu.RUnlock()
		return p, nil
	}
	pm.mu.RUnlock()

	if pm.db == nil {
		return nil, nil
	}
	dbUser, err := pm.db.GetByWeChatID(ctx, wechatID)
	if err != nil {
		return nil, err
	}
	if dbUser == nil {
		return nil, nil
	}

	p := puppetFromDBUser(dbUser)

	pm.mu.Lock()
	pm.puppets[wechatID] = p
	pm.mu.Unlock()

	return p, nil
}

// GetByMatrixID returns a puppet by Matrix user ID.
func (pm *PuppetManager) GetByMatrixID(ctx context.Context, matrixID string) (*Puppet, error) {
	wechatID := pm.matrixIDToWeChatID(matrixID)
	if wechatID == "" {
		return nil, nil
	}
	return pm.GetByWeChatID(ctx, wechatID)
}

// wechatIDToMatrixID converts a WeChat ID to a Matrix user ID.
func (pm *PuppetManager) wechatIDToMatrixID(wechatID string) string {
	localpart := strings.ReplaceAll(pm.template, "{{.}}", wechatID)
	return fmt.Sprintf("@%s:%s", localpart, pm.domain)
}

// matrixIDToWeChatID extracts a WeChat ID from a Matrix user ID.
func (pm *PuppetManager) matrixIDToWeChatID(matrixID string) string {
	prefix := "@" + strings.ReplaceAll(pm.template, "{{.}}", "")
	suffix := ":" + pm.domain

	if !strings.HasPrefix(matrixID, prefix) || !strings.HasSuffix(matrixID, suffix) {
		return ""
	}

	return matrixID[len(prefix) : len(matrixID)-len(suffix)]
}

// formatDisplayName formats the display name for a puppet using the template.
func (pm *PuppetManager) formatDisplayName(contact *wechat.ContactInfo) string {
	name := contact.Nickname
	if contact.Remark != "" {
		name = contact.Remark
	}
	return strings.ReplaceAll(pm.dnTempl, "{{.Nickname}}", name)
}

// IsPuppet returns true if the Matrix user ID corresponds to a puppet user.
func (pm *PuppetManager) IsPuppet(matrixID string) bool {
	return pm.matrixIDToWeChatID(matrixID) != ""
}
