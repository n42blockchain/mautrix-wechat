package ipad

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

type errReader struct{}
type roundTripFunc func(*http.Request) (*http.Response, error)

func (errReader) Read(_ []byte) (int, error) {
	return 0, errors.New("boom")
}

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type loginCaptureHandler struct {
	mu     sync.Mutex
	logins []*wechat.LoginEvent
	ch     chan *wechat.LoginEvent
}

func newLoginCaptureHandler() *loginCaptureHandler {
	return &loginCaptureHandler{ch: make(chan *wechat.LoginEvent, 8)}
}

func (h *loginCaptureHandler) OnMessage(context.Context, *wechat.Message) error { return nil }
func (h *loginCaptureHandler) OnContactUpdate(context.Context, *wechat.ContactInfo) error {
	return nil
}
func (h *loginCaptureHandler) OnGroupMemberUpdate(context.Context, string, []*wechat.GroupMember) error {
	return nil
}
func (h *loginCaptureHandler) OnPresence(context.Context, string, bool) error { return nil }
func (h *loginCaptureHandler) OnTyping(context.Context, string, string) error { return nil }
func (h *loginCaptureHandler) OnRevoke(context.Context, string, string) error { return nil }

func (h *loginCaptureHandler) OnLoginEvent(_ context.Context, evt *wechat.LoginEvent) error {
	copyEvt := *evt
	h.mu.Lock()
	h.logins = append(h.logins, &copyEvt)
	h.mu.Unlock()
	h.ch <- &copyEvt
	return nil
}

func (h *loginCaptureHandler) waitForState(state wechat.LoginState, timeout time.Duration) *wechat.LoginEvent {
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

func TestProvider_StartRecreatesStopChannel(t *testing.T) {
	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
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

func TestProvider_StartWithoutHandlerSkipsCallbackServer(t *testing.T) {
	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
		CallbackURL: "http://127.0.0.1/callback",
		Extra: map[string]string{
			"callback_port": "29352",
		},
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Stop()

	if p.callbackSrv != nil {
		t.Fatal("callback server should stay disabled when handler is nil")
	}
}

func TestProvider_BuildRiskControlConfig_ParsesRandomDelayBoolean(t *testing.T) {
	p := &Provider{cfg: &wechat.ProviderConfig{Extra: map[string]string{"random_delay": "false"}}}
	cfg := p.buildRiskControlConfig()
	if cfg.RandomDelay {
		t.Fatal("random_delay=false should stay disabled")
	}

	p.cfg = &wechat.ProviderConfig{Extra: map[string]string{"random_delay": "true"}}
	cfg = p.buildRiskControlConfig()
	if !cfg.RandomDelay {
		t.Fatal("random_delay=true should enable random delay")
	}
}

func TestProvider_Login_DecodesQRCodeAndCompletesLogin(t *testing.T) {
	qrPNG := []byte("qr-png")
	handler := newLoginCaptureHandler()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/qrcode":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"qr_url":"https://example.com/scan","qr_base64":"` + base64.StdEncoding.EncodeToString(qrPNG) + `"}`))
		case "/login/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":2,"user_id":"wxid_self","nickname":"Bridge Bot","avatar":"https://example.com/avatar.jpg"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		Extra: map[string]string{
			"message_interval_ms": "1",
			"random_delay":        "false",
		},
	}, handler); err != nil {
		t.Fatalf("init: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.Login(ctx); err != nil {
		t.Fatalf("Login error: %v", err)
	}

	qrEvent := handler.waitForState(wechat.LoginStateQRCode, time.Second)
	if qrEvent == nil {
		t.Fatal("expected QR code login event")
	}
	if qrEvent.QRURL != "https://example.com/scan" {
		t.Fatalf("QRURL = %q", qrEvent.QRURL)
	}
	if string(qrEvent.QRCode) != string(qrPNG) {
		t.Fatalf("QRCode = %q", string(qrEvent.QRCode))
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

func TestProvider_SendImageAndFile_IncludePayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch r.URL.Path {
		case "/message/send/image":
			if req["to_user"] != "wxid_target" {
				t.Fatalf("image to_user = %v", req["to_user"])
			}
			if req["filename"] != "image.png" {
				t.Fatalf("image filename = %v", req["filename"])
			}
			if req["data"] != base64.StdEncoding.EncodeToString([]byte("image-bytes")) {
				t.Fatalf("image data = %v", req["data"])
			}
		case "/message/send/file":
			if req["to_user"] != "wxid_target" {
				t.Fatalf("file to_user = %v", req["to_user"])
			}
			if req["filename"] != "report.pdf" {
				t.Fatalf("file filename = %v", req["filename"])
			}
			if req["data"] != base64.StdEncoding.EncodeToString([]byte("file-bytes")) {
				t.Fatalf("file data = %v", req["data"])
			}
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"msg_id":"msg-1"}`))
	}))
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	if _, err := p.SendImage(context.Background(), "wxid_target", strings.NewReader("image-bytes"), "image.png"); err != nil {
		t.Fatalf("SendImage error: %v", err)
	}
	if _, err := p.SendFile(context.Background(), "wxid_target", strings.NewReader("file-bytes"), "report.pdf"); err != nil {
		t.Fatalf("SendFile error: %v", err)
	}
}

func TestProvider_SendVideo_ReadThumbnailError(t *testing.T) {
	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	_, err := p.SendVideo(context.Background(), "wxid_target", strings.NewReader("video-bytes"), "clip.mp4", errReader{})
	if err == nil || !strings.Contains(err.Error(), "read video thumbnail") {
		t.Fatalf("error = %v", err)
	}
}

func TestProvider_SendVoice_IncludesDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		if r.URL.Path != "/message/send/voice" {
			t.Fatalf("path = %s", r.URL.Path)
		}

		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["to_user"] != "wxid_target" {
			t.Fatalf("to_user = %v", req["to_user"])
		}
		if req["duration"] != float64(7) {
			t.Fatalf("duration = %v", req["duration"])
		}
		if req["data"] != base64.StdEncoding.EncodeToString([]byte("voice-bytes")) {
			t.Fatalf("voice data = %v", req["data"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"msg_id":"msg-voice"}`))
	}))
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	if _, err := p.SendVoice(context.Background(), "wxid_target", strings.NewReader("voice-bytes"), 7); err != nil {
		t.Fatalf("SendVoice error: %v", err)
	}
}

