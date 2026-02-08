package message

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"

	"github.com/n42/mautrix-wechat/internal/bridge"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// MentionResolver resolves WeChat nicknames to Matrix IDs and vice versa.
// The EventRouter provides this based on PuppetManager lookups.
type MentionResolver interface {
	// ResolveWeChatMention maps a WeChat nickname to (matrixUserID, displayName).
	ResolveWeChatMention(nickname string) (matrixID, displayName string)
	// ResolveMatrixMention maps a Matrix user ID to (wechatID, nickname).
	ResolveMatrixMention(matrixID string) (wechatID, nickname string)
}

// Processor converts messages between WeChat and Matrix formats.
// It implements bridge.MessageProcessor.
type Processor struct {
	log             *slog.Logger
	matrixClient    bridge.MatrixClient
	mentionResolver MentionResolver
}

// Ensure Processor implements bridge.MessageProcessor.
var _ bridge.MessageProcessor = (*Processor)(nil)

// NewProcessor creates a new message processor.
func NewProcessor(log *slog.Logger, client bridge.MatrixClient) *Processor {
	return &Processor{
		log:          log,
		matrixClient: client,
	}
}

// SetMentionResolver sets the mention resolver for @mention conversion.
func (p *Processor) SetMentionResolver(resolver MentionResolver) {
	p.mentionResolver = resolver
}

// WeChatToMatrix converts a WeChat message to Matrix event content.
func (p *Processor) WeChatToMatrix(ctx context.Context, msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	switch msg.Type {
	case wechat.MsgText:
		return p.convertText(msg)
	case wechat.MsgImage:
		return p.convertImage(ctx, msg)
	case wechat.MsgVoice:
		return p.convertVoice(ctx, msg)
	case wechat.MsgVideo:
		return p.convertVideo(ctx, msg)
	case wechat.MsgEmoji:
		return p.convertEmoji(ctx, msg)
	case wechat.MsgLocation:
		return p.convertLocation(msg)
	case wechat.MsgLink:
		return p.convertLink(msg)
	case wechat.MsgFile:
		return p.convertFile(ctx, msg)
	case wechat.MsgMiniApp:
		return p.convertMiniApp(msg)
	case wechat.MsgSystem:
		return p.convertSystem(msg)
	case wechat.MsgRevoke:
		// Revocations are handled separately via OnRevoke
		return nil, nil
	case wechat.MsgContact:
		return p.convertContact(msg)
	default:
		p.log.Warn("unsupported wechat message type", "type", msg.Type)
		return nil, nil
	}
}

// MatrixToWeChat converts a Matrix event to a WeChat send action.
func (p *Processor) MatrixToWeChat(ctx context.Context, evt *bridge.MatrixEvent) (*bridge.WeChatSendAction, error) {
	msgtype, _ := evt.Content["msgtype"].(string)

	switch msgtype {
	case "m.text":
		return p.matrixTextToWeChat(evt)
	case "m.image":
		return p.matrixMediaToWeChat(evt, wechat.MsgImage)
	case "m.video":
		return p.matrixMediaToWeChat(evt, wechat.MsgVideo)
	case "m.audio":
		return p.matrixMediaToWeChat(evt, wechat.MsgVoice)
	case "m.file":
		return p.matrixMediaToWeChat(evt, wechat.MsgFile)
	case "m.location":
		return p.matrixLocationToWeChat(evt)
	case "m.notice":
		return p.matrixTextToWeChat(evt)
	case "m.emote":
		return p.matrixEmoteToWeChat(evt)
	default:
		p.log.Warn("unsupported matrix msgtype", "msgtype", msgtype)
		return nil, nil
	}
}

// --- WeChat -> Matrix converters ---

