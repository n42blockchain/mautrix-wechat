package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// WeChatUser represents a row in the wechat_user table.
type WeChatUser struct {
	WeChatID       string
	Alias          string
	Nickname       string
	AvatarURL      string
	AvatarMXC      string
	Gender         int
	Province       string
	City           string
	Signature      string
	MatrixUserID   string
	NameSet        bool
	AvatarSet      bool
	ContactInfoSet bool
	LastSync       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// UserStore provides CRUD operations for WeChat user puppet mappings.
type UserStore struct {
	db *sql.DB
}

// Upsert inserts or updates a WeChat user record.
func (s *UserStore) Upsert(ctx context.Context, u *WeChatUser) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wechat_user (wechat_id, alias, nickname, avatar_url, avatar_mxc, gender,
			province, city, signature, matrix_user_id, name_set, avatar_set, contact_info_set, last_sync, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW())
		ON CONFLICT (wechat_id) DO UPDATE SET
			alias = EXCLUDED.alias,
			nickname = EXCLUDED.nickname,
			avatar_url = EXCLUDED.avatar_url,
			avatar_mxc = EXCLUDED.avatar_mxc,
			gender = EXCLUDED.gender,
			province = EXCLUDED.province,
			city = EXCLUDED.city,
			signature = EXCLUDED.signature,
			name_set = EXCLUDED.name_set,
			avatar_set = EXCLUDED.avatar_set,
			contact_info_set = EXCLUDED.contact_info_set,
			last_sync = EXCLUDED.last_sync,
			updated_at = NOW()
	`, u.WeChatID, u.Alias, u.Nickname, u.AvatarURL, u.AvatarMXC, u.Gender,
		u.Province, u.City, u.Signature, u.MatrixUserID, u.NameSet, u.AvatarSet,
		u.ContactInfoSet, u.LastSync)
	if err != nil {
		return fmt.Errorf("upsert wechat user: %w", err)
	}
	return nil
}

// wechatUserColumns is the column list shared by all user queries.
const wechatUserColumns = `wechat_id, alias, nickname, avatar_url, avatar_mxc, gender,
	province, city, signature, matrix_user_id, name_set, avatar_set,
	contact_info_set, last_sync, created_at, updated_at`

// scanWeChatUser scans a row into a WeChatUser struct.
func scanWeChatUser(scanner interface{ Scan(...interface{}) error }, u *WeChatUser) error {
	return scanner.Scan(
		&u.WeChatID, &u.Alias, &u.Nickname, &u.AvatarURL, &u.AvatarMXC, &u.Gender,
		&u.Province, &u.City, &u.Signature, &u.MatrixUserID, &u.NameSet, &u.AvatarSet,
		&u.ContactInfoSet, &u.LastSync, &u.CreatedAt, &u.UpdatedAt,
	)
}

// GetByWeChatID looks up a user by their WeChat ID.
func (s *UserStore) GetByWeChatID(ctx context.Context, wechatID string) (*WeChatUser, error) {
	u := &WeChatUser{}
	err := scanWeChatUser(s.db.QueryRowContext(ctx,
		`SELECT `+wechatUserColumns+` FROM wechat_user WHERE wechat_id = $1`, wechatID), u)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wechat user by id: %w", err)
	}
	return u, nil
}

// GetByMatrixID looks up a user by their Matrix user ID.
func (s *UserStore) GetByMatrixID(ctx context.Context, matrixID string) (*WeChatUser, error) {
	u := &WeChatUser{}
	err := scanWeChatUser(s.db.QueryRowContext(ctx,
		`SELECT `+wechatUserColumns+` FROM wechat_user WHERE matrix_user_id = $1`, matrixID), u)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wechat user by matrix id: %w", err)
	}
	return u, nil
}

// GetAll returns all WeChat user records.
func (s *UserStore) GetAll(ctx context.Context) ([]*WeChatUser, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+wechatUserColumns+` FROM wechat_user ORDER BY nickname`)
	if err != nil {
		return nil, fmt.Errorf("list wechat users: %w", err)
	}
	defer rows.Close()

	var users []*WeChatUser
	for rows.Next() {
		u := &WeChatUser{}
		if err := scanWeChatUser(rows, u); err != nil {
			return nil, fmt.Errorf("scan wechat user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// Delete removes a WeChat user record.
func (s *UserStore) Delete(ctx context.Context, wechatID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM wechat_user WHERE wechat_id = $1", wechatID)
	if err != nil {
		return fmt.Errorf("delete wechat user: %w", err)
	}
	return nil
}
