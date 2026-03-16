package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/n42/mautrix-wechat/internal/config"
	"github.com/n42/mautrix-wechat/internal/database"
)

// NodeState tracks runtime state for a single PadPro server node.
type NodeState struct {
	Config      config.PadProNodeConfig
	ActiveUsers int
	Healthy     bool
}

// NodePool manages PadPro server nodes and user-to-node assignment in multi-tenant mode.
type NodePool struct {
	mu    sync.RWMutex
	nodes map[string]*NodeState // node ID → state
	db    *database.Database
	log   *slog.Logger

	healthClient    *http.Client
	stopHealth      chan struct{}
	stopOnce        sync.Once
	cancelHealthCtx context.CancelFunc
}

// NewNodePool creates a NodePool from the enabled nodes in config.
func NewNodePool(nodes []config.PadProNodeConfig, db *database.Database, log *slog.Logger) *NodePool {
	np := &NodePool{
		nodes: make(map[string]*NodeState),
		db:    db,
		log:   log,
		healthClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		stopHealth: make(chan struct{}),
	}

	for _, n := range nodes {
		if !n.Enabled {
			continue
		}
		np.nodes[n.ID] = &NodeState{
			Config:  n,
			Healthy: true, // assume healthy until first check
		}
	}

	return np
}

// GetNode returns the NodeState for a given node ID.
func (np *NodePool) GetNode(nodeID string) (*NodeState, bool) {
	np.mu.RLock()
	defer np.mu.RUnlock()
	n, ok := np.nodes[nodeID]
	return n, ok
}

// AssignNode assigns a node to a bridge user. If the user already has an assignment,
// the existing node is returned. Otherwise, the least-loaded healthy node is chosen.
func (np *NodePool) AssignNode(ctx context.Context, bridgeUserID string) (*NodeState, error) {
	if np.db == nil || np.db.NodeAssignment == nil {
		return nil, fmt.Errorf("node assignment store not initialized")
	}

	// Check for existing assignment in DB
	existing, err := np.db.NodeAssignment.GetByBridgeUser(ctx, bridgeUserID)
	if err != nil {
		return nil, fmt.Errorf("check existing assignment: %w", err)
	}

	if existing != nil {
		np.mu.RLock()
		node, ok := np.nodes[existing.NodeID]
		np.mu.RUnlock()
		if ok && node.Healthy {
			np.log.Info("reusing existing node assignment",
				"bridge_user", bridgeUserID, "node", existing.NodeID)
			return node, nil
		}
		// Node no longer exists or is unhealthy — reassign below
		np.log.Warn("existing node assignment invalid, reassigning",
			"bridge_user", bridgeUserID, "old_node", existing.NodeID)
	}

	// Find the least-loaded healthy node
	np.mu.Lock()
	defer np.mu.Unlock()

	var best *NodeState
	var bestID string
	for id, node := range np.nodes {
		if !node.Healthy {
			continue
		}
		if node.ActiveUsers >= node.Config.MaxUsers {
			continue
		}
		if best == nil || node.ActiveUsers < best.ActiveUsers {
			best = node
			bestID = id
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no available node: all nodes are full or unhealthy")
	}

	best.ActiveUsers++

	// Persist assignment
	now := time.Now()
	assignment := &database.NodeAssignment{
		BridgeUser: bridgeUserID,
		NodeID:     bestID,
		AssignedAt: now,
		LastActive: now,
	}
	if err := np.db.NodeAssignment.Upsert(ctx, assignment); err != nil {
		best.ActiveUsers-- // rollback in-memory count
		return nil, fmt.Errorf("persist node assignment: %w", err)
	}

	np.log.Info("assigned node to user",
		"bridge_user", bridgeUserID, "node", bestID,
		"active_users", best.ActiveUsers, "max", best.Config.MaxUsers)

	return best, nil
}

// ReleaseNode releases a user's node assignment when they log out.
func (np *NodePool) ReleaseNode(ctx context.Context, bridgeUserID string) error {
	if np.db == nil || np.db.NodeAssignment == nil {
		return fmt.Errorf("node assignment store not initialized")
	}

	existing, err := np.db.NodeAssignment.GetByBridgeUser(ctx, bridgeUserID)
	if err != nil {
		return fmt.Errorf("get assignment for release: %w", err)
	}
	if existing == nil {
		return nil
	}

	if err := np.db.NodeAssignment.Delete(ctx, bridgeUserID); err != nil {
		return fmt.Errorf("delete assignment: %w", err)
	}

	np.mu.Lock()
	if node, ok := np.nodes[existing.NodeID]; ok {
		if node.ActiveUsers > 0 {
			node.ActiveUsers--
		}
	}
	np.mu.Unlock()

	np.log.Info("released node assignment",
		"bridge_user", bridgeUserID, "node", existing.NodeID)
	return nil
}

// LoadAssignments restores ActiveUsers counts from the database on startup.
func (np *NodePool) LoadAssignments(ctx context.Context) error {
	if np.db == nil || np.db.NodeAssignment == nil {
		return fmt.Errorf("node assignment store not initialized")
	}

	assignments, err := np.db.NodeAssignment.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("load assignments: %w", err)
	}

	np.mu.Lock()
	defer np.mu.Unlock()

	// Reset all counts
	for _, node := range np.nodes {
		node.ActiveUsers = 0
	}

	for _, a := range assignments {
		if node, ok := np.nodes[a.NodeID]; ok {
			node.ActiveUsers++
		} else {
			np.log.Warn("assignment references unknown node, will be reassigned",
				"bridge_user", a.BridgeUser, "node", a.NodeID)
		}
	}

	np.log.Info("loaded node assignments from database", "count", len(assignments))
	return nil
}

