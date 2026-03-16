package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

type rewriteTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

func newRewriteTransport(t *testing.T, rawURL string) *rewriteTransport {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}

	return &rewriteTransport{
		base: parsed,
		rt:   http.DefaultTransport,
	}
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.base.Scheme
	clone.URL.Host = t.base.Host
	clone.Host = t.base.Host
	return t.rt.RoundTrip(clone)
}

type providerAPIMock struct {
	t                 *testing.T
	server            *httptest.Server
	mu                sync.Mutex
	sendRequests      []sendMessageRequest
	groupSendRequests []appChatSendRequest
	updateRequests    []appChatUpdateRequest
	recallRequests    []recallMessageRequest
	remarkRequests    []map[string]string
	uploadTypes       []string
	tokenFailures     int
}

func newProviderAPIMock(t *testing.T) *providerAPIMock {
	t.Helper()

	mock := &providerAPIMock{t: t}
	mux := http.NewServeMux()

	mux.HandleFunc("/cgi-bin/gettoken", func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		failures := mock.tokenFailures
		if failures > 0 {
			mock.tokenFailures--
		}
		mock.mu.Unlock()

		if failures > 0 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"errcode": 40013,
				"errmsg":  "invalid corpid",
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode":      0,
			"errmsg":       "ok",
			"access_token": "token_1",
			"expires_in":   7200,
		})
	})

	mux.HandleFunc("/cgi-bin/agent/get", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode":         0,
			"errmsg":          "ok",
			"agentid":         1000001,
			"name":            "Test Bot",
			"square_logo_url": "https://example.com/avatar.jpg",
			"description":     "Bridge bot",
		})
	})

	mux.HandleFunc("/cgi-bin/department/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode": 0,
			"errmsg":  "ok",
			"department": []map[string]interface{}{
				{"id": 1, "name": "Root", "parentid": 0},
				{"id": 2, "name": "Eng", "parentid": 1},
			},
		})
	})

	mux.HandleFunc("/cgi-bin/user/list", func(w http.ResponseWriter, r *http.Request) {
		deptID := r.URL.Query().Get("department_id")
		users := []map[string]interface{}{}
		switch deptID {
		case "1":
			users = []map[string]interface{}{
				{"userid": "user001", "name": "Alice", "gender": "2", "avatar": "https://example.com/avatar.jpg"},
				{"userid": "user002", "name": "Bob", "gender": "1", "avatar": "https://example.com/avatar.jpg"},
			}
		case "2":
			users = []map[string]interface{}{
				{"userid": "user002", "name": "Bob", "gender": "1", "avatar": "https://example.com/avatar.jpg"},
				{"userid": "user003", "name": "Carol", "gender": "2", "avatar": "https://example.com/avatar.jpg"},
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode":  0,
			"errmsg":   "ok",
			"userlist": users,
		})
	})

	mux.HandleFunc("/cgi-bin/user/get", func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("userid")
		if userID == "external_1" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"errcode": 60111,
				"errmsg":  "user not found",
			})
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode": 0,
			"errmsg":  "ok",
			"userid":  userID,
			"name":    "User " + userID,
			"gender":  "1",
			"avatar":  "https://example.com/avatar.jpg",
			"alias":   "alias_" + userID,
		})
	})

	mux.HandleFunc("/cgi-bin/externalcontact/get", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode": 0,
			"errmsg":  "ok",
			"external_contact": map[string]interface{}{
				"external_userid": "external_1",
				"name":            "External User",
				"avatar":          "https://example.com/avatar.jpg",
				"gender":          2,
			},
			"follow_user": []map[string]interface{}{
				{"userid": "agent_1000001", "remark": "VIP"},
			},
		})
	})

	mux.HandleFunc("/cgi-bin/externalcontact/remark", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			mock.t.Fatalf("decode remark payload: %v", err)
		}
		mock.mu.Lock()
		mock.remarkRequests = append(mock.remarkRequests, payload)
		mock.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errcode": 0, "errmsg": "ok"})
	})

	mux.HandleFunc("/cgi-bin/appchat/create", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errcode": 0, "errmsg": "ok", "chatid": "chat_001"})
	})

	mux.HandleFunc("/cgi-bin/appchat/get", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode": 0,
			"errmsg":  "ok",
			"chat_info": map[string]interface{}{
				"chatid":   "chat_001",
				"name":     "Test Group",
				"owner":    "agent_1000001",
				"userlist": []string{"agent_1000001", "user001", "user002"},
			},
		})
	})

	mux.HandleFunc("/cgi-bin/appchat/update", func(w http.ResponseWriter, r *http.Request) {
		var payload appChatUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			mock.t.Fatalf("decode appchat update: %v", err)
		}
		mock.mu.Lock()
		mock.updateRequests = append(mock.updateRequests, payload)
		mock.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errcode": 0, "errmsg": "ok"})
	})

	mux.HandleFunc("/cgi-bin/appchat/send", func(w http.ResponseWriter, r *http.Request) {
		var payload appChatSendRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			mock.t.Fatalf("decode appchat send: %v", err)
		}
		mock.mu.Lock()
		mock.groupSendRequests = append(mock.groupSendRequests, payload)
		mock.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errcode": 0, "errmsg": "ok"})
	})

	mux.HandleFunc("/cgi-bin/message/send", func(w http.ResponseWriter, r *http.Request) {
		var payload sendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			mock.t.Fatalf("decode send message payload: %v", err)
		}
		mock.mu.Lock()
		mock.sendRequests = append(mock.sendRequests, payload)
		msgID := len(mock.sendRequests)
		mock.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode": 0,
			"errmsg":  "ok",
			"msgid":   "msg_" + string(rune('0'+msgID)),
		})
	})

	mux.HandleFunc("/cgi-bin/message/recall", func(w http.ResponseWriter, r *http.Request) {
		var payload recallMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			mock.t.Fatalf("decode recall payload: %v", err)
		}
		mock.mu.Lock()
		mock.recallRequests = append(mock.recallRequests, payload)
		mock.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errcode": 0, "errmsg": "ok"})
	})

	mux.HandleFunc("/cgi-bin/media/upload", func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		mock.uploadTypes = append(mock.uploadTypes, r.URL.Query().Get("type"))
		mock.mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode":    0,
			"errmsg":     "ok",
			"type":       r.URL.Query().Get("type"),
			"media_id":   r.URL.Query().Get("type") + "_media",
			"created_at": "123",
		})
	})

	mux.HandleFunc("/cgi-bin/media/get", func(w http.ResponseWriter, r *http.Request) {
		mediaID := r.URL.Query().Get("media_id")
		if mediaID == "bad_media" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"errcode": 40007,
				"errmsg":  "invalid media_id",
			})
			return
		}

		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("media:" + mediaID))
	})

	mux.HandleFunc("/avatar.jpg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("avatar-bytes"))
	})

	mock.server = httptest.NewServer(mux)
	return mock
}

