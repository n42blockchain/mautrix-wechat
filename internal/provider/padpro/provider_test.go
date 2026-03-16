package padpro

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

type asyncLoginHandler struct {
	mu     sync.Mutex
	events []*wechat.LoginEvent
	ch     chan *wechat.LoginEvent
}

func newAsyncLoginHandler() *asyncLoginHandler {
	return &asyncLoginHandler{ch: make(chan *wechat.LoginEvent, 8)}
}

func (h *asyncLoginHandler) OnMessage(context.Context, *wechat.Message) error { return nil }
func (h *asyncLoginHandler) OnContactUpdate(context.Context, *wechat.ContactInfo) error {
	return nil
}
func (h *asyncLoginHandler) OnGroupMemberUpdate(context.Context, string, []*wechat.GroupMember) error {
	return nil
}
func (h *asyncLoginHandler) OnPresence(context.Context, string, bool) error { return nil }
func (h *asyncLoginHandler) OnTyping(context.Context, string, string) error { return nil }
func (h *asyncLoginHandler) OnRevoke(context.Context, string, string) error { return nil }

func (h *asyncLoginHandler) OnLoginEvent(_ context.Context, evt *wechat.LoginEvent) error {
	copyEvt := *evt
	h.mu.Lock()
	h.events = append(h.events, &copyEvt)
	h.mu.Unlock()
	h.ch <- &copyEvt
	return nil
}

func (h *asyncLoginHandler) waitForState(state wechat.LoginState, timeout time.Duration) *wechat.LoginEvent {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case evt := <-h.ch:
			if evt.State == state {
				return evt
			}
		case <-timer.C:
			return nil
		}
	}
}

func TestProvider_Init_RequiresAPIEndpoint(t *testing.T) {
	p := &Provider{}

	err := p.Init(&wechat.ProviderConfig{
		APIToken: "token",
		Extra:    map[string]string{},
	}, nil)
	if err == nil {
		t.Fatal("expected missing api endpoint error")
	}
}

func TestProvider_Init_DerivesWSEndpoint(t *testing.T) {
	p := &Provider{}

	err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "https://padpro.example.com:1239",
		APIToken:    "token",
		Extra:       map[string]string{},
	}, nil)
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if p.ws == nil {
		t.Fatal("expected websocket client to be initialized")
	}
	if p.ws.endpoint != "wss://padpro.example.com:1239" {
		t.Fatalf("ws endpoint = %s", p.ws.endpoint)
	}
}

func TestProvider_Init_UsesAuthKeyFallback(t *testing.T) {
	p := &Provider{}

	err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://padpro.example.com:1239",
		Extra: map[string]string{
			"auth_key": "legacy-key",
		},
	}, nil)
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if p.api == nil {
		t.Fatal("expected API client to be initialized")
	}
}

func TestFormatMsgID_PrefersNewMsgID(t *testing.T) {
	id := formatMsgID(&sendMsgResponse{MsgID: 11, NewMsgID: 22})
	if id != "22" {
		t.Fatalf("id = %s, want 22", id)
	}
}

func TestGuessMimeType(t *testing.T) {
	if got := guessMimeType(&wechat.Message{Type: wechat.MsgImage}); got != "image/jpeg" {
		t.Fatalf("image mime = %s", got)
	}
	if got := guessMimeType(&wechat.Message{Type: wechat.MsgVoice}); got != "audio/amr" {
		t.Fatalf("voice mime = %s", got)
	}
	if got := guessMimeType(&wechat.Message{Type: wechat.MsgVideo}); got != "video/mp4" {
		t.Fatalf("video mime = %s", got)
	}
	if got := guessMimeType(&wechat.Message{Type: wechat.MsgText}); got != "application/octet-stream" {
		t.Fatalf("fallback mime = %s", got)
	}
}

func TestProvider_StartRecreatesStopChannel(t *testing.T) {
	p := &Provider{}

	err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
		APIToken:    "token",
		Extra:       map[string]string{},
	}, nil)
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer p.Stop()

	select {
	case <-p.stopCh:
		t.Fatal("stop channel should be recreated on restart")
	default:
	}
}

func TestProvider_StartRecreatesStopChannel_WithInboundLoop(t *testing.T) {
	p := &Provider{}

	err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
		APIToken:    "token",
		Extra:       map[string]string{},
	}, &testHandler{})
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := p.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	oldStopCh := p.stopCh

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	defer p.Stop()

	if p.stopCh == oldStopCh {
		t.Fatal("stop channel should be recreated on restart")
	}
}

func TestProvider_StartWithoutHandlerSkipsInboundServers(t *testing.T) {
	p := &Provider{}

	err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
		APIToken:    "token",
		Extra: map[string]string{
			"callback_port": "29353",
			"webhook_url":   "http://127.0.0.1/callback",
		},
	}, nil)
	if err != nil {
		t.Fatalf("Init error: %v", err)
	}

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Stop()

	if p.callbackServer != nil {
		t.Fatal("callback server should stay disabled when handler is nil")
	}
}

func TestProvider_Login_EmitsErrorEventOnQRCodeFailure(t *testing.T) {
	handler := newAsyncLoginHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusBadGateway)
	}))
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		APIToken:    "token",
		Extra:       map[string]string{},
	}, handler); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	if err := p.Login(context.Background()); err == nil || !strings.Contains(err.Error(), "request QR code") {
		t.Fatalf("Login error = %v", err)
	}

	evt := handler.waitForState(wechat.LoginStateError, time.Second)
	if evt == nil {
		t.Fatal("expected login error event")
	}
	if !strings.Contains(evt.Error, "request QR code") {
		t.Fatalf("unexpected error event: %+v", evt)
	}
	if p.GetLoginState() != wechat.LoginStateError {
		t.Fatalf("login state = %v", p.GetLoginState())
	}
}

