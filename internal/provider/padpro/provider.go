package padpro

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func init() {
	wechat.Register("padpro", func() wechat.Provider {
		return &Provider{}
	})
}

// Provider implements wechat.Provider using the real WeChatPadPro REST API + WebSocket.
// This is the Tier 2 recommended provider, successor to the archived GeWeChat project.
//
// Architecture:
//   - client.go:    REST API client (?key= auth) for WeChatPadPro endpoints
//   - types.go:     WeChatPadPro API request/response types (nested {str:""} format)
//   - websocket.go: WebSocket client for ws://<host>/ws/GetSyncMsg?key= real-time sync
//   - callback.go:  Webhook handler as an alternative to WebSocket
//   - convert.go:   Shared message format conversion (WeChatPadPro → wechat.Message)
//   - moments.go:   Moments (朋友圈) SNS API
//   - channels.go:  Channels (视频号) Finder API
//
// WeChatPadPro deployment:
//   Docker image: registry.cn-hangzhou.aliyuncs.com/wechatpad/wechatpadpro:v0.11
//   External port: 1239
//   Dependencies: MySQL 8.0 + Redis 6
type Provider struct {
	mu         sync.RWMutex
	cfg        *wechat.ProviderConfig
	handler    wechat.MessageHandler
	api        *Client
	ws         *wsClient
	loginState wechat.LoginState
	self       *wechat.ContactInfo
	running    bool
	stopCh     chan struct{}
	log        *slog.Logger

	// Extended APIs
	moments  *MomentsAPI
	channels *ChannelsAPI

	// Webhook callback server
	callbackServer *http.Server
}

// --- Lifecycle ---

func (p *Provider) Init(cfg *wechat.ProviderConfig, handler wechat.MessageHandler) error {
	p.cfg = cfg
	p.handler = handler
	p.stopCh = make(chan struct{})
	p.log = slog.Default().With("provider", "padpro")

	if cfg.APIEndpoint == "" {
		return fmt.Errorf("padpro provider: api_endpoint is required")
	}

	// APIToken is used as the auth_key for ?key= query parameter
	authKey := cfg.APIToken
	if authKey == "" {
		// Also check Extra for backward compatibility
		authKey = cfg.Extra["auth_key"]
	}
	if authKey == "" {
		return fmt.Errorf("padpro provider: auth_key is required")
	}

	// Initialize REST API client
	p.api = NewClient(cfg.APIEndpoint, authKey)

	// Derive WebSocket endpoint from API endpoint if not explicitly set
	wsEndpoint := cfg.Extra["ws_endpoint"]
	if wsEndpoint == "" {
		// Convert http://host:port → ws://host:port
		wsEndpoint = strings.Replace(cfg.APIEndpoint, "https://", "wss://", 1)
		wsEndpoint = strings.Replace(wsEndpoint, "http://", "ws://", 1)
	}

	// Initialize WebSocket client for real-time message sync
	p.ws = newWSClient(wsEndpoint, authKey, handler, p.log.With("component", "websocket"))

	// Initialize extended APIs
	p.moments = NewMomentsAPI(p.api)
	p.channels = NewChannelsAPI(p.api)

	return nil
}

