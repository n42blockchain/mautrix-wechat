package ipad

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

var testCallbackLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

type testHandler struct {
	messages []*wechat.Message
	logins   []*wechat.LoginEvent
	contacts []*wechat.ContactInfo
	groups   []groupMemberUpdate
	revokes  []revokeEvent
	typings  []typingEvent
	presence []presenceEvent
}

type groupMemberUpdate struct {
	GroupID string
	Members []*wechat.GroupMember
}

type revokeEvent struct {
	MsgID      string
	ReplaceTip string
}

type typingEvent struct {
	UserID string
	ChatID string
}

type presenceEvent struct {
	UserID string
	Online bool
}

func (h *testHandler) OnMessage(_ context.Context, msg *wechat.Message) error {
	h.messages = append(h.messages, msg)
	return nil
}
func (h *testHandler) OnLoginEvent(_ context.Context, evt *wechat.LoginEvent) error {
	h.logins = append(h.logins, evt)
	return nil
}
func (h *testHandler) OnContactUpdate(_ context.Context, c *wechat.ContactInfo) error {
	h.contacts = append(h.contacts, c)
	return nil
}
func (h *testHandler) OnGroupMemberUpdate(_ context.Context, groupID string, members []*wechat.GroupMember) error {
	h.groups = append(h.groups, groupMemberUpdate{GroupID: groupID, Members: members})
	return nil
}
func (h *testHandler) OnPresence(_ context.Context, userID string, online bool) error {
	h.presence = append(h.presence, presenceEvent{UserID: userID, Online: online})
	return nil
}
func (h *testHandler) OnTyping(_ context.Context, userID string, chatID string) error {
	h.typings = append(h.typings, typingEvent{UserID: userID, ChatID: chatID})
	return nil
}
func (h *testHandler) OnRevoke(_ context.Context, msgID string, replaceTip string) error {
	h.revokes = append(h.revokes, revokeEvent{MsgID: msgID, ReplaceTip: replaceTip})
	return nil
}

func postCallback(ch *CallbackHandler, payload map[string]interface{}) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	ch.ServeHTTP(w, req)
	return w
}

func TestCallbackHandler_TextMessage(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	w := postCallback(ch, map[string]interface{}{
		"type":      "message",
		"msg_id":    "msg_001",
		"msg_type":  float64(1),
		"from_user": "wxid_sender",
		"to_user":   "wxid_receiver",
		"content":   "Hello World",
		"timestamp": float64(1700000000000),
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}

	if len(h.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(h.messages))
	}

	msg := h.messages[0]
	if msg.MsgID != "msg_001" {
		t.Fatalf("msg_id: %s", msg.MsgID)
	}
	if msg.Type != wechat.MsgText {
		t.Fatalf("type: %v", msg.Type)
	}
	if msg.Content != "Hello World" {
		t.Fatalf("content: %s", msg.Content)
	}
	if msg.FromUser != "wxid_sender" {
		t.Fatalf("from: %s", msg.FromUser)
	}
}

func TestCallbackHandler_ImageMessage(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":      "message",
		"msg_id":    "msg_002",
		"msg_type":  float64(3),
		"from_user": "wxid_sender",
		"media_url": "https://example.com/image.jpg",
	})

	if len(h.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(h.messages))
	}

	msg := h.messages[0]
	if msg.Type != wechat.MsgImage {
		t.Fatalf("type: %v", msg.Type)
	}
	if msg.MediaURL != "https://example.com/image.jpg" {
		t.Fatalf("media_url: %s", msg.MediaURL)
	}
}

func TestCallbackHandler_LocationMessage(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":      "message",
		"msg_id":    "msg_003",
		"msg_type":  float64(48),
		"from_user": "wxid_sender",
		"latitude":  23.134521,
		"longitude": 113.358803,
		"label":     "Guangzhou",
		"poiname":   "Canton Tower",
	})

	if len(h.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(h.messages))
	}

	msg := h.messages[0]
	if msg.Type != wechat.MsgLocation {
		t.Fatalf("type: %v", msg.Type)
	}
	if msg.Location == nil {
		t.Fatal("location is nil")
	}
	if msg.Location.Latitude != 23.134521 {
		t.Fatalf("latitude: %f", msg.Location.Latitude)
	}
	if msg.Location.Label != "Guangzhou" {
		t.Fatalf("label: %s", msg.Location.Label)
	}
	if msg.Location.Poiname != "Canton Tower" {
		t.Fatalf("poiname: %s", msg.Location.Poiname)
	}
}