func (p *Processor) convertText(msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	content := map[string]interface{}{
		"msgtype": "m.text",
		"body":    msg.Content,
	}

	// Convert WeChat @mentions to Matrix HTML pills
	if p.mentionResolver != nil && strings.Contains(msg.Content, "@") {
		plainText, htmlText, _ := ConvertWeChatMentionsToMatrix(
			msg.Content, p.mentionResolver.ResolveWeChatMention,
		)
		if htmlText != "" {
			content["body"] = plainText
			content["format"] = "org.matrix.custom.html"
			content["formatted_body"] = htmlText
		}
	}

	// Reply-to is resolved by EventRouter after conversion (it has message mapping access).
	// We leave msg.ReplyTo for the EventRouter to handle.

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertImage(ctx context.Context, msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	mxcURI, mimeType, err := p.uploadMedia(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("upload image: %w", err)
	}

	content := map[string]interface{}{
		"msgtype": "m.image",
		"body":    fileNameOrDefault(msg.FileName, "image.jpg"),
		"url":     mxcURI,
		"info": map[string]interface{}{
			"mimetype": mimeType,
			"size":     msg.FileSize,
		},
	}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertVoice(ctx context.Context, msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	mxcURI, mimeType, err := p.uploadMedia(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("upload voice: %w", err)
	}

	content := map[string]interface{}{
		"msgtype": "m.audio",
		"body":    fileNameOrDefault(msg.FileName, "voice.ogg"),
		"url":     mxcURI,
		"info": map[string]interface{}{
			"mimetype": mimeType,
			"duration": msg.Duration * 1000, // seconds to ms
			"size":     msg.FileSize,
		},
	}

	// Add voice message flag (MSC3245)
	content["org.matrix.msc3245.voice"] = map[string]interface{}{}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertVideo(ctx context.Context, msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	mxcURI, mimeType, err := p.uploadMedia(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("upload video: %w", err)
	}

	info := map[string]interface{}{
		"mimetype": mimeType,
		"duration": msg.Duration * 1000,
		"size":     msg.FileSize,
	}

	// Upload thumbnail if available
	if len(msg.Thumbnail) > 0 {
		thumbMXC, err := p.matrixClient.UploadMedia(ctx, msg.Thumbnail, "image/jpeg", "thumbnail.jpg")
		if err == nil {
			info["thumbnail_url"] = thumbMXC
			info["thumbnail_info"] = map[string]interface{}{
				"mimetype": "image/jpeg",
			}
		}
	}

	content := map[string]interface{}{
		"msgtype": "m.video",
		"body":    fileNameOrDefault(msg.FileName, "video.mp4"),
		"url":     mxcURI,
		"info":    info,
	}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertEmoji(ctx context.Context, msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	// Custom stickers are sent as images
	if len(msg.MediaData) > 0 || msg.MediaURL != "" {
		return p.convertImage(ctx, msg)
	}

	// Fallback to text emoji
	return p.convertText(msg)
}

func (p *Processor) convertLocation(msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	if msg.Location == nil {
		return nil, fmt.Errorf("location info is nil")
	}

	geoURI := fmt.Sprintf("geo:%f,%f", msg.Location.Latitude, msg.Location.Longitude)
	body := msg.Location.Label
	if msg.Location.Poiname != "" {
		body = msg.Location.Poiname + " - " + body
	}

	content := map[string]interface{}{
		"msgtype": "m.location",
		"body":    body,
		"geo_uri": geoURI,
	}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertLink(msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	if msg.LinkInfo == nil {
		return p.convertText(msg)
	}

	// Format as HTML
	htmlBody := fmt.Sprintf(
		`<blockquote><a href="%s"><strong>%s</strong></a><br/>%s</blockquote>`,
		html.EscapeString(msg.LinkInfo.URL),
		html.EscapeString(msg.LinkInfo.Title),
		html.EscapeString(msg.LinkInfo.Description),
	)

	textBody := fmt.Sprintf("%s\n%s\n%s", msg.LinkInfo.Title, msg.LinkInfo.Description, msg.LinkInfo.URL)

	content := map[string]interface{}{
		"msgtype":        "m.text",
		"body":           textBody,
		"format":         "org.matrix.custom.html",
		"formatted_body": htmlBody,
	}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertFile(ctx context.Context, msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	mxcURI, mimeType, err := p.uploadMedia(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}

	content := map[string]interface{}{
		"msgtype": "m.file",
		"body":    fileNameOrDefault(msg.FileName, "file"),
		"url":     mxcURI,
		"info": map[string]interface{}{
			"mimetype": mimeType,
			"size":     msg.FileSize,
		},
	}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertMiniApp(msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	// Extract mini app title and link from content
	title := "Mini App"
	url := ""

	if msg.LinkInfo != nil {
		title = msg.LinkInfo.Title
		url = msg.LinkInfo.URL
	}

	body := fmt.Sprintf("[Mini App] %s", title)
	if url != "" {
		body += "\n" + url
	}

	htmlBody := fmt.Sprintf(`<em>[Mini App]</em> <strong>%s</strong>`, html.EscapeString(title))
	if url != "" {
		htmlBody += fmt.Sprintf(`<br/><a href="%s">%s</a>`, html.EscapeString(url), html.EscapeString(url))
	}

	content := map[string]interface{}{
		"msgtype":        "m.text",
		"body":           body,
		"format":         "org.matrix.custom.html",
		"formatted_body": htmlBody,
	}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertSystem(msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	// System messages (join/leave/rename) are sent as m.notice
	content := map[string]interface{}{
		"msgtype": "m.notice",
		"body":    msg.Content,
	}

	// Check for pat (拍一拍) messages
	if isPat(msg.Content) {
		content["msgtype"] = "m.emote"
	}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

func (p *Processor) convertContact(msg *wechat.Message) (*bridge.MatrixEventContent, error) {
	body := fmt.Sprintf("[Contact Card] %s", msg.Content)
	content := map[string]interface{}{
		"msgtype": "m.text",
		"body":    body,
	}

	return &bridge.MatrixEventContent{
		EventType: "m.room.message",
		Content:   content,
	}, nil
}

// --- Matrix -> WeChat converters ---

func (p *Processor) matrixTextToWeChat(evt *bridge.MatrixEvent) (*bridge.WeChatSendAction, error) {
	body, _ := evt.Content["body"].(string)
	action := &bridge.WeChatSendAction{
		Type: wechat.MsgText,
		Text: body,
	}

	// Convert Matrix HTML pill @mentions to WeChat @mentions
	if p.mentionResolver != nil {
		formattedBody, _ := evt.Content["formatted_body"].(string)
		format, _ := evt.Content["format"].(string)
		if format == "org.matrix.custom.html" && formattedBody != "" {
			text, mentionedIDs := ConvertMatrixMentionsToWeChat(
				formattedBody, body, p.mentionResolver.ResolveMatrixMention,
			)
			if len(mentionedIDs) > 0 {
				action.Text = text
				action.Mentions = mentionedIDs
			}
		}
	}

	// Extract reply-to relation (EventRouter resolves Matrix event ID → WeChat msg ID)
	if relatesTo, ok := evt.Content["m.relates_to"].(map[string]interface{}); ok {
		if inReplyTo, ok := relatesTo["m.in_reply_to"].(map[string]interface{}); ok {
			if eventID, ok := inReplyTo["event_id"].(string); ok {
				action.ReplyTo = eventID // EventRouter will convert to WeChat msg ID
			}
		}
	}

	return action, nil
}

func (p *Processor) matrixMediaToWeChat(evt *bridge.MatrixEvent, msgType wechat.MsgType) (*bridge.WeChatSendAction, error) {
	url, _ := evt.Content["url"].(string)
	body, _ := evt.Content["body"].(string)

	return &bridge.WeChatSendAction{
		Type: msgType,
		File: body,
		Extra: map[string]interface{}{
			"mxc_url": url,
		},
	}, nil
}

func (p *Processor) matrixLocationToWeChat(evt *bridge.MatrixEvent) (*bridge.WeChatSendAction, error) {
	body, _ := evt.Content["body"].(string)
	geoURI, _ := evt.Content["geo_uri"].(string)

	return &bridge.WeChatSendAction{
		Type: wechat.MsgText,
		Text: fmt.Sprintf("[Location] %s %s", body, geoURI),
	}, nil
}

func (p *Processor) matrixEmoteToWeChat(evt *bridge.MatrixEvent) (*bridge.WeChatSendAction, error) {
	body, _ := evt.Content["body"].(string)
	return &bridge.WeChatSendAction{
		Type: wechat.MsgText,
		Text: fmt.Sprintf("* %s", body),
	}, nil
}

// --- Helpers ---

func (p *Processor) uploadMedia(ctx context.Context, msg *wechat.Message) (string, string, error) {
	if len(msg.MediaData) == 0 {
		return "", "", fmt.Errorf("no media data")
	}

	mimeType := guessMimeType(msg)
	fileName := fileNameOrDefault(msg.FileName, "media")

	mxcURI, err := p.matrixClient.UploadMedia(ctx, msg.MediaData, mimeType, fileName)
	if err != nil {
		return "", "", err
	}

	return mxcURI, mimeType, nil
}

func fileNameOrDefault(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

func guessMimeType(msg *wechat.Message) string {
	switch msg.Type {
	case wechat.MsgImage:
		return "image/jpeg"
	case wechat.MsgVoice:
		return "audio/ogg"
	case wechat.MsgVideo:
		return "video/mp4"
	case wechat.MsgEmoji:
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

func isPat(content string) bool {
	return strings.Contains(content, "拍了拍") || strings.Contains(content, "patted")
}