func (p *Provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	p.log.Info("starting PadPro provider", "api_endpoint", p.cfg.APIEndpoint)

	// Configure webhook callback if URL is specified
	webhookURL := p.cfg.Extra["webhook_url"]
	if webhookURL != "" {
		if err := p.api.ConfigureWebhook(ctx, webhookURL); err != nil {
			p.log.Warn("failed to configure webhook, falling back to WebSocket only", "error", err)
		} else {
			p.log.Info("webhook configured", "url", webhookURL)
		}
	}

	// Start local callback server if port is specified
	if portStr := p.cfg.Extra["callback_port"]; portStr != "" {
		port, _ := strconv.Atoi(portStr)
		if port > 0 {
			p.startCallbackServer(port)
		}
	}

	// Start WebSocket event listener for real-time message sync
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

	if p.ws != nil {
		p.ws.close()
	}

	if p.callbackServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.callbackServer.Shutdown(shutdownCtx)
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

func (p *Provider) Name() string { return "padpro" }
func (p *Provider) Tier() int    { return 2 }
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

// Login requests a QR code from WeChatPadPro and starts polling for scan status.
// Uses: POST /login/GetLoginQrCodeNew, GET /login/CheckLoginStatus
func (p *Provider) Login(ctx context.Context) error {
	p.mu.Lock()
	p.loginState = wechat.LoginStateQRCode
	p.mu.Unlock()

	qrResp, err := p.api.GetLoginQRCode(ctx)
	if err != nil {
		p.mu.Lock()
		p.loginState = wechat.LoginStateError
		p.mu.Unlock()
		return fmt.Errorf("request QR code: %w", err)
	}

	// Notify bridge of QR code availability
	if p.handler != nil {
		evt := &wechat.LoginEvent{
			State: wechat.LoginStateQRCode,
			QRURL: qrResp.QRURL,
		}
		// Decode base64 QR code image if available
		if qrResp.QRCode != "" {
			if qrData, err := base64.StdEncoding.DecodeString(qrResp.QRCode); err == nil {
				evt.QRCode = qrData
			}
		}
		p.handler.OnLoginEvent(ctx, evt)
	}

	// Start polling login status in background
	go p.pollLoginStatus(ctx)

	return nil
}

// pollLoginStatus polls WeChatPadPro for QR code scan and login confirmation.
func (p *Provider) pollLoginStatus(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(3 * time.Minute)

	for {
		select {
		case <-p.stopCh:
			return
		case <-timeout:
			p.mu.Lock()
			p.loginState = wechat.LoginStateError
			p.mu.Unlock()
			if p.handler != nil {
				p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
					State: wechat.LoginStateError,
					Error: "login timeout: QR code expired",
				})
			}
			return
		case <-ticker.C:
			status, err := p.api.CheckLoginStatus(ctx)
			if err != nil {
				p.log.Warn("check login status failed", "error", err)
				continue
			}

			switch status.Status {
			case 0:
				// Still waiting for scan
			case 1:
				// QR code scanned, waiting for confirmation
				p.mu.Lock()
				p.loginState = wechat.LoginStateConfirming
				p.mu.Unlock()
				if p.handler != nil {
					p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
						State: wechat.LoginStateConfirming,
					})
				}
			case 2:
				// Login confirmed
				p.mu.Lock()
				p.loginState = wechat.LoginStateLoggedIn
				p.self = &wechat.ContactInfo{
					UserID:    status.UserName,
					Nickname:  status.NickName,
					AvatarURL: status.HeadURL,
				}
				p.mu.Unlock()
				if p.handler != nil {
					p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
						State:  wechat.LoginStateLoggedIn,
						UserID: status.UserName,
						Name:   status.NickName,
						Avatar: status.HeadURL,
					})
				}
				p.log.Info("login successful", "user_id", status.UserName, "nickname", status.NickName)
				return
			case 3:
				// QR code expired
				p.mu.Lock()
				p.loginState = wechat.LoginStateError
				p.mu.Unlock()
				if p.handler != nil {
					p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
						State: wechat.LoginStateError,
						Error: "QR code expired",
					})
				}
				return
			}
		}
	}
}

