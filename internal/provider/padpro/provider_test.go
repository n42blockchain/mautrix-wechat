package padpro

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n42/mautrix-wechat/pkg/wechat"
)

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
