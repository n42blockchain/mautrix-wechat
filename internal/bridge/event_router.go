package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/n42/mautrix-wechat/internal/database"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// MatrixEvent represents an incoming Matrix event received via the AS API.
type MatrixEvent struct {
	ID        string
	Type      string // e.g. "m.room.message", "m.room.redaction"
	RoomID    string
	Sender    string
	Content   map[string]interface{}
	Timestamp int64
	Unsigned  map[string]interface{} // unsigned data (e.g. redacts field)
}

// EventRouter dispatches events between Matrix and WeChat.
// Matrix -> WeChat: receives Matrix events via HandleMatrixEvent
// WeChat -> Matrix: implements wechat.MessageHandler to receive provider callbacks
type EventRouter struct {
	log          *slog.Logger
	puppets      *PuppetManager
	processor    MessageProcessor
	provider     wechat.Provider
	providerMu   sync.RWMutex
	rooms        *database.RoomMappingStore
	messages     *database.MessageMappingStore
	bridgeUsers  *database.BridgeUserStore
	groupMembers *database.GroupMemberStore
	matrixClient MatrixClient
	crypto       CryptoHelper
	metrics      *Metrics
}

// MessageProcessor converts between WeChat and Matrix message formats.
type MessageProcessor interface {
	// WeChatToMatrix converts a WeChat message to Matrix event content.
	WeChatToMatrix(ctx context.Context, msg *wechat.Message) (*MatrixEventContent, error)
	// MatrixToWeChat converts a Matrix event to a WeChat send action.
	MatrixToWeChat(ctx context.Context, evt *MatrixEvent) (*WeChatSendAction, error)
}

// MatrixEventContent represents a Matrix event to be sent.
type MatrixEventContent struct {
	EventType string                 // e.g. "m.room.message"
	Content   map[string]interface{} // Matrix event content
}

// WeChatSendAction describes a message to be sent to WeChat.
type WeChatSendAction struct {
	Type     wechat.MsgType
	ToUser   string
	Text     string
	Media    []byte
	File     string
	ReplyTo  string   // WeChat message ID to reply to
	Mentions []string // WeChat user IDs to @mention
	Extra    map[string]interface{}
}

// EventRouterConfig holds configuration for the event router.
type EventRouterConfig struct {
	Log          *slog.Logger
	Puppets      *PuppetManager
	Processor    MessageProcessor
	Provider     wechat.Provider
	Rooms        *database.RoomMappingStore
	Messages     *database.MessageMappingStore
	BridgeUsers  *database.BridgeUserStore
	GroupMembers *database.GroupMemberStore
	MatrixClient MatrixClient
	Crypto       CryptoHelper
	Metrics      *Metrics
}

// NewEventRouter creates a new EventRouter.
func NewEventRouter(cfg EventRouterConfig) *EventRouter {
	crypto := cfg.Crypto
	if crypto == nil {
		crypto = &noopCryptoHelper{}
	}
	return &EventRouter{
		log:          cfg.Log,
		puppets:      cfg.Puppets,
		processor:    cfg.Processor,
		provider:     cfg.Provider,
		rooms:        cfg.Rooms,
		messages:     cfg.Messages,
		bridgeUsers:  cfg.BridgeUsers,
		groupMembers: cfg.GroupMembers,
		matrixClient: cfg.MatrixClient,
		crypto:       crypto,
		metrics:      cfg.Metrics,
	}
}

// SetProvider updates the active provider (used when failover switches providers).
func (er *EventRouter) SetProvider(p wechat.Provider) {
	er.providerMu.Lock()
	defer er.providerMu.Unlock()
	er.provider = p
}

// getProvider returns the current active provider in a thread-safe manner.
func (er *EventRouter) getProvider() wechat.Provider {
	er.providerMu.RLock()
	defer er.providerMu.RUnlock()
	return er.provider
}

// === Matrix → WeChat direction ===

