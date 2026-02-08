package ipad

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
	wechat.Register("ipad", func() wechat.Provider {
		return &Provider{}
	})
}

// Provider implements wechat.Provider using the iPad protocol (GeWeChat API).
// It integrates risk control for anti-ban protection and automatic reconnection.
type Provider struct {
	mu          sync.RWMutex
	cfg         *wechat.ProviderConfig
	handler     wechat.MessageHandler
	client      *http.Client
	loginState  wechat.LoginState
	self        *wechat.ContactInfo
	running     bool
	stopCh      chan struct{}
	callbackSrv *http.Server
	log         *slog.Logger

	// Sub-components
	riskControl     *RiskControl
	reconnector     *Reconnector
	callbackHandler *CallbackHandler
	voiceConverter  *VoiceConverter
}

// --- Lifecycle ---

func (p *Provider) Init(cfg *wechat.ProviderConfig, handler wechat.MessageHandler) error {
	p.cfg = cfg
	p.handler = handler
	p.client = &http.Client{Timeout: 30 * time.Second}
	p.stopCh = make(chan struct{})
	p.log = slog.Default().With("provider", "ipad")

	if cfg.APIEndpoint == "" {
		return fmt.Errorf("ipad provider: api_endpoint is required")
	}

	// Initialize risk control
	p.riskControl = NewRiskControl(p.buildRiskControlConfig())

	// Initialize reconnector
	p.reconnector = NewReconnector(ReconnectorConfig{
		Log:               p.log.With("component", "reconnector"),
		HeartbeatInterval: 30 * time.Second,
		MaxBackoff:        5 * time.Minute,
		BaseBackoff:       2 * time.Second,
		CheckAlive:        p.checkAlive,
		DoReconnect:       p.doReconnect,
		OnConnected: func() {
			p.log.Info("connection restored")
		},
		OnDisconnected: func() {
			p.log.Warn("connection lost, will attempt reconnect")
		},
	})

	// Initialize callback handler
	p.callbackHandler = NewCallbackHandler(
		p.log.With("component", "callback"),
		handler,
	)

	// Initialize voice converter (optional — graceful degradation if ffmpeg missing)
	vc, err := NewVoiceConverter("")
	if err != nil {
		p.log.Warn("voice converter not available, silk transcoding disabled", "error", err)
	} else {
		p.voiceConverter = vc
		p.log.Info("voice converter initialized",
			"can_encode", vc.CanEncode())
	}

	return nil
}

func (p *Provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	// Start callback HTTP server
	if p.cfg.CallbackURL != "" {
		go p.startCallbackServer()
	}

	// Start reconnection monitor
	p.reconnector.Start()

	p.running = true
	p.log.Info("ipad provider started",
		"api_endpoint", p.cfg.APIEndpoint,
		"callback_url", p.cfg.CallbackURL)

	return nil
}

func (p *Provider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	close(p.stopCh)

	// Stop reconnector
	p.reconnector.Stop()

	// Shutdown callback server
	if p.callbackSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.callbackSrv.Shutdown(ctx)
	}

	p.running = false
	p.log.Info("ipad provider stopped")
	return nil
}

func (p *Provider) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// --- Identity ---

func (p *Provider) Name() string { return "ipad" }
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

	// Request QR code from GeWeChat API
	resp, err := p.apiCall(ctx, "/login/qrcode", nil)
	if err != nil {
		p.setLoginState(wechat.LoginStateError)
		p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
			State: wechat.LoginStateError,
			Error: fmt.Sprintf("failed to get QR code: %v", err),
		})
		return err
	}

	qrURL, _ := resp["qr_url"].(string)
	qrBase64, _ := resp["qr_base64"].(string)

	p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
		State:  wechat.LoginStateQRCode,
		QRURL:  qrURL,
		QRCode: []byte(qrBase64),
	})

	// Poll for login status
	go p.pollLoginStatus(ctx)

	return nil
}