func (m *providerAPIMock) close() {
	m.server.Close()
}

func newMockProvider(t *testing.T, mock *providerAPIMock) (*Provider, *mockHandler) {
	t.Helper()

	handler := &mockHandler{}
	p := &Provider{}
	if err := p.Init(&wechat.ProviderConfig{
		CorpID:    "test_corp",
		AppSecret: "test_secret",
		AgentID:   1000001,
	}, handler); err != nil {
		t.Fatalf("init provider: %v", err)
	}

	p.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	p.client.httpClient.Transport = newRewriteTransport(t, mock.server.URL)
	p.client.httpClient.Timeout = 5 * time.Second
	return p, handler
}

func TestProviderStartStopAndLoginLifecycle(t *testing.T) {
	mock := newProviderAPIMock(t)
	defer mock.close()

	provider, handler := newMockProvider(t, mock)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- provider.Start(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("start provider: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider start timed out")
	}

	if !provider.IsRunning() {
		t.Fatal("provider should be running after start")
	}
	if provider.GetLoginState() != wechat.LoginStateLoggedIn {
		t.Fatalf("login state after start: %v", provider.GetLoginState())
	}
	if self := provider.GetSelf(); self == nil || self.UserID != "agent_1000001" {
		t.Fatalf("unexpected self after start: %+v", self)
	}

	if err := provider.Logout(ctx); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if provider.GetLoginState() != wechat.LoginStateLoggedOut {
		t.Fatalf("login state after logout: %v", provider.GetLoginState())
	}

	if err := provider.Login(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}
	if len(handler.logins) != 1 || handler.logins[0].State != wechat.LoginStateLoggedIn {
		t.Fatalf("unexpected login events: %+v", handler.logins)
	}
	if handler.logins[0].UserID != "agent_1000001" {
		t.Fatalf("unexpected login event user id: %+v", handler.logins[0])
	}

	if err := provider.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if provider.IsRunning() {
		t.Fatal("provider should not be running after stop")
	}
}

