package message

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/n42/mautrix-wechat/internal/bridge"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

var testLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

// --- Mock MatrixClient for testing ---

type mockMatrixClient struct {
	uploaded []mockUpload
}

type mockUpload struct {
	data     []byte
	mimeType string
	fileName string
}

func (m *mockMatrixClient) EnsureRegistered(_ context.Context, _ string) error { return nil }
func (m *mockMatrixClient) SetDisplayName(_ context.Context, _, _ string) error { return nil }
func (m *mockMatrixClient) SetAvatarURL(_ context.Context, _, _ string) error   { return nil }
func (m *mockMatrixClient) UploadMedia(_ context.Context, data []byte, mimeType, fileName string) (string, error) {
	m.uploaded = append(m.uploaded, mockUpload{data, mimeType, fileName})
	return "mxc://test/uploaded", nil
}
func (m *mockMatrixClient) SendMessage(_ context.Context, _, _ string, _ interface{}) (string, error) {
	return "$event:test", nil
}
func (m *mockMatrixClient) SendMessageWithTimestamp(_ context.Context, _, _ string, _ interface{}, _ int64) (string, error) {
	return "$event:test", nil
}
func (m *mockMatrixClient) CreateRoom(_ context.Context, _ *bridge.CreateRoomRequest) (string, error) {
	return "!room:test", nil
}
func (m *mockMatrixClient) JoinRoom(_ context.Context, _, _ string) error             { return nil }
func (m *mockMatrixClient) LeaveRoom(_ context.Context, _, _ string) error            { return nil }
func (m *mockMatrixClient) InviteToRoom(_ context.Context, _, _ string) error         { return nil }
func (m *mockMatrixClient) KickFromRoom(_ context.Context, _, _, _ string) error      { return nil }
func (m *mockMatrixClient) RedactEvent(_ context.Context, _, _, _ string) error       { return nil }
func (m *mockMatrixClient) SendStateEvent(_ context.Context, _, _, _ string, _ interface{}) error {
	return nil
}
func (m *mockMatrixClient) SetRoomName(_ context.Context, _, _ string) error   { return nil }
func (m *mockMatrixClient) SetRoomAvatar(_ context.Context, _, _ string) error { return nil }
func (m *mockMatrixClient) SetRoomTopic(_ context.Context, _, _ string) error  { return nil }
func (m *mockMatrixClient) SetTyping(_ context.Context, _, _ string, _ bool, _ int) error {
	return nil
}
func (m *mockMatrixClient) SetPresence(_ context.Context, _ string, _ bool) error        { return nil }
func (m *mockMatrixClient) SendReadReceipt(_ context.Context, _, _, _ string) error      { return nil }
func (m *mockMatrixClient) CreateSpace(_ context.Context, _ *bridge.CreateSpaceRequest) (string, error) {
	return "!space:test", nil
}
func (m *mockMatrixClient) AddRoomToSpace(_ context.Context, _, _ string) error { return nil }

// --- Mock MentionResolver ---

type mockMentionResolver struct {
	wechatToMatrix map[string][2]string // nickname -> (matrixID, displayName)
	matrixToWeChat map[string][2]string // matrixID -> (wechatID, nickname)
}

func (r *mockMentionResolver) ResolveWeChatMention(nickname string) (string, string) {
	if pair, ok := r.wechatToMatrix[nickname]; ok {
		return pair[0], pair[1]
	}
	return "", ""
}

func (r *mockMentionResolver) ResolveMatrixMention(matrixID string) (string, string) {
	if pair, ok := r.matrixToWeChat[matrixID]; ok {
		return pair[0], pair[1]
	}
	return "", ""
}

// --- Tests ---

func TestProcessor_TextMessage(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID:   "msg001",
		Type:    wechat.MsgText,
		Content: "Hello, world!",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content == nil {
		t.Fatal("content should not be nil")
	}
	if content.EventType != "m.room.message" {
		t.Fatalf("event type: %s", content.EventType)
	}
	if content.Content["msgtype"] != "m.text" {
		t.Fatalf("msgtype: %v", content.Content["msgtype"])
	}
	if content.Content["body"] != "Hello, world!" {
		t.Fatalf("body: %v", content.Content["body"])
	}
}

func TestProcessor_TextMessageWithMentions(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})
	p.SetMentionResolver(&mockMentionResolver{
		wechatToMatrix: map[string][2]string{
			"Alice": {"@wechat_alice:example.com", "Alice (WeChat)"},
		},
	})

	msg := &wechat.Message{
		MsgID:   "msg002",
		Type:    wechat.MsgText,
		Content: "Hey @Alice check this out",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if content.Content["format"] != "org.matrix.custom.html" {
		t.Fatalf("should have HTML format: %v", content.Content["format"])
	}

	formattedBody, _ := content.Content["formatted_body"].(string)
	if formattedBody == "" {
		t.Fatal("formatted_body should not be empty")
	}
	if !containsStr(formattedBody, "matrix.to") {
		t.Fatalf("formatted_body should contain Matrix pill: %s", formattedBody)
	}
}

