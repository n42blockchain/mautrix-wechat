package wecom

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

var testLog = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

func newTestCrypto(t *testing.T) *CallbackCrypto {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	key = key[:43]

	c, err := NewCallbackCrypto("testtoken", key, "testcorp")
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}
	return c
}

func TestCallbackServer_URLVerification(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	// Encrypt an echostr
	echoStr := "echo_test_12345"
	encrypted, signature, err := crypto.EncryptMessage(echoStr, "12345", "nonce1")
	if err != nil {
		t.Fatalf("encrypt echostr: %v", err)
	}

	// Build the verification URL (echostr must be URL-encoded since it's base64)
	reqURL := fmt.Sprintf("/callback?msg_signature=%s&timestamp=12345&nonce=nonce1&echostr=%s",
		signature, url.QueryEscape(encrypted))

	req := httptest.NewRequest(http.MethodGet, reqURL, nil)
	w := httptest.NewRecorder()

	cs.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}

	if w.Body.String() != echoStr {
		t.Fatalf("echostr mismatch: got %q, want %q", w.Body.String(), echoStr)
	}
}

func TestCallbackServer_URLVerificationBadSignature(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	url := "/callback?msg_signature=badsig&timestamp=12345&nonce=nonce1&echostr=whatever"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()

	cs.handleRequest(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestCallbackServer_MissingParams(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	req := httptest.NewRequest(http.MethodGet, "/callback", nil)
	w := httptest.NewRecorder()

	cs.handleRequest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCallbackServer_ReceiveTextMessage(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	// Build the decrypted XML message
	msgXML := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<FromUserName><![CDATA[user001]]></FromUserName>
		<CreateTime>1348831860</CreateTime>
		<MsgType><![CDATA[text]]></MsgType>
		<Content><![CDATA[Hello Bridge!]]></Content>
		<MsgId>1234567890</MsgId>
		<AgentID>1000001</AgentID>
	</xml>`

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "test_nonce_123"

	// Encrypt it
	encrypted, signature, err := crypto.EncryptMessage(msgXML, timestamp, nonce)
	if err != nil {
		t.Fatalf("encrypt message: %v", err)
	}

	// Build the encrypted XML body
	encXML := CallbackEncryptedXML{
		ToUserName: "testcorp",
		Encrypt:    encrypted,
		AgentID:    "1000001",
	}
	bodyBytes, _ := xml.Marshal(encXML)

	url := fmt.Sprintf("/callback?msg_signature=%s&timestamp=%s&nonce=%s",
		signature, timestamp, nonce)

	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	cs.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
	}

	// Verify the handler received the message
	if len(handler.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(handler.messages))
	}

	msg := handler.messages[0]
	if msg.Type != wechat.MsgText {
		t.Fatalf("msg type: %v", msg.Type)
	}
	if msg.Content != "Hello Bridge!" {
		t.Fatalf("msg content: %s", msg.Content)
	}
	if msg.FromUser != "user001" {
		t.Fatalf("from user: %s", msg.FromUser)
	}
	if msg.MsgID != "1234567890" {
		t.Fatalf("msg id: %s", msg.MsgID)
	}
}

func TestCallbackServer_ReceiveImageMessage(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	msgXML := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<FromUserName><![CDATA[user002]]></FromUserName>
		<CreateTime>1348831860</CreateTime>
		<MsgType><![CDATA[image]]></MsgType>
		<PicUrl><![CDATA[https://example.com/pic.jpg]]></PicUrl>
		<MediaId><![CDATA[media_abc]]></MediaId>
		<MsgId>9876543210</MsgId>
	</xml>`

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "img_nonce"

	encrypted, signature, err := crypto.EncryptMessage(msgXML, timestamp, nonce)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	encXML := CallbackEncryptedXML{
		ToUserName: "testcorp",
		Encrypt:    encrypted,
	}
	bodyBytes, _ := xml.Marshal(encXML)

	url := fmt.Sprintf("/callback?msg_signature=%s&timestamp=%s&nonce=%s",
		signature, timestamp, nonce)

	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	w := httptest.NewRecorder()
	cs.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}

	if len(handler.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(handler.messages))
	}

	msg := handler.messages[0]
	if msg.Type != wechat.MsgImage {
		t.Fatalf("msg type: %v", msg.Type)
	}
	if msg.MediaURL != "https://example.com/pic.jpg" {
		t.Fatalf("media url: %s", msg.MediaURL)
	}
	if msg.Extra["media_id"] != "media_abc" {
		t.Fatalf("media_id: %s", msg.Extra["media_id"])
	}
}

func TestCallbackServer_ReceiveEvent(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	msgXML := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<FromUserName><![CDATA[user003]]></FromUserName>
		<CreateTime>1348831860</CreateTime>
		<MsgType><![CDATA[event]]></MsgType>
		<Event><![CDATA[subscribe]]></Event>
		<AgentID>1000001</AgentID>
	</xml>`

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "evt_nonce"

	encrypted, signature, err := crypto.EncryptMessage(msgXML, timestamp, nonce)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	encXML := CallbackEncryptedXML{
		ToUserName: "testcorp",
		Encrypt:    encrypted,
	}
	bodyBytes, _ := xml.Marshal(encXML)

	url := fmt.Sprintf("/callback?msg_signature=%s&timestamp=%s&nonce=%s",
		signature, timestamp, nonce)

	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	w := httptest.NewRecorder()
	cs.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}

	// Subscribe event should trigger a contact update
	if len(handler.contacts) != 1 {
		t.Fatalf("expected 1 contact update, got %d", len(handler.contacts))
	}

	contact := handler.contacts[0]
	if contact.UserID != "user003" {
		t.Fatalf("contact user id: %s", contact.UserID)
	}
}

func TestCallbackServer_ReceiveExternalContactEvent(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	msgXML := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<FromUserName><![CDATA[sys]]></FromUserName>
		<CreateTime>1348831860</CreateTime>
		<MsgType><![CDATA[event]]></MsgType>
		<Event><![CDATA[change_external_contact]]></Event>
		<ChangeType><![CDATA[add_external_contact]]></ChangeType>
		<UserID><![CDATA[internal_user]]></UserID>
		<ExternalUserID><![CDATA[ext_user_001]]></ExternalUserID>
	</xml>`

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "ext_nonce"

	encrypted, signature, err := crypto.EncryptMessage(msgXML, timestamp, nonce)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	encXML := CallbackEncryptedXML{
		ToUserName: "testcorp",
		Encrypt:    encrypted,
	}
	bodyBytes, _ := xml.Marshal(encXML)

	url := fmt.Sprintf("/callback?msg_signature=%s&timestamp=%s&nonce=%s",
		signature, timestamp, nonce)

	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	w := httptest.NewRecorder()

	// We need to set a non-nil context
	ctx := context.Background()
	req = req.WithContext(ctx)

	cs.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}

	if len(handler.contacts) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(handler.contacts))
	}

	if handler.contacts[0].UserID != "ext_user_001" {
		t.Fatalf("external user id: %s", handler.contacts[0].UserID)
	}
}

