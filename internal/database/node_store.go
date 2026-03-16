package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// NodeAssignment represents a bridge user's assignment to a PadPro server node.
type NodeAssignment struct {
	BridgeUser string
	NodeID     string
	AssignedAt time.Time
	LastActive time.Time
	WeChatID   string
	LoginState int
}

// NodeAssignmentStore provides CRUD operations for the node_assignment table.
type NodeAssignmentStore struct {
	db *sql.DB
}

// NewNodeAssignmentStore creates a NodeAssignmentStore from an existing sql.DB.
func NewNodeAssignmentStore(db *sql.DB) *NodeAssignmentStore {
	return &NodeAssignmentStore{db: db}
}

// Upsert inserts or updates a node assignment.
func (s *NodeAssignmentStore) Upsert(ctx context.Context, a *NodeAssignment) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO node_assignment (bridge_user, node_id, assigned_at, last_active, wechat_id, login_state)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (bridge_user) DO UPDATE SET
			node_id = EXCLUDED.node_id,
			last_active = EXCLUDED.last_active,
			wechat_id = EXCLUDED.wechat_id,
			login_state = EXCLUDED.login_state
	`, a.BridgeUser, a.NodeID, a.AssignedAt, a.LastActive, a.WeChatID, a.LoginState)
	if err != nil {
		return fmt.Errorf("upsert node assignment: %w", err)
	}
	return nil
}

// GetByBridgeUser returns the assignment for a given bridge user, or nil if not found.
func (s *NodeAssignmentStore) GetByBridgeUser(ctx context.Context, bridgeUser string) (*NodeAssignment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT bridge_user, node_id, assigned_at, last_active, wechat_id, login_state
		FROM node_assignment WHERE bridge_user = $1
	`, bridgeUser)

	a := &NodeAssignment{}
	err := row.Scan(&a.BridgeUser, &a.NodeID, &a.AssignedAt, &a.LastActive, &a.WeChatID, &a.LoginState)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get node assignment: %w", err)
	}
	return a, nil
}

// CountByNodeID returns the number of active assignments for a given node.
func (s *NodeAssignmentStore) CountByNodeID(ctx context.Context, nodeID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM node_assignment WHERE node_id = $1
	`, nodeID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count node assignments: %w", err)
	}
	return count, nil
}

// GetAllByLoginState returns all assignments with the given login state.
func (s *NodeAssignmentStore) GetAllByLoginState(ctx context.Context, loginState int) ([]*NodeAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT bridge_user, node_id, assigned_at, last_active, wechat_id, login_state
		FROM node_assignment WHERE login_state = $1
	`, loginState)
	if err != nil {
		return nil, fmt.Errorf("get assignments by login state: %w", err)
	}
	defer rows.Close()

	var assignments []*NodeAssignment
	for rows.Next() {
		a := &NodeAssignment{}
		if err := rows.Scan(&a.BridgeUser, &a.NodeID, &a.AssignedAt, &a.LastActive, &a.WeChatID, &a.LoginState); err != nil {
			return nil, fmt.Errorf("scan node assignment: %w", err)
		}
		assignments = append(assignments, a)
	}
	return assignments, rows.Err()
}

// GetAll returns all node assignments.
func (s *NodeAssignmentStore) GetAll(ctx context.Context) ([]*NodeAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT bridge_user, node_id, assigned_at, last_active, wechat_id, login_state
		FROM node_assignment
	`)
	if err != nil {
		return nil, fmt.Errorf("get all node assignments: %w", err)
	}
	defer rows.Close()

	var assignments []*NodeAssignment
	for rows.Next() {
		a := &NodeAssignment{}
		if err := rows.Scan(&a.BridgeUser, &a.NodeID, &a.AssignedAt, &a.LastActive, &a.WeChatID, &a.LoginState); err != nil {
			return nil, fmt.Errorf("scan node assignment: %w", err)
		}
		assignments = append(assignments, a)
	}
	return assignments, rows.Err()
}

// UpdateLoginState updates the login state for a bridge user.
func (s *NodeAssignmentStore) UpdateLoginState(ctx context.Context, bridgeUser string, loginState int, wechatID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE node_assignment
		SET login_state = $1, wechat_id = $2, last_active = NOW()
		WHERE bridge_user = $3
	`, loginState, wechatID, bridgeUser)
	if err != nil {
		return fmt.Errorf("update login state: %w", err)
	}
	return nil
}

// UpdateLastActive updates the last_active timestamp for a bridge user.
func (s *NodeAssignmentStore) UpdateLastActive(ctx context.Context, bridgeUser string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE node_assignment SET last_active = NOW() WHERE bridge_user = $1
	`, bridgeUser)
	if err != nil {
		return fmt.Errorf("update last active: %w", err)
	}
	return nil
}

// Delete removes a node assignment.
func (s *NodeAssignmentStore) Delete(ctx context.Context, bridgeUser string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM node_assignment WHERE bridge_user = $1
	`, bridgeUser)
	if err != nil {
		return fmt.Errorf("delete node assignment: %w", err)
	}
	return nil
}

// DeleteExceptLoginState removes assignments that are not in the given login state.
// This is used to clean up stale assignments that cannot be restored on startup.
func (s *NodeAssignmentStore) DeleteExceptLoginState(ctx context.Context, loginState int) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM node_assignment WHERE login_state <> $1
	`, loginState)
	if err != nil {
		return 0, fmt.Errorf("delete stale node assignments: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted node assignments: %w", err)
	}
	return rows, nil
}