func TestProcessor_TextMessageWithMentions_NoResolver(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})
	// No mention resolver set

	msg := &wechat.Message{
		MsgID:   "msg003",
		Type:    wechat.MsgText,
		Content: "Hey @Alice check this out",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	// Without resolver, should not have HTML format
	if _, ok := content.Content["format"]; ok {
		t.Fatal("should not have format without mention resolver")
	}
}

func TestProcessor_ImageMessage(t *testing.T) {
	client := &mockMatrixClient{}
	p := NewProcessor(testLog, client)

	msg := &wechat.Message{
		MsgID:     "msg004",
		Type:      wechat.MsgImage,
		MediaData: []byte("fake image data"),
		FileName:  "photo.jpg",
		FileSize:  1024,
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.image" {
		t.Fatalf("msgtype: %v", content.Content["msgtype"])
	}
	if content.Content["url"] != "mxc://test/uploaded" {
		t.Fatalf("url: %v", content.Content["url"])
	}
	if len(client.uploaded) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(client.uploaded))
	}
}

func TestProcessor_VoiceMessage(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID:     "msg005",
		Type:      wechat.MsgVoice,
		MediaData: []byte("fake voice data"),
		Duration:  5,
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.audio" {
		t.Fatalf("msgtype: %v", content.Content["msgtype"])
	}

	// Should have MSC3245 voice flag
	if _, ok := content.Content["org.matrix.msc3245.voice"]; !ok {
		t.Fatal("should have MSC3245 voice flag")
	}

	info, _ := content.Content["info"].(map[string]interface{})
	if info["duration"] != 5000 {
		t.Fatalf("duration should be 5000ms: %v", info["duration"])
	}
}

func TestProcessor_LocationMessage(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID: "msg006",
		Type:  wechat.MsgLocation,
		Location: &wechat.LocationInfo{
			Latitude:  39.9042,
			Longitude: 116.4074,
			Label:     "Beijing, China",
			Poiname:   "Tiananmen Square",
		},
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.location" {
		t.Fatalf("msgtype: %v", content.Content["msgtype"])
	}
	geoURI, _ := content.Content["geo_uri"].(string)
	if !containsStr(geoURI, "geo:") {
		t.Fatalf("geo_uri: %v", geoURI)
	}
}

func TestProcessor_LinkMessage(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID: "msg007",
		Type:  wechat.MsgLink,
		LinkInfo: &wechat.LinkCardInfo{
			Title:       "Test Article",
			Description: "Article description",
			URL:         "https://example.com/article",
		},
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["format"] != "org.matrix.custom.html" {
		t.Fatalf("should have HTML format")
	}
	body, _ := content.Content["body"].(string)
	if !containsStr(body, "Test Article") {
		t.Fatalf("body should contain title: %s", body)
	}
}

func TestProcessor_SystemMessage(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID:   "msg008",
		Type:    wechat.MsgSystem,
		Content: "Bob joined the group",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.notice" {
		t.Fatalf("system messages should be m.notice: %v", content.Content["msgtype"])
	}
}

func TestProcessor_PatMessage(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID:   "msg009",
		Type:    wechat.MsgSystem,
		Content: "A拍了拍B",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.emote" {
		t.Fatalf("pat messages should be m.emote: %v", content.Content["msgtype"])
	}
}

func TestProcessor_RevokeMessage(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID: "msg010",
		Type:  wechat.MsgRevoke,
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content != nil {
		t.Fatal("revoke messages should return nil (handled separately)")
	}
}

func TestProcessor_ContactCard(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID:   "msg011",
		Type:    wechat.MsgContact,
		Content: "张三 (wxid_zhangsan)",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	body, _ := content.Content["body"].(string)
	if !containsStr(body, "[Contact Card]") {
		t.Fatalf("should prefix with [Contact Card]: %s", body)
	}
}

func TestProcessor_MiniApp(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID: "msg012",
		Type:  wechat.MsgMiniApp,
		LinkInfo: &wechat.LinkCardInfo{
			Title: "Test Mini App",
			URL:   "https://example.com/miniapp",
		},
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	body, _ := content.Content["body"].(string)
	if !containsStr(body, "[Mini App]") {
		t.Fatalf("should prefix with [Mini App]: %s", body)
	}
}

func TestProcessor_UnsupportedType(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID: "msg013",
		Type:  wechat.MsgType(99999),
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content != nil {
		t.Fatal("unsupported type should return nil")
	}
}

// --- Matrix → WeChat tests ---

func TestProcessor_MatrixTextToWeChat(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	evt := &bridge.MatrixEvent{
		ID:     "$evt001",
		Type:   "m.room.message",
		RoomID: "!room:test",
		Sender: "@user:test",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Hello from Matrix",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action.Type != wechat.MsgText {
		t.Fatalf("type: %v", action.Type)
	}
	if action.Text != "Hello from Matrix" {
		t.Fatalf("text: %s", action.Text)
	}
}

