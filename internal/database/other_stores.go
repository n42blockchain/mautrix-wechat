package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// --- GroupMemberStore ---

// GroupMemberRow represents a row in the group_member table.
type GroupMemberRow struct {
	GroupID     string
	WeChatID   string
	DisplayName string
	IsAdmin    bool
	IsOwner    bool
	JoinedAt   *time.Time
}

// GroupMemberStore provides operations for group member records.
type GroupMemberStore struct {
	db *sql.DB
}

// Upsert inserts or updates a group member.
func (s *GroupMemberStore) Upsert(ctx context.Context, m *GroupMemberRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO group_member (group_id, wechat_id, display_name, is_admin, is_owner, joined_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (group_id, wechat_id) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			is_admin = EXCLUDED.is_admin,
			is_owner = EXCLUDED.is_owner
	`, m.GroupID, m.WeChatID, m.DisplayName, m.IsAdmin, m.IsOwner, m.JoinedAt)
	if err != nil {
		return fmt.Errorf("upsert group member: %w", err)
	}
	return nil
}

// GetByGroup returns all members of a group.
func (s *GroupMemberStore) GetByGroup(ctx context.Context, groupID string) ([]*GroupMemberRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT group_id, wechat_id, display_name, is_admin, is_owner, joined_at
		FROM group_member WHERE group_id = $1
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	defer rows.Close()

	var members []*GroupMemberRow
	for rows.Next() {
		m := &GroupMemberRow{}
		if err := rows.Scan(&m.GroupID, &m.WeChatID, &m.DisplayName, &m.IsAdmin, &m.IsOwner, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("scan group member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// DeleteMember removes a member from a group.
func (s *GroupMemberStore) DeleteMember(ctx context.Context, groupID, wechatID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM group_member WHERE group_id = $1 AND wechat_id = $2",
		groupID, wechatID)
	return err
}

// DeleteGroup removes all members of a group.
func (s *GroupMemberStore) DeleteGroup(ctx context.Context, groupID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM group_member WHERE group_id = $1", groupID)
	return err
}

// --- MediaCacheStore ---

// MediaCacheEntry maps a WeChat media ID to a Matrix MXC URI.
type MediaCacheEntry struct {
	WeChatMediaID string
	MatrixMXC     string
	MimeType      string
	FileSize      int64
	FileName      string
	CachedAt      time.Time
}

// MediaCacheStore provides operations for the media cache.
type MediaCacheStore struct {
	db *sql.DB
}

// Put inserts or updates a media cache entry.
func (s *MediaCacheStore) Put(ctx context.Context, e *MediaCacheEntry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_cache (wechat_media_id, matrix_mxc, mime_type, file_size, file_name)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (wechat_media_id) DO UPDATE SET
			matrix_mxc = EXCLUDED.matrix_mxc,
			mime_type = EXCLUDED.mime_type,
			file_size = EXCLUDED.file_size,
			file_name = EXCLUDED.file_name,
			cached_at = NOW()
	`, e.WeChatMediaID, e.MatrixMXC, e.MimeType, e.FileSize, e.FileName)
	if err != nil {
		return fmt.Errorf("put media cache: %w", err)
	}
	return nil
}

// Get retrieves a media cache entry by WeChat media ID.
func (s *MediaCacheStore) Get(ctx context.Context, mediaID string) (*MediaCacheEntry, error) {
	e := &MediaCacheEntry{}
	err := s.db.QueryRowContext(ctx, `
		SELECT wechat_media_id, matrix_mxc, mime_type, file_size, file_name, cached_at
		FROM media_cache WHERE wechat_media_id = $1
	`, mediaID).Scan(&e.WeChatMediaID, &e.MatrixMXC, &e.MimeType, &e.FileSize, &e.FileName, &e.CachedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get media cache: %w", err)
	}
	return e, nil
}

// --- ProviderSessionStore ---

// ProviderSessionRow stores provider login session state.
type ProviderSessionRow struct {
	BridgeUser   string
	ProviderType string
	SessionData  json.RawMessage
	Cookies      []byte
	DeviceInfo   json.RawMessage
	UpdatedAt    time.Time
}

// ProviderSessionStore provides operations for provider sessions.
type ProviderSessionStore struct {
	db *sql.DB
}