func TestCallbackHandler_GroupMessage(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":      "message",
		"msg_id":    "msg_004",
		"msg_type":  float64(1),
		"from_user": "wxid_sender",
		"group_id":  "12345678@chatroom",
		"content":   "Group hello",
	})

	if len(h.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(h.messages))
	}

	msg := h.messages[0]
	if !msg.IsGroup {
		t.Fatal("should be group message")
	}
	if msg.GroupID != "12345678@chatroom" {
		t.Fatalf("group_id: %s", msg.GroupID)
	}
}

func TestCallbackHandler_LinkMessage(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":       "message",
		"msg_id":     "msg_005",
		"msg_type":   float64(49),
		"from_user":  "wxid_sender",
		"link_title": "Test Article",
		"link_desc":  "Article description",
		"link_url":   "https://example.com/article",
		"link_thumb": "https://example.com/thumb.jpg",
	})

	if len(h.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(h.messages))
	}

	msg := h.messages[0]
	if msg.Type != wechat.MsgLink {
		t.Fatalf("type: %v", msg.Type)
	}
	if msg.LinkInfo == nil {
		t.Fatal("link info is nil")
	}
	if msg.LinkInfo.Title != "Test Article" {
		t.Fatalf("title: %s", msg.LinkInfo.Title)
	}
	if msg.LinkInfo.URL != "https://example.com/article" {
		t.Fatalf("url: %s", msg.LinkInfo.URL)
	}
}

func TestCallbackHandler_MsgTypeString(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":      "message",
		"msg_id":    "msg_006",
		"msg_type":  "image",
		"from_user": "wxid_sender",
	})

	if len(h.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(h.messages))
	}

	if h.messages[0].Type != wechat.MsgImage {
		t.Fatalf("type: %v", h.messages[0].Type)
	}
}

func TestCallbackHandler_ContactUpdate(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":       "contact_update",
		"user_id":    "wxid_friend",
		"nickname":   "New Name",
		"avatar_url": "https://example.com/avatar.jpg",
		"gender":     float64(1),
	})

	if len(h.contacts) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(h.contacts))
	}

	c := h.contacts[0]
	if c.UserID != "wxid_friend" {
		t.Fatalf("user_id: %s", c.UserID)
	}
	if c.Nickname != "New Name" {
		t.Fatalf("nickname: %s", c.Nickname)
	}
	if c.Gender != 1 {
		t.Fatalf("gender: %d", c.Gender)
	}
}

func TestCallbackHandler_GroupMemberUpdate(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":     "group_member_update",
		"group_id": "group_001@chatroom",
		"members": []interface{}{
			map[string]interface{}{
				"user_id":  "wxid_user1",
				"nickname": "User One",
				"is_admin": true,
			},
			map[string]interface{}{
				"user_id":  "wxid_user2",
				"nickname": "User Two",
				"is_owner": true,
			},
		},
	})

	if len(h.groups) != 1 {
		t.Fatalf("expected 1 group update, got %d", len(h.groups))
	}

	gu := h.groups[0]
	if gu.GroupID != "group_001@chatroom" {
		t.Fatalf("group_id: %s", gu.GroupID)
	}
	if len(gu.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(gu.Members))
	}
	if !gu.Members[0].IsAdmin {
		t.Fatal("member 0 should be admin")
	}
	if !gu.Members[1].IsOwner {
		t.Fatal("member 1 should be owner")
	}
}

func TestCallbackHandler_Revoke(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":        "revoke",
		"msg_id":      "msg_to_revoke",
		"replace_tip": "Message has been recalled",
	})

	if len(h.revokes) != 1 {
		t.Fatalf("expected 1 revoke, got %d", len(h.revokes))
	}

	if h.revokes[0].MsgID != "msg_to_revoke" {
		t.Fatalf("msg_id: %s", h.revokes[0].MsgID)
	}
	if h.revokes[0].ReplaceTip != "Message has been recalled" {
		t.Fatalf("replace_tip: %s", h.revokes[0].ReplaceTip)
	}
}

func TestCallbackHandler_Typing(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":    "typing",
		"user_id": "wxid_typer",
		"chat_id": "chat_001",
	})

	if len(h.typings) != 1 {
		t.Fatalf("expected 1 typing, got %d", len(h.typings))
	}

	if h.typings[0].UserID != "wxid_typer" {
		t.Fatalf("user_id: %s", h.typings[0].UserID)
	}
	if h.typings[0].ChatID != "chat_001" {
		t.Fatalf("chat_id: %s", h.typings[0].ChatID)
	}
}

