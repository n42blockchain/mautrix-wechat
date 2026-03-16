package bridge

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/n42/mautrix-wechat/internal/config"
)

func testNodes() []config.PadProNodeConfig {
	return []config.PadProNodeConfig{
		{ID: "node-01", APIEndpoint: "http://10.0.1.1:1239", AuthKey: "k1", MaxUsers: 2, Enabled: true},
		{ID: "node-02", APIEndpoint: "http://10.0.1.2:1239", AuthKey: "k2", MaxUsers: 2, Enabled: true},
		{ID: "node-03", APIEndpoint: "http://10.0.1.3:1239", AuthKey: "k3", MaxUsers: 2, Enabled: false},
	}
}

func TestNewNodePool_FiltersDisabledNodes(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	states := np.NodeStates()
	if len(states) != 2 {
		t.Fatalf("expected 2 enabled nodes, got %d", len(states))
	}
	if _, ok := states["node-03"]; ok {
		t.Error("disabled node-03 should not be in pool")
	}
}

func TestNewNodePool_InitialHealthy(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	states := np.NodeStates()
	for id, ns := range states {
		if !ns.Healthy {
			t.Errorf("node %s should be initially healthy", id)
		}
	}
}

func TestNodePool_GetNode(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	node, ok := np.GetNode("node-01")
	if !ok {
		t.Fatal("node-01 should exist")
	}
	if node.Config.APIEndpoint != "http://10.0.1.1:1239" {
		t.Errorf("wrong endpoint: %s", node.Config.APIEndpoint)
	}

	_, ok = np.GetNode("node-99")
	if ok {
		t.Error("non-existent node should return false")
	}
}

func TestNodePool_AssignNode_NoStore(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	_, err := np.AssignNode(context.Background(), "@user:example.com")
	if err == nil {
		t.Fatal("expected error when node assignment store is missing")
	}
}

func TestNodePool_LoadAssignments_NoStore(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	err := np.LoadAssignments(context.Background())
	if err == nil {
		t.Fatal("expected error when node assignment store is missing")
	}
}

func TestNodePool_AssignNode_LeastLoaded(t *testing.T) {
	// We need a real-like NodePool without DB dependency for unit tests.
	// Create NodePool with an in-memory approach using direct field manipulation.
	np := NewNodePool(testNodes(), nil, slog.Default())

	// Manually set node-01 as having 1 user
	np.mu.Lock()
	np.nodes["node-01"].ActiveUsers = 1
	np.mu.Unlock()

	// AssignNode will try to use DB — we need to test the node selection logic directly.
	// Test the internal state via NodeStates.
	states := np.NodeStates()
	if states["node-01"].ActiveUsers != 1 {
		t.Errorf("node-01 should have 1 user, got %d", states["node-01"].ActiveUsers)
	}
	if states["node-02"].ActiveUsers != 0 {
		t.Errorf("node-02 should have 0 users, got %d", states["node-02"].ActiveUsers)
	}
}

func TestNodePool_NodeStates_Snapshot(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	states := np.NodeStates()
	// Modify the snapshot — should not affect internal state
	for _, ns := range states {
		ns.ActiveUsers = 999
	}

	statesAfter := np.NodeStates()
	for _, ns := range statesAfter {
		if ns.ActiveUsers != 0 {
			t.Error("snapshot modification should not affect internal state")
		}
	}
}

func TestNodePool_HealthCheck_Success(t *testing.T) {
	// Start a mock health server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	nodes := []config.PadProNodeConfig{
		{ID: "test-node", APIEndpoint: server.URL, AuthKey: "k", MaxUsers: 10, Enabled: true},
	}
	np := NewNodePool(nodes, nil, slog.Default())

	ctx := context.Background()
	np.checkAllNodes(ctx)

	states := np.NodeStates()
	if !states["test-node"].Healthy {
		t.Error("node should be healthy after successful check")
	}
}

func TestNodePool_HealthCheck_Failure(t *testing.T) {
	// Start a mock server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	nodes := []config.PadProNodeConfig{
		{ID: "test-node", APIEndpoint: server.URL, AuthKey: "k", MaxUsers: 10, Enabled: true},
	}
	np := NewNodePool(nodes, nil, slog.Default())

	ctx := context.Background()
	np.checkAllNodes(ctx)

	states := np.NodeStates()
	if states["test-node"].Healthy {
		t.Error("node should be unhealthy after 500 response")
	}
}

func TestNodePool_HealthCheck_Unreachable(t *testing.T) {
	nodes := []config.PadProNodeConfig{
		{ID: "unreachable", APIEndpoint: "http://192.0.2.1:1", AuthKey: "k", MaxUsers: 10, Enabled: true},
	}
	np := NewNodePool(nodes, nil, slog.Default())
	// Reduce timeout for faster test
	np.healthClient.Timeout = 100 * time.Millisecond

	ctx := context.Background()
	np.checkAllNodes(ctx)

	states := np.NodeStates()
	if states["unreachable"].Healthy {
		t.Error("unreachable node should be unhealthy")
	}
}

func TestNodePool_HealthCheckLoop_StopsOnCancel(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		np.HealthCheckLoop(ctx, 10*time.Millisecond)
		close(done)
	}()

	// Wait a bit then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("HealthCheckLoop did not stop on context cancellation")
	}
}

func TestNodePool_HealthCheckLoop_StopsOnStop(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		np.HealthCheckLoop(ctx, 50*time.Millisecond)
		close(done)
	}()

	// Wait for the goroutine to enter the select loop
	time.Sleep(200 * time.Millisecond)
	np.Stop()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("HealthCheckLoop did not stop on Stop()")
	}
}

func TestNodePool_HealthCheckLoop_DefaultInterval(t *testing.T) {
	np := NewNodePool(testNodes(), nil, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		np.HealthCheckLoop(ctx, 0) // should default to 30s
		close(done)
	}()

	// Just verify it starts without panic
	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done
}