func TestProviderLoginErrorEmitsErrorEvent(t *testing.T) {
	mock := newProviderAPIMock(t)
	mock.tokenFailures = 1
	defer mock.close()

	provider, handler := newMockProvider(t, mock)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := provider.Login(ctx); err == nil {
		t.Fatal("expected login error")
	}
	if provider.GetLoginState() != wechat.LoginStateError {
		t.Fatalf("login state: %v", provider.GetLoginState())
	}
	if len(handler.logins) != 1 || handler.logins[0].State != wechat.LoginStateError {
		t.Fatalf("unexpected login events: %+v", handler.logins)
	}
}

func TestProviderContactAndGroupOperations(t *testing.T) {
	mock := newProviderAPIMock(t)
	defer mock.close()

	provider, _ := newMockProvider(t, mock)
	provider.self = &wechat.ContactInfo{UserID: "agent_1000001"}
	ctx := context.Background()

	contacts, err := provider.GetContactList(ctx)
	if err != nil {
		t.Fatalf("get contacts: %v", err)
	}
	if len(contacts) != 3 {
		t.Fatalf("expected deduplicated contacts, got %d", len(contacts))
	}

	internalContact, err := provider.GetContactInfo(ctx, "user001")
	if err != nil || internalContact.UserID != "user001" {
		t.Fatalf("get internal contact: %v %+v", err, internalContact)
	}

	externalContact, err := provider.GetContactInfo(ctx, "external_1")
	if err != nil || externalContact.UserID != "external_1" || externalContact.Gender != 2 {
		t.Fatalf("get external contact: %v %+v", err, externalContact)
	}

	avatar, mimeType, err := provider.GetUserAvatar(ctx, "user001")
	if err != nil {
		t.Fatalf("get avatar: %v", err)
	}
	if string(avatar) != "avatar-bytes" || mimeType == "" {
		t.Fatalf("unexpected avatar response: %q %s", string(avatar), mimeType)
	}

	if err := provider.SetContactRemark(ctx, "external_1", "VIP"); err != nil {
		t.Fatalf("set contact remark: %v", err)
	}

	groupID, err := provider.CreateGroup(ctx, "Test Group", []string{"user001", "user002"})
	if err != nil || groupID != "chat_001" {
		t.Fatalf("create group: %v %s", err, groupID)
	}

	groupInfo, err := provider.GetGroupInfo(ctx, groupID)
	if err != nil || !groupInfo.IsGroup || groupInfo.MemberCount != 3 {
		t.Fatalf("get group info: %v %+v", err, groupInfo)
	}

	members, err := provider.GetGroupMembers(ctx, groupID)
	if err != nil || len(members) != 3 || !members[0].IsOwner {
		t.Fatalf("get group members: %v %+v", err, members)
	}

	if err := provider.InviteToGroup(ctx, groupID, []string{"user003"}); err != nil {
		t.Fatalf("invite to group: %v", err)
	}
	if err := provider.RemoveFromGroup(ctx, groupID, []string{"user002"}); err != nil {
		t.Fatalf("remove from group: %v", err)
	}
	if err := provider.SetGroupName(ctx, groupID, "Renamed"); err != nil {
		t.Fatalf("set group name: %v", err)
	}
	if err := provider.SetGroupAnnouncement(ctx, groupID, "hello team"); err != nil {
		t.Fatalf("set group announcement: %v", err)
	}

	groupList, err := provider.GetGroupList(ctx)
	if err != nil || len(groupList) != 0 {
		t.Fatalf("get group list: %v %+v", err, groupList)
	}

	if err := provider.LeaveGroup(ctx, groupID); err == nil {
		t.Fatal("expected leave group error")
	}
	if err := provider.AcceptFriendRequest(ctx, "<xml/>"); err == nil {
		t.Fatal("expected friend request error")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.remarkRequests) != 1 || mock.remarkRequests[0]["userid"] != "agent_1000001" {
		t.Fatalf("unexpected remark requests: %+v", mock.remarkRequests)
	}
	if len(mock.updateRequests) != 3 {
		t.Fatalf("unexpected appchat update count: %d", len(mock.updateRequests))
	}
	if len(mock.sendRequests) == 0 || mock.sendRequests[len(mock.sendRequests)-1].Text.Content != "[Announcement] hello team" {
		t.Fatalf("unexpected send requests: %+v", mock.sendRequests)
	}
}

