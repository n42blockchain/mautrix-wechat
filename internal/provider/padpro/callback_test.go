package padpro

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

type testHandler struct {
	messages []*wechat.Message
	revokes  []string
}

func (h *testHandler) OnMessage(_ context.Context, msg *wechat.Message) error {
	h.messages = append(h.messages, msg)
	return nil
}

func (h *testHandler) OnLoginEvent(_ context.Context, _ *wechat.LoginEvent) error {
	return nil
}

func (h *testHandler) OnContactUpdate(_ context.Context, _ *wechat.ContactInfo) error {
	return nil
}

func (h *testHandler) OnGroupMemberUpdate(_ context.Context, _ string, _ []*wechat.GroupMember) error {
	return nil
}

func (h *testHandler) OnPresence(_ context.Context, _ string, _ bool) error {
	return nil
}

func (h *testHandler) OnTyping(_ context.Context, _ string, _ string) error {
	return nil
}

func (h *testHandler) OnRevoke(_ context.Context, msgID string, _ string) error {
	h.revokes = append(h.revokes, msgID)
	return nil
}

func TestWebhookHandler_RequiresHandler(t *testing.T) {
	handler := NewWebhookHandler(slog.Default(), nil)

	req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestWebhookHandler_DispatchesMessage(t *testing.T) {
	th := &testHandler{}
	handler := NewWebhookHandler(slog.Default(), th)

	body, err := json.Marshal(wsMessage{
		NewMsgID:     123,
		MsgType:      int(wechat.MsgText),
		FromUserName: strField{Str: "wxid_sender"},
		ToUserName:   strField{Str: "wxid_receiver"},
		Content:      strField{Str: "hello"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(th.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(th.messages))
	}
	if th.messages[0].Content != "hello" {
		t.Fatalf("content = %s", th.messages[0].Content)
	}
}

func TestWebhookHandler_DispatchesRevoke(t *testing.T) {
	th := &testHandler{}
	handler := NewWebhookHandler(slog.Default(), th)

	body, err := json.Marshal(wsMessage{
		NewMsgID:     456,
		MsgType:      int(wechat.MsgRevoke),
		FromUserName: strField{Str: "wxid_sender"},
		Content:      strField{Str: "revoked"},
		PushContent:  "message revoked",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(th.revokes) != 1 {
		t.Fatalf("expected 1 revoke, got %d", len(th.revokes))
	}
	if th.revokes[0] != "456" {
		t.Fatalf("revoke id = %s", th.revokes[0])
	}
}