// HealthCheckLoop periodically probes each node's /health endpoint.
// Call this in a goroutine; it blocks until ctx is canceled or Stop() is called.
func (np *NodePool) HealthCheckLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}

	// Create a child context that Stop() can cancel to abort in-flight health checks
	healthCtx, cancel := context.WithCancel(ctx)
	np.mu.Lock()
	np.cancelHealthCtx = cancel
	np.mu.Unlock()
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-healthCtx.Done():
			return
		case <-np.stopHealth:
			return
		case <-ticker.C:
			np.checkAllNodes(healthCtx)
		}
	}
}

// Stop signals the health check loop to stop. Safe to call multiple times.
func (np *NodePool) Stop() {
	np.stopOnce.Do(func() {
		// Cancel in-flight health checks first
		np.mu.RLock()
		cancel := np.cancelHealthCtx
		np.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
		close(np.stopHealth)
	})
}

// NodeStates returns a snapshot of all node states for health reporting.
func (np *NodePool) NodeStates() map[string]NodeState {
	np.mu.RLock()
	defer np.mu.RUnlock()

	result := make(map[string]NodeState, len(np.nodes))
	for id, ns := range np.nodes {
		result[id] = *ns
	}
	return result
}

func (np *NodePool) checkAllNodes(ctx context.Context) {
	np.mu.RLock()
	nodesCopy := make(map[string]*NodeState, len(np.nodes))
	for id, ns := range np.nodes {
		nodesCopy[id] = ns
	}
	np.mu.RUnlock()

	for id, node := range nodesCopy {
		healthy := np.probeNode(ctx, node.Config.APIEndpoint)

		np.mu.Lock()
		if n, ok := np.nodes[id]; ok {
			prev := n.Healthy
			n.Healthy = healthy
			if prev != healthy {
				np.log.Warn("node health changed",
					"node", id, "healthy", healthy, "endpoint", node.Config.APIEndpoint)
			}
		}
		np.mu.Unlock()
	}
}

func (np *NodePool) probeNode(ctx context.Context, apiEndpoint string) bool {
	url := apiEndpoint + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	resp, err := np.healthClient.Do(req)
	if err != nil {
		np.log.Debug("health check failed", "url", url, "error", err)
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
