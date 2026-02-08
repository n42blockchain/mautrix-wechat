package pchook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func init() {
	wechat.Register("pchook", func() wechat.Provider {
		return &Provider{}
	})
}

// Provider implements wechat.Provider using PC Hook (WeChatFerry) via TCP RPC.
// This is a Tier 3 provider, recommended only for development and testing.
//
// Architecture:
//   - rpcclient.go: TCP JSON-RPC client for communicating with WeChatFerry
//   - message.go:   Message parsing and serialization helpers
//   - provider.go:  Provider interface implementation (this file)
//
// WeChatFerry must be running on a Windows host with WeChat injected.
// The bridge communicates via TCP to the RPC endpoint.
type Provider struct {
	mu         sync.RWMutex
	cfg        *wechat.ProviderConfig
	handler    wechat.MessageHandler
	loginState wechat.LoginState
	self       *wechat.ContactInfo
	running    bool
	log        *slog.Logger

	rpc    *RPCClient
	stopCh chan struct{}

	// tempDir for received media files
	tempDir string
}

// --- Lifecycle ---

func (p *Provider) Init(cfg *wechat.ProviderConfig, handler wechat.MessageHandler) error {
	p.cfg = cfg
	p.handler = handler
	p.log = slog.Default().With("provider", "pchook")

	endpoint := fmt.Sprintf("localhost:%d", cfg.RPCPort)
	if cfg.Extra != nil {
		if ep, ok := cfg.Extra["rpc_endpoint"]; ok {
			endpoint = ep
		}
	}

	p.rpc = NewRPCClient(endpoint, p.log.With("component", "rpc"))
	p.stopCh = make(chan struct{})

	// Set up temp directory for media
	p.tempDir = filepath.Join(cfg.DataDir, "pchook_media")
	if cfg.DataDir != "" {
		os.MkdirAll(p.tempDir, 0750)
	}

	return nil
}

func (p *Provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	// Connect to WeChatFerry RPC
	if err := p.rpc.Connect(ctx); err != nil {
		return fmt.Errorf("connect to WeChatFerry: %w", err)
	}

	// Set up notification handler for incoming messages
	p.rpc.SetNotificationHandler(p.handleNotification)

	// Verify connection with a ping
	if err := p.rpc.Ping(ctx); err != nil {
		p.rpc.Close()
		return fmt.Errorf("ping WeChatFerry: %w", err)
	}

	// Fetch login status
	p.checkLoginStatus(ctx)

	// Start heartbeat goroutine
	go p.heartbeatLoop()

	p.running = true
	p.log.Info("pchook provider started", "endpoint", p.cfg.RPCPort)
	return nil
}

func (p *Provider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	close(p.stopCh)
	p.running = false

	if p.rpc != nil {
		p.rpc.Close()
	}

	p.log.Info("pchook provider stopped")
	return nil
}

func (p *Provider) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// --- Identity ---

func (p *Provider) Name() string { return "pchook" }
func (p *Provider) Tier() int    { return 3 }

func (p *Provider) Capabilities() wechat.Capability {
	return wechat.Capability{
		SendText:       true,
		SendImage:      true,
		SendVideo:      false, // limited support
		SendVoice:      false, // limited support
		SendFile:       true,
		SendLocation:   false,
		SendLink:       false,
		SendMiniApp:    false,
		ReceiveMessage: true,
		GroupManage:    true,
		ContactManage:  true,
		MomentAccess:   false,
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
	// PC Hook login happens externally — the user must log in via the WeChat
	// desktop client. We just check the status.
	p.checkLoginStatus(ctx)

	if p.GetLoginState() != wechat.LoginStateLoggedIn {
		p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
			State: wechat.LoginStateError,
			Error: "WeChat PC client is not logged in; please log in via the desktop client first",
		})
		return fmt.Errorf("WeChat PC client is not logged in")
	}

	return nil
}

func (p *Provider) Logout(ctx context.Context) error {
	p.setLoginState(wechat.LoginStateLoggedOut)
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
	result, err := p.rpc.Call(ctx, "send_text", sendTextParams{
		ToUser:  toUser,
		Content: text,
	})
	if err != nil {
		return "", fmt.Errorf("send text: %w", err)
	}

	var msgID string
	if err := json.Unmarshal(result, &msgID); err != nil {
		return "", fmt.Errorf("parse send_text result: %w", err)
	}
	return msgID, nil
}

