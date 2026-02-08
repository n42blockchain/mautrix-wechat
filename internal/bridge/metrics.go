package bridge

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics collects bridge performance metrics for Prometheus exposition.
type Metrics struct {
	// Message counters
	messagesReceived atomic.Int64
	messagesSent     atomic.Int64
	messagesFailed   atomic.Int64

	// Media counters
	mediaUploaded   atomic.Int64
	mediaDownloaded atomic.Int64

	// Lifecycle counters
	loginAttempts  atomic.Int64
	loginSuccesses atomic.Int64
	loginFailures  atomic.Int64
	puppetsCreated atomic.Int64
	roomsCreated   atomic.Int64

	// Error counters
	providerErrors     atomic.Int64
	encryptionErrors   atomic.Int64
	riskControlBlocked atomic.Int64
	reconnectAttempts  atomic.Int64
	reconnectSuccesses atomic.Int64

	// Gauges
	activeUsers    atomic.Int64
	connectedState atomic.Int64 // 1=connected, 0=disconnected
	loginState     atomic.Int64 // maps to wechat.LoginState enum

	// Latency histograms (manual implementation, no external deps)
	wechatToMatrixLatency *histogram
	matrixToWechatLatency *histogram

	// Per-type message counters
	messagesByType sync.Map // map[string]*atomic.Int64

	startTime time.Time
}

// NewMetrics creates a new Metrics instance.
func NewMetrics() *Metrics {
	return &Metrics{
		startTime:             time.Now(),
		wechatToMatrixLatency: newHistogram(defaultBuckets),
		matrixToWechatLatency: newHistogram(defaultBuckets),
	}
}

// --- Counter increments ---

func (m *Metrics) IncrMessagesReceived()    { m.messagesReceived.Add(1) }
func (m *Metrics) IncrMessagesSent()        { m.messagesSent.Add(1) }
func (m *Metrics) IncrMessagesFailed()      { m.messagesFailed.Add(1) }
func (m *Metrics) IncrMediaUploaded()       { m.mediaUploaded.Add(1) }
func (m *Metrics) IncrMediaDownloaded()     { m.mediaDownloaded.Add(1) }
func (m *Metrics) IncrProviderErrors()      { m.providerErrors.Add(1) }
func (m *Metrics) IncrEncryptionErrors()    { m.encryptionErrors.Add(1) }
func (m *Metrics) IncrRiskControlBlocked()  { m.riskControlBlocked.Add(1) }
func (m *Metrics) IncrReconnectAttempts()   { m.reconnectAttempts.Add(1) }
func (m *Metrics) IncrReconnectSuccesses()  { m.reconnectSuccesses.Add(1) }
func (m *Metrics) IncrLoginAttempts()       { m.loginAttempts.Add(1) }
func (m *Metrics) IncrLoginSuccesses()      { m.loginSuccesses.Add(1) }
func (m *Metrics) IncrLoginFailures()       { m.loginFailures.Add(1) }
func (m *Metrics) IncrPuppetsCreated()      { m.puppetsCreated.Add(1) }
func (m *Metrics) IncrRoomsCreated()        { m.roomsCreated.Add(1) }

// IncrMessagesByType increments the counter for a specific message type label.
func (m *Metrics) IncrMessagesByType(direction, msgType string) {
	key := direction + ":" + msgType
	val, _ := m.messagesByType.LoadOrStore(key, &atomic.Int64{})
	val.(*atomic.Int64).Add(1)
}

// --- Gauge setters ---

func (m *Metrics) SetActiveUsers(n int64)    { m.activeUsers.Store(n) }
func (m *Metrics) SetConnected(connected bool) {
	if connected {
		m.connectedState.Store(1)
	} else {
		m.connectedState.Store(0)
	}
}
func (m *Metrics) SetLoginState(state int) { m.loginState.Store(int64(state)) }

// --- Latency observations ---

// ObserveWeChatToMatrixLatency records the time taken to bridge a message from WeChat to Matrix.
func (m *Metrics) ObserveWeChatToMatrixLatency(d time.Duration) {
	m.wechatToMatrixLatency.observe(d.Seconds())
}

// ObserveMatrixToWeChatLatency records the time taken to bridge a message from Matrix to WeChat.
func (m *Metrics) ObserveMatrixToWeChatLatency(d time.Duration) {
	m.matrixToWechatLatency.observe(d.Seconds())
}

// --- Health ---

// HealthStatus returns a structured health status.
func (m *Metrics) HealthStatus() map[string]interface{} {
	return map[string]interface{}{
		"connected":   m.connectedState.Load() == 1,
		"login_state": m.loginState.Load(),
		"uptime_secs": time.Since(m.startTime).Seconds(),
		"messages": map[string]int64{
			"received": m.messagesReceived.Load(),
			"sent":     m.messagesSent.Load(),
			"failed":   m.messagesFailed.Load(),
		},
		"errors": map[string]int64{
			"provider":       m.providerErrors.Load(),
			"encryption":     m.encryptionErrors.Load(),
			"risk_blocked":   m.riskControlBlocked.Load(),
		},
		"reconnects": map[string]int64{
			"attempts":  m.reconnectAttempts.Load(),
			"successes": m.reconnectSuccesses.Load(),
		},
	}
}

// --- Prometheus exposition ---

// Handler returns an HTTP handler that serves Prometheus metrics.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.writeMetrics(w)
	})
}