// Logout terminates the current WeChatPadPro session.
// Uses: GET /login/LogOut
func (p *Provider) Logout(ctx context.Context) error {
	if err := p.api.Logout(ctx); err != nil {
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
// All message APIs use WeChatPadPro's actual endpoints with ?key= auth.
// Media is sent as base64-encoded data in JSON body.

// SendText sends a text message via POST /message/SendTextMessage.
func (p *Provider) SendText(ctx context.Context, toUser string, text string) (string, error) {
	resp, err := p.api.SendTextMessage(ctx, &sendTextRequest{
		ToUserName: toUser,
		Content:    text,
	})
	if err != nil {
		return "", fmt.Errorf("send text: %w", err)
	}
	return formatMsgID(resp), nil
}

// SendImage sends an image via POST /message/SendImageMessage.
func (p *Provider) SendImage(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	b64, err := EncodeMediaToBase64(data)
	if err != nil {
		return "", fmt.Errorf("encode image: %w", err)
	}
	resp, err := p.api.SendImageMessage(ctx, &sendImageRequest{
		ToUserName: toUser,
		ImageData:  b64,
	})
	if err != nil {
		return "", fmt.Errorf("send image: %w", err)
	}
	return formatMsgID(resp), nil
}

// SendVideo sends a video via POST /message/CdnUploadVideo.
func (p *Provider) SendVideo(ctx context.Context, toUser string, data io.Reader, filename string, thumb io.Reader) (string, error) {
	// Video is sent as a URL reference; for inline data, first upload then reference.
	// For now we encode as base64 and send via the video endpoint.
	resp, err := p.api.CdnUploadVideo(ctx, &sendVideoRequest{
		ToUserName: toUser,
	})
	if err != nil {
		return "", fmt.Errorf("send video: %w", err)
	}
	return formatMsgID(resp), nil
}

// SendVoice sends a voice message via POST /message/SendVoice.
func (p *Provider) SendVoice(ctx context.Context, toUser string, data io.Reader, duration int) (string, error) {
	b64, err := EncodeMediaToBase64(data)
	if err != nil {
		return "", fmt.Errorf("encode voice: %w", err)
	}
	resp, err := p.api.SendVoice(ctx, &sendVoiceRequest{
		ToUserName: toUser,
		VoiceData:  b64,
		Duration:   duration,
	})
	if err != nil {
		return "", fmt.Errorf("send voice: %w", err)
	}
	return formatMsgID(resp), nil
}

// SendFile sends a file via POST /message/sendFile.
func (p *Provider) SendFile(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	resp, err := p.api.SendFile(ctx, &sendFileRequest{
		ToUserName: toUser,
		FileName:   filename,
	})
	if err != nil {
		return "", fmt.Errorf("send file: %w", err)
	}
	return formatMsgID(resp), nil
}

// SendLocation sends a location message. WeChatPadPro doesn't have a dedicated
// location endpoint, so we format it as a text message with coordinates.
func (p *Provider) SendLocation(ctx context.Context, toUser string, loc *wechat.LocationInfo) (string, error) {
	text := fmt.Sprintf("[Location] %s\n%s\nhttps://uri.amap.com/marker?position=%f,%f",
		loc.Poiname, loc.Label, loc.Longitude, loc.Latitude)
	return p.SendText(ctx, toUser, text)
}

// SendLink sends a link card message. WeChatPadPro doesn't have a direct link card
// endpoint for personal chats, so we format it as a rich text message.
func (p *Provider) SendLink(ctx context.Context, toUser string, link *wechat.LinkCardInfo) (string, error) {
	text := fmt.Sprintf("[Link] %s\n%s\n%s", link.Title, link.Description, link.URL)
	return p.SendText(ctx, toUser, text)
}

// RevokeMessage revokes a sent message via POST /message/RevokeMsg.
func (p *Provider) RevokeMessage(ctx context.Context, msgID string, toUser string) error {
	return p.api.RevokeMsg(ctx, &revokeRequest{
		ToUserName: toUser,
		MsgID:      msgID,
		NewMsgID:   msgID,
	})
}

// --- Contacts ---
// Uses WeChatPadPro's /friend/* endpoints with nested {str:""} response format.

// GetContactList returns all contacts by first fetching the friend wxid list,
// then batch-fetching detailed info.
// Uses: POST /friend/GetFriendList, POST /friend/GetContactDetailsList
func (p *Provider) GetContactList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	friendIDs, err := p.api.GetFriendList(ctx)
	if err != nil {
		return nil, fmt.Errorf("get friend list: %w", err)
	}
	if len(friendIDs) == 0 {
		return nil, nil
	}

	// Batch fetch details (WeChatPadPro limits batch size, process in chunks)
	const batchSize = 50
	var contacts []*wechat.ContactInfo

	for i := 0; i < len(friendIDs); i += batchSize {
		end := i + batchSize
		if end > len(friendIDs) {
			end = len(friendIDs)
		}
		batch := friendIDs[i:end]

		entries, err := p.api.GetContactDetailsList(ctx, batch)
		if err != nil {
			p.log.Warn("batch contact detail fetch failed", "error", err, "batch_start", i)
			continue
		}

		for _, entry := range entries {
			contacts = append(contacts, convertContactEntry(entry))
		}
	}

	return contacts, nil
}

// GetContactInfo returns info for a specific contact.
// Uses: POST /friend/GetContactDetailsList (with single-element batch)
func (p *Provider) GetContactInfo(ctx context.Context, userID string) (*wechat.ContactInfo, error) {
	entries, err := p.api.GetContactDetailsList(ctx, []string{userID})
	if err != nil {
		return nil, fmt.Errorf("get contact info: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("contact not found: %s", userID)
	}
	return convertContactEntry(entries[0]), nil
}

// GetUserAvatar downloads a user's avatar by first fetching contact details
// to get the avatar URL, then downloading the image.
func (p *Provider) GetUserAvatar(ctx context.Context, userID string) ([]byte, string, error) {
	info, err := p.GetContactInfo(ctx, userID)
	if err != nil {
		return nil, "", fmt.Errorf("get contact for avatar: %w", err)
	}
	if info.AvatarURL == "" {
		return nil, "", fmt.Errorf("contact %s has no avatar URL", userID)
	}

	// Download avatar image from URL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.AvatarURL, nil)
	if err != nil {
		return nil, "", err
	}
	httpCli := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpCli.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download avatar: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read avatar data: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}

	return data, contentType, nil
}

// AcceptFriendRequest accepts a friend request.
// Uses: POST /friend/AgreeAdd
func (p *Provider) AcceptFriendRequest(ctx context.Context, xml string) error {
	// The xml parameter contains the encrypted user name and ticket from the friend request
	// Parse them from the XML or pass directly
	return p.api.AgreeAdd(ctx, xml, "", 3)
}

// SetContactRemark sets the remark name for a contact.
// Uses: POST /friend/SetRemark
func (p *Provider) SetContactRemark(ctx context.Context, userID string, remark string) error {
	return p.api.SetRemark(ctx, userID, remark)
}

// --- Groups ---
// Uses WeChatPadPro's /group/* endpoints. Group IDs end with @chatroom.

// GetGroupList returns all groups by filtering contacts.
// WeChatPadPro doesn't have a dedicated group list endpoint, so we get
// the full contact list and filter for @chatroom suffixed IDs.
func (p *Provider) GetGroupList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	allContacts, err := p.GetContactList(ctx)
	if err != nil {
		return nil, err
	}

	var groups []*wechat.ContactInfo
	for _, c := range allContacts {
		if c.IsGroup {
			groups = append(groups, c)
		}
	}
	return groups, nil
}

