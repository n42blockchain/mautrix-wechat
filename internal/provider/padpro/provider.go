package padpro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func init() {
	wechat.Register("padpro", func() wechat.Provider {
		return &Provider{}
	})
}

// Provider implements wechat.Provider using WeChatPadPro REST API + WebSocket.
// This is the Tier 2 recommended provider, successor to the archived GeWeChat project.
//
// Architecture:
//   - client.go:    REST API client for sending messages and managing contacts
//   - websocket.go: WebSocket client for receiving real-time events
//   - callback.go:  Event callback handler and message parsing
//
// WeChatPadPro must be running as a Docker container or standalone service.
// The bridge communicates via HTTP REST API and receives events via WebSocket.
type Provider struct {
	mu          sync.RWMutex
	cfg         *wechat.ProviderConfig
	handler     wechat.MessageHandler
	client      *http.Client
	loginState  wechat.LoginState
	self        *wechat.ContactInfo
	running     bool
	stopCh      chan struct{}
	log         *slog.Logger

	wsEndpoint  string
	wsConn      io.Closer // WebSocket connection (interface for testability)
}

// --- Lifecycle ---

func (p *Provider) Init(cfg *wechat.ProviderConfig, handler wechat.MessageHandler) error {
	p.cfg = cfg
	p.handler = handler
	p.client = &http.Client{Timeout: 30 * time.Second}
	p.stopCh = make(chan struct{})
	p.log = slog.Default().With("provider", "padpro")

	if cfg.APIEndpoint == "" {
		return fmt.Errorf("padpro provider: api_endpoint is required")
	}

	p.wsEndpoint = cfg.Extra["ws_endpoint"]
	if p.wsEndpoint == "" {
		return fmt.Errorf("padpro provider: ws_endpoint is required")
	}

	return nil
}

func (p *Provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	p.log.Info("starting PadPro provider",
		"api_endpoint", p.cfg.APIEndpoint,
		"ws_endpoint", p.wsEndpoint)

	// Verify connectivity
	if err := p.healthCheck(ctx); err != nil {
		return fmt.Errorf("padpro health check failed: %w", err)
	}

	// Start WebSocket event listener
	go p.wsEventLoop()

	p.running = true
	p.log.Info("PadPro provider started")
	return nil
}

func (p *Provider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	close(p.stopCh)

	if p.wsConn != nil {
		p.wsConn.Close()
	}

	p.running = false
	p.loginState = wechat.LoginStateLoggedOut
	p.log.Info("PadPro provider stopped")
	return nil
}

func (p *Provider) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// --- Identity ---

func (p *Provider) Name() string        { return "padpro" }
func (p *Provider) Tier() int            { return 2 }
func (p *Provider) Capabilities() wechat.Capability {
	return wechat.Capability{
		SendText:       true,
		SendImage:      true,
		SendVideo:      true,
		SendVoice:      true,
		SendFile:       true,
		SendLocation:   true,
		SendLink:       true,
		SendMiniApp:    false,
		ReceiveMessage: true,
		GroupManage:    true,
		ContactManage:  true,
		MomentAccess:   true,
		MomentRead:     true,
		MomentWrite:    false, // High ban risk, disabled by default
		ChannelsRead:   true,  // Can receive shared Channels links
		VoiceCall:      false,
		VideoCall:      false,
		Revoke:         true,
		Reaction:       false,
		ReadReceipt:    false,
		Typing:         false,
	}
}

// --- Authentication ---