func (p *Provider) Logout(ctx context.Context) error {
	_, err := p.apiCall(ctx, "/login/logout", nil)
	p.setLoginState(wechat.LoginStateLoggedOut)
	p.self = nil
	p.reconnector.MarkDisconnected()
	return err
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

// --- Messaging (with risk control) ---

func (p *Provider) SendText(ctx context.Context, toUser string, text string) (string, error) {
	if err := p.checkMessageRisk(); err != nil {
		return "", err
	}

	resp, err := p.apiCall(ctx, "/message/send/text", map[string]interface{}{
		"to_user": toUser,
		"content": text,
	})
	if err != nil {
		return "", err
	}
	msgID, _ := resp["msg_id"].(string)
	return msgID, nil
}

func (p *Provider) SendImage(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	if err := p.checkMessageRisk(); err != nil {
		return "", err
	}

	body, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read image data: %w", err)
	}
	resp, err := p.apiCall(ctx, "/message/send/image", map[string]interface{}{
		"to_user":  toUser,
		"data":     body,
		"filename": filename,
	})
	if err != nil {
		return "", err
	}
	msgID, _ := resp["msg_id"].(string)
	return msgID, nil
}

func (p *Provider) SendVideo(ctx context.Context, toUser string, data io.Reader, filename string, thumb io.Reader) (string, error) {
	if err := p.checkMessageRisk(); err != nil {
		return "", err
	}

	body, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read video data: %w", err)
	}
	payload := map[string]interface{}{
		"to_user":  toUser,
		"data":     body,
		"filename": filename,
	}
	if thumb != nil {
		thumbData, _ := io.ReadAll(thumb)
		payload["thumbnail"] = thumbData
	}
	resp, err := p.apiCall(ctx, "/message/send/video", payload)
	if err != nil {
		return "", err
	}
	msgID, _ := resp["msg_id"].(string)
	return msgID, nil
}

func (p *Provider) SendVoice(ctx context.Context, toUser string, data io.Reader, duration int) (string, error) {
	if err := p.checkMessageRisk(); err != nil {
		return "", err
	}

	body, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read voice data: %w", err)
	}

	// Convert ogg/opus to silk if voice converter is available
	if p.voiceConverter != nil && p.voiceConverter.CanEncode() {
		silkData, convErr := p.voiceConverter.OggToSilk(bytes.NewReader(body))
		if convErr == nil {
			body = silkData
		} else {
			p.log.Warn("voice conversion failed, sending original", "error", convErr)
		}
	}

	resp, err := p.apiCall(ctx, "/message/send/voice", map[string]interface{}{
		"to_user":  toUser,
		"data":     body,
		"duration": duration,
	})
	if err != nil {
		return "", err
	}
	msgID, _ := resp["msg_id"].(string)
	return msgID, nil
}

func (p *Provider) SendFile(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	if err := p.checkMessageRisk(); err != nil {
		return "", err
	}

	body, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read file data: %w", err)
	}
	resp, err := p.apiCall(ctx, "/message/send/file", map[string]interface{}{
		"to_user":  toUser,
		"data":     body,
		"filename": filename,
	})
	if err != nil {
		return "", err
	}
	msgID, _ := resp["msg_id"].(string)
	return msgID, nil
}

func (p *Provider) SendLocation(ctx context.Context, toUser string, loc *wechat.LocationInfo) (string, error) {
	if err := p.checkMessageRisk(); err != nil {
		return "", err
	}

	resp, err := p.apiCall(ctx, "/message/send/location", map[string]interface{}{
		"to_user":   toUser,
		"latitude":  loc.Latitude,
		"longitude": loc.Longitude,
		"label":     loc.Label,
		"poiname":   loc.Poiname,
	})
	if err != nil {
		return "", err
	}
	msgID, _ := resp["msg_id"].(string)
	return msgID, nil
}

func (p *Provider) SendLink(ctx context.Context, toUser string, link *wechat.LinkCardInfo) (string, error) {
	if err := p.checkMessageRisk(); err != nil {
		return "", err
	}

	resp, err := p.apiCall(ctx, "/message/send/link", map[string]interface{}{
		"to_user":     toUser,
		"title":       link.Title,
		"description": link.Description,
		"url":         link.URL,
		"thumb_url":   link.ThumbURL,
	})
	if err != nil {
		return "", err
	}
	msgID, _ := resp["msg_id"].(string)
	return msgID, nil
}

func (p *Provider) RevokeMessage(ctx context.Context, msgID string, toUser string) error {
	_, err := p.apiCall(ctx, "/message/revoke", map[string]interface{}{
		"msg_id":  msgID,
		"to_user": toUser,
	})
	return err
}

// --- Contacts ---

func (p *Provider) GetContactList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	resp, err := p.apiCall(ctx, "/contact/list", nil)
	if err != nil {
		return nil, err
	}
	return parseContactList(resp)
}