func TestProvider_DownloadMedia_UsesStatusAndMimeFallback(t *testing.T) {
	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	p.client = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/ok":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("media-bytes")),
				}, nil
			case "/typed":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
					Body:       io.NopCloser(strings.NewReader("typed-bytes")),
				}, nil
			case "/missing":
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("missing")),
				}, nil
			default:
				return nil, errors.New("unexpected path")
			}
		}),
	}

	reader, mimeType, err := p.DownloadMedia(context.Background(), &wechat.Message{MediaURL: "http://media.local/ok"})
	if err != nil {
		t.Fatalf("DownloadMedia error: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read media: %v", err)
	}
	_ = reader.Close()
	if string(data) != "media-bytes" || mimeType != "application/octet-stream" {
		t.Fatalf("unexpected media fallback: %q %s", string(data), mimeType)
	}

	reader, mimeType, err = p.DownloadMedia(context.Background(), &wechat.Message{MediaURL: "http://media.local/typed"})
	if err != nil {
		t.Fatalf("typed DownloadMedia error: %v", err)
	}
	data, err = io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read typed media: %v", err)
	}
	_ = reader.Close()
	if string(data) != "typed-bytes" || mimeType != "image/jpeg" {
		t.Fatalf("unexpected typed media: %q %s", string(data), mimeType)
	}

	if _, _, err := p.DownloadMedia(context.Background(), &wechat.Message{MediaURL: "http://media.local/missing"}); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("error = %v", err)
	}
}

func TestProvider_DownloadMedia_UsesEmbeddedBytes(t *testing.T) {
	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: "http://127.0.0.1:1",
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	reader, mimeType, err := p.DownloadMedia(context.Background(), &wechat.Message{MediaData: []byte("embedded")})
	if err != nil {
		t.Fatalf("DownloadMedia error: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read embedded media: %v", err)
	}
	_ = reader.Close()

	if string(data) != "embedded" || mimeType != "application/octet-stream" {
		t.Fatalf("unexpected embedded media: %q %s", string(data), mimeType)
	}
}

func TestProvider_GetUserAvatar_RejectsHTTPError(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/contact/avatar":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"avatar_url":"` + serverURL + `/avatar/missing"}`))
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
		Extra:       map[string]string{},
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	if _, _, err := p.GetUserAvatar(context.Background(), "wxid_avatar"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("error = %v", err)
	}
}

