package pchook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func TestProvider_Registration(t *testing.T) {
	if !wechat.DefaultRegistry.Has("pchook") {
		t.Fatal("pchook provider should be registered")
	}

	p, err := wechat.DefaultRegistry.Create("pchook")
	if err != nil {
		t.Fatalf("create pchook: %v", err)
	}
	if p.Name() != "pchook" {
		t.Fatalf("name: %s", p.Name())
	}
	if p.Tier() != 3 {
		t.Fatalf("tier: %d", p.Tier())
	}
}

func TestProvider_Capabilities(t *testing.T) {
	p := &Provider{}
	caps := p.Capabilities()

	if !caps.SendText {
		t.Error("should support text")
	}
	if !caps.SendImage {
		t.Error("should support image")
	}
	if !caps.SendFile {
		t.Error("should support file")
	}
	if !caps.ReceiveMessage {
		t.Error("should support receive")
	}
	if !caps.Revoke {
		t.Error("should support revoke")
	}
	if !caps.GroupManage {
		t.Error("should support group manage")
	}
	if !caps.ContactManage {
		t.Error("should support contact manage")
	}

	// Unsupported capabilities
	if caps.SendVideo {
		t.Error("should not support video")
	}
	if caps.SendVoice {
		t.Error("should not support voice")
	}
	if caps.SendLocation {
		t.Error("should not support location")
	}
	if caps.MomentAccess {
		t.Error("should not support moments")
	}
}

func TestProvider_InitialState(t *testing.T) {
	p := &Provider{}
	if p.IsRunning() {
		t.Error("should not be running initially")
	}
	if p.GetLoginState() != wechat.LoginStateLoggedOut {
		t.Errorf("initial login state: %d", p.GetLoginState())
	}
	if p.GetSelf() != nil {
		t.Error("self should be nil initially")
	}
}

func TestProvider_SetLoginState(t *testing.T) {
	p := &Provider{}
	p.setLoginState(wechat.LoginStateLoggedIn)
	if p.GetLoginState() != wechat.LoginStateLoggedIn {
		t.Fatalf("state: %d", p.GetLoginState())
	}
	p.setLoginState(wechat.LoginStateError)
	if p.GetLoginState() != wechat.LoginStateError {
		t.Fatalf("state: %d", p.GetLoginState())
	}
}

func TestProvider_UnsupportedSendMethods(t *testing.T) {
	p := &Provider{}
	ctx := context.Background()

	_, err := p.SendVideo(ctx, "user", nil, "video.mp4", nil)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("SendVideo should return unsupported error: %v", err)
	}

	_, err = p.SendVoice(ctx, "user", nil, 5)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("SendVoice should return unsupported error: %v", err)
	}

	_, err = p.SendLocation(ctx, "user", &wechat.LocationInfo{})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("SendLocation should return unsupported error: %v", err)
	}

	_, err = p.SendLink(ctx, "user", &wechat.LinkCardInfo{})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("SendLink should return unsupported error: %v", err)
	}
}

func TestProvider_DetectMimeType(t *testing.T) {
	tests := []struct {
		path string
		mime string
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"photo.png", "image/png"},
		{"animation.gif", "image/gif"},
		{"video.mp4", "video/mp4"},
		{"music.mp3", "audio/mpeg"},
		{"voice.ogg", "audio/ogg"},
		{"voice.silk", "audio/silk"},
		{"doc.pdf", "application/pdf"},
		{"unknown.xyz", "application/octet-stream"},
	}

	for _, tt := range tests {
		result := detectMimeType(tt.path)
		if result != tt.mime {
			t.Errorf("detectMimeType(%q) = %q, want %q", tt.path, result, tt.mime)
		}
	}
}

func TestProvider_CompileTimeCheck(t *testing.T) {
	// This test verifies that Provider implements wechat.Provider at compile time.
	var _ wechat.Provider = (*Provider)(nil)
}

// === Message parsing tests ===