func (p *Provider) Login(ctx context.Context) error {
	p.mu.Lock()
	p.loginState = wechat.LoginStateQRCode
	p.mu.Unlock()

	resp, err := p.apiPost(ctx, "/login/qrcode", nil)
	if err != nil {
		p.mu.Lock()
		p.loginState = wechat.LoginStateError
		p.mu.Unlock()
		return fmt.Errorf("request QR code: %w", err)
	}

	var qrResp struct {
		Code int    `json:"code"`
		Data struct {
			QRCode string `json:"qr_code"`
			QRURL  string `json:"qr_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &qrResp); err != nil {
		return fmt.Errorf("parse QR response: %w", err)
	}

	if p.handler != nil {
		p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
			State: wechat.LoginStateQRCode,
			QRURL: qrResp.Data.QRURL,
		})
	}

	return nil
}

func (p *Provider) Logout(ctx context.Context) error {
	_, err := p.apiPost(ctx, "/login/logout", nil)
	if err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	p.mu.Lock()
	p.loginState = wechat.LoginStateLoggedOut
	p.self = nil
	p.mu.Unlock()
	return nil
}

func (p *Provider) GetLoginState() wechat.LoginState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.loginState
}

func (p *Provider) GetSelf() *wechat.ContactInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.self
}

// --- Messaging ---

func (p *Provider) SendText(ctx context.Context, toUser string, text string) (string, error) {
	body := map[string]interface{}{
		"to_user": toUser,
		"content": text,
	}
	resp, err := p.apiPost(ctx, "/message/send/text", body)
	if err != nil {
		return "", fmt.Errorf("send text: %w", err)
	}
	return p.extractMsgID(resp)
}

func (p *Provider) SendImage(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	buf, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read image data: %w", err)
	}
	body := map[string]interface{}{
		"to_user":  toUser,
		"filename": filename,
		"data":     buf,
	}
	resp, err := p.apiPost(ctx, "/message/send/image", body)
	if err != nil {
		return "", fmt.Errorf("send image: %w", err)
	}
	return p.extractMsgID(resp)
}

func (p *Provider) SendVideo(ctx context.Context, toUser string, data io.Reader, filename string, thumb io.Reader) (string, error) {
	buf, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read video data: %w", err)
	}
	body := map[string]interface{}{
		"to_user":  toUser,
		"filename": filename,
		"data":     buf,
	}
	resp, err := p.apiPost(ctx, "/message/send/video", body)
	if err != nil {
		return "", fmt.Errorf("send video: %w", err)
	}
	return p.extractMsgID(resp)
}

func (p *Provider) SendVoice(ctx context.Context, toUser string, data io.Reader, duration int) (string, error) {
	buf, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read voice data: %w", err)
	}
	body := map[string]interface{}{
		"to_user":  toUser,
		"duration": duration,
		"data":     buf,
	}
	resp, err := p.apiPost(ctx, "/message/send/voice", body)
	if err != nil {
		return "", fmt.Errorf("send voice: %w", err)
	}
	return p.extractMsgID(resp)
}

func (p *Provider) SendFile(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	buf, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read file data: %w", err)
	}
	body := map[string]interface{}{
		"to_user":  toUser,
		"filename": filename,
		"data":     buf,
	}
	resp, err := p.apiPost(ctx, "/message/send/file", body)
	if err != nil {
		return "", fmt.Errorf("send file: %w", err)
	}
	return p.extractMsgID(resp)
}

func (p *Provider) SendLocation(ctx context.Context, toUser string, loc *wechat.LocationInfo) (string, error) {
	body := map[string]interface{}{
		"to_user":   toUser,
		"latitude":  loc.Latitude,
		"longitude": loc.Longitude,
		"label":     loc.Label,
		"poiname":   loc.Poiname,
	}
	resp, err := p.apiPost(ctx, "/message/send/location", body)
	if err != nil {
		return "", fmt.Errorf("send location: %w", err)
	}
	return p.extractMsgID(resp)
}

func (p *Provider) SendLink(ctx context.Context, toUser string, link *wechat.LinkCardInfo) (string, error) {
	body := map[string]interface{}{
		"to_user":     toUser,
		"title":       link.Title,
		"description": link.Description,
		"url":         link.URL,
		"thumb_url":   link.ThumbURL,
	}
	resp, err := p.apiPost(ctx, "/message/send/link", body)
	if err != nil {
		return "", fmt.Errorf("send link: %w", err)
	}
	return p.extractMsgID(resp)
}

func (p *Provider) RevokeMessage(ctx context.Context, msgID string, toUser string) error {
	body := map[string]interface{}{
		"msg_id":  msgID,
		"to_user": toUser,
	}
	_, err := p.apiPost(ctx, "/message/revoke", body)
	if err != nil {
		return fmt.Errorf("revoke message: %w", err)
	}
	return nil
}

// --- Contacts ---

func (p *Provider) GetContactList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	resp, err := p.apiGet(ctx, "/contact/list")
	if err != nil {
		return nil, fmt.Errorf("get contact list: %w", err)
	}
	var result struct {
		Data []*wechat.ContactInfo `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse contact list: %w", err)
	}
	return result.Data, nil
}

func (p *Provider) GetContactInfo(ctx context.Context, userID string) (*wechat.ContactInfo, error) {
	resp, err := p.apiGet(ctx, "/contact/info?user_id="+userID)
	if err != nil {
		return nil, fmt.Errorf("get contact info: %w", err)
	}
	var result struct {
		Data *wechat.ContactInfo `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse contact info: %w", err)
	}
	return result.Data, nil
}

func (p *Provider) GetUserAvatar(ctx context.Context, userID string) ([]byte, string, error) {
	resp, err := p.apiGet(ctx, "/contact/avatar?user_id="+userID)
	if err != nil {
		return nil, "", fmt.Errorf("get avatar: %w", err)
	}
	return resp, "image/jpeg", nil
}

func (p *Provider) AcceptFriendRequest(ctx context.Context, xml string) error {
	body := map[string]interface{}{"xml": xml}
	_, err := p.apiPost(ctx, "/contact/accept", body)
	return err
}

func (p *Provider) SetContactRemark(ctx context.Context, userID string, remark string) error {
	body := map[string]interface{}{
		"user_id": userID,
		"remark":  remark,
	}
	_, err := p.apiPost(ctx, "/contact/remark", body)
	return err
}

// --- Groups ---

func (p *Provider) GetGroupList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	resp, err := p.apiGet(ctx, "/group/list")
	if err != nil {
		return nil, fmt.Errorf("get group list: %w", err)
	}
	var result struct {
		Data []*wechat.ContactInfo `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse group list: %w", err)
	}
	return result.Data, nil
}

func (p *Provider) GetGroupMembers(ctx context.Context, groupID string) ([]*wechat.GroupMember, error) {
	resp, err := p.apiGet(ctx, "/group/members?group_id="+groupID)
	if err != nil {
		return nil, fmt.Errorf("get group members: %w", err)
	}
	var result struct {
		Data []*wechat.GroupMember `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse group members: %w", err)
	}
	return result.Data, nil
}

func (p *Provider) GetGroupInfo(ctx context.Context, groupID string) (*wechat.ContactInfo, error) {
	resp, err := p.apiGet(ctx, "/group/info?group_id="+groupID)
	if err != nil {
		return nil, fmt.Errorf("get group info: %w", err)
	}
	var result struct {
		Data *wechat.ContactInfo `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse group info: %w", err)
	}
	return result.Data, nil
}

func (p *Provider) CreateGroup(ctx context.Context, name string, members []string) (string, error) {
	body := map[string]interface{}{
		"name":    name,
		"members": members,
	}
	resp, err := p.apiPost(ctx, "/group/create", body)
	if err != nil {
		return "", fmt.Errorf("create group: %w", err)
	}
	var result struct {
		Data struct {
			GroupID string `json:"group_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parse create group response: %w", err)
	}
	return result.Data.GroupID, nil
}

func (p *Provider) InviteToGroup(ctx context.Context, groupID string, userIDs []string) error {
	body := map[string]interface{}{"group_id": groupID, "user_ids": userIDs}
	_, err := p.apiPost(ctx, "/group/invite", body)
	return err
}

func (p *Provider) RemoveFromGroup(ctx context.Context, groupID string, userIDs []string) error {
	body := map[string]interface{}{"group_id": groupID, "user_ids": userIDs}
	_, err := p.apiPost(ctx, "/group/remove", body)
	return err
}

func (p *Provider) SetGroupName(ctx context.Context, groupID string, name string) error {
	body := map[string]interface{}{"group_id": groupID, "name": name}
	_, err := p.apiPost(ctx, "/group/rename", body)
	return err
}

func (p *Provider) SetGroupAnnouncement(ctx context.Context, groupID string, text string) error {
	body := map[string]interface{}{"group_id": groupID, "announcement": text}
	_, err := p.apiPost(ctx, "/group/announcement", body)
	return err
}

func (p *Provider) LeaveGroup(ctx context.Context, groupID string) error {
	body := map[string]interface{}{"group_id": groupID}
	_, err := p.apiPost(ctx, "/group/leave", body)
	return err
}

// --- Media ---

func (p *Provider) DownloadMedia(ctx context.Context, msg *wechat.Message) (io.ReadCloser, string, error) {
	if msg.MediaURL == "" {
		return nil, "", fmt.Errorf("no media URL in message")
	}
	resp, err := p.apiGet(ctx, "/media/download?url="+msg.MediaURL)
	if err != nil {
		return nil, "", fmt.Errorf("download media: %w", err)
	}
	return io.NopCloser(bytes.NewReader(resp)), "application/octet-stream", nil
}

// --- Internal helpers ---

func (p *Provider) apiGet(ctx context.Context, path string) ([]byte, error) {
	url := p.cfg.APIEndpoint + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return p.doRequest(req)
}

func (p *Provider) apiPost(ctx context.Context, path string, body interface{}) ([]byte, error) {
	url := p.cfg.APIEndpoint + path
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return p.doRequest(req)
}

func (p *Provider) doRequest(req *http.Request) ([]byte, error) {
	if p.cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

func (p *Provider) extractMsgID(resp []byte) (string, error) {
	var result struct {
		Data struct {
			MsgID string `json:"msg_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parse message response: %w", err)
	}
	if result.Data.MsgID == "" {
		p.log.Warn("API returned empty msg_id", "response", string(resp))
	}
	return result.Data.MsgID, nil
}

func (p *Provider) healthCheck(ctx context.Context) error {
	resp, err := p.apiGet(ctx, "/health")
	if err != nil {
		return err
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("parse health response: %w", err)
	}
	if result.Code != 0 && result.Code != 200 {
		return fmt.Errorf("unhealthy: %s", result.Msg)
	}
	return nil
}

// wsEventLoop connects to the WebSocket endpoint and dispatches events.
func (p *Provider) wsEventLoop() {
	p.log.Info("WebSocket event loop started", "endpoint", p.wsEndpoint)

	for {
		select {
		case <-p.stopCh:
			p.log.Info("WebSocket event loop stopped")
			return
		default:
			if err := p.wsConnect(); err != nil {
				p.log.Error("WebSocket connection error, reconnecting in 5s", "error", err)
				select {
				case <-p.stopCh:
					return
				case <-time.After(5 * time.Second):
				}
			}
		}
	}
}

// wsConnect establishes a WebSocket connection and reads events.
// This is a placeholder — the actual WebSocket implementation requires
// a WebSocket library (e.g. gorilla/websocket or nhooyr.io/websocket).
func (p *Provider) wsConnect() error {
	p.log.Debug("connecting to WebSocket", "endpoint", p.wsEndpoint)
	// TODO: Implement actual WebSocket connection using gorilla/websocket
	// 1. Dial p.wsEndpoint
	// 2. Read JSON messages in a loop
	// 3. Parse message type and dispatch to p.handler callbacks
	// 4. Handle login state changes (QR scanned, confirmed, logged in)
	// 5. Handle reconnection on connection loss
	select {
	case <-p.stopCh:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("WebSocket not yet implemented, retry")
	}
}