func TestCallbackServer_ReceiveLocationMessage(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	msgXML := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<FromUserName><![CDATA[user004]]></FromUserName>
		<CreateTime>1348831860</CreateTime>
		<MsgType><![CDATA[location]]></MsgType>
		<Location_X>23.134521</Location_X>
		<Location_Y>113.358803</Location_Y>
		<Scale>20</Scale>
		<Label><![CDATA[Guangzhou]]></Label>
		<MsgId>5555555555</MsgId>
	</xml>`

	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	nonce := "loc_nonce"

	encrypted, signature, err := crypto.EncryptMessage(msgXML, timestamp, nonce)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	encXML := CallbackEncryptedXML{
		ToUserName: "testcorp",
		Encrypt:    encrypted,
	}
	bodyBytes, _ := xml.Marshal(encXML)

	url := fmt.Sprintf("/callback?msg_signature=%s&timestamp=%s&nonce=%s",
		signature, timestamp, nonce)

	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	w := httptest.NewRecorder()
	cs.handleRequest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: %d", w.Code)
	}

	if len(handler.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(handler.messages))
	}

	msg := handler.messages[0]
	if msg.Type != wechat.MsgLocation {
		t.Fatalf("msg type: %v", msg.Type)
	}
	if msg.Location == nil {
		t.Fatal("location is nil")
	}
	if msg.Location.Label != "Guangzhou" {
		t.Fatalf("label: %s", msg.Location.Label)
	}
}

func TestCallbackServer_MethodNotAllowed(t *testing.T) {
	crypto := newTestCrypto(t)
	handler := &mockHandler{}
	cs := NewCallbackServer(testLog, crypto, handler)

	req := httptest.NewRequest(http.MethodPut, "/callback", nil)
	w := httptest.NewRecorder()
	cs.handleRequest(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}