func (p *Provider) SendImage(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	// PC Hook requires a local file path — we write to temp and send the path
	localPath, err := p.saveToTemp(data, filename)
	if err != nil {
		return "", fmt.Errorf("save image: %w", err)
	}

	result, err := p.rpc.Call(ctx, "send_image", sendImageParams{
		ToUser: toUser,
		Path:   localPath,
	})
	if err != nil {
		return "", fmt.Errorf("send image: %w", err)
	}

	var msgID string
	if err := json.Unmarshal(result, &msgID); err != nil {
		return "", fmt.Errorf("parse send_image result: %w", err)
	}
	return msgID, nil
}

func (p *Provider) SendVideo(ctx context.Context, toUser string, data io.Reader, filename string, thumb io.Reader) (string, error) {
	return "", fmt.Errorf("pchook: video sending not supported")
}

func (p *Provider) SendVoice(ctx context.Context, toUser string, data io.Reader, duration int) (string, error) {
	return "", fmt.Errorf("pchook: voice sending not supported")
}

func (p *Provider) SendFile(ctx context.Context, toUser string, data io.Reader, filename string) (string, error) {
	localPath, err := p.saveToTemp(data, filename)
	if err != nil {
		return "", fmt.Errorf("save file: %w", err)
	}

	result, err := p.rpc.Call(ctx, "send_file", sendFileParams{
		ToUser: toUser,
		Path:   localPath,
	})
	if err != nil {
		return "", fmt.Errorf("send file: %w", err)
	}

	var msgID string
	if err := json.Unmarshal(result, &msgID); err != nil {
		return "", fmt.Errorf("parse send_file result: %w", err)
	}
	return msgID, nil
}

func (p *Provider) SendLocation(ctx context.Context, toUser string, loc *wechat.LocationInfo) (string, error) {
	return "", fmt.Errorf("pchook: location sending not supported")
}

func (p *Provider) SendLink(ctx context.Context, toUser string, link *wechat.LinkCardInfo) (string, error) {
	return "", fmt.Errorf("pchook: link card sending not supported")
}

func (p *Provider) RevokeMessage(ctx context.Context, msgID string, toUser string) error {
	_, err := p.rpc.Call(ctx, "revoke_msg", revokeParams{
		MsgID:  msgID,
		ToUser: toUser,
	})
	return err
}

// --- Contacts ---

func (p *Provider) GetContactList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	result, err := p.rpc.Call(ctx, "get_contacts", nil)
	if err != nil {
		return nil, err
	}

	var contacts []contactResult
	if err := json.Unmarshal(result, &contacts); err != nil {
		return nil, fmt.Errorf("parse contacts: %w", err)
	}

	out := make([]*wechat.ContactInfo, len(contacts))
	for i, c := range contacts {
		out[i] = c.toContactInfo()
	}
	return out, nil
}

func (p *Provider) GetContactInfo(ctx context.Context, userID string) (*wechat.ContactInfo, error) {
	result, err := p.rpc.Call(ctx, "get_contact_info", map[string]string{"wxid": userID})
	if err != nil {
		return nil, err
	}

	var c contactResult
	if err := json.Unmarshal(result, &c); err != nil {
		return nil, fmt.Errorf("parse contact: %w", err)
	}
	return c.toContactInfo(), nil
}

func (p *Provider) GetUserAvatar(ctx context.Context, userID string) ([]byte, string, error) {
	result, err := p.rpc.Call(ctx, "get_avatar", map[string]string{"wxid": userID})
	if err != nil {
		return nil, "", err
	}

	var avatarPath string
	if err := json.Unmarshal(result, &avatarPath); err != nil {
		return nil, "", fmt.Errorf("parse avatar path: %w", err)
	}

	// Read from the local path reported by WeChatFerry
	data, err := os.ReadFile(avatarPath)
	if err != nil {
		return nil, "", fmt.Errorf("read avatar file: %w", err)
	}

	return data, "image/jpeg", nil
}

func (p *Provider) AcceptFriendRequest(ctx context.Context, xml string) error {
	_, err := p.rpc.Call(ctx, "accept_friend", map[string]string{"xml": xml})
	return err
}

func (p *Provider) SetContactRemark(ctx context.Context, userID string, remark string) error {
	_, err := p.rpc.Call(ctx, "set_remark", map[string]interface{}{
		"wxid":   userID,
		"remark": remark,
	})
	return err
}

// --- Groups ---

func (p *Provider) GetGroupList(ctx context.Context) ([]*wechat.ContactInfo, error) {
	contacts, err := p.GetContactList(ctx)
	if err != nil {
		return nil, err
	}

	groups := make([]*wechat.ContactInfo, 0)
	for _, c := range contacts {
		if c.IsGroup {
			groups = append(groups, c)
		}
	}
	return groups, nil
}

