package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RoomMapping maps a WeChat chat to a Matrix room.
type RoomMapping struct {
	WeChatChatID string
	MatrixRoomID string
	BridgeUser   string
	IsGroup      bool
	Name         string
	AvatarMXC    string
	Topic        string
	Encrypted    bool
	NameSet      bool
	AvatarSet    bool
	CreatedAt    time.Time
}

// RoomMappingStore provides CRUD operations for room mappings.
type RoomMappingStore struct {
	db *sql.DB
}

// Upsert inserts or updates a room mapping.
func (s *RoomMappingStore) Upsert(ctx context.Context, r *RoomMapping) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO room_mapping (wechat_chat_id, matrix_room_id, bridge_user, is_group,
			name, avatar_mxc, topic, encrypted, name_set, avatar_set)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (wechat_chat_id, bridge_user) DO UPDATE SET
			matrix_room_id = EXCLUDED.matrix_room_id,
			is_group = EXCLUDED.is_group,
			name = EXCLUDED.name,
			avatar_mxc = EXCLUDED.avatar_mxc,
			topic = EXCLUDED.topic,
			encrypted = EXCLUDED.encrypted,
			name_set = EXCLUDED.name_set,
			avatar_set = EXCLUDED.avatar_set
	`, r.WeChatChatID, r.MatrixRoomID, r.BridgeUser, r.IsGroup,
		r.Name, r.AvatarMXC, r.Topic, r.Encrypted, r.NameSet, r.AvatarSet)
	if err != nil {
		return fmt.Errorf("upsert room mapping: %w", err)
	}
	return nil
}

// roomMappingColumns is the column list shared by all room mapping queries.
const roomMappingColumns = `wechat_chat_id, matrix_room_id, bridge_user, is_group,
	name, avatar_mxc, topic, encrypted, name_set, avatar_set, created_at`

// scanRoomMapping scans a row into a RoomMapping struct.
func scanRoomMapping(scanner interface{ Scan(...interface{}) error }, r *RoomMapping) error {
	return scanner.Scan(
		&r.WeChatChatID, &r.MatrixRoomID, &r.BridgeUser, &r.IsGroup,
		&r.Name, &r.AvatarMXC, &r.Topic, &r.Encrypted, &r.NameSet, &r.AvatarSet, &r.CreatedAt,
	)
}

// GetByMatrixRoomID looks up a room mapping by Matrix room ID.
func (s *RoomMappingStore) GetByMatrixRoomID(ctx context.Context, roomID string) (*RoomMapping, error) {
	r := &RoomMapping{}
	err := scanRoomMapping(s.db.QueryRowContext(ctx,
		`SELECT `+roomMappingColumns+` FROM room_mapping WHERE matrix_room_id = $1`, roomID), r)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get room by matrix id: %w", err)
	}
	return r, nil
}

// GetByWeChatChat looks up a room mapping by WeChat chat ID and bridge user.
func (s *RoomMappingStore) GetByWeChatChat(ctx context.Context, wechatChatID, bridgeUser string) (*RoomMapping, error) {
	r := &RoomMapping{}
	err := scanRoomMapping(s.db.QueryRowContext(ctx,
		`SELECT `+roomMappingColumns+` FROM room_mapping WHERE wechat_chat_id = $1 AND bridge_user = $2`,
		wechatChatID, bridgeUser), r)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get room by wechat chat: %w", err)
	}
	return r, nil
}

// GetAllForUser returns all room mappings for a bridge user.
func (s *RoomMappingStore) GetAllForUser(ctx context.Context, bridgeUser string) ([]*RoomMapping, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+roomMappingColumns+` FROM room_mapping WHERE bridge_user = $1 ORDER BY created_at`,
		bridgeUser)
	if err != nil {
		return nil, fmt.Errorf("list rooms for user: %w", err)
	}
	defer rows.Close()

	var rooms []*RoomMapping
	for rows.Next() {
		r := &RoomMapping{}
		if err := scanRoomMapping(rows, r); err != nil {
			return nil, fmt.Errorf("scan room mapping: %w", err)
		}
		rooms = append(rooms, r)
	}
	return rooms, rows.Err()
}

// Delete removes a room mapping.
func (s *RoomMappingStore) Delete(ctx context.Context, wechatChatID, bridgeUser string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM room_mapping WHERE wechat_chat_id = $1 AND bridge_user = $2",
		wechatChatID, bridgeUser)
	if err != nil {
		return fmt.Errorf("delete room mapping: %w", err)
	}
	return nil
}