func (p *Provider) GetContactInfo(ctx context.Context, userID string) (*wechat.ContactInfo, error) {
	resp, err := p.apiCall(ctx, "/contact/info", map[string]interface{}{
		"user_id": userID,
	})
	if err != nil {
		return nil, err
	}
	return parseContact(resp)
}

func (p *Provider) GetUserAvatar(ctx context.Context, userID string) ([]byte, string, error) {
	resp, err := p.apiCall(ctx, "/contact/avatar", map[string]interface{}{
		"user_id": userID,
	})
	if err != nil {
		return nil, "", err
	}
	avatarURL, _ := resp["avatar_url"].(string)
	if avatarURL == "" {
		return nil, "", fmt.Errorf("no avatar URL")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, avatarURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create avatar request: %w", err)
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("download avatar: %w", err)
	}
	defer httpResp.Body.Close()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read avatar: %w", err)
	}

	mimeType := httpResp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	return data, mimeType, nil
}

func (p *Provider) AcceptFriendRequest(ctx context.Context, xml string) error {
	if !p.riskControl.CheckFriendOperation() {
		return fmt.Errorf("friend operation rate limit exceeded")
	}

	_, err := p.apiCall(ctx, "/contact/accept", map[string]interface{}{
		"xml": xml,
	})
	return err
}

func (p *Provider) SetContactRemark(ctx context.Context, userID string, remark string) error {
	_, err := p.apiCall(ctx, "/contact/remark", map[string]interface{}{
		"user_id": userID,
		"remark":  remark,
	})
	return err
}

// --- Groups ---

func (p *Provider) GetGroupList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	resp, err := p.apiCall(ctx, "/group/list", nil)
	if err != nil {
		return nil, err
	}
	return parseContactList(resp)
}

func (p *Provider) GetGroupMembers(ctx context.Context, groupID string) ([]*wechat.GroupMember, error) {
	resp, err := p.apiCall(ctx, "/group/members", map[string]interface{}{
		"group_id": groupID,
	})
	if err != nil {
		return nil, err
	}
	return parseGroupMembers(resp)
}

func (p *Provider) GetGroupInfo(ctx context.Context, groupID string) (*wechat.ContactInfo, error) {
	resp, err := p.apiCall(ctx, "/group/info", map[string]interface{}{
		"group_id": groupID,
	})
	if err != nil {
		return nil, err
	}
	return parseContact(resp)
}

func (p *Provider) CreateGroup(ctx context.Context, name string, members []string) (string, error) {
	if !p.riskControl.CheckGroupOperation() {
		return "", fmt.Errorf("group operation rate limit exceeded")
	}

	resp, err := p.apiCall(ctx, "/group/create", map[string]interface{}{
		"name":    name,
		"members": members,
	})
	if err != nil {
		return "", err
	}
	groupID, _ := resp["group_id"].(string)
	return groupID, nil
}

func (p *Provider) InviteToGroup(ctx context.Context, groupID string, userIDs []string) error {
	if !p.riskControl.CheckGroupOperation() {
		return fmt.Errorf("group operation rate limit exceeded")
	}

	_, err := p.apiCall(ctx, "/group/invite", map[string]interface{}{
		"group_id": groupID,
		"user_ids": userIDs,
	})
	return err
}

func (p *Provider) RemoveFromGroup(ctx context.Context, groupID string, userIDs []string) error {
	_, err := p.apiCall(ctx, "/group/remove", map[string]interface{}{
		"group_id": groupID,
		"user_ids": userIDs,
	})
	return err
}

func (p *Provider) SetGroupName(ctx context.Context, groupID string, name string) error {
	_, err := p.apiCall(ctx, "/group/name", map[string]interface{}{
		"group_id": groupID,
		"name":     name,
	})
	return err
}

func (p *Provider) SetGroupAnnouncement(ctx context.Context, groupID string, text string) error {
	_, err := p.apiCall(ctx, "/group/announcement", map[string]interface{}{
		"group_id":     groupID,
		"announcement": text,
	})
	return err
}

func (p *Provider) LeaveGroup(ctx context.Context, groupID string) error {
	_, err := p.apiCall(ctx, "/group/leave", map[string]interface{}{
		"group_id": groupID,
	})
	return err
}

// --- Media ---

