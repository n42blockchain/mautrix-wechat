package bridge

import (
	"context"
	"fmt"
	"strings"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// defaultMessageProcessor provides a basic bidirectional message conversion
// between WeChat and Matrix formats. It handles text, image, file, redaction,
// and other common message types.
type defaultMessageProcessor struct{}

var _ MessageProcessor = (*defaultMessageProcessor)(nil)

// WeChatToMatrix converts a WeChat message to Matrix event content.
func (p *defaultMessageProcessor) WeChatToMatrix(_ context.Context, msg *wechat.Message) (*MatrixEventContent, error) {
	switch msg.Type {
	case wechat.MsgText:
		return p.textToMatrix(msg), nil
	case wechat.MsgImage:
		return p.imageToMatrix(msg), nil
	case wechat.MsgVideo:
		return p.videoToMatrix(msg), nil
	case wechat.MsgVoice:
		return p.voiceToMatrix(msg), nil
	case wechat.MsgFile:
		return p.fileToMatrix(msg), nil
	case wechat.MsgLocation:
		return p.locationToMatrix(msg), nil
	case wechat.MsgLink:
		return p.linkToMatrix(msg), nil
	case wechat.MsgEmoji:
		return p.emojiToMatrix(msg), nil
	case wechat.MsgRevoke:
		// Revoke is handled via OnRevoke callback, not via OnMessage
		return nil, nil
	case wechat.MsgSystem:
		return p.systemToMatrix(msg), nil
	default:
		// Unknown type — pass through as notice
		return &MatrixEventContent{
			EventType: "m.room.message",
			Content: map[string]interface{}{
				"msgtype": "m.notice",
				"body":    fmt.Sprintf("[unsupported message type %d]", msg.Type),
			},
		}, nil
	}
}

// MatrixToWeChat converts a Matrix event to a WeChat send action.
func (p *defaultMessageProcessor) MatrixToWeChat(_ context.Context, evt *MatrixEvent) (*WeChatSendAction, error) {
	msgtype, _ := evt.Content["msgtype"].(string)

	switch msgtype {
	case "m.text", "m.notice":
		body, _ := evt.Content["body"].(string)
		if body == "" {
			return nil, nil
		}

		action := &WeChatSendAction{
			Type: wechat.MsgText,
			Text: body,
		}

		// Check for reply
		if relatesTo, ok := evt.Content["m.relates_to"].(map[string]interface{}); ok {
			if inReplyTo, ok := relatesTo["m.in_reply_to"].(map[string]interface{}); ok {
				if eventID, ok := inReplyTo["event_id"].(string); ok {
					action.ReplyTo = eventID
				}
			}
		}

		// Extract @mentions
		if mentions, ok := evt.Content["m.mentions"].(map[string]interface{}); ok {
			if userIDs, ok := mentions["user_ids"].([]interface{}); ok {
				for _, id := range userIDs {
					if s, ok := id.(string); ok {
						action.Mentions = append(action.Mentions, s)
					}
				}
			}
		}

		return action, nil

	case "m.image":
		return &WeChatSendAction{
			Type: wechat.MsgImage,
			File: extractMXCURL(evt.Content),
		}, nil

	case "m.file":
		return &WeChatSendAction{
			Type: wechat.MsgFile,
			File: extractMXCURL(evt.Content),
		}, nil

	case "m.emote":
		body, _ := evt.Content["body"].(string)
		return &WeChatSendAction{
			Type: wechat.MsgText,
			Text: "* " + body,
		}, nil

	default:
		return nil, nil
	}
}

// --- Type-specific converters ---

func (p *defaultMessageProcessor) textToMatrix(msg *wechat.Message) *MatrixEventContent {
	return &MatrixEventContent{
		EventType: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    msg.Content,
		},
	}
}

func (p *defaultMessageProcessor) imageToMatrix(msg *wechat.Message) *MatrixEventContent {
	content := map[string]interface{}{
		"msgtype": "m.image",
		"body":    "image",
	}
	if msg.MediaURL != "" {
		content["url"] = msg.MediaURL
	}
	return &MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}
}

func (p *defaultMessageProcessor) videoToMatrix(msg *wechat.Message) *MatrixEventContent {
	content := map[string]interface{}{
		"msgtype": "m.video",
		"body":    "video",
	}
	if msg.MediaURL != "" {
		content["url"] = msg.MediaURL
	}
	return &MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}
}

func (p *defaultMessageProcessor) voiceToMatrix(msg *wechat.Message) *MatrixEventContent {
	content := map[string]interface{}{
		"msgtype": "m.audio",
		"body":    "voice message",
	}
	if msg.MediaURL != "" {
		content["url"] = msg.MediaURL
	}
	return &MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}
}

func (p *defaultMessageProcessor) fileToMatrix(msg *wechat.Message) *MatrixEventContent {
	body := msg.FileName
	if body == "" {
		body = "file"
	}
	content := map[string]interface{}{
		"msgtype": "m.file",
		"body":    body,
	}
	if msg.MediaURL != "" {
		content["url"] = msg.MediaURL
	}
	if msg.FileSize > 0 {
		content["info"] = map[string]interface{}{
			"size": msg.FileSize,
		}
	}
	return &MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}
}

func (p *defaultMessageProcessor) locationToMatrix(msg *wechat.Message) *MatrixEventContent {
	body := "Location"
	geoURI := ""
	if msg.Location != nil {
		if msg.Location.Label != "" {
			body = msg.Location.Label
		}
		if msg.Location.Latitude != 0 || msg.Location.Longitude != 0 {
			geoURI = fmt.Sprintf("geo:%f,%f", msg.Location.Latitude, msg.Location.Longitude)
		}
	}
	return &MatrixEventContent{
		EventType: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.location",
			"body":    body,
			"geo_uri": geoURI,
		},
	}
}

func (p *defaultMessageProcessor) linkToMatrix(msg *wechat.Message) *MatrixEventContent {
	body := msg.Content
	if msg.LinkInfo != nil {
		parts := []string{}
		if msg.LinkInfo.Title != "" {
			parts = append(parts, msg.LinkInfo.Title)
		}
		if msg.LinkInfo.Description != "" {
			parts = append(parts, msg.LinkInfo.Description)
		}
		if msg.LinkInfo.URL != "" {
			parts = append(parts, msg.LinkInfo.URL)
		}
		if len(parts) > 0 {
			body = strings.Join(parts, "\n")
		}
	}

	return &MatrixEventContent{
		EventType: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.text",
			"body":    body,
		},
	}
}

func (p *defaultMessageProcessor) emojiToMatrix(msg *wechat.Message) *MatrixEventContent {
	content := map[string]interface{}{
		"msgtype": "m.image",
		"body":    "sticker",
	}
	if msg.MediaURL != "" {
		content["url"] = msg.MediaURL
	}
	return &MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}
}

func (p *defaultMessageProcessor) systemToMatrix(msg *wechat.Message) *MatrixEventContent {
	return &MatrixEventContent{
		EventType: "m.room.message",
		Content: map[string]interface{}{
			"msgtype": "m.notice",
			"body":    msg.Content,
		},
	}
}

// extractMXCURL extracts the MXC URL from Matrix event content.
func extractMXCURL(content map[string]interface{}) string {
	if url, ok := content["url"].(string); ok {
		return url
	}
	return ""
}