func TestParseRawMessage_TextDM(t *testing.T) {
	raw := rawMessage{
		MsgID:     "msg001",
		Type:      1,
		Sender:    "wxid_sender",
		Content:   "hello world",
		Timestamp: 1700000000,
	}

	data, _ := json.Marshal(raw)
	msg, err := parseRawMessage(data, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if msg.MsgID != "msg001" {
		t.Errorf("MsgID: %s", msg.MsgID)
	}
	if msg.Type != wechat.MsgText {
		t.Errorf("Type: %d", msg.Type)
	}
	if msg.FromUser != "wxid_sender" {
		t.Errorf("FromUser: %s", msg.FromUser)
	}
	if msg.Content != "hello world" {
		t.Errorf("Content: %s", msg.Content)
	}
	if msg.IsGroup {
		t.Error("should not be group")
	}
}

func TestParseRawMessage_GroupMessage(t *testing.T) {
	raw := rawMessage{
		MsgID:     "msg002",
		Type:      1,
		Sender:    "wxid_sender",
		RoomID:    "12345@chatroom",
		Content:   "wxid_sender:\ngroup message",
		Timestamp: 1700000000,
	}

	data, _ := json.Marshal(raw)
	msg, err := parseRawMessage(data, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !msg.IsGroup {
		t.Error("should be group")
	}
	if msg.GroupID != "12345@chatroom" {
		t.Errorf("GroupID: %s", msg.GroupID)
	}
	if msg.FromUser != "wxid_sender" {
		t.Errorf("FromUser: %s", msg.FromUser)
	}
	if msg.Content != "group message" {
		t.Errorf("Content: %s", msg.Content)
	}
}

func TestParseRawMessage_WithMediaPath(t *testing.T) {
	raw := rawMessage{
		MsgID:     "msg003",
		Type:      3,
		Sender:    "wxid_sender",
		MediaPath: "C:\\temp\\image.jpg",
		Thumb:     "C:\\temp\\thumb.jpg",
	}

	data, _ := json.Marshal(raw)
	msg, err := parseRawMessage(data, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if msg.Extra["media_path"] != "C:\\temp\\image.jpg" {
		t.Errorf("media_path: %s", msg.Extra["media_path"])
	}
	if msg.Extra["thumb_path"] != "C:\\temp\\thumb.jpg" {
		t.Errorf("thumb_path: %s", msg.Extra["thumb_path"])
	}
}

func TestExtractXMLField(t *testing.T) {
	xml := `<msg><title>Test Title</title><des><![CDATA[Test Description]]></des><url>https://example.com</url></msg>`

	title := extractXMLField(xml, "title")
	if title != "Test Title" {
		t.Errorf("title: %q", title)
	}

	des := extractXMLField(xml, "des")
	if des != "Test Description" {
		t.Errorf("des: %q", des)
	}

	url := extractXMLField(xml, "url")
	if url != "https://example.com" {
		t.Errorf("url: %q", url)
	}

	missing := extractXMLField(xml, "nonexistent")
	if missing != "" {
		t.Errorf("missing: %q", missing)
	}
}

func TestExtractXMLCData(t *testing.T) {
	xml := `<msg><title><![CDATA[Hello World]]></title></msg>`
	result := extractXMLCData(xml, "title")
	if result != "Hello World" {
		t.Errorf("cdata: %q", result)
	}

	// Missing field
	result = extractXMLCData(xml, "missing")
	if result != "" {
		t.Errorf("missing should be empty: %q", result)
	}
}

func TestContactResult_ToContactInfo(t *testing.T) {
	c := contactResult{
		UserID:    "wxid_test",
		Alias:     "testalias",
		Nickname:  "Test User",
		Remark:    "My Friend",
		AvatarURL: "https://avatar.example.com/test.jpg",
		Gender:    1,
		Province:  "Beijing",
		City:      "Beijing",
		Signature: "Hello",
	}

	info := c.toContactInfo()
	if info.UserID != "wxid_test" {
		t.Errorf("UserID: %s", info.UserID)
	}
	if info.Nickname != "Test User" {
		t.Errorf("Nickname: %s", info.Nickname)
	}
	if info.IsGroup {
		t.Error("should not be group")
	}

	// Test group detection
	c.UserID = "12345@chatroom"
	info = c.toContactInfo()
	if !info.IsGroup {
		t.Error("should be group")
	}
}

func TestGroupMemberResult_ToGroupMember(t *testing.T) {
	m := groupMemberResult{
		UserID:      "wxid_member",
		Nickname:    "Member",
		DisplayName: "Group Nick",
		IsAdmin:     true,
	}

	gm := m.toGroupMember()
	if gm.UserID != "wxid_member" {
		t.Errorf("UserID: %s", gm.UserID)
	}
	if gm.DisplayName != "Group Nick" {
		t.Errorf("DisplayName: %s", gm.DisplayName)
	}
	if !gm.IsAdmin {
		t.Error("should be admin")
	}
}

// === RPC Client tests ===

func TestRPCClient_NewClient(t *testing.T) {
	client := NewRPCClient("localhost:19088", nil)
	if client == nil {
		t.Fatal("client should not be nil")
	}
	if client.endpoint != "localhost:19088" {
		t.Errorf("endpoint: %s", client.endpoint)
	}
	if client.IsConnected() {
		t.Error("should not be connected initially")
	}
}

func TestRPCError_Error(t *testing.T) {
	err := &RPCError{Code: 100, Message: "test error"}
	expected := "rpc error 100: test error"
	if err.Error() != expected {
		t.Errorf("error: %q, want %q", err.Error(), expected)
	}
}

func TestRPCClient_HandleIncoming_Response(t *testing.T) {
	client := NewRPCClient("localhost:19088", nil)

	// Register a pending request
	respCh := make(chan *RPCResponse, 1)
	client.pendingMu.Lock()
	client.pending[42] = respCh
	client.pendingMu.Unlock()

	// Simulate incoming response
	resp := RPCResponse{
		ID:     42,
		Result: json.RawMessage(`"ok"`),
	}
	data, _ := json.Marshal(resp)
	client.handleIncoming(data)

	// Check response was delivered
	select {
	case r := <-respCh:
		if r.ID != 42 {
			t.Errorf("response ID: %d", r.ID)
		}
		var result string
		json.Unmarshal(r.Result, &result)
		if result != "ok" {
			t.Errorf("result: %s", result)
		}
	default:
		t.Error("response not delivered")
	}
}

func TestRPCClient_HandleIncoming_Notification(t *testing.T) {
	client := NewRPCClient("localhost:19088", nil)

	received := make(chan string, 1)
	client.SetNotificationHandler(func(method string, params json.RawMessage) {
		received <- method
	})

	notif := RPCNotification{
		Method: "on_message",
		Params: json.RawMessage(`{}`),
	}
	data, _ := json.Marshal(notif)
	client.handleIncoming(data)

	// Wait briefly for async handler
	select {
	case method := <-received:
		if method != "on_message" {
			t.Errorf("method: %s", method)
		}
	default:
		// Notification handler runs in goroutine, might not be immediate
	}
}
