package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MessageMapping maps a WeChat message ID to a Matrix event ID.
type MessageMapping struct {
	WeChatMsgID   string
	MatrixEventID string
	MatrixRoomID  string
	Sender        string
	MsgType       int
	Timestamp     time.Time
	CreatedAt     time.Time
}

// MessageMappingStore provides CRUD operations for message mappings.
type MessageMappingStore struct {
	db *sql.DB
}

// Insert creates a new message mapping.
func (s *MessageMappingStore) Insert(ctx context.Context, m *MessageMapping) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO message_mapping (wechat_msg_id, matrix_event_id, matrix_room_id, sender, msg_type, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (wechat_msg_id, matrix_room_id) DO NOTHING
	`, m.WeChatMsgID, m.MatrixEventID, m.MatrixRoomID, m.Sender, m.MsgType, m.Timestamp)
	if err != nil {
		return fmt.Errorf("insert message mapping: %w", err)
	}
	return nil
}

// messageMappingColumns is the column list shared by all message mapping queries.
const messageMappingColumns = `wechat_msg_id, matrix_event_id, matrix_room_id, sender, msg_type, timestamp, created_at`

// scanMessageMapping scans a row into a MessageMapping struct.
func scanMessageMapping(scanner interface{ Scan(...interface{}) error }, m *MessageMapping) error {
	return scanner.Scan(
		&m.WeChatMsgID, &m.MatrixEventID, &m.MatrixRoomID, &m.Sender,
		&m.MsgType, &m.Timestamp, &m.CreatedAt,
	)
}

// GetByWeChatMsgID looks up a message mapping by WeChat message ID and room.
func (s *MessageMappingStore) GetByWeChatMsgID(ctx context.Context, msgID, roomID string) (*MessageMapping, error) {
	m := &MessageMapping{}
	err := scanMessageMapping(s.db.QueryRowContext(ctx,
		`SELECT `+messageMappingColumns+` FROM message_mapping WHERE wechat_msg_id = $1 AND matrix_room_id = $2`,
		msgID, roomID), m)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get message by wechat id: %w", err)
	}
	return m, nil
}

// GetByMatrixEventID looks up a message mapping by Matrix event ID.
func (s *MessageMappingStore) GetByMatrixEventID(ctx context.Context, eventID string) (*MessageMapping, error) {
	m := &MessageMapping{}
	err := scanMessageMapping(s.db.QueryRowContext(ctx,
		`SELECT `+messageMappingColumns+` FROM message_mapping WHERE matrix_event_id = $1`,
		eventID), m)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get message by matrix event id: %w", err)
	}
	return m, nil
}

// DeleteByRoom deletes all message mappings for a room.
func (s *MessageMappingStore) DeleteByRoom(ctx context.Context, roomID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM message_mapping WHERE matrix_room_id = $1", roomID)
	if err != nil {
		return fmt.Errorf("delete messages by room: %w", err)
	}
	return nil
}