// Upsert inserts or updates a provider session.
func (s *ProviderSessionStore) Upsert(ctx context.Context, sess *ProviderSessionRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO provider_session (bridge_user, provider_type, session_data, cookies, device_info, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (bridge_user) DO UPDATE SET
			provider_type = EXCLUDED.provider_type,
			session_data = EXCLUDED.session_data,
			cookies = EXCLUDED.cookies,
			device_info = EXCLUDED.device_info,
			updated_at = NOW()
	`, sess.BridgeUser, sess.ProviderType, sess.SessionData, sess.Cookies, sess.DeviceInfo)
	if err != nil {
		return fmt.Errorf("upsert provider session: %w", err)
	}
	return nil
}

// Get retrieves a provider session for a bridge user.
func (s *ProviderSessionStore) Get(ctx context.Context, bridgeUser string) (*ProviderSessionRow, error) {
	sess := &ProviderSessionRow{}
	err := s.db.QueryRowContext(ctx, `
		SELECT bridge_user, provider_type, session_data, cookies, device_info, updated_at
		FROM provider_session WHERE bridge_user = $1
	`, bridgeUser).Scan(
		&sess.BridgeUser, &sess.ProviderType, &sess.SessionData,
		&sess.Cookies, &sess.DeviceInfo, &sess.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get provider session: %w", err)
	}
	return sess, nil
}

// Delete removes a provider session.
func (s *ProviderSessionStore) Delete(ctx context.Context, bridgeUser string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM provider_session WHERE bridge_user = $1", bridgeUser)
	return err
}

// --- AuditLogStore ---

// AuditLogEntry represents an audit log record.
type AuditLogEntry struct {
	ID           int64
	BridgeUser   string
	Action       string
	ProviderType string
	Details      json.RawMessage
	IPAddress    string
	CreatedAt    time.Time
}

// AuditLogStore provides operations for the audit log.
type AuditLogStore struct {
	db *sql.DB
}

// Log writes an audit log entry.
func (s *AuditLogStore) Log(ctx context.Context, entry *AuditLogEntry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bridge_audit_log (bridge_user, action, provider_type, details, ip_address)
		VALUES ($1, $2, $3, $4, $5::inet)
	`, entry.BridgeUser, entry.Action, entry.ProviderType, entry.Details, nullString(entry.IPAddress))
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// Recent returns the N most recent audit log entries for a user.
func (s *AuditLogStore) Recent(ctx context.Context, bridgeUser string, limit int) ([]*AuditLogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, bridge_user, action, provider_type, details, ip_address, created_at
		FROM bridge_audit_log
		WHERE bridge_user = $1
		ORDER BY created_at DESC LIMIT $2
	`, bridgeUser, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}
	defer rows.Close()

	var entries []*AuditLogEntry
	for rows.Next() {
		e := &AuditLogEntry{}
		var ip sql.NullString
		if err := rows.Scan(&e.ID, &e.BridgeUser, &e.Action, &e.ProviderType,
			&e.Details, &ip, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		e.IPAddress = ip.String
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// --- RateLimitStore ---

// RateLimitEntry represents a rate limit counter window.
type RateLimitEntry struct {
	BridgeUser   string
	WindowStart  time.Time
	MessageCount int
	MediaCount   int
	APICallCount int
}

// RateLimitStore provides operations for rate limiting.
type RateLimitStore struct {
	db *sql.DB
}

// Increment atomically increments a rate limit counter for the current window.
func (s *RateLimitStore) Increment(ctx context.Context, bridgeUser string, msgDelta, mediaDelta, apiDelta int) (*RateLimitEntry, error) {
	windowStart := time.Now().Truncate(time.Minute)
	entry := &RateLimitEntry{}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO rate_limit (bridge_user, window_start, message_count, media_count, api_call_count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_user, window_start) DO UPDATE SET
			message_count = rate_limit.message_count + EXCLUDED.message_count,
			media_count = rate_limit.media_count + EXCLUDED.media_count,
			api_call_count = rate_limit.api_call_count + EXCLUDED.api_call_count
		RETURNING bridge_user, window_start, message_count, media_count, api_call_count
	`, bridgeUser, windowStart, msgDelta, mediaDelta, apiDelta).Scan(
		&entry.BridgeUser, &entry.WindowStart,
		&entry.MessageCount, &entry.MediaCount, &entry.APICallCount,
	)
	if err != nil {
		return nil, fmt.Errorf("increment rate limit: %w", err)
	}
	return entry, nil
}

// GetCurrent returns the rate limit counters for the current window.
func (s *RateLimitStore) GetCurrent(ctx context.Context, bridgeUser string) (*RateLimitEntry, error) {
	windowStart := time.Now().Truncate(time.Minute)
	entry := &RateLimitEntry{}
	err := s.db.QueryRowContext(ctx, `
		SELECT bridge_user, window_start, message_count, media_count, api_call_count
		FROM rate_limit WHERE bridge_user = $1 AND window_start = $2
	`, bridgeUser, windowStart).Scan(
		&entry.BridgeUser, &entry.WindowStart,
		&entry.MessageCount, &entry.MediaCount, &entry.APICallCount,
	)
	if err == sql.ErrNoRows {
		return &RateLimitEntry{BridgeUser: bridgeUser, WindowStart: windowStart}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rate limit: %w", err)
	}
	return entry, nil
}

// Cleanup removes old rate limit entries older than the given duration.
func (s *RateLimitStore) Cleanup(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	_, err := s.db.ExecContext(ctx, "DELETE FROM rate_limit WHERE window_start < $1", cutoff)
	return err
}

func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
