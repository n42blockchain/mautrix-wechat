package wecom

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

// WeCom message send request types.
// See: https://developer.work.weixin.qq.com/document/path/90236

// sendMessageRequest is the common envelope for /cgi-bin/message/send.
type sendMessageRequest struct {
	ToUser  string `json:"touser,omitempty"`
	ToParty string `json:"toparty,omitempty"`
	ToTag   string `json:"totag,omitempty"`
	MsgType string `json:"msgtype"`
	AgentID int    `json:"agentid"`

	Text     *textContent     `json:"text,omitempty"`
	Image    *mediaContent    `json:"image,omitempty"`
	Voice    *mediaContent    `json:"voice,omitempty"`
	Video    *videoContent    `json:"video,omitempty"`
	File     *mediaContent    `json:"file,omitempty"`
	TextCard *textCardContent `json:"textcard,omitempty"`
	News     *newsContent     `json:"news,omitempty"`
	Markdown *markdownContent `json:"markdown,omitempty"`
}

type textContent struct {
	Content string `json:"content"`
}

type mediaContent struct {
	MediaID string `json:"media_id"`
}

type videoContent struct {
	MediaID     string `json:"media_id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type textCardContent struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	BtnTxt      string `json:"btntxt,omitempty"`
}

type newsContent struct {
	Articles []newsArticle `json:"articles"`
}

type newsArticle struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
	PicURL      string `json:"picurl,omitempty"`
}

type markdownContent struct {
	Content string `json:"content"`
}

// sendMessageResponse is the response from /cgi-bin/message/send.
type sendMessageResponse struct {
	APIResponse
	InvalidUser  string `json:"invaliduser,omitempty"`
	InvalidParty string `json:"invalidparty,omitempty"`
	InvalidTag   string `json:"invalidtag,omitempty"`
	MsgID        string `json:"msgid,omitempty"`
	ResponseCode string `json:"response_code,omitempty"`
}

// Group chat message types for /cgi-bin/appchat/send.
type appChatSendRequest struct {
	ChatID  string `json:"chatid"`
	MsgType string `json:"msgtype"`

	Text     *textContent     `json:"text,omitempty"`
	Image    *mediaContent    `json:"image,omitempty"`
	Voice    *mediaContent    `json:"voice,omitempty"`
	Video    *videoContent    `json:"video,omitempty"`
	File     *mediaContent    `json:"file,omitempty"`
	TextCard *textCardContent `json:"textcard,omitempty"`
	News     *newsContent     `json:"news,omitempty"`
}

// Recall message request for /cgi-bin/message/recall.
type recallMessageRequest struct {
	MsgID string `json:"msgid"`
}

// sendMessage sends a message via /cgi-bin/message/send and returns the msg ID.
func (p *Provider) sendMessage(ctx context.Context, req *sendMessageRequest) (string, error) {
	req.AgentID = p.client.agentID

	var resp sendMessageResponse
	if err := p.client.PostJSON(ctx, "/cgi-bin/message/send", req, &resp); err != nil {
		return "", err
	}

	if resp.ErrCode != 0 {
		return "", fmt.Errorf("send message: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}

	return resp.MsgID, nil
}

// sendGroupMessage sends a message to a group chat via /cgi-bin/appchat/send.
func (p *Provider) sendGroupMessage(ctx context.Context, req *appChatSendRequest) error {
	var resp APIResponse
	if err := p.client.PostJSON(ctx, "/cgi-bin/appchat/send", req, &resp); err != nil {
		return err
	}

	if resp.ErrCode != 0 {
		return fmt.Errorf("send group message: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}

	return nil
}

// isGroupChat checks if a toUser ID looks like a WeCom group chat ID.
func isGroupChat(toUser string) bool {
	// WeCom group chat IDs are typically returned from appchat/create
	// They don't have the wxid_ prefix that personal chats do
	// In practice, the bridge will track this via room metadata
	return false
}

// SendText sends a text message.
func (p *Provider) SendText(ctx context.Context, toUser string, text string) (string, error) {
	if isGroupChat(toUser) {
		err := p.sendGroupMessage(ctx, &appChatSendRequest{
			ChatID:  toUser,
			MsgType: "text",
			Text:    &textContent{Content: text},
		})
		return "", err
	}

	return p.sendMessage(ctx, &sendMessageRequest{
		ToUser:  toUser,
		MsgType: "text",
		Text:    &textContent{Content: text},
	})
}

// SendImage uploads an image and sends it.
func (p *Provider) SendImage(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	mediaResp, err := p.client.UploadMedia(ctx, "image", filename, data)
	if err != nil {
		return "", fmt.Errorf("upload image: %w", err)
	}

	if isGroupChat(toUser) {
		err := p.sendGroupMessage(ctx, &appChatSendRequest{
			ChatID:  toUser,
			MsgType: "image",
			Image:   &mediaContent{MediaID: mediaResp.MediaID},
		})
		return "", err
	}

	return p.sendMessage(ctx, &sendMessageRequest{
		ToUser:  toUser,
		MsgType: "image",
		Image:   &mediaContent{MediaID: mediaResp.MediaID},
	})
}

// SendVideo uploads a video and sends it.
func (p *Provider) SendVideo(ctx context.Context, toUser string, data io.Reader, filename string, thumb io.Reader) (string, error) {
	mediaResp, err := p.client.UploadMedia(ctx, "video", filename, data)
	if err != nil {
		return "", fmt.Errorf("upload video: %w", err)
	}

	if isGroupChat(toUser) {
		err := p.sendGroupMessage(ctx, &appChatSendRequest{
			ChatID:  toUser,
			MsgType: "video",
			Video:   &videoContent{MediaID: mediaResp.MediaID},
		})
		return "", err
	}

	return p.sendMessage(ctx, &sendMessageRequest{
		ToUser:  toUser,
		MsgType: "video",
		Video:   &videoContent{MediaID: mediaResp.MediaID},
	})
}

// SendVoice uploads a voice recording and sends it.
func (p *Provider) SendVoice(ctx context.Context, toUser string, data io.Reader, duration int) (string, error) {
	mediaResp, err := p.client.UploadMedia(ctx, "voice", "voice.amr", data)
	if err != nil {
		return "", fmt.Errorf("upload voice: %w", err)
	}

	if isGroupChat(toUser) {
		err := p.sendGroupMessage(ctx, &appChatSendRequest{
			ChatID:  toUser,
			MsgType: "voice",
			Voice:   &mediaContent{MediaID: mediaResp.MediaID},
		})
		return "", err
	}

	return p.sendMessage(ctx, &sendMessageRequest{
		ToUser:  toUser,
		MsgType: "voice",
		Voice:   &mediaContent{MediaID: mediaResp.MediaID},
	})
}

// SendFile uploads a file and sends it.
func (p *Provider) SendFile(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	mediaResp, err := p.client.UploadMedia(ctx, "file", filename, data)
	if err != nil {
		return "", fmt.Errorf("upload file: %w", err)
	}

	if isGroupChat(toUser) {
		err := p.sendGroupMessage(ctx, &appChatSendRequest{
			ChatID:  toUser,
			MsgType: "file",
			File:    &mediaContent{MediaID: mediaResp.MediaID},
		})
		return "", err
	}

	return p.sendMessage(ctx, &sendMessageRequest{
		ToUser:  toUser,
		MsgType: "file",
		File:    &mediaContent{MediaID: mediaResp.MediaID},
	})
}

// SendLocation is not directly supported by WeCom application messages.
// We convert it to a text message with a map link.
func (p *Provider) SendLocation(ctx context.Context, toUser string, loc *wechat.LocationInfo) (string, error) {
	text := fmt.Sprintf("[Location] %s\n%s\nhttps://uri.amap.com/marker?position=%f,%f",
		loc.Poiname, loc.Label, loc.Longitude, loc.Latitude)
	return p.SendText(ctx, toUser, text)
}

// SendLink sends a textcard message (WeCom's equivalent of link cards).
func (p *Provider) SendLink(ctx context.Context, toUser string, link *wechat.LinkCardInfo) (string, error) {
	if isGroupChat(toUser) {
		err := p.sendGroupMessage(ctx, &appChatSendRequest{
			ChatID:  toUser,
			MsgType: "textcard",
			TextCard: &textCardContent{
				Title:       link.Title,
				Description: link.Description,
				URL:         link.URL,
				BtnTxt:      "View",
			},
		})
		return "", err
	}

	return p.sendMessage(ctx, &sendMessageRequest{
		ToUser:  toUser,
		MsgType: "textcard",
		TextCard: &textCardContent{
			Title:       link.Title,
			Description: link.Description,
			URL:         link.URL,
			BtnTxt:      "View",
		},
	})
}

// RevokeMessage recalls a sent message.
func (p *Provider) RevokeMessage(ctx context.Context, msgID string, toUser string) error {
	var resp APIResponse
	err := p.client.PostJSON(ctx, "/cgi-bin/message/recall", &recallMessageRequest{
		MsgID: msgID,
	}, &resp)
	if err != nil {
		return err
	}

	if resp.ErrCode != 0 {
		return fmt.Errorf("recall message: [%d] %s", resp.ErrCode, resp.ErrMsg)
	}

	return nil
}

// DownloadMedia downloads media from WeCom by media_id.
func (p *Provider) DownloadMedia(ctx context.Context, msg *wechat.Message) (io.ReadCloser, string, error) {
	mediaID := msg.Extra["media_id"]
	if mediaID == "" {
		mediaID = msg.MsgID
	}

	return p.client.DownloadMedia(ctx, mediaID)
}

// makeMessageID generates a unique message ID for tracking purposes.
func makeMessageID(agentID int, ts int64) string {
	return fmt.Sprintf("wecom_%d_%d", agentID, ts)
}

// formatMsgID converts the WeCom msgid to string.
func formatMsgID(id interface{}) string {
	switch v := id.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}