func (p *Provider) GetGroupMembers(ctx context.Context, groupID string) ([]*wechat.GroupMember, error) {
	result, err := p.rpc.Call(ctx, "get_group_members", map[string]string{"group_id": groupID})
	if err != nil {
		return nil, err
	}

	var members []groupMemberResult
	if err := json.Unmarshal(result, &members); err != nil {
		return nil, fmt.Errorf("parse group members: %w", err)
	}

	out := make([]*wechat.GroupMember, len(members))
	for i, m := range members {
		out[i] = m.toGroupMember()
	}
	return out, nil
}

func (p *Provider) GetGroupInfo(ctx context.Context, groupID string) (*wechat.ContactInfo, error) {
	return p.GetContactInfo(ctx, groupID)
}

func (p *Provider) CreateGroup(ctx context.Context, name string, members []string) (string, error) {
	result, err := p.rpc.Call(ctx, "create_group", map[string]interface{}{
		"name":    name,
		"members": members,
	})
	if err != nil {
		return "", err
	}

	var groupID string
	if err := json.Unmarshal(result, &groupID); err != nil {
		return "", fmt.Errorf("parse create_group result: %w", err)
	}
	return groupID, nil
}

func (p *Provider) InviteToGroup(ctx context.Context, groupID string, userIDs []string) error {
	_, err := p.rpc.Call(ctx, "invite_to_group", map[string]interface{}{
		"group_id": groupID,
		"members":  userIDs,
	})
	return err
}

func (p *Provider) RemoveFromGroup(ctx context.Context, groupID string, userIDs []string) error {
	_, err := p.rpc.Call(ctx, "remove_from_group", map[string]interface{}{
		"group_id": groupID,
		"members":  userIDs,
	})
	return err
}

func (p *Provider) SetGroupName(ctx context.Context, groupID string, name string) error {
	_, err := p.rpc.Call(ctx, "set_group_name", map[string]interface{}{
		"group_id": groupID,
		"name":     name,
	})
	return err
}

func (p *Provider) SetGroupAnnouncement(ctx context.Context, groupID string, text string) error {
	_, err := p.rpc.Call(ctx, "set_group_announcement", map[string]interface{}{
		"group_id":     groupID,
		"announcement": text,
	})
	return err
}

func (p *Provider) LeaveGroup(ctx context.Context, groupID string) error {
	_, err := p.rpc.Call(ctx, "leave_group", map[string]string{"group_id": groupID})
	return err
}

// --- Media ---

func (p *Provider) DownloadMedia(ctx context.Context, msg *wechat.Message) (io.ReadCloser, string, error) {
	// Try media_path from Extra (local file on the Windows host)
	if mediaPath, ok := msg.Extra["media_path"]; ok && mediaPath != "" {
		data, err := os.Open(mediaPath)
		if err != nil {
			return nil, "", fmt.Errorf("open media file: %w", err)
		}
		mimeType := detectMimeType(mediaPath)
		return data, mimeType, nil
	}

	// Fallback: ask WeChatFerry to download the media
	result, err := p.rpc.Call(ctx, "download_media", map[string]string{"msg_id": msg.MsgID})
	if err != nil {
		return nil, "", fmt.Errorf("download media: %w", err)
	}

	var filePath string
	if err := json.Unmarshal(result, &filePath); err != nil {
		return nil, "", fmt.Errorf("parse media path: %w", err)
	}

	data, err := os.Open(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("open downloaded media: %w", err)
	}

	return data, detectMimeType(filePath), nil
}

// --- Internal ---

// handleNotification processes push notifications from WeChatFerry.
func (p *Provider) handleNotification(method string, params json.RawMessage) {
	ctx := context.Background()

	switch method {
	case "on_message":
		msg, err := parseRawMessage(params, p.log)
		if err != nil {
			p.log.Error("failed to parse notification message", "error", err)
			return
		}
		if p.handler != nil {
			if err := p.handler.OnMessage(ctx, msg); err != nil {
				p.log.Error("handler.OnMessage failed", "error", err)
			}
		}

	case "on_contact_update":
		var c contactResult
		if err := json.Unmarshal(params, &c); err != nil {
			p.log.Error("failed to parse contact update", "error", err)
			return
		}
		if p.handler != nil {
			p.handler.OnContactUpdate(ctx, c.toContactInfo())
		}

	case "on_revoke":
		var data struct {
			MsgID      string `json:"msg_id"`
			ReplaceTip string `json:"replace_tip"`
		}
		if err := json.Unmarshal(params, &data); err != nil {
			p.log.Error("failed to parse revoke notification", "error", err)
			return
		}
		if p.handler != nil {
			p.handler.OnRevoke(ctx, data.MsgID, data.ReplaceTip)
		}

	default:
		p.log.Debug("unknown notification method", "method", method)
	}
}

