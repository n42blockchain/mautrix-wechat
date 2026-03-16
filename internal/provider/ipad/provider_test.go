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
	"testing"

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