// HandleMatrixEvent processes an incoming Matrix event and forwards it to WeChat.
func (er *EventRouter) HandleMatrixEvent(ctx context.Context, evt *MatrixEvent) error {
	// Ignore events from puppet users (echo prevention)
	if er.puppets.IsPuppet(evt.Sender) {
		return nil
	}

	// Look up the room mapping
	if er.rooms == nil {
		return fmt.Errorf("room store not initialized")
	}
	room, err := er.rooms.GetByMatrixRoomID(ctx, evt.RoomID)
	if err != nil {
		return fmt.Errorf("look up room %s: %w", evt.RoomID, err)
	}
	if room == nil {
		er.log.Debug("ignoring event in unmapped room", "room_id", evt.RoomID)
		return nil
	}

	switch evt.Type {
	case "m.room.message":
		return er.handleMatrixMessage(ctx, evt, room)
	case "m.room.redaction":
		return er.handleMatrixRedaction(ctx, evt, room)
	case "m.room.encrypted":
		return er.handleMatrixEncrypted(ctx, evt, room)
	case "m.room.encryption":
		return er.crypto.SetEncryptionForRoom(ctx, evt.RoomID)
	case "m.room.member":
		membership, _ := evt.Content["membership"].(string)
		return er.crypto.HandleMemberEvent(ctx, evt.RoomID, evt.Sender, membership)
	default:
		er.log.Debug("ignoring unsupported matrix event type", "type", evt.Type)
		return nil
	}
}

// handleMatrixMessage processes a Matrix message event.
func (er *EventRouter) handleMatrixMessage(ctx context.Context, evt *MatrixEvent, room *database.RoomMapping) error {
	startTime := time.Now()
	if er.metrics != nil {
		defer func() {
			er.metrics.ObserveMatrixToWeChatLatency(time.Since(startTime))
		}()
	}

	if er.processor == nil {
		return fmt.Errorf("message processor not initialized")
	}

	action, err := er.processor.MatrixToWeChat(ctx, evt)
	if err != nil {
		return fmt.Errorf("convert matrix event to wechat: %w", err)
	}
	if action == nil {
		return nil
	}

	// Resolve reply-to: convert Matrix event ID → WeChat message ID
	if action.ReplyTo != "" {
		mapping, err := er.messages.GetByMatrixEventID(ctx, action.ReplyTo)
		if err == nil && mapping != nil {
			action.ReplyTo = mapping.WeChatMsgID
		} else {
			er.log.Debug("reply-to matrix event not found in mapping",
				"event_id", action.ReplyTo)
			action.ReplyTo = ""
		}
	}

	provider := er.getProvider()
	if provider == nil {
		return fmt.Errorf("no active provider")
	}

	target := room.WeChatChatID

	var msgID string
	switch action.Type {
	case wechat.MsgText:
		msgID, err = provider.SendText(ctx, target, action.Text)
	case wechat.MsgImage:
		er.log.Warn("image sending from Matrix not yet fully implemented")
		return nil
	default:
		er.log.Warn("unsupported wechat send type", "type", action.Type)
		return nil
	}

	if err != nil {
		if er.metrics != nil {
			er.metrics.IncrMessagesFailed()
		}
		return fmt.Errorf("send wechat message: %w", err)
	}

	if er.metrics != nil {
		er.metrics.IncrMessagesSent()
	}

	// Save message mapping
	if msgID != "" {
		mapping := &database.MessageMapping{
			WeChatMsgID:   msgID,
			MatrixEventID: evt.ID,
			MatrixRoomID:  evt.RoomID,
			Sender:        evt.Sender,
			MsgType:       int(action.Type),
		}
		if err := er.messages.Insert(ctx, mapping); err != nil {
			er.log.Error("failed to save message mapping", "error", err)
		}
	}

	return nil
}