// checkLoginStatus queries WeChatFerry for the current login state.
func (p *Provider) checkLoginStatus(ctx context.Context) {
	result, err := p.rpc.Call(ctx, "get_self_info", nil)
	if err != nil {
		p.log.Warn("failed to check login status", "error", err)
		p.setLoginState(wechat.LoginStateError)
		return
	}

	var self contactResult
	if err := json.Unmarshal(result, &self); err != nil || self.UserID == "" {
		p.setLoginState(wechat.LoginStateLoggedOut)
		return
	}

	p.mu.Lock()
	p.loginState = wechat.LoginStateLoggedIn
	p.self = self.toContactInfo()
	p.mu.Unlock()

	p.log.Info("logged in as", "wxid", self.UserID, "nickname", self.Nickname)
}

// heartbeatLoop periodically pings WeChatFerry to detect disconnects.
func (p *Provider) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := p.rpc.Ping(ctx); err != nil {
				p.log.Warn("heartbeat failed", "error", err)
				p.setLoginState(wechat.LoginStateError)

				// Attempt to reconnect
				if connErr := p.rpc.Connect(ctx); connErr != nil {
					p.log.Error("reconnect failed", "error", connErr)
				} else {
					p.checkLoginStatus(ctx)
				}
			}
			cancel()
		}
	}
}

// setLoginState updates the login state.
func (p *Provider) setLoginState(state wechat.LoginState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loginState = state
}

// saveToTemp writes reader data to a temporary file and returns the path.
func (p *Provider) saveToTemp(r io.Reader, filename string) (string, error) {
	if p.tempDir == "" {
		p.tempDir = os.TempDir()
	}

	filePath := filepath.Join(p.tempDir, fmt.Sprintf("%d_%s", time.Now().UnixMilli(), filename))
	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("write temp file: %w", err)
	}

	return filePath, nil
}

// detectMimeType guesses the MIME type from a file extension.
func detectMimeType(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".silk", ".slk":
		return "audio/silk"
	case ".pdf":
		return "application/pdf"
	case ".doc", ".docx":
		return "application/msword"
	default:
		return "application/octet-stream"
	}
}

// DownloadMediaToBytes is a convenience method that reads all media into memory.
func (p *Provider) DownloadMediaToBytes(ctx context.Context, msg *wechat.Message) ([]byte, string, error) {
	reader, mimeType, err := p.DownloadMedia(ctx, msg)
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("read media: %w", err)
	}

	return data, mimeType, nil
}

// ensure Provider implements wechat.Provider at compile time
var _ wechat.Provider = (*Provider)(nil)

// GetRPCClient returns the underlying RPC client for testing/diagnostics.
func (p *Provider) GetRPCClient() *RPCClient {
	return p.rpc
}

// SendImageFromPath sends an image directly from a file path on the host.
func (p *Provider) SendImageFromPath(ctx context.Context, toUser, path string) (string, error) {
	result, err := p.rpc.Call(ctx, "send_image", sendImageParams{
		ToUser: toUser,
		Path:   path,
	})
	if err != nil {
		return "", fmt.Errorf("send image: %w", err)
	}

	var msgID string
	if err := json.Unmarshal(result, &msgID); err != nil {
		return "", fmt.Errorf("parse send_image result: %w", err)
	}
	return msgID, nil
}

// SendFileFromPath sends a file directly from a path on the host.
func (p *Provider) SendFileFromPath(ctx context.Context, toUser, path string) (string, error) {
	result, err := p.rpc.Call(ctx, "send_file", sendFileParams{
		ToUser: toUser,
		Path:   path,
	})
	if err != nil {
		return "", err
	}

	var msgID string
	if err := json.Unmarshal(result, &msgID); err != nil {
		return "", fmt.Errorf("parse send_file result: %w", err)
	}
	return msgID, nil
}

// saveToTempFromBytes writes bytes to a temporary file.
func (p *Provider) saveToTempFromBytes(data []byte, filename string) (string, error) {
	return p.saveToTemp(bytes.NewReader(data), filename)
}
