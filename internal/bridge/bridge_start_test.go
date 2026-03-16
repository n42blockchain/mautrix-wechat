package bridge

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"

	"github.com/n42/mautrix-wechat/internal/config"
	"github.com/n42/mautrix-wechat/pkg/wechat"
)

func testBridgeLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBridgePrepareHTTPServerFailsOnOccupiedPort(t *testing.T) {
	t.Parallel()

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer occupied.Close()

	b := &Bridge{
		Log: testBridgeLogger(),
		httpServer: &http.Server{
			Addr:    occupied.Addr().String(),
			Handler: http.NewServeMux(),
		},
	}

	if err := b.prepareHTTPServer(); err == nil {
		t.Fatal("expected prepareHTTPServer to fail on occupied port")
	}
	if b.httpListener != nil {
		t.Fatal("http listener should remain nil on bind failure")
	}
}

func TestBridgePrepareMetricsServerFailsOnOccupiedPort(t *testing.T) {
	t.Parallel()

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer occupied.Close()

	b := &Bridge{
		Config: &config.Config{
			Metrics: config.MetricsConfig{Listen: occupied.Addr().String()},
		},
		Log:     testBridgeLogger(),
		Metrics: NewMetrics(),
	}

	if err := b.prepareMetricsServer(); err == nil {
		t.Fatal("expected prepareMetricsServer to fail on occupied port")
	}
	if b.metricsListener != nil {
		t.Fatal("metrics listener should remain nil on bind failure")
	}
}

func TestBridgeCleanupStartupLockedReleasesListenersAndProvider(t *testing.T) {
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen http: %v", err)
	}
	metricsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		httpListener.Close()
		t.Fatalf("listen metrics: %v", err)
	}

	provider := newMockProvider("padpro", 2)
	provider.running = true
	provider.loginState = wechat.LoginStateLoggedIn

	metrics := NewMetrics()
	metrics.SetConnected(true)
	metrics.SetLoginState(int(wechat.LoginStateLoggedIn))

	b := &Bridge{
		Log:             testBridgeLogger(),
		Provider:        provider,
		Metrics:         metrics,
		httpServer:      &http.Server{Addr: httpListener.Addr().String(), Handler: http.NewServeMux()},
		httpListener:    httpListener,
		metricsServer:   &http.Server{Addr: metricsListener.Addr().String(), Handler: http.NewServeMux()},
		metricsListener: metricsListener,
		running:         true,
	}

	b.cleanupStartupLocked()

	if provider.IsRunning() {
		t.Fatal("provider should be stopped during startup cleanup")
	}
	if b.running {
		t.Fatal("bridge should not remain marked running after startup cleanup")
	}
	if b.httpServer != nil || b.httpListener != nil {
		t.Fatal("HTTP server resources should be cleared during startup cleanup")
	}
	if b.metricsServer != nil || b.metricsListener != nil {
		t.Fatal("metrics server resources should be cleared during startup cleanup")
	}
	if metrics.connectedState.Load() != 0 {
		t.Fatalf("connected state = %d", metrics.connectedState.Load())
	}
	if metrics.loginState.Load() != int64(wechat.LoginStateLoggedOut) {
		t.Fatalf("login state = %d", metrics.loginState.Load())
	}

	reboundHTTP, err := net.Listen("tcp", httpListener.Addr().String())
	if err != nil {
		t.Fatalf("rebind http listener: %v", err)
	}
	reboundHTTP.Close()

	reboundMetrics, err := net.Listen("tcp", metricsListener.Addr().String())
	if err != nil {
		t.Fatalf("rebind metrics listener: %v", err)
	}
	reboundMetrics.Close()
}

type stubCryptoHelper struct {
	closed bool
}

func (s *stubCryptoHelper) Init(context.Context) error { return nil }
func (s *stubCryptoHelper) Close() error {
	s.closed = true
	return nil
}
func (s *stubCryptoHelper) Encrypt(context.Context, string, string, map[string]interface{}) (string, map[string]interface{}, error) {
	return "", nil, nil
}
func (s *stubCryptoHelper) Decrypt(context.Context, string, map[string]interface{}) (string, map[string]interface{}, error) {
	return "", nil, nil
}
func (s *stubCryptoHelper) IsEncrypted(context.Context, string) bool { return false }
func (s *stubCryptoHelper) HandleMemberEvent(context.Context, string, string, string) error {
	return nil
}
func (s *stubCryptoHelper) ShareKeysWithUser(context.Context, string, string) error {
	return nil
}
func (s *stubCryptoHelper) SetEncryptionForRoom(context.Context, string) error { return nil }

func TestBridgeCleanupStartupLockedClosesCrypto(t *testing.T) {
	crypto := &stubCryptoHelper{}
	b := &Bridge{
		Log:    testBridgeLogger(),
		Crypto: crypto,
	}

	b.cleanupStartupLocked()

	if !crypto.closed {
		t.Fatal("crypto helper should be closed during startup cleanup")
	}
}