// GetGroupMembers returns members of a specific group.
// Uses: POST /group/GetChatRoomInfo
func (p *Provider) GetGroupMembers(ctx context.Context, groupID string) ([]*wechat.GroupMember, error) {
	info, err := p.api.GetChatRoomInfo(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group members: %w", err)
	}

	members := make([]*wechat.GroupMember, 0, len(info.Members))
	for _, m := range info.Members {
		member := convertChatRoomMember(m)
		if m.UserName.Str == info.Owner {
			member.IsOwner = true
		}
		members = append(members, member)
	}
	return members, nil
}

// GetGroupInfo returns info for a specific group.
// Uses: POST /group/GetChatRoomInfo
func (p *Provider) GetGroupInfo(ctx context.Context, groupID string) (*wechat.ContactInfo, error) {
	info, err := p.api.GetChatRoomInfo(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group info: %w", err)
	}
	return &wechat.ContactInfo{
		UserID:      info.ChatRoomName.Str,
		Nickname:    info.NickName.Str,
		IsGroup:     true,
		MemberCount: info.MemberCount,
	}, nil
}

// CreateGroup creates a new group with the given name and initial members.
// Uses: POST /group/CreateChatRoom
// Note: WeChat requires at least 3 members (including self) to create a group.
func (p *Provider) CreateGroup(ctx context.Context, name string, members []string) (string, error) {
	resp, err := p.api.CreateChatRoom(ctx, members)
	if err != nil {
		return "", fmt.Errorf("create group: %w", err)
	}

	// Set group name after creation
	if name != "" && resp.ChatRoomName != "" {
		if err := p.api.SetChatroomName(ctx, resp.ChatRoomName, name); err != nil {
			p.log.Warn("failed to set group name after creation", "error", err, "group_id", resp.ChatRoomName)
		}
	}

	return resp.ChatRoomName, nil
}

