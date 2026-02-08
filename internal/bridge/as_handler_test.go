package bridge

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestASHandler creates an ASHandler with a minimal EventRouter for testing.
func newTestASHandler(hsToken string) *ASHandler {
	pm := NewPuppetManager("example.com", "wechat_{{.}}", "{{.Nickname}} (WeChat)", nil, nil)
	router := &EventRouter{
		log:       slog.Default(),
		puppets:   pm,
		processor: &defaultMessageProcessor{},
	}
	return NewASHandler(slog.Default(), hsToken, router)
}

func TestASHandler_Ping(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ping status: %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type: %s", w.Header().Get("Content-Type"))
	}
	if w.Body.String() != "{}" {
		t.Errorf("ping body: %s", w.Body.String())
	}
}

func TestASHandler_PingMatrixPath(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/ping", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("matrix ping status: %d", w.Code)
	}
}

func TestASHandler_AuthenticateQueryParam(t *testing.T) {
	h := newTestASHandler("my_secret_token")

	req := httptest.NewRequest("GET", "/users/test?access_token=my_secret_token", nil)
	if !h.authenticate(req) {
		t.Error("should authenticate with correct query param token")
	}
}

func TestASHandler_AuthenticateBearerHeader(t *testing.T) {
	h := newTestASHandler("my_secret_token")

	req := httptest.NewRequest("GET", "/users/test", nil)
	req.Header.Set("Authorization", "Bearer my_secret_token")
	if !h.authenticate(req) {
		t.Error("should authenticate with correct bearer token")
	}
}

func TestASHandler_AuthenticateInvalidToken(t *testing.T) {
	h := newTestASHandler("my_secret_token")

	// Wrong token
	req := httptest.NewRequest("GET", "/users/test?access_token=wrong_token", nil)
	if h.authenticate(req) {
		t.Error("should not authenticate with wrong token")
	}

	// No token
	req = httptest.NewRequest("GET", "/users/test", nil)
	if h.authenticate(req) {
		t.Error("should not authenticate without token")
	}

	// Wrong header format
	req = httptest.NewRequest("GET", "/users/test", nil)
	req.Header.Set("Authorization", "Basic my_secret_token")
	if h.authenticate(req) {
		t.Error("should not authenticate with Basic auth")
	}
}

func TestASHandler_UserQuery_Forbidden(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("GET", "/users/@wechat_test:example.com", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["errcode"] != "M_FORBIDDEN" {
		t.Errorf("errcode: %s", resp["errcode"])
	}
}

func TestASHandler_UserQuery_PuppetExists(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("GET", "/users/@wechat_wxid_test:example.com?access_token=test_token", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for puppet user, got %d", w.Code)
	}
}

func TestASHandler_UserQuery_NotPuppet(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("GET", "/users/@other_user:example.com?access_token=test_token", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-puppet user, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["errcode"] != "M_NOT_FOUND" {
		t.Errorf("errcode: %s", resp["errcode"])
	}
}

func TestASHandler_UserQuery_MatrixPath(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("GET", "/_matrix/app/v1/users/@wechat_wxid_test:example.com?access_token=test_token", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 via matrix path, got %d", w.Code)
	}
}

func TestASHandler_RoomQuery_AlwaysNotFound(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("GET", "/rooms/%23test:example.com?access_token=test_token", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestASHandler_RoomQuery_Forbidden(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("GET", "/rooms/%23test:example.com", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestASHandler_Transaction_Forbidden(t *testing.T) {
	h := newTestASHandler("test_token")

	body := `{"events":[]}`
	req := httptest.NewRequest("PUT", "/transactions/txn1", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestASHandler_Transaction_BadJSON(t *testing.T) {
	h := newTestASHandler("test_token")

	req := httptest.NewRequest("PUT", "/transactions/txn1?access_token=test_token", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["errcode"] != "M_BAD_JSON" {
		t.Errorf("errcode: %s", resp["errcode"])
	}
}

func TestASHandler_Transaction_EmptyEvents(t *testing.T) {
	h := newTestASHandler("test_token")

	body := `{"events":[]}`
	req := httptest.NewRequest("PUT", "/transactions/txn1?access_token=test_token", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestASHandler_Transaction_MatrixPath(t *testing.T) {
	h := newTestASHandler("test_token")

	body := `{"events":[]}`
	req := httptest.NewRequest("PUT", "/_matrix/app/v1/transactions/txn2?access_token=test_token", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 via matrix path, got %d", w.Code)
	}
}

func TestASHandler_Transaction_WithEvents(t *testing.T) {
	h := newTestASHandler("test_token")

	txn := ASTransaction{
		Events: []ASEvent{
			{
				ID:     "$event1",
				Type:   "m.room.message",
				RoomID: "!room1:example.com",
				Sender: "@user:example.com",
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "hello",
				},
				OriginServerTS: 1234567890,
			},
		},
	}
	data, _ := json.Marshal(txn)
	req := httptest.NewRequest("PUT", "/transactions/txn3?access_token=test_token", bytes.NewReader(data))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should return 200 even if event routing logs errors (non-fatal per event)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestASHandler_Transaction_PuppetSenderIgnored(t *testing.T) {
	h := newTestASHandler("test_token")

	txn := ASTransaction{
		Events: []ASEvent{
			{
				ID:     "$event2",
				Type:   "m.room.message",
				RoomID: "!room1:example.com",
				Sender: "@wechat_wxid_test:example.com", // puppet sender, should be ignored
				Content: map[string]interface{}{
					"msgtype": "m.text",
					"body":    "echo",
				},
			},
		},
	}
	data, _ := json.Marshal(txn)
	req := httptest.NewRequest("PUT", "/transactions/txn4?access_token=test_token", bytes.NewReader(data))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestASHandler_JsonError(t *testing.T) {
	h := newTestASHandler("test_token")
	w := httptest.NewRecorder()

	h.jsonError(w, http.StatusNotFound, "M_NOT_FOUND", "not found message")

	if w.Code != http.StatusNotFound {
		t.Errorf("status code: %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type: %s", w.Header().Get("Content-Type"))
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["errcode"] != "M_NOT_FOUND" {
		t.Errorf("errcode: %s", resp["errcode"])
	}
	if resp["error"] != "not found message" {
		t.Errorf("error message: %s", resp["error"])
	}
}

func TestASHandler_JsonOK(t *testing.T) {
	h := newTestASHandler("test_token")
	w := httptest.NewRecorder()

	h.jsonOK(w)

	if w.Code != http.StatusOK {
		t.Errorf("status code: %d", w.Code)
	}
	if w.Body.String() != "{}" {
		t.Errorf("body: %s", w.Body.String())
	}
}

func TestASHandler_QueryParamPrecedence(t *testing.T) {
	h := newTestASHandler("correct_token")

	// Both query param and header provided, query param should take precedence
	req := httptest.NewRequest("GET", "/users/@wechat_test:example.com?access_token=correct_token", nil)
	req.Header.Set("Authorization", "Bearer wrong_token")

	if !h.authenticate(req) {
		t.Error("query param token should take precedence")
	}
}
