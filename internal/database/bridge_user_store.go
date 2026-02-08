package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// BridgeUser represents a real Matrix user who uses the bridge.
type BridgeUser struct {
	MatrixUserID   string
	WeChatID       string
	ProviderType   string
	LoginState     int
	ManagementRoom string
	SpaceRoom      string
	LastLogin      *time.Time
	CreatedAt      time.Time
}

// BridgeUserStore provides CRUD operations for bridge users.
type BridgeUserStore struct {
	db *sql.DB
}

// Upsert inserts or updates a bridge user.
func (s *BridgeUserStore) Upsert(ctx context.Context, u *BridgeUser) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bridge_user (matrix_user_id, wechat_id, provider_type, login_state,
			management_room, space_room, last_login)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (matrix_user_id) DO UPDATE SET
			wechat_id = EXCLUDED.wechat_id,
			provider_type = EXCLUDED.provider_type,
			login_state = EXCLUDED.login_state,
			management_room = EXCLUDED.management_room,
			space_room = EXCLUDED.space_room,
			last_login = EXCLUDED.last_login
	`, u.MatrixUserID, u.WeChatID, u.ProviderType, u.LoginState,
		u.ManagementRoom, u.SpaceRoom, u.LastLogin)
	if err != nil {
		return fmt.Errorf("upsert bridge user: %w", err)
	}
	return nil
}

// bridgeUserColumns is the column list shared by all bridge user queries.
const bridgeUserColumns = `matrix_user_id, wechat_id, provider_type, login_state,
	management_room, space_room, last_login, created_at`

// scanBridgeUser scans a row into a BridgeUser struct.
func scanBridgeUser(scanner interface{ Scan(...interface{}) error }, u *BridgeUser) error {
	return scanner.Scan(
		&u.MatrixUserID, &u.WeChatID, &u.ProviderType, &u.LoginState,
		&u.ManagementRoom, &u.SpaceRoom, &u.LastLogin, &u.CreatedAt,
	)
}

// GetByMatrixID looks up a bridge user by Matrix user ID.
func (s *BridgeUserStore) GetByMatrixID(ctx context.Context, matrixID string) (*BridgeUser, error) {
	u := &BridgeUser{}
	err := scanBridgeUser(s.db.QueryRowContext(ctx,
		`SELECT `+bridgeUserColumns+` FROM bridge_user WHERE matrix_user_id = $1`, matrixID), u)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get bridge user: %w", err)
	}
	return u, nil
}

// GetByWeChatID looks up a bridge user by their linked WeChat ID.
func (s *BridgeUserStore) GetByWeChatID(ctx context.Context, wechatID string) (*BridgeUser, error) {
	u := &BridgeUser{}
	err := scanBridgeUser(s.db.QueryRowContext(ctx,
		`SELECT `+bridgeUserColumns+` FROM bridge_user WHERE wechat_id = $1`, wechatID), u)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get bridge user by wechat id: %w", err)
	}
	return u, nil
}

// GetAll returns all bridge users.
func (s *BridgeUserStore) GetAll(ctx context.Context) ([]*BridgeUser, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bridgeUserColumns+` FROM bridge_user`)
	if err != nil {
		return nil, fmt.Errorf("list bridge users: %w", err)
	}
	defer rows.Close()

	var users []*BridgeUser
	for rows.Next() {
		u := &BridgeUser{}
		if err := scanBridgeUser(rows, u); err != nil {
			return nil, fmt.Errorf("scan bridge user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateLoginState updates the login state for a bridge user.
func (s *BridgeUserStore) UpdateLoginState(ctx context.Context, matrixID string, state int) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE bridge_user SET login_state = $1 WHERE matrix_user_id = $2",
		state, matrixID)
	if err != nil {
		return fmt.Errorf("update login state: %w", err)
	}
	return nil
}

// Delete removes a bridge user.
func (s *BridgeUserStore) Delete(ctx context.Context, matrixID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM bridge_user WHERE matrix_user_id = $1", matrixID)
	if err != nil {
		return fmt.Errorf("delete bridge user: %w", err)
	}
	return nil
}