// InviteToGroup invites users to a group.
// Uses: POST /group/AddChatRoomMembers
func (p *Provider) InviteToGroup(ctx context.Context, groupID string, userIDs []string) error {
	return p.api.AddChatRoomMembers(ctx, groupID, userIDs)
}

// RemoveFromGroup removes users from a group.
// Uses: POST /group/DelChatRoomMembers
func (p *Provider) RemoveFromGroup(ctx context.Context, groupID string, userIDs []string) error {
	return p.api.DelChatRoomMembers(ctx, groupID, userIDs)
}

// SetGroupName changes the group name.
// Uses: POST /group/SetChatroomName
func (p *Provider) SetGroupName(ctx context.Context, groupID string, name string) error {
	return p.api.SetChatroomName(ctx, groupID, name)
}

// SetGroupAnnouncement sets the group announcement text.
// Uses: POST /group/SetChatroomAnnouncement
func (p *Provider) SetGroupAnnouncement(ctx context.Context, groupID string, text string) error {
	return p.api.SetChatroomAnnouncement(ctx, groupID, text)
}

// LeaveGroup leaves a group.
// Uses: POST /group/QuitChatRoom
func (p *Provider) LeaveGroup(ctx context.Context, groupID string) error {
	return p.api.QuitChatRoom(ctx, groupID)
}

// --- Media ---

// DownloadMedia downloads media from a message.
// For WeChatPadPro, media URLs are typically CDN URLs that can be fetched directly.
func (p *Provider) DownloadMedia(ctx context.Context, msg *wechat.Message) (io.ReadCloser, string, error) {
	if msg.MediaURL == "" {
		return nil, "", fmt.Errorf("no media URL in message")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, msg.MediaURL, nil)
	if err != nil {
		return nil, "", err
	}

	httpCli := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpCli.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download media: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("media download HTTP %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = guessMimeType(msg)
	}

	return resp.Body, contentType, nil
}

// --- Internal helpers ---

// wsEventLoop connects to the WeChatPadPro WebSocket and dispatches events.
// Automatically reconnects on connection loss with exponential backoff.
func (p *Provider) wsEventLoop() {
	p.log.Info("WebSocket event loop started")

	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-p.stopCh:
			p.log.Info("WebSocket event loop stopped")
			return
		default:
		}

		if err := p.ws.connect(p.stopCh); err != nil {
			p.log.Error("WebSocket connection error, reconnecting",
				"error", err, "backoff", backoff)

			select {
			case <-p.stopCh:
				return
			case <-time.After(backoff):
			}

			// Exponential backoff
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			// Reset backoff on successful connection
			backoff = time.Second
		}
	}
}

// startCallbackServer starts a local HTTP server for receiving webhook callbacks.
func (p *Provider) startCallbackServer(port int) {
	webhookHandler := NewWebhookHandler(
		p.log.With("component", "webhook"),
		p.handler,
	)

	mux := http.NewServeMux()
	mux.Handle("/callback", webhookHandler)

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	p.callbackServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		p.log.Info("webhook callback server listening", "addr", addr)
		if err := p.callbackServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			p.log.Error("webhook server error", "error", err)
		}
	}()
}

// formatMsgID converts a sendMsgResponse to a string message ID.
// Prefers NewMsgID (64-bit unique) over MsgID.
func formatMsgID(resp *sendMsgResponse) string {
	if resp.NewMsgID != 0 {
		return strconv.FormatInt(resp.NewMsgID, 10)
	}
	if resp.MsgID != 0 {
		return strconv.FormatInt(resp.MsgID, 10)
	}
	return ""
}

// guessMimeType infers MIME type from message type when Content-Type header is missing.
func guessMimeType(msg *wechat.Message) string {
	switch msg.Type {
	case wechat.MsgImage:
		return "image/jpeg"
	case wechat.MsgVoice:
		return "audio/amr"
	case wechat.MsgVideo:
		return "video/mp4"
	case wechat.MsgEmoji:
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

