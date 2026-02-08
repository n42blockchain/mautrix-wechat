package bridge

import (
	"context"
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func TestDefaultProcessor_TextToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		MsgID:   "msg001",
		Type:    wechat.MsgText,
		Content: "hello world",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content == nil {
		t.Fatal("content should not be nil")
	}
	if content.EventType != "m.room.message" {
		t.Errorf("event type: %s", content.EventType)
	}
	if content.Content["msgtype"] != "m.text" {
		t.Errorf("msgtype: %v", content.Content["msgtype"])
	}
	if content.Content["body"] != "hello world" {
		t.Errorf("body: %v", content.Content["body"])
	}
}

func TestDefaultProcessor_ImageToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		Type:     wechat.MsgImage,
		MediaURL: "http://example.com/image.jpg",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.image" {
		t.Errorf("msgtype: %v", content.Content["msgtype"])
	}
	if content.Content["url"] != "http://example.com/image.jpg" {
		t.Errorf("url: %v", content.Content["url"])
	}
}

func TestDefaultProcessor_LocationToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		Type: wechat.MsgLocation,
		Location: &wechat.LocationInfo{
			Latitude:  39.9,
			Longitude: 116.3,
			Label:     "Beijing",
		},
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.location" {
		t.Errorf("msgtype: %v", content.Content["msgtype"])
	}
	if content.Content["body"] != "Beijing" {
		t.Errorf("body: %v", content.Content["body"])
	}
	geoURI, _ := content.Content["geo_uri"].(string)
	if geoURI == "" {
		t.Error("geo_uri should not be empty")
	}
}

func TestDefaultProcessor_LinkToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		Type: wechat.MsgLink,
		LinkInfo: &wechat.LinkCardInfo{
			Title:       "Test Article",
			Description: "Description",
			URL:         "https://example.com/article",
		},
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	body, _ := content.Content["body"].(string)
	if body == "" {
		t.Error("body should not be empty")
	}
}

func TestDefaultProcessor_SystemToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		Type:    wechat.MsgSystem,
		Content: "User joined the group",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.notice" {
		t.Errorf("msgtype: %v", content.Content["msgtype"])
	}
}

func TestDefaultProcessor_RevokeReturnsNil(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{Type: wechat.MsgRevoke}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content != nil {
		t.Error("revoke should return nil content")
	}
}

func TestDefaultProcessor_UnknownType(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{Type: wechat.MsgType(9999)}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.notice" {
		t.Errorf("unknown type should be notice: %v", content.Content["msgtype"])
	}
}

func TestDefaultProcessor_MatrixTextToWeChat(t *testing.T) {
	p := &defaultMessageProcessor{}
	evt := &MatrixEvent{
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "hello from matrix",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action == nil {
		t.Fatal("action should not be nil")
	}
	if action.Type != wechat.MsgText {
		t.Errorf("type: %d", action.Type)
	}
	if action.Text != "hello from matrix" {
		t.Errorf("text: %s", action.Text)
	}
}

func TestDefaultProcessor_MatrixTextWithReply(t *testing.T) {
	p := &defaultMessageProcessor{}
	evt := &MatrixEvent{
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "reply message",
			"m.relates_to": map[string]interface{}{
				"m.in_reply_to": map[string]interface{}{
					"event_id": "$event123",
				},
			},
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action.ReplyTo != "$event123" {
		t.Errorf("reply_to: %s", action.ReplyTo)
	}
}

func TestDefaultProcessor_MatrixEmoteToWeChat(t *testing.T) {
	p := &defaultMessageProcessor{}
	evt := &MatrixEvent{
		Content: map[string]interface{}{
			"msgtype": "m.emote",
			"body":    "waves",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action.Text != "* waves" {
		t.Errorf("text: %s", action.Text)
	}
}

func TestDefaultProcessor_MatrixImageToWeChat(t *testing.T) {
	p := &defaultMessageProcessor{}
	evt := &MatrixEvent{
		Content: map[string]interface{}{
			"msgtype": "m.image",
			"body":    "image.png",
			"url":     "mxc://example.com/abc123",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action.Type != wechat.MsgImage {
		t.Errorf("type: %d", action.Type)
	}
	if action.File != "mxc://example.com/abc123" {
		t.Errorf("file: %s", action.File)
	}
}

func TestDefaultProcessor_MatrixUnknownReturnsNil(t *testing.T) {
	p := &defaultMessageProcessor{}
	evt := &MatrixEvent{
		Content: map[string]interface{}{
			"msgtype": "m.custom.unknown",
			"body":    "test",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action != nil {
		t.Error("unknown type should return nil action")
	}
}

func TestDefaultProcessor_MatrixEmptyBodyReturnsNil(t *testing.T) {
	p := &defaultMessageProcessor{}
	evt := &MatrixEvent{
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    "",
		},
	}

	action, err := p.MatrixToWeChat(context.Background(), evt)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if action != nil {
		t.Error("empty body should return nil action")
	}
}

func TestDefaultProcessor_VideoToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		Type:     wechat.MsgVideo,
		MediaURL: "http://example.com/video.mp4",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.video" {
		t.Errorf("msgtype: %v", content.Content["msgtype"])
	}
}

func TestDefaultProcessor_VoiceToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		Type:     wechat.MsgVoice,
		MediaURL: "http://example.com/voice.ogg",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.audio" {
		t.Errorf("msgtype: %v", content.Content["msgtype"])
	}
}

func TestDefaultProcessor_FileToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		Type:     wechat.MsgFile,
		FileName: "document.pdf",
		FileSize: 1024,
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.file" {
		t.Errorf("msgtype: %v", content.Content["msgtype"])
	}
	if content.Content["body"] != "document.pdf" {
		t.Errorf("body: %v", content.Content["body"])
	}
}

func TestDefaultProcessor_EmojiToMatrix(t *testing.T) {
	p := &defaultMessageProcessor{}
	msg := &wechat.Message{
		Type:     wechat.MsgEmoji,
		MediaURL: "http://example.com/emoji.gif",
	}

	content, err := p.WeChatToMatrix(context.Background(), msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if content.Content["msgtype"] != "m.image" {
		t.Errorf("msgtype: %v", content.Content["msgtype"])
	}
}

func TestExtractMXCURL(t *testing.T) {
	content := map[string]interface{}{
		"url": "mxc://example.com/abc",
	}
	if extractMXCURL(content) != "mxc://example.com/abc" {
		t.Error("should extract mxc url")
	}

	empty := map[string]interface{}{}
	if extractMXCURL(empty) != "" {
		t.Error("should return empty for missing url")
	}
}