func TestProviderMessageAndMediaOperations(t *testing.T) {
	mock := newProviderAPIMock(t)
	defer mock.close()

	provider, _ := newMockProvider(t, mock)
	provider.self = &wechat.ContactInfo{UserID: "agent_1000001"}
	ctx := context.Background()

	if _, err := provider.SendText(ctx, "user001", "hello"); err != nil {
		t.Fatalf("send text: %v", err)
	}
	if _, err := provider.SendImage(ctx, "user001", bytes.NewBufferString("image"), "photo.jpg"); err != nil {
		t.Fatalf("send image: %v", err)
	}
	if _, err := provider.SendVoice(ctx, "user001", bytes.NewBufferString("voice"), 3); err != nil {
		t.Fatalf("send voice: %v", err)
	}
	if _, err := provider.SendVideo(ctx, "user001", bytes.NewBufferString("video"), "video.mp4", nil); err != nil {
		t.Fatalf("send video: %v", err)
	}
	if _, err := provider.SendFile(ctx, "user001", bytes.NewBufferString("file"), "doc.pdf"); err != nil {
		t.Fatalf("send file: %v", err)
	}
	if _, err := provider.SendLocation(ctx, "user001", &wechat.LocationInfo{
		Poiname:   "Office",
		Label:     "Main Office",
		Latitude:  1.23,
		Longitude: 4.56,
	}); err != nil {
		t.Fatalf("send location: %v", err)
	}
	if _, err := provider.SendLink(ctx, "user001", &wechat.LinkCardInfo{
		Title:       "Docs",
		Description: "Open docs",
		URL:         "https://example.com",
	}); err != nil {
		t.Fatalf("send link: %v", err)
	}

	groupMsgID, err := provider.sendGroupMessage(ctx, &appChatSendRequest{
		ChatID:  "chat_001",
		MsgType: "text",
		Text:    &textContent{Content: "group hello"},
	})
	if err != nil {
		t.Fatalf("send group message: %v", err)
	}
	if !strings.HasPrefix(groupMsgID, "wecom_1000001_") {
		t.Fatalf("unexpected synthetic group msg id: %s", groupMsgID)
	}

	if err := provider.RevokeMessage(ctx, "msg_1", "user001"); err != nil {
		t.Fatalf("revoke message: %v", err)
	}

	reader, mimeType, err := provider.DownloadMedia(ctx, &wechat.Message{
		Extra: map[string]string{"media_id": "image_media"},
	})
	if err != nil {
		t.Fatalf("download media by media_id: %v", err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "media:image_media" || mimeType != "image/png" {
		t.Fatalf("unexpected media download: %v %q %s", err, string(data), mimeType)
	}

	reader, mimeType, err = provider.DownloadMedia(ctx, &wechat.Message{MsgID: "msg_fallback"})
	if err != nil {
		t.Fatalf("download media by msg id: %v", err)
	}
	data, err = io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "media:msg_fallback" || mimeType != "image/png" {
		t.Fatalf("unexpected fallback media download: %v %q %s", err, string(data), mimeType)
	}

	if _, _, err := provider.DownloadMedia(ctx, &wechat.Message{
		Extra: map[string]string{"media_id": "bad_media"},
	}); err == nil {
		t.Fatal("expected media download error")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sendRequests) < 7 {
		t.Fatalf("expected multiple send requests, got %d", len(mock.sendRequests))
	}
	if len(mock.groupSendRequests) != 1 {
		t.Fatalf("expected one group send request, got %d", len(mock.groupSendRequests))
	}
	if len(mock.recallRequests) != 1 || mock.recallRequests[0].MsgID != "msg_1" {
		t.Fatalf("unexpected recall requests: %+v", mock.recallRequests)
	}
	if strings.Join(mock.uploadTypes, ",") != "image,voice,video,file" {
		t.Fatalf("unexpected upload types: %+v", mock.uploadTypes)
	}
}