func TestProvider_APIBackedOperationsAndReconnect(t *testing.T) {
	var mu sync.Mutex
	payloads := make(map[string][]map[string]interface{})
	var serverURL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/avatar.jpg" && r.Header.Get("Authorization") != "Bearer api-token" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}

		if r.Method == http.MethodPost && r.URL.Path != "/avatar.jpg" {
			defer r.Body.Close()
			if r.ContentLength != 0 {
				var payload map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode %s payload: %v", r.URL.Path, err)
				}
				mu.Lock()
				payloads[r.URL.Path] = append(payloads[r.URL.Path], payload)
				mu.Unlock()
			}
		}

		switch r.URL.Path {
		case "/message/send/text", "/message/send/location", "/message/send/link":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"msg_id":"` + strings.TrimPrefix(strings.ReplaceAll(r.URL.Path, "/", "_"), "_") + `"}`))
		case "/message/revoke", "/contact/accept", "/contact/remark", "/group/invite", "/group/remove", "/group/name", "/group/announcement", "/group/leave", "/login/logout":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case "/contact/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"contacts":[{"user_id":"wxid_friend","alias":"friend_alias","nickname":"Friend","remark":"Bestie","avatar_url":"https://example.com/avatar.jpg","gender":1,"province":"GD","city":"SZ","signature":"hello","is_group":false},{"user_id":"group@chatroom","nickname":"Bridge Group","is_group":true,"member_count":2}]}`))
		case "/contact/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"wxid_friend","alias":"friend_alias","nickname":"Friend","remark":"Bestie","avatar_url":"https://example.com/avatar.jpg","gender":1,"province":"GD","city":"SZ","signature":"hello","is_group":false}`))
		case "/contact/avatar":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"avatar_url":"` + serverURL + `/avatar.jpg"}`))
		case "/avatar.jpg":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("avatar-bytes"))
		case "/group/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"contacts":[{"user_id":"group@chatroom","nickname":"Bridge Group","is_group":true,"member_count":2}]}`))
		case "/group/members":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"members":[{"user_id":"wxid_friend","nickname":"Friend","display_name":"Friendly","avatar_url":"https://example.com/avatar.jpg","is_admin":true,"is_owner":true}]}`))
		case "/group/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"group@chatroom","nickname":"Bridge Group","is_group":true,"member_count":2}`))
		case "/group/create":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"group_id":"group@chatroom"}`))
		case "/login/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":3}`))
		case "/login/reconnect":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":3,"user_id":"wxid_self","nickname":"Bridge Bot"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = server.URL
	defer server.Close()

	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		APIEndpoint: server.URL,
		APIToken:    "api-token",
		Extra: map[string]string{
			"message_interval_ms":  "1",
			"max_messages_per_day": "20",
			"random_delay":         "false",
		},
	}, nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !p.IsRunning() {
		t.Fatal("provider should be running")
	}
	if p.Name() != "ipad" || p.Tier() != 2 || !p.Capabilities().SendLocation || !p.Capabilities().SendLink {
		t.Fatalf("unexpected provider identity or capabilities")
	}

	ctx := context.Background()
	if msgID, err := p.SendText(ctx, "wxid_friend", "hello"); err != nil || msgID != "message_send_text" {
		t.Fatalf("SendText: %v msg=%s", err, msgID)
	}
	if msgID, err := p.SendLocation(ctx, "wxid_friend", &wechat.LocationInfo{
		Latitude:  23.1291,
		Longitude: 113.2644,
		Label:     "Tianhe Road",
		Poiname:   "Guangzhou",
	}); err != nil || msgID != "message_send_location" {
		t.Fatalf("SendLocation: %v msg=%s", err, msgID)
	}
	if msgID, err := p.SendLink(ctx, "wxid_friend", &wechat.LinkCardInfo{
		Title:       "N42",
		Description: "Bridge update",
		URL:         "https://example.com",
		ThumbURL:    "https://example.com/thumb.jpg",
	}); err != nil || msgID != "message_send_link" {
		t.Fatalf("SendLink: %v msg=%s", err, msgID)
	}
	if err := p.RevokeMessage(ctx, "message_send_text", "wxid_friend"); err != nil {
		t.Fatalf("RevokeMessage: %v", err)
	}

	contacts, err := p.GetContactList(ctx)
	if err != nil || len(contacts) != 2 {
		t.Fatalf("GetContactList: %v len=%d", err, len(contacts))
	}
	contact, err := p.GetContactInfo(ctx, "wxid_friend")
	if err != nil || contact == nil || contact.UserID != "wxid_friend" || contact.Remark != "Bestie" {
		t.Fatalf("GetContactInfo: %v %+v", err, contact)
	}
	avatar, mimeType, err := p.GetUserAvatar(ctx, "wxid_friend")
	if err != nil || string(avatar) != "avatar-bytes" || mimeType != "image/png" {
		t.Fatalf("GetUserAvatar: %v %q %s", err, string(avatar), mimeType)
	}
	if err := p.AcceptFriendRequest(ctx, "<xml/>"); err != nil {
		t.Fatalf("AcceptFriendRequest: %v", err)
	}
	if err := p.SetContactRemark(ctx, "wxid_friend", "Buddy"); err != nil {
		t.Fatalf("SetContactRemark: %v", err)
	}

	groups, err := p.GetGroupList(ctx)
	if err != nil || len(groups) != 1 || !groups[0].IsGroup {
		t.Fatalf("GetGroupList: %v %+v", err, groups)
	}
	members, err := p.GetGroupMembers(ctx, "group@chatroom")
	if err != nil || len(members) != 1 || !members[0].IsAdmin || !members[0].IsOwner {
		t.Fatalf("GetGroupMembers: %v %+v", err, members)
	}
	groupInfo, err := p.GetGroupInfo(ctx, "group@chatroom")
	if err != nil || groupInfo == nil || !groupInfo.IsGroup || groupInfo.MemberCount != 2 {
		t.Fatalf("GetGroupInfo: %v %+v", err, groupInfo)
	}
	groupID, err := p.CreateGroup(ctx, "Bridge Group", []string{"wxid_friend"})
	if err != nil || groupID != "group@chatroom" {
		t.Fatalf("CreateGroup: %v group=%s", err, groupID)
	}
	if err := p.InviteToGroup(ctx, groupID, []string{"wxid_friend"}); err != nil {
		t.Fatalf("InviteToGroup: %v", err)
	}
	if err := p.RemoveFromGroup(ctx, groupID, []string{"wxid_friend"}); err != nil {
		t.Fatalf("RemoveFromGroup: %v", err)
	}
	if err := p.SetGroupName(ctx, groupID, "Renamed"); err != nil {
		t.Fatalf("SetGroupName: %v", err)
	}
	if err := p.SetGroupAnnouncement(ctx, groupID, "hello team"); err != nil {
		t.Fatalf("SetGroupAnnouncement: %v", err)
	}
	if err := p.LeaveGroup(ctx, groupID); err != nil {
		t.Fatalf("LeaveGroup: %v", err)
	}

	p.setLoginState(wechat.LoginStateLoggedIn)
	if !p.checkAlive(ctx) {
		t.Fatal("checkAlive should report logged-in status")
	}
	p.self = nil
	if err := p.doReconnect(ctx); err != nil {
		t.Fatalf("doReconnect: %v", err)
	}
	if p.GetLoginState() != wechat.LoginStateLoggedIn {
		t.Fatalf("login state after reconnect = %v", p.GetLoginState())
	}
	if self := p.GetSelf(); self == nil || self.UserID != "wxid_self" || self.Nickname != "Bridge Bot" {
		t.Fatalf("unexpected self after reconnect: %+v", self)
	}

	messages, groupOps, friendOps := p.GetRiskControlStats()
	if messages != 3 || groupOps != 2 || friendOps != 1 {
		t.Fatalf("unexpected risk stats: messages=%d groups=%d friends=%d", messages, groupOps, friendOps)
	}
	if stats := p.GetReconnectStats(); stats.ReconnectCount != 0 {
		t.Fatalf("unexpected reconnect stats: %+v", stats)
	}

	if err := p.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if p.GetLoginState() != wechat.LoginStateLoggedOut || p.GetSelf() != nil {
		t.Fatalf("unexpected logout state: state=%v self=%+v", p.GetLoginState(), p.GetSelf())
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if p.IsRunning() {
		t.Fatal("provider should be stopped")
	}

	mu.Lock()
	defer mu.Unlock()
	if got := payloads["/message/send/location"][0]["label"]; got != "Tianhe Road" {
		t.Fatalf("location label payload = %v", got)
	}
	if got := payloads["/message/send/link"][0]["thumb_url"]; got != "https://example.com/thumb.jpg" {
		t.Fatalf("link thumb_url payload = %v", got)
	}
	if got := payloads["/group/create"][0]["name"]; got != "Bridge Group" {
		t.Fatalf("group create payload = %v", got)
	}
}