func (m *Metrics) writeMetrics(w http.ResponseWriter) {
	uptime := time.Since(m.startTime).Seconds()

	// Uptime
	writeGauge(w, "mautrix_wechat_uptime_seconds", "Bridge uptime in seconds", uptime)

	// Connection state
	writeGauge(w, "mautrix_wechat_connected", "Whether the bridge is connected to WeChat (1=yes, 0=no)", float64(m.connectedState.Load()))
	writeGauge(w, "mautrix_wechat_login_state", "Current login state (0=logged_out, 1=qr_code, 2=confirming, 3=logged_in, 4=error)", float64(m.loginState.Load()))

	// Message counters
	writeCounter(w, "mautrix_wechat_messages_received_total", "Total messages received from WeChat", float64(m.messagesReceived.Load()))
	writeCounter(w, "mautrix_wechat_messages_sent_total", "Total messages sent to WeChat", float64(m.messagesSent.Load()))
	writeCounter(w, "mautrix_wechat_messages_failed_total", "Total failed message deliveries", float64(m.messagesFailed.Load()))

	// Media counters
	writeCounter(w, "mautrix_wechat_media_uploaded_total", "Total media files uploaded to Matrix", float64(m.mediaUploaded.Load()))
	writeCounter(w, "mautrix_wechat_media_downloaded_total", "Total media files downloaded from WeChat", float64(m.mediaDownloaded.Load()))

	// Lifecycle counters
	writeCounter(w, "mautrix_wechat_login_attempts_total", "Total login attempts", float64(m.loginAttempts.Load()))
	writeCounter(w, "mautrix_wechat_login_successes_total", "Total successful logins", float64(m.loginSuccesses.Load()))
	writeCounter(w, "mautrix_wechat_login_failures_total", "Total failed logins", float64(m.loginFailures.Load()))
	writeGauge(w, "mautrix_wechat_active_users", "Number of active bridge users", float64(m.activeUsers.Load()))
	writeCounter(w, "mautrix_wechat_puppets_created_total", "Total puppet users created", float64(m.puppetsCreated.Load()))
	writeCounter(w, "mautrix_wechat_rooms_created_total", "Total Matrix rooms created", float64(m.roomsCreated.Load()))

	// Error counters
	writeCounter(w, "mautrix_wechat_provider_errors_total", "Total provider errors", float64(m.providerErrors.Load()))
	writeCounter(w, "mautrix_wechat_encryption_errors_total", "Total encryption/decryption errors", float64(m.encryptionErrors.Load()))
	writeCounter(w, "mautrix_wechat_risk_control_blocked_total", "Total messages blocked by risk control", float64(m.riskControlBlocked.Load()))

	// Reconnect counters
	writeCounter(w, "mautrix_wechat_reconnect_attempts_total", "Total reconnection attempts", float64(m.reconnectAttempts.Load()))
	writeCounter(w, "mautrix_wechat_reconnect_successes_total", "Total successful reconnections", float64(m.reconnectSuccesses.Load()))

	// Latency histograms
	m.wechatToMatrixLatency.writePrometheus(w, "mautrix_wechat_wechat_to_matrix_latency_seconds", "Message bridging latency from WeChat to Matrix")
	m.matrixToWechatLatency.writePrometheus(w, "mautrix_wechat_matrix_to_wechat_latency_seconds", "Message bridging latency from Matrix to WeChat")

	// Per-type message counters
	var typeKeys []string
	m.messagesByType.Range(func(key, _ interface{}) bool {
		typeKeys = append(typeKeys, key.(string))
		return true
	})
	sort.Strings(typeKeys)

	if len(typeKeys) > 0 {
		fmt.Fprintf(w, "# HELP mautrix_wechat_messages_by_type_total Messages by direction and type\n")
		fmt.Fprintf(w, "# TYPE mautrix_wechat_messages_by_type_total counter\n")
		for _, key := range typeKeys {
			val, _ := m.messagesByType.Load(key)
			count := val.(*atomic.Int64).Load()
			// key format: "direction:msgType"
			direction, msgType := splitTypeKey(key)
			fmt.Fprintf(w, "mautrix_wechat_messages_by_type_total{direction=%q,msg_type=%q} %d\n", direction, msgType, count)
		}
		fmt.Fprintln(w)
	}
}

// --- Helpers ---

func writeCounter(w http.ResponseWriter, name, help string, value float64) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	fmt.Fprintf(w, "%s %g\n\n", name, value)
}

func writeGauge(w http.ResponseWriter, name, help string, value float64) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s gauge\n", name)
	fmt.Fprintf(w, "%s %g\n\n", name, value)
}

func splitTypeKey(key string) (string, string) {
	for i, c := range key {
		if c == ':' {
			return key[:i], key[i+1:]
		}
	}
	return key, "unknown"
}

// --- Histogram (lightweight, no external deps) ---

// Default latency buckets in seconds: 10ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2.5s, 5s, 10s
var defaultBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

type histogram struct {
	mu      sync.Mutex
	buckets []float64
	counts  []uint64 // counts[i] = observations <= buckets[i]
	total   uint64
	sum     float64
}

func newHistogram(buckets []float64) *histogram {
	return &histogram{
		buckets: buckets,
		counts:  make([]uint64, len(buckets)),
	}
}

func (h *histogram) observe(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.total++
	h.sum += value

	for i, b := range h.buckets {
		if value <= b {
			h.counts[i]++
		}
	}
}

func (h *histogram) writePrometheus(w http.ResponseWriter, name, help string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s histogram\n", name)

	for i, b := range h.buckets {
		label := fmt.Sprintf("%g", b)
		fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", name, label, h.counts[i])
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, h.total)
	fmt.Fprintf(w, "%s_sum %s\n", name, formatFloat(h.sum))
	fmt.Fprintf(w, "%s_count %d\n\n", name, h.total)
}

func formatFloat(f float64) string {
	if f == 0 {
		return "0"
	}
	if f == math.Trunc(f) {
		return fmt.Sprintf("%.1f", f)
	}
	return fmt.Sprintf("%g", f)
}
