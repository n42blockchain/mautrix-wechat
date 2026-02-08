package ipad

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// CallbackHandler processes incoming webhook callbacks from the GeWeChat service.
type CallbackHandler struct {
	log     *slog.Logger
	handler wechat.MessageHandler
}

// NewCallbackHandler creates a new callback handler.
func NewCallbackHandler(log *slog.Logger, handler wechat.MessageHandler) *CallbackHandler {
	return &CallbackHandler{
		log:     log,
		handler: handler,
	}
}

// ServeHTTP implements http.Handler for the callback endpoint.
func (ch *CallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		ch.log.Warn("invalid callback payload", "error", err)
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	ch.dispatch(ctx, payload)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"ret":0}`)
}

// dispatch routes the callback payload to the appropriate handler.
func (ch *CallbackHandler) dispatch(ctx context.Context, payload map[string]interface{}) {
	cbType, _ := payload["type"].(string)

	switch cbType {
	case "message":
		ch.handleMessage(ctx, payload)
	case "contact_update":
		ch.handleContactUpdate(ctx, payload)
	case "group_member_update":
		ch.handleGroupMemberUpdate(ctx, payload)
	case "friend_request":
		ch.handleFriendRequest(ctx, payload)
	case "revoke":
		ch.handleRevoke(ctx, payload)
	case "typing":
		ch.handleTyping(ctx, payload)
	case "presence":
		ch.handlePresence(ctx, payload)
	case "login_status":
		ch.handleLoginStatus(ctx, payload)
	default:
		ch.log.Debug("unknown callback type", "type", cbType, "payload_keys", mapKeys(payload))
	}
}

// handleMessage processes incoming messages of all types.
func (ch *CallbackHandler) handleMessage(ctx context.Context, data map[string]interface{}) {
	msg := ch.parseMessage(data)
	if msg == nil {
		ch.log.Warn("failed to parse callback message")
		return
	}

	ch.log.Debug("received message",
		"msg_id", msg.MsgID,
		"type", msg.Type,
		"from", msg.FromUser,
		"group", msg.GroupID)

	if err := ch.handler.OnMessage(ctx, msg); err != nil {
		ch.log.Error("handle message failed", "error", err, "msg_id", msg.MsgID)
	}
}

// handleContactUpdate processes contact changes (name, avatar, etc.).
func (ch *CallbackHandler) handleContactUpdate(ctx context.Context, data map[string]interface{}) {
	contact := ch.parseContact(data)
	if contact == nil {
		return
	}

	ch.log.Debug("contact update", "user_id", contact.UserID, "nickname", contact.Nickname)

	if err := ch.handler.OnContactUpdate(ctx, contact); err != nil {
		ch.log.Error("handle contact update failed", "error", err, "user_id", contact.UserID)
	}
}

// handleGroupMemberUpdate processes group membership changes.
func (ch *CallbackHandler) handleGroupMemberUpdate(ctx context.Context, data map[string]interface{}) {
	groupID, _ := data["group_id"].(string)
	if groupID == "" {
		return
	}

	membersRaw, ok := data["members"].([]interface{})
	if !ok {
		return
	}

	members := make([]*wechat.GroupMember, 0, len(membersRaw))
	for _, item := range membersRaw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		member := &wechat.GroupMember{}
		member.UserID, _ = m["user_id"].(string)
		member.Nickname, _ = m["nickname"].(string)
		member.DisplayName, _ = m["display_name"].(string)
		member.AvatarURL, _ = m["avatar_url"].(string)
		if ia, ok := m["is_admin"].(bool); ok {
			member.IsAdmin = ia
		}
		if io, ok := m["is_owner"].(bool); ok {
			member.IsOwner = io
		}
		members = append(members, member)
	}

	ch.log.Debug("group member update", "group_id", groupID, "count", len(members))

	if err := ch.handler.OnGroupMemberUpdate(ctx, groupID, members); err != nil {
		ch.log.Error("handle group member update failed", "error", err, "group_id", groupID)
	}
}

// handleFriendRequest processes incoming friend requests.
func (ch *CallbackHandler) handleFriendRequest(ctx context.Context, data map[string]interface{}) {
	fromUser, _ := data["from_user"].(string)
	content, _ := data["content"].(string)

	ch.log.Info("friend request received", "from", fromUser, "content", content)

	// Emit as a contact update with pending status
	contact := &wechat.ContactInfo{
		UserID:   fromUser,
		Nickname: fromUser,
	}
	if nickname, ok := data["nickname"].(string); ok {
		contact.Nickname = nickname
	}
	if avatarURL, ok := data["avatar_url"].(string); ok {
		contact.AvatarURL = avatarURL
	}

	if err := ch.handler.OnContactUpdate(ctx, contact); err != nil {
		ch.log.Error("handle friend request failed", "error", err)
	}
}

// handleRevoke processes message revocation events.
func (ch *CallbackHandler) handleRevoke(ctx context.Context, data map[string]interface{}) {
	msgID, _ := data["msg_id"].(string)
	replaceTip, _ := data["replace_tip"].(string)
	if msgID == "" {
		return
	}

	ch.log.Debug("message revoke", "msg_id", msgID)

	if err := ch.handler.OnRevoke(ctx, msgID, replaceTip); err != nil {
		ch.log.Error("handle revoke failed", "error", err, "msg_id", msgID)
	}
}

// handleTyping processes typing indicator events.
func (ch *CallbackHandler) handleTyping(ctx context.Context, data map[string]interface{}) {
	userID, _ := data["user_id"].(string)
	chatID, _ := data["chat_id"].(string)
	if userID == "" {
		return
	}

	if err := ch.handler.OnTyping(ctx, userID, chatID); err != nil {
		ch.log.Error("handle typing failed", "error", err)
	}
}

// handlePresence processes online/offline status changes.
func (ch *CallbackHandler) handlePresence(ctx context.Context, data map[string]interface{}) {
	userID, _ := data["user_id"].(string)
	online, _ := data["online"].(bool)
	if userID == "" {
		return
	}

	if err := ch.handler.OnPresence(ctx, userID, online); err != nil {
		ch.log.Error("handle presence failed", "error", err)
	}
}

// handleLoginStatus processes login status changes from GeWeChat push.
func (ch *CallbackHandler) handleLoginStatus(ctx context.Context, data map[string]interface{}) {
	statusCode, _ := data["status"].(float64)
	evt := &wechat.LoginEvent{}

	switch int(statusCode) {
	case 0:
		evt.State = wechat.LoginStateLoggedOut
	case 1:
		evt.State = wechat.LoginStateQRCode
		evt.QRURL, _ = data["qr_url"].(string)
	case 2:
		evt.State = wechat.LoginStateConfirming
	case 3:
		evt.State = wechat.LoginStateLoggedIn
		evt.UserID, _ = data["user_id"].(string)
		evt.Name, _ = data["nickname"].(string)
		evt.Avatar, _ = data["avatar"].(string)
	case -1:
		evt.State = wechat.LoginStateError
		evt.Error, _ = data["error"].(string)
	default:
		ch.log.Warn("unknown login status", "status", int(statusCode))
		return
	}

	ch.log.Info("login status change", "state", evt.State)

	if err := ch.handler.OnLoginEvent(ctx, evt); err != nil {
		ch.log.Error("handle login status failed", "error", err)
	}
}

// parseMessage converts a callback payload to a wechat.Message.
// Returns nil if required fields (msg_id, from_user) are missing or invalid.
func (ch *CallbackHandler) parseMessage(data map[string]interface{}) *wechat.Message {
	msg := &wechat.Message{
		Extra: make(map[string]string),
	}

	var ok bool
	msg.MsgID, ok = data["msg_id"].(string)
	if !ok || msg.MsgID == "" {
		ch.log.Warn("callback message missing msg_id", "keys", mapKeys(data))
		return nil
	}
	msg.FromUser, ok = data["from_user"].(string)
	if !ok || msg.FromUser == "" {
		ch.log.Warn("callback message missing from_user", "msg_id", msg.MsgID)
		return nil
	}

	msg.ToUser, _ = data["to_user"].(string)
	msg.Content, _ = data["content"].(string)
	msg.MediaURL, _ = data["media_url"].(string)
	msg.FileName, _ = data["file_name"].(string)
	msg.GroupID, _ = data["group_id"].(string)
	msg.ReplyTo, _ = data["reply_to"].(string)

	// Parse message type
	if typeNum, ok := data["msg_type"].(float64); ok {
		msg.Type = wechat.MsgType(int(typeNum))
	} else if typeStr, ok := data["msg_type"].(string); ok {
		msg.Type = parseMsgTypeString(typeStr)
	}

	// Numeric fields
	if size, ok := data["file_size"].(float64); ok {
		msg.FileSize = int64(size)
	}
	if dur, ok := data["duration"].(float64); ok {
		msg.Duration = int(dur)
	}
	if ts, ok := data["timestamp"].(float64); ok {
		msg.Timestamp = int64(ts)
	}
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixMilli()
	}

	// Boolean fields
	if isGroup, ok := data["is_group"].(bool); ok {
		msg.IsGroup = isGroup
	}
	// Infer group message from GroupID
	if msg.GroupID != "" {
		msg.IsGroup = true
	}

	// Location
	if lat, ok := data["latitude"].(float64); ok {
		lng, _ := data["longitude"].(float64)
		label, _ := data["label"].(string)
		poiname, _ := data["poiname"].(string)
		msg.Location = &wechat.LocationInfo{
			Latitude:  lat,
			Longitude: lng,
			Label:     label,
			Poiname:   poiname,
		}
	}

	// Link card info
	if title, ok := data["link_title"].(string); ok {
		msg.LinkInfo = &wechat.LinkCardInfo{
			Title: title,
		}
		msg.LinkInfo.Description, _ = data["link_desc"].(string)
		msg.LinkInfo.URL, _ = data["link_url"].(string)
		msg.LinkInfo.ThumbURL, _ = data["link_thumb"].(string)
	}

	// Thumbnail data
	if thumb, ok := data["thumbnail"].(string); ok {
		msg.Thumbnail = []byte(thumb)
	}

	// Extra fields
	if extra, ok := data["extra"].(map[string]interface{}); ok {
		for k, v := range extra {
			msg.Extra[k] = fmt.Sprint(v)
		}
	}

	return msg
}

// parseContact extracts contact info from callback data.
func (ch *CallbackHandler) parseContact(data map[string]interface{}) *wechat.ContactInfo {
	c := &wechat.ContactInfo{}
	c.UserID, _ = data["user_id"].(string)
	if c.UserID == "" {
		return nil
	}
	c.Alias, _ = data["alias"].(string)
	c.Nickname, _ = data["nickname"].(string)
	c.Remark, _ = data["remark"].(string)
	c.AvatarURL, _ = data["avatar_url"].(string)
	c.Province, _ = data["province"].(string)
	c.City, _ = data["city"].(string)
	c.Signature, _ = data["signature"].(string)

	if g, ok := data["gender"].(float64); ok {
		c.Gender = int(g)
	}
	if ig, ok := data["is_group"].(bool); ok {
		c.IsGroup = ig
	}
	if mc, ok := data["member_count"].(float64); ok {
		c.MemberCount = int(mc)
	}
	return c
}

// parseMsgTypeString converts a string message type to wechat.MsgType.
func parseMsgTypeString(s string) wechat.MsgType {
	switch strings.ToLower(s) {
	case "text":
		return wechat.MsgText
	case "image":
		return wechat.MsgImage
	case "voice":
		return wechat.MsgVoice
	case "video":
		return wechat.MsgVideo
	case "emoji":
		return wechat.MsgEmoji
	case "location":
		return wechat.MsgLocation
	case "link":
		return wechat.MsgLink
	case "file":
		return wechat.MsgFile
	case "revoke":
		return wechat.MsgRevoke
	case "system":
		return wechat.MsgSystem
	case "miniapp":
		return wechat.MsgMiniApp
	default:
		// Try numeric parse
		if n, err := strconv.Atoi(s); err == nil {
			return wechat.MsgType(n)
		}
		return wechat.MsgText
	}
}

// mapKeys returns the keys of a map (for debug logging).
func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