func TestCallbackHandler_Presence(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":    "presence",
		"user_id": "wxid_online",
		"online":  true,
	})

	if len(h.presence) != 1 {
		t.Fatalf("expected 1 presence, got %d", len(h.presence))
	}

	if h.presence[0].UserID != "wxid_online" {
		t.Fatalf("user_id: %s", h.presence[0].UserID)
	}
	if !h.presence[0].Online {
		t.Fatal("should be online")
	}
}

func TestCallbackHandler_LoginStatus(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":     "login_status",
		"status":   float64(3),
		"user_id":  "wxid_logged_in",
		"nickname": "Test User",
		"avatar":   "https://example.com/avatar.jpg",
	})

	if len(h.logins) != 1 {
		t.Fatalf("expected 1 login event, got %d", len(h.logins))
	}

	evt := h.logins[0]
	if evt.State != wechat.LoginStateLoggedIn {
		t.Fatalf("state: %v", evt.State)
	}
	if evt.UserID != "wxid_logged_in" {
		t.Fatalf("user_id: %s", evt.UserID)
	}
}

func TestCallbackHandler_MethodNotAllowed(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	req := httptest.NewRequest(http.MethodGet, "/callback", nil)
	w := httptest.NewRecorder()
	ch.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestCallbackHandler_BadJSON(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	ch.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCallbackHandler_UnknownType(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	w := postCallback(ch, map[string]interface{}{
		"type": "unknown_event",
	})

	// Should still return 200
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// No messages should be processed
	if len(h.messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(h.messages))
	}
}

func TestCallbackHandler_ExtraFields(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":      "message",
		"msg_id":    "msg_extra",
		"msg_type":  float64(1),
		"from_user": "wxid_sender",
		"content":   "test",
		"extra": map[string]interface{}{
			"custom_key": "custom_value",
			"number":     float64(42),
		},
	})

	if len(h.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(h.messages))
	}

	msg := h.messages[0]
	if msg.Extra["custom_key"] != "custom_value" {
		t.Fatalf("extra custom_key: %s", msg.Extra["custom_key"])
	}
	if msg.Extra["number"] != "42" {
		t.Fatalf("extra number: %s", msg.Extra["number"])
	}
}

func TestCallbackHandler_FriendRequest(t *testing.T) {
	h := &testHandler{}
	ch := NewCallbackHandler(testCallbackLog, h)

	postCallback(ch, map[string]interface{}{
		"type":       "friend_request",
		"from_user":  "wxid_stranger",
		"nickname":   "Stranger",
		"avatar_url": "https://example.com/stranger.jpg",
		"content":    "Hi, I want to be friends",
	})

	if len(h.contacts) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(h.contacts))
	}

	c := h.contacts[0]
	if c.UserID != "wxid_stranger" {
		t.Fatalf("user_id: %s", c.UserID)
	}
	if c.Nickname != "Stranger" {
		t.Fatalf("nickname: %s", c.Nickname)
	}
}

func TestParseMsgTypeString(t *testing.T) {
	tests := []struct {
		input    string
		expected wechat.MsgType
	}{
		{"text", wechat.MsgText},
		{"image", wechat.MsgImage},
		{"voice", wechat.MsgVoice},
		{"video", wechat.MsgVideo},
		{"emoji", wechat.MsgEmoji},
		{"location", wechat.MsgLocation},
		{"link", wechat.MsgLink},
		{"file", wechat.MsgFile},
		{"revoke", wechat.MsgRevoke},
		{"system", wechat.MsgSystem},
		{"miniapp", wechat.MsgMiniApp},
		{"TEXT", wechat.MsgText},   // case insensitive
		{"Image", wechat.MsgImage}, // case insensitive
		{"49", wechat.MsgLink},     // numeric string
		{"1", wechat.MsgText},      // numeric string
	}

	for _, tt := range tests {
		got := parseMsgTypeString(tt.input)
		if got != tt.expected {
			t.Errorf("parseMsgTypeString(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestMapKeys(t *testing.T) {
	m := map[string]interface{}{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	keys := mapKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	// Check all keys present (order may vary)
	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}
	for _, expected := range []string{"a", "b", "c"} {
		if !keySet[expected] {
			t.Fatalf("missing key: %s", expected)
		}
	}
}