// handleMatrixRedaction processes a Matrix redaction event (Matrix → WeChat revoke).
func (er *EventRouter) handleMatrixRedaction(ctx context.Context, evt *MatrixEvent, room *database.RoomMapping) error {
	redactedEventID, _ := evt.Content["redacts"].(string)
	if redactedEventID == "" {
		return nil
	}

	mapping, err := er.messages.GetByMatrixEventID(ctx, redactedEventID)
	if err != nil {
		return fmt.Errorf("look up redacted message: %w", err)
	}
	if mapping == nil {
		er.log.Debug("ignoring redaction for unknown message", "event_id", redactedEventID)
		return nil
	}

	provider := er.getProvider()
	if provider == nil {
		return fmt.Errorf("no active provider")
	}
	if err := provider.RevokeMessage(ctx, mapping.WeChatMsgID, room.WeChatChatID); err != nil {
		return fmt.Errorf("revoke wechat message: %w", err)
	}

	er.log.Info("forwarded Matrix redaction to WeChat revoke",
		"matrix_event", redactedEventID, "wechat_msg", mapping.WeChatMsgID)
	return nil
}

// handleMatrixEncrypted decrypts an m.room.encrypted event and processes the plaintext.
func (er *EventRouter) handleMatrixEncrypted(ctx context.Context, evt *MatrixEvent, room *database.RoomMapping) error {
	decryptedType, decryptedContent, err := er.crypto.Decrypt(ctx, evt.RoomID, evt.Content)
	if err != nil {
		er.log.Error("failed to decrypt matrix event",
			"error", err, "event_id", evt.ID, "room_id", evt.RoomID)
		return fmt.Errorf("decrypt matrix event: %w", err)
	}

	// Create a new event with decrypted content and process normally
	decryptedEvt := &MatrixEvent{
		ID:        evt.ID,
		Type:      decryptedType,
		RoomID:    evt.RoomID,
		Sender:    evt.Sender,
		Content:   decryptedContent,
		Timestamp: evt.Timestamp,
		Unsigned:  evt.Unsigned,
	}

	switch decryptedType {
	case "m.room.message":
		return er.handleMatrixMessage(ctx, decryptedEvt, room)
	case "m.room.redaction":
		return er.handleMatrixRedaction(ctx, decryptedEvt, room)
	default:
		er.log.Debug("ignoring unsupported decrypted event type", "type", decryptedType)
		return nil
	}
}

// === WeChat → Matrix direction (wechat.MessageHandler implementation) ===

// OnMessage handles incoming WeChat messages and forwards them to Matrix.
func (er *EventRouter) OnMessage(ctx context.Context, msg *wechat.Message) error {
	startTime := time.Now()
	er.log.Info("received wechat message",
		"msg_id", msg.MsgID, "type", msg.Type, "from", msg.FromUser)

	if er.metrics != nil {
		er.metrics.IncrMessagesReceived()
		defer func() {
			er.metrics.ObserveWeChatToMatrixLatency(time.Since(startTime))
		}()
	}

	// Get or create the sender puppet
	senderPuppet, err := er.puppets.GetOrCreate(ctx, &wechat.ContactInfo{
		UserID:   msg.FromUser,
		Nickname: msg.FromUser,
	})
	if err != nil {
		return fmt.Errorf("get sender puppet: %w", err)
	}

	// Determine the chat ID (group or DM)
	chatID := msg.FromUser
	if msg.IsGroup {
		chatID = msg.GroupID
	}

	// Find the bridge user for this chat
	bridgeUser, err := er.findBridgeUser(ctx)
	if err != nil {
		return fmt.Errorf("find bridge user: %w", err)
	}
	if bridgeUser == nil {
		er.log.Warn("no bridge user found for incoming message")
		return nil
	}

	// Get or create the room
	room, err := er.getOrCreateRoom(ctx, chatID, msg.IsGroup, bridgeUser.MatrixUserID)
	if err != nil {
		return fmt.Errorf("get or create room: %w", err)
	}

	// Convert the message
	if er.processor == nil {
		return fmt.Errorf("message processor not initialized")
	}
	content, err := er.processor.WeChatToMatrix(ctx, msg)
	if err != nil {
		return fmt.Errorf("convert wechat message: %w", err)
	}
	if content == nil {
		return nil
	}

	// Resolve reply-to: convert WeChat msg ID → Matrix event ID
	if msg.ReplyTo != "" {
		er.resolveReplyTo(ctx, msg.ReplyTo, room.MatrixRoomID, content)
	}

	// Encrypt if the room has encryption enabled
	encEventType, encContent, encErr := er.crypto.Encrypt(ctx, room.MatrixRoomID, content.EventType, content.Content)
	if encErr != nil {
		er.log.Warn("failed to encrypt event, sending unencrypted",
			"error", encErr, "room_id", room.MatrixRoomID)
	} else {
		content.EventType = encEventType
		content.Content = encContent
	}

	// Send to Matrix as the puppet user
	if er.matrixClient == nil {
		er.log.Warn("matrixClient not configured, cannot forward to Matrix",
			"msg_id", msg.MsgID)
		return nil
	}
	eventID, err := er.matrixClient.SendMessage(ctx, room.MatrixRoomID, senderPuppet.MatrixUserID, content.Content)
	if err != nil {
		return fmt.Errorf("send matrix message: %w", err)
	}

	// Save message mapping
	mapping := &database.MessageMapping{
		WeChatMsgID:   msg.MsgID,
		MatrixEventID: eventID,
		MatrixRoomID:  room.MatrixRoomID,
		Sender:        msg.FromUser,
		MsgType:       int(msg.Type),
	}
	if err := er.messages.Insert(ctx, mapping); err != nil {
		er.log.Error("failed to save message mapping", "error", err)
	}

	return nil
}