func (p *Provider) DownloadMedia(ctx context.Context, msg *wechat.Message) (io.ReadCloser, string, error) {
	if msg.MediaURL != "" {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, msg.MediaURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("create media request: %w", err)
		}
		resp, err := p.client.Do(httpReq)
		if err != nil {
			return nil, "", fmt.Errorf("download media: %w", err)
		}
		mimeType := resp.Header.Get("Content-Type")
		return resp.Body, mimeType, nil
	}

	if len(msg.MediaData) > 0 {
		return io.NopCloser(bytes.NewReader(msg.MediaData)), "application/octet-stream", nil
	}

	return nil, "", fmt.Errorf("no media available")
}

// --- Internal: Risk Control ---

// checkMessageRisk validates the message against risk control and applies required delay.
func (p *Provider) checkMessageRisk() error {
	delay, allowed := p.riskControl.CheckMessage()
	if !allowed {
		if p.riskControl.IsInSilencePeriod() {
			return fmt.Errorf("account is in silence period (new account protection)")
		}
		return fmt.Errorf("daily message limit reached (%d remaining)", p.riskControl.RemainingMessages())
	}

	if delay > 0 {
		p.log.Debug("risk control delay", "delay", delay)
		time.Sleep(delay)
	}

	return nil
}

// buildRiskControlConfig creates a RiskControlConfig from provider configuration.
func (p *Provider) buildRiskControlConfig() RiskControlConfig {
	cfg := RiskControlConfig{}

	if p.cfg.Extra != nil {
		if v, ok := p.cfg.Extra["max_messages_per_day"]; ok {
			fmt.Sscanf(v, "%d", &cfg.MaxMessagesPerDay)
		}
		if v, ok := p.cfg.Extra["max_groups_per_day"]; ok {
			fmt.Sscanf(v, "%d", &cfg.MaxGroupsPerDay)
		}
		if v, ok := p.cfg.Extra["max_friends_per_day"]; ok {
			fmt.Sscanf(v, "%d", &cfg.MaxFriendsPerDay)
		}
		if v, ok := p.cfg.Extra["message_interval_ms"]; ok {
			fmt.Sscanf(v, "%d", &cfg.MessageIntervalMs)
		}
		if v, ok := p.cfg.Extra["new_account_silence_days"]; ok {
			fmt.Sscanf(v, "%d", &cfg.NewAccountSilenceDays)
		}
		if _, ok := p.cfg.Extra["random_delay"]; ok {
			cfg.RandomDelay = true
		}
	}

	return cfg
}

// --- Internal: Reconnection ---

// checkAlive verifies the connection to GeWeChat is alive by pinging the API.
func (p *Provider) checkAlive(ctx context.Context) bool {
	if p.GetLoginState() != wechat.LoginStateLoggedIn {
		return false
	}

	resp, err := p.apiCall(ctx, "/login/status", nil)
	if err != nil {
		p.log.Warn("health check failed", "error", err)
		return false
	}

	status, _ := resp["status"].(float64)
	return int(status) == 3 // logged in
}

// doReconnect attempts to re-establish the connection after a disconnect.
func (p *Provider) doReconnect(ctx context.Context) error {
	p.log.Info("attempting to reconnect to GeWeChat")

	// Try to restore session via API
	resp, err := p.apiCall(ctx, "/login/reconnect", nil)
	if err != nil {
		return fmt.Errorf("reconnect api call: %w", err)
	}

	status, _ := resp["status"].(float64)
	if int(status) != 3 {
		return fmt.Errorf("reconnect returned status %d, expected 3", int(status))
	}

	// Update self info if available
	if userID, ok := resp["user_id"].(string); ok && userID != "" {
		p.mu.Lock()
		p.loginState = wechat.LoginStateLoggedIn
		if p.self == nil {
			p.self = &wechat.ContactInfo{}
		}
		p.self.UserID = userID
		if name, ok := resp["nickname"].(string); ok {
			p.self.Nickname = name
		}
		p.mu.Unlock()
	}

	p.log.Info("reconnection successful")
	return nil
}

// --- Internal: API & Callback ---

// apiCall makes an HTTP API call to the GeWeChat service.
func (p *Provider) apiCall(ctx context.Context, path string, payload map[string]interface{}) (map[string]interface{}, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		body = bytes.NewReader(data)
	}

	url := p.cfg.APIEndpoint + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api call %s: %w", path, err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response from %s: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		errMsg, _ := result["error"].(string)
		return nil, fmt.Errorf("api %s returned %d: %s", path, resp.StatusCode, errMsg)
	}

	return result, nil
}