func TestProcessor_MatrixTextToWeChat_WithMentions(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})
	p.SetMentionResolver(&mockMentionResolver{
		matrixToWeChat: map[string][2]string{
			"@wechat_alice:example.com": {"wxid_alice", "Alice"},
		},
	})

	evt := &bridge.MatrixEvent{
		ID:     "$evt002",
		Type:   "m.room.message",
		RoomID: "!room:test",
		Sender: "@user:test",
		Content: map[string]interface{}{
			"msgtype":        "m.text",
			"body":           "Hello Alice",
			"format":         "org.matrix.custom.html",
			"formatted_body": `Hello <a href="https://matrix.to/#/@wechat_alice:example.com">Alice</a>`,
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if len(action.Mentions) != 1 || action.Mentions[0] != "wxid_alice" {
		t.Fatalf("mentions: %v", action.Mentions)
	}
	if !containsStr(action.Text, "@Alice") {
		t.Fatalf("text should contain @Alice: %s", action.Text)
	}
}

func TestProcessor_MatrixTextToWeChat_WithReplyTo(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	evt := &bridge.MatrixEvent{
		ID:     "$evt003",
		Type:   "m.room.message",
		RoomID: "!room:test",
		Sender: "@user:test",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "Reply to this",
			"m.relates_to": map[string]interface{}{
				"m.in_reply_to": map[string]interface{}{
					"event_id": "$original_event",
				},
			},
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action.ReplyTo != "$original_event" {
		t.Fatalf("reply_to: %s", action.ReplyTo)
	}
}

func TestProcessor_MatrixImageToWeChat(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	evt := &bridge.MatrixEvent{
		ID:     "$evt004",
		Type:   "m.room.message",
		RoomID: "!room:test",
		Sender: "@user:test",
		Content: map[string]interface{}{
			"msgtype": "m.image",
			"body":    "photo.jpg",
			"url":     "mxc://test/image123",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action.Type != wechat.MsgImage {
		t.Fatalf("type: %v", action.Type)
	}
}

func TestProcessor_MatrixLocationToWeChat(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	evt := &bridge.MatrixEvent{
		ID:     "$evt005",
		Type:   "m.room.message",
		RoomID: "!room:test",
		Sender: "@user:test",
		Content: map[string]interface{}{
			"msgtype": "m.location",
			"body":    "Some place",
			"geo_uri": "geo:39.9042,116.4074",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action.Type != wechat.MsgText {
		t.Fatalf("type: %v", action.Type)
	}
	if !containsStr(action.Text, "[Location]") {
		t.Fatalf("text should contain [Location]: %s", action.Text)
	}
}

func TestProcessor_MatrixEmoteToWeChat(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	evt := &bridge.MatrixEvent{
		ID:     "$evt006",
		Type:   "m.room.message",
		RoomID: "!room:test",
		Sender: "@user:test",
		Content: map[string]interface{}{
			"msgtype": "m.emote",
			"body":    "waves hello",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action.Text != "* waves hello" {
		t.Fatalf("text: %s", action.Text)
	}
}

func TestProcessor_MatrixUnsupportedType(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	evt := &bridge.MatrixEvent{
		ID:     "$evt007",
		Type:   "m.room.message",
		RoomID: "!room:test",
		Sender: "@user:test",
		Content: map[string]interface{}{
			"msgtype": "m.sticker",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action != nil {
		t.Fatal("unsupported msgtype should return nil")
	}
}

func TestProcessor_FileMessage(t *testing.T) {
	p := NewProcessor(testLog, &mockMatrixClient{})

	msg := &wechat.Message{
		MsgID:     "msg014",
		Type:      wechat.MsgFile,
		MediaData: []byte("file content"),
		FileName:  "document.pdf",
		FileSize:  2048,
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.file" {
		t.Fatalf("msgtype: %v", content.Content["msgtype"])
	}
	if content.Content["body"] != "document.pdf" {
		t.Fatalf("body: %v", content.Content["body"])
	}
}

func TestProcessor_VideoMessage(t *testing.T) {
	client := &mockMatrixClient{}
	p := NewProcessor(testLog, client)

	msg := &wechat.Message{
		MsgID:     "msg015",
		Type:      wechat.MsgVideo,
		MediaData: []byte("video data"),
		FileName:  "video.mp4",
		Duration:  30,
		Thumbnail: []byte("thumb data"),
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.video" {
		t.Fatalf("msgtype: %v", content.Content["msgtype"])
	}

	info, _ := content.Content["info"].(map[string]interface{})
	if info["duration"] != 30000 {
		t.Fatalf("duration should be 30000ms: %v", info["duration"])
	}

	// Should have uploaded both video and thumbnail
	if len(client.uploaded) != 2 {
		t.Fatalf("expected 2 uploads (video + thumb), got %d", len(client.uploaded))
	}
}