// OnLoginEvent handles login state changes from the provider.
func (er *EventRouter) OnLoginEvent(ctx context.Context, evt *wechat.LoginEvent) error {
	er.log.Info("login event", "state", evt.State)
	return nil
}

// OnContactUpdate handles contact info updates from the provider.
// It syncs WeChat nicknames and avatars to Matrix puppet profiles.
func (er *EventRouter) OnContactUpdate(ctx context.Context, contact *wechat.ContactInfo) error {
	if err := er.puppets.UpdateProfile(ctx, contact); err != nil {
		er.log.Error("failed to update puppet profile", "error", err, "user_id", contact.UserID)
		return err
	}

	// If avatar URL changed, try to upload and set on Matrix
	puppet, err := er.puppets.GetByWeChatID(ctx, contact.UserID)
	if err != nil || puppet == nil {
		return nil
	}

	if contact.AvatarURL != "" && !puppet.AvatarSet {
		er.syncPuppetAvatar(ctx, puppet, contact)
	}

	return nil
}

// OnGroupMemberUpdate handles group member changes.
// It syncs WeChat group membership to Matrix room membership.
func (er *EventRouter) OnGroupMemberUpdate(ctx context.Context, groupID string, members []*wechat.GroupMember) error {
	er.log.Info("group member update", "group_id", groupID, "count", len(members))

	bridgeUser, err := er.findBridgeUser(ctx)
	if err != nil || bridgeUser == nil {
		return nil
	}

	room, err := er.rooms.GetByWeChatChat(ctx, groupID, bridgeUser.MatrixUserID)
	if err != nil || room == nil {
		er.log.Debug("no room found for group, skipping member sync", "group_id", groupID)
		return nil
	}

	if er.groupMembers == nil {
		return nil
	}

	// Get current members from DB
	existingMembers, err := er.groupMembers.GetByGroup(ctx, groupID)
	if err != nil {
		return fmt.Errorf("get existing members: %w", err)
	}

	existingMap := make(map[string]*database.GroupMemberRow)
	for _, m := range existingMembers {
		existingMap[m.WeChatID] = m
	}

	// Build new member set
	newMemberIDs := make(map[string]bool)
	for _, m := range members {
		newMemberIDs[m.UserID] = true

		// Ensure puppet exists and is in the room
		puppet, err := er.puppets.GetOrCreate(ctx, &wechat.ContactInfo{
			UserID:   m.UserID,
			Nickname: m.Nickname,
		})
		if err != nil {
			er.log.Error("failed to create puppet for group member", "error", err, "user_id", m.UserID)
			continue
		}

		// If new member, invite/join to Matrix room
		if _, exists := existingMap[m.UserID]; !exists && er.matrixClient != nil {
			if err := er.matrixClient.InviteToRoom(ctx, room.MatrixRoomID, puppet.MatrixUserID); err != nil {
				er.log.Warn("failed to invite puppet to room", "error", err, "user_id", m.UserID)
			}
			if err := er.matrixClient.JoinRoom(ctx, puppet.MatrixUserID, room.MatrixRoomID); err != nil {
				er.log.Warn("failed to join puppet to room", "error", err, "user_id", m.UserID)
			}
		}

		// Update display name if changed
		if m.DisplayName != "" {
			if existing, ok := existingMap[m.UserID]; !ok || existing.DisplayName != m.DisplayName {
				// Could set room-level display name override here
			}
		}

		// Upsert in DB
		now := time.Now()
		if err := er.groupMembers.Upsert(ctx, &database.GroupMemberRow{
			GroupID:     groupID,
			WeChatID:    m.UserID,
			DisplayName: m.DisplayName,
			IsAdmin:     m.IsAdmin,
			IsOwner:     m.IsOwner,
			JoinedAt:    &now,
		}); err != nil {
			er.log.Error("failed to upsert group member", "error", err, "group_id", groupID, "user_id", m.UserID)
		}
	}

	// Handle removed members
	for wechatID, member := range existingMap {
		if !newMemberIDs[wechatID] {
			puppet, _ := er.puppets.GetByWeChatID(ctx, wechatID)
			if puppet != nil && er.matrixClient != nil {
				if err := er.matrixClient.KickFromRoom(ctx, room.MatrixRoomID, puppet.MatrixUserID, "removed from WeChat group"); err != nil {
					er.log.Warn("failed to kick puppet from room", "error", err, "user_id", wechatID)
				}
			}
			if err := er.groupMembers.DeleteMember(ctx, groupID, member.WeChatID); err != nil {
				er.log.Error("failed to delete group member", "error", err, "group_id", groupID, "user_id", member.WeChatID)
			}
		}
	}

	return nil
}