func TestProvider_Login_DecodesQRCodeAndCompletesLogin(t *testing.T) {
	qrPNG := []byte("qr-png")
	handler := newAsyncLoginHandler()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/GetLoginQrCodeNew":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"qr_code":"` + base64.StdEncoding.EncodeToString(qrPNG) + `","qr_url":"https://example.com/scan","uuid":"uuid-1"}}`))
		case "/login/CheckLoginStatus":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"status":2,"user_name":"wxid_self","nick_name":"Bridge Bot","head_url":"https://example.com/avatar.jpg"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		APIToken:    "token",
		Extra:       map[string]string{},
	}, handler); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.Login(ctx); err != nil {
		t.Fatalf("Login error: %v", err)
	}

	qrEvt := handler.waitForState(wechat.LoginStateQRCode, time.Second)
	if qrEvt == nil {
		t.Fatal("expected QR event")
	}
	if qrEvt.QRURL != "https://example.com/scan" {
		t.Fatalf("QRURL = %q", qrEvt.QRURL)
	}
	if string(qrEvt.QRCode) != string(qrPNG) {
		t.Fatalf("QRCode = %q", string(qrEvt.QRCode))
	}

	loggedIn := handler.waitForState(wechat.LoginStateLoggedIn, 3*time.Second)
	if loggedIn == nil {
		t.Fatal("expected logged-in event")
	}
	if loggedIn.UserID != "wxid_self" || loggedIn.Name != "Bridge Bot" {
		t.Fatalf("unexpected logged-in event: %+v", loggedIn)
	}
	if p.GetLoginState() != wechat.LoginStateLoggedIn {
		t.Fatalf("login state = %v", p.GetLoginState())
	}
	if self := p.GetSelf(); self == nil || self.UserID != "wxid_self" || self.Nickname != "Bridge Bot" {
		t.Fatalf("unexpected self: %+v", self)
	}
}

func TestProvider_SendVideo_EncodesMediaAndThumbnail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/CdnUploadVideo" {
			t.Fatalf("path = %s", r.URL.Path)
		}

		var req sendVideoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.ToUserName != "wxid_target" {
			t.Fatalf("to_user_name = %s", req.ToUserName)
		}
		if req.VideoData != base64.StdEncoding.EncodeToString([]byte("video-bytes")) {
			t.Fatalf("video_data = %s", req.VideoData)
		}
		if req.ThumbData != base64.StdEncoding.EncodeToString([]byte("thumb-bytes")) {
			t.Fatalf("thumb_data = %s", req.ThumbData)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_id":11,"new_msg_id":22}}`))
	}))
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		APIToken:    "token",
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	msgID, err := p.SendVideo(context.Background(), "wxid_target", strings.NewReader("video-bytes"), "clip.mp4", strings.NewReader("thumb-bytes"))
	if err != nil {
		t.Fatalf("SendVideo error: %v", err)
	}
	if msgID != "22" {
		t.Fatalf("msgID = %s", msgID)
	}
}

func TestProvider_SendFile_EncodesData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/sendFile" {
			t.Fatalf("path = %s", r.URL.Path)
		}

		var req sendFileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.ToUserName != "wxid_target" {
			t.Fatalf("to_user_name = %s", req.ToUserName)
		}
		if req.FileName != "report.pdf" {
			t.Fatalf("file_name = %s", req.FileName)
		}
		if req.FileData != base64.StdEncoding.EncodeToString([]byte("file-bytes")) {
			t.Fatalf("file_data = %s", req.FileData)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"msg_id":13}}`))
	}))
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		APIToken:    "token",
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	msgID, err := p.SendFile(context.Background(), "wxid_target", strings.NewReader("file-bytes"), "report.pdf")
	if err != nil {
		t.Fatalf("SendFile error: %v", err)
	}
	if msgID != "13" {
		t.Fatalf("msgID = %s", msgID)
	}
}

func TestProvider_DownloadMedia_UsesEmbeddedBytes(t *testing.T) {
	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
		APIToken:    "token",
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	reader, mimeType, err := p.DownloadMedia(context.Background(), &wechat.Message{
		Type:      wechat.MsgVideo,
		MediaData: []byte("embedded-video"),
	})
	if err != nil {
		t.Fatalf("DownloadMedia error: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read media: %v", err)
	}
	_ = reader.Close()

	if string(data) != "embedded-video" || mimeType != "video/mp4" {
		t.Fatalf("unexpected embedded media: %q %s", string(data), mimeType)
	}
}

func TestProvider_GetUserAvatar_RejectsHTTPError(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/friend/GetContactDetailsList":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"contacts":[{"user_name":{"str":"wxid_avatar"},"nick_name":{"str":"Avatar"},"remark":{"str":""},"head_img_url":"` + serverURL + `/avatar/missing"}]}}`))
		case "/avatar/missing":
			http.Error(w, "missing", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = server.URL
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		APIToken:    "token",
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	if _, _, err := p.GetUserAvatar(context.Background(), "wxid_avatar"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("error = %v", err)
	}
}
