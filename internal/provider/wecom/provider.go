package wecom

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func init() {
	wechat.Register("wecom", func() wechat.Provider {
		return &Provider{}
	})
}

// Provider implements wechat.Provider using the WeCom (enterprise WeChat) official API.
// This is the Tier 1 (most stable) provider, but limited to enterprise scenarios.
//
// Architecture:
//   - client.go:   API client with automatic token management
//   - message.go:  Message sending (text/image/voice/video/file/link/revoke)
//   - contact.go:  Contact and group management
//   - callback.go: Webhook callback server for receiving messages
//   - crypto.go:   AES-CBC encryption/decryption for callback verification
type Provider struct {
	mu         sync.RWMutex
	cfg        *wechat.ProviderConfig
	handler    wechat.MessageHandler
	loginState wechat.LoginState
	self       *wechat.ContactInfo
	running    bool
	log        *slog.Logger

	client     *Client
	callbackSrv *CallbackServer
	crypto     *CallbackCrypto
}

// --- Lifecycle ---

func (p *Provider) Init(cfg *wechat.ProviderConfig, handler wechat.MessageHandler) error {
	p.cfg = cfg
	p.handler = handler
	p.log = slog.Default().With("provider", "wecom")

	if cfg.CorpID == "" || cfg.AppSecret == "" {
		return fmt.Errorf("wecom provider: corp_id and app_secret are required")
	}

	// Create API client
	p.client = NewClient(cfg.CorpID, cfg.AppSecret, cfg.AgentID, p.log)

	// Create callback crypto if token and AES key are provided
	if cfg.Token != "" && cfg.AESKey != "" {
		var err error
		p.crypto, err = NewCallbackCrypto(cfg.Token, cfg.AESKey, cfg.CorpID)
		if err != nil {
			return fmt.Errorf("init callback crypto: %w", err)
		}
	}

	return nil
}

func (p *Provider) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	// Verify token by fetching it
	token, err := p.client.GetToken(ctx)
	if err != nil {
		return fmt.Errorf("initial token fetch: %w", err)
	}
	_ = token

	// Start callback server if crypto is configured
	if p.crypto != nil && p.handler != nil {
		callbackPort := 29351 // default
		if p.cfg.Extra != nil {
			if portStr, ok := p.cfg.Extra["callback_port"]; ok {
				fmt.Sscanf(portStr, "%d", &callbackPort)
			}
		}

		p.callbackSrv = NewCallbackServer(
			p.log.With("component", "callback"),
			p.crypto,
			p.handler,
		)
		if err := p.callbackSrv.Start(callbackPort); err != nil {
			return fmt.Errorf("start callback server: %w", err)
		}
	}

	// Fetch self info
	p.fetchSelfInfo(ctx)

	p.running = true
	p.loginState = wechat.LoginStateLoggedIn
	p.log.Info("wecom provider started", "corp_id", p.cfg.CorpID, "agent_id", p.cfg.AgentID)

	return nil
}

func (p *Provider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	if p.callbackSrv != nil {
		if err := p.callbackSrv.Stop(); err != nil {
			p.log.Error("stop callback server", "error", err)
		}
	}

	p.running = false
	p.loginState = wechat.LoginStateLoggedOut
	p.log.Info("wecom provider stopped")
	return nil
}

func (p *Provider) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// --- Identity ---

func (p *Provider) Name() string { return "wecom" }
func (p *Provider) Tier() int   { return 1 }

func (p *Provider) Capabilities() wechat.Capability {
	return wechat.Capability{
		SendText:       true,
		SendImage:      true,
		SendVideo:      true,
		SendVoice:      true,
		SendFile:       true,
		SendLocation:   true, // via text with map link
		SendLink:       true,
		SendMiniApp:    false,
		ReceiveMessage: true,
		GroupManage:     true,
		ContactManage:  true,
		MomentAccess:   false,
		VoiceCall:      false,
		VideoCall:      false,
		Revoke:         true,
		Reaction:       false,
		ReadReceipt:    true,
		Typing:         false,
	}
}

// --- Authentication ---

// Login for WeCom is implicit — we use OAuth2 client credentials via access token.
// No QR code is needed for API access.
func (p *Provider) Login(ctx context.Context) error {
	_, err := p.client.GetToken(ctx)
	if err != nil {
		p.setLoginState(wechat.LoginStateError)
		if p.handler != nil {
			p.handler.OnLoginEvent(ctx, &wechat.LoginEvent{
				State: wechat.LoginStateError,
				Error: err.Error(),
			})
		}
		return err
	}

	p.setLoginState(wechat.LoginStateLoggedIn)
	p.fetchSelfInfo(ctx)

	if p.handler != nil {
		evt := &wechat.LoginEvent{
			State: wechat.LoginStateLoggedIn,
		}
		if p.self != nil {
			evt.UserID = p.self.UserID
			evt.Name = p.self.Nickname
		}
		p.handler.OnLoginEvent(ctx, evt)
	}

	return nil
}

func (p *Provider) Logout(ctx context.Context) error {
	p.setLoginState(wechat.LoginStateLoggedOut)
	p.client.invalidateToken()
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

// --- Helpers ---

func (p *Provider) setLoginState(state wechat.LoginState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loginState = state
}

// fetchSelfInfo retrieves the bot/agent info from WeCom.
func (p *Provider) fetchSelfInfo(ctx context.Context) {
	// WeCom doesn't have a "get self" API like personal WeChat.
	// We use the agent info as the bot identity.
	type agentInfoResp struct {
		APIResponse
		AgentID     int    `json:"agentid"`
		Name        string `json:"name"`
		SquareLogo  string `json:"square_logo_url"`
		Description string `json:"description"`
	}

	var resp agentInfoResp
	path := fmt.Sprintf("/cgi-bin/agent/get?agentid=%d", p.cfg.AgentID)
	if err := p.client.Get(ctx, path, &resp); err != nil {
		p.log.Warn("fetch agent info failed", "error", err)
		return
	}

	if resp.ErrCode == 0 {
		p.mu.Lock()
		p.self = &wechat.ContactInfo{
			UserID:    fmt.Sprintf("agent_%d", resp.AgentID),
			Nickname:  resp.Name,
			AvatarURL: resp.SquareLogo,
			Signature: resp.Description,
		}
		p.mu.Unlock()

		p.log.Info("agent info loaded", "name", resp.Name, "agent_id", resp.AgentID)
	}
}