// pollLoginStatus polls the GeWeChat API for login status changes.
func (p *Provider) pollLoginStatus(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			state := p.GetLoginState()
			if state != wechat.LoginStateQRCode && state != wechat.LoginStateConfirming {
				return
			}

			resp, err := p.apiCall(ctx, "/login/status", nil)
			if err != nil {
				p.log.Error("poll login status failed", "error", err)
				continue
			}

			statusCode, _ := resp["status"].(float64)
			switch int(statusCode) {
			case 0: // waiting
				// no change
			case 1: // scanned
				p.setLoginState(wechat.LoginStateConfirming)
				p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
					State: wechat.LoginStateConfirming,
				})
			case 2: // confirmed / logged in
				userID, _ := resp["user_id"].(string)
				name, _ := resp["nickname"].(string)
				avatar, _ := resp["avatar"].(string)

				p.mu.Lock()
				p.loginState = wechat.LoginStateLoggedIn
				p.self = &wechat.ContactInfo{
					UserID:    userID,
					Nickname:  name,
					AvatarURL: avatar,
				}
				p.mu.Unlock()

				// Mark reconnector as connected
				p.reconnector.MarkConnected()

				p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
					State:  wechat.LoginStateLoggedIn,
					UserID: userID,
					Name:   name,
					Avatar: avatar,
				})
				return
			case -1: // error / expired
				errMsg, _ := resp["error"].(string)
				p.setLoginState(wechat.LoginStateError)
				p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
					State: wechat.LoginStateError,
					Error: errMsg,
				})
				return
			}
		}
	}
}

// startCallbackServer starts an HTTP server to receive GeWeChat callbacks.
func (p *Provider) startCallbackServer() {
	mux := http.NewServeMux()
	mux.Handle("POST /callback", p.callbackHandler)

	port := "29352"
	if p.cfg.Extra != nil {
		if pp, ok := p.cfg.Extra["callback_port"]; ok {
			port = pp
		}
	}

	p.callbackSrv = &http.Server{
		Addr:         "0.0.0.0:" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	p.log.Info("callback server listening", "port", port)
	if err := p.callbackSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		p.log.Error("callback server error", "error", err)
	}
}

func (p *Provider) setLoginState(state wechat.LoginState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loginState = state
}

// --- Stats ---

// GetRiskControlStats returns current risk control statistics.
func (p *Provider) GetRiskControlStats() (messages, groups, friends int) {
	return p.riskControl.GetStats()
}

// GetReconnectStats returns reconnection statistics.
func (p *Provider) GetReconnectStats() ReconnectStats {
	return p.reconnector.Stats()
}

// --- Parsing helpers ---

func parseContact(data map[string]interface{}) (*wechat.ContactInfo, error) {
	c := &wechat.ContactInfo{}
	c.UserID, _ = data["user_id"].(string)
	c.Alias, _ = data["alias"].(string)
	c.Nickname, _ = data["nickname"].(string)
	c.Remark, _ = data["remark"].(string)
	c.AvatarURL, _ = data["avatar_url"].(string)
	if g, ok := data["gender"].(float64); ok {
		c.Gender = int(g)
	}
	c.Province, _ = data["province"].(string)
	c.City, _ = data["city"].(string)
	c.Signature, _ = data["signature"].(string)
	if ig, ok := data["is_group"].(bool); ok {
		c.IsGroup = ig
	}
	if mc, ok := data["member_count"].(float64); ok {
		c.MemberCount = int(mc)
	}
	return c, nil
}

func parseContactList(data map[string]interface{}) ([]*wechat.ContactInfo, error) {
	list, ok := data["contacts"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid contacts response")
	}

	contacts := make([]*wechat.ContactInfo, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		c, err := parseContact(m)
		if err == nil {
			contacts = append(contacts, c)
		}
	}
	return contacts, nil
}

func parseGroupMembers(data map[string]interface{}) ([]*wechat.GroupMember, error) {
	list, ok := data["members"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid members response")
	}

	members := make([]*wechat.GroupMember, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		gm := &wechat.GroupMember{}
		gm.UserID, _ = m["user_id"].(string)
		gm.Nickname, _ = m["nickname"].(string)
		gm.DisplayName, _ = m["display_name"].(string)
		gm.AvatarURL, _ = m["avatar_url"].(string)
		if ia, ok := m["is_admin"].(bool); ok {
			gm.IsAdmin = ia
		}
		if isOwner, ok := m["is_owner"].(bool); ok {
			gm.IsOwner = isOwner
		}
		members = append(members, gm)
	}
	return members, nil
}