// OnPresence handles online/offline status changes.
func (er *EventRouter) OnPresence(ctx context.Context, userID string, online bool) error {
	if er.matrixClient == nil {
		return nil
	}
	puppet, err := er.puppets.GetByWeChatID(ctx, userID)
	if err != nil || puppet == nil {
		return nil
	}

	return er.matrixClient.SetPresence(ctx, puppet.MatrixUserID, online)
}

// OnTyping handles typing indicator events.
func (er *EventRouter) OnTyping(ctx context.Context, userID string, chatID string) error {
	if er.matrixClient == nil {
		return nil
	}
	puppet, err := er.puppets.GetByWeChatID(ctx, userID)
	if err != nil || puppet == nil {
		return nil
	}

	bridgeUser, err := er.findBridgeUser(ctx)
	if err != nil || bridgeUser == nil {
		return nil
	}

	room, err := er.rooms.GetByWeChatChat(ctx, chatID, bridgeUser.MatrixUserID)
	if err != nil || room == nil {
		return nil
	}

	return er.matrixClient.SetTyping(ctx, room.MatrixRoomID, puppet.MatrixUserID, true, 30000)
}

// OnRevoke handles message revocation events (WeChat → Matrix redaction).
func (er *EventRouter) OnRevoke(ctx context.Context, msgID string, replaceTip string) error {
	if er.matrixClient == nil {
		return nil
	}
	mapping, err := er.messages.GetByWeChatMsgID(ctx, msgID, "")
	if err != nil || mapping == nil {
		er.log.Debug("ignoring revoke for unknown message", "msg_id", msgID)
		return nil
	}

	reason := replaceTip
	if reason == "" {
		reason = "message revoked"
	}

	if err := er.matrixClient.RedactEvent(ctx, mapping.MatrixRoomID, mapping.MatrixEventID, reason); err != nil {
		return fmt.Errorf("redact matrix event: %w", err)
	}

	er.log.Info("forwarded WeChat revoke to Matrix redaction",
		"wechat_msg", msgID, "matrix_event", mapping.MatrixEventID)
	return nil
}

// === Reply resolution ===

// resolveReplyTo converts a WeChat reply-to message ID to a Matrix m.in_reply_to reference.
func (er *EventRouter) resolveReplyTo(ctx context.Context, wechatMsgID, matrixRoomID string, content *MatrixEventContent) {
	mapping, err := er.messages.GetByWeChatMsgID(ctx, wechatMsgID, matrixRoomID)
	if err != nil || mapping == nil {
		er.log.Debug("reply-to message not found in mapping", "wechat_msg_id", wechatMsgID)
		return
	}

	// Set Matrix reply relation
	content.Content["m.relates_to"] = map[string]interface{}{
		"m.in_reply_to": map[string]interface{}{
			"event_id": mapping.MatrixEventID,
		},
	}
}

// === Avatar sync ===

// syncPuppetAvatar downloads a WeChat avatar and uploads it to Matrix.
func (er *EventRouter) syncPuppetAvatar(ctx context.Context, puppet *Puppet, contact *wechat.ContactInfo) {
	provider := er.getProvider()
	if provider == nil {
		er.log.Warn("no active provider, cannot sync avatar",
			"user_id", contact.UserID)
		return
	}
	avatarData, mimeType, err := provider.GetUserAvatar(ctx, contact.UserID)
	if err != nil {
		er.log.Warn("failed to download avatar for puppet",
			"error", err, "user_id", contact.UserID)
		return
	}

	mxcURI, err := er.matrixClient.UploadMedia(ctx, avatarData, mimeType, "avatar")
	if err != nil {
		er.log.Warn("failed to upload avatar to Matrix",
			"error", err, "user_id", contact.UserID)
		return
	}

	if err := er.matrixClient.SetAvatarURL(ctx, puppet.MatrixUserID, mxcURI); err != nil {
		er.log.Warn("failed to set puppet avatar",
			"error", err, "user_id", contact.UserID)
		return
	}

	puppet.AvatarMXC = mxcURI
	puppet.AvatarSet = true
	er.log.Info("synced puppet avatar", "user_id", contact.UserID, "mxc", mxcURI)
}

// === Space management ===

// EnsureUserSpace creates or returns the Matrix Space for a bridge user.
func (er *EventRouter) EnsureUserSpace(ctx context.Context, bridgeUser *database.BridgeUser) (string, error) {
	if bridgeUser.SpaceRoom != "" {
		return bridgeUser.SpaceRoom, nil
	}

	if er.matrixClient == nil {
		return "", fmt.Errorf("matrixClient not configured")
	}

	spaceID, err := er.matrixClient.CreateSpace(ctx, &CreateSpaceRequest{
		Name:   "WeChat",
		Topic:  "WeChat bridged chats",
		Invite: []string{bridgeUser.MatrixUserID},
	})
	if err != nil {
		return "", fmt.Errorf("create space: %w", err)
	}

	bridgeUser.SpaceRoom = spaceID
	if err := er.bridgeUsers.Upsert(ctx, bridgeUser); err != nil {
		return "", fmt.Errorf("save space room: %w", err)
	}

	er.log.Info("created WeChat Space", "space_id", spaceID, "user", bridgeUser.MatrixUserID)
	return spaceID, nil
}

// AddRoomToUserSpace adds a bridged room to the user's WeChat Space.
func (er *EventRouter) AddRoomToUserSpace(ctx context.Context, bridgeUser *database.BridgeUser, roomID string) error {
	spaceID, err := er.EnsureUserSpace(ctx, bridgeUser)
	if err != nil {
		return err
	}
	return er.matrixClient.AddRoomToSpace(ctx, spaceID, roomID)
}

// === History backfill ===

// BackfillRoom fetches recent messages from WeChat and inserts them into a Matrix room
// with correct timestamps, creating the illusion of message history.
func (er *EventRouter) BackfillRoom(ctx context.Context, room *database.RoomMapping, messages []*wechat.Message) error {
	if len(messages) == 0 {
		return nil
	}
	if er.processor == nil || er.matrixClient == nil {
		return fmt.Errorf("processor or matrixClient not initialized for backfill")
	}

	er.log.Info("backfilling room",
		"room_id", room.MatrixRoomID,
		"message_count", len(messages))

	for _, msg := range messages {
		// Skip already-bridged messages
		existing, _ := er.messages.GetByWeChatMsgID(ctx, msg.MsgID, room.MatrixRoomID)
		if existing != nil {
			continue
		}

		// Get puppet for sender
		senderPuppet, err := er.puppets.GetOrCreate(ctx, &wechat.ContactInfo{
			UserID:   msg.FromUser,
			Nickname: msg.FromUser,
		})
		if err != nil {
			er.log.Error("backfill: failed to get puppet", "error", err, "user", msg.FromUser)
			continue
		}

		// Convert message
		content, err := er.processor.WeChatToMatrix(ctx, msg)
		if err != nil || content == nil {
			continue
		}

		// Resolve replies
		if msg.ReplyTo != "" {
			er.resolveReplyTo(ctx, msg.ReplyTo, room.MatrixRoomID, content)
		}

		// Send with historical timestamp
		eventID, err := er.matrixClient.SendMessageWithTimestamp(
			ctx, room.MatrixRoomID, senderPuppet.MatrixUserID,
			content.Content, msg.Timestamp,
		)
		if err != nil {
			er.log.Error("backfill: failed to send message",
				"error", err, "msg_id", msg.MsgID)
			continue
		}

		// Save mapping
		er.messages.Insert(ctx, &database.MessageMapping{
			WeChatMsgID:   msg.MsgID,
			MatrixEventID: eventID,
			MatrixRoomID:  room.MatrixRoomID,
			Sender:        msg.FromUser,
			MsgType:       int(msg.Type),
		})
	}

	return nil
}

// === Helpers ===

// findBridgeUser returns the first logged-in bridge user (single-user mode).
func (er *EventRouter) findBridgeUser(ctx context.Context) (*database.BridgeUser, error) {
	if er.bridgeUsers == nil {
		return nil, fmt.Errorf("bridge user store not initialized")
	}
	users, err := er.bridgeUsers.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.LoginState == int(wechat.LoginStateLoggedIn) {
			return u, nil
		}
	}
	return nil, nil
}

// getOrCreateRoom finds or creates a Matrix room for a WeChat chat.
func (er *EventRouter) getOrCreateRoom(ctx context.Context, chatID string, isGroup bool, bridgeUser string) (*database.RoomMapping, error) {
	room, err := er.rooms.GetByWeChatChat(ctx, chatID, bridgeUser)
	if err != nil {
		return nil, err
	}
	if room != nil {
		return room, nil
	}

	if er.matrixClient == nil {
		return nil, fmt.Errorf("matrixClient not configured, cannot create room")
	}

	// Create the room
	req := &CreateRoomRequest{
		IsDirect: !isGroup,
		Invite:   []string{bridgeUser},
	}

	provider := er.getProvider()
	if isGroup && provider != nil {
		groupInfo, err := provider.GetGroupInfo(ctx, chatID)
		if err == nil && groupInfo != nil {
			req.Name = groupInfo.Nickname
		}
	}

	matrixRoomID, err := er.matrixClient.CreateRoom(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create matrix room: %w", err)
	}

	room = &database.RoomMapping{
		WeChatChatID: chatID,
		MatrixRoomID: matrixRoomID,
		BridgeUser:   bridgeUser,
		IsGroup:      isGroup,
		Name:         req.Name,
	}

	if err := er.rooms.Upsert(ctx, room); err != nil {
		return nil, fmt.Errorf("save room mapping: %w", err)
	}

	// Add to user's Space
	user, _ := er.bridgeUsers.GetByMatrixID(ctx, bridgeUser)
	if user != nil {
		er.AddRoomToUserSpace(ctx, user, matrixRoomID)
	}

	return room, nil
}
