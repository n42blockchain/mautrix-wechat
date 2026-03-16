package database

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGroupMemberStore_CRUD(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := &GroupMemberStore{db: db}
	now := time.Now()

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO group_member (group_id, wechat_id, display_name, is_admin, is_owner, joined_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (group_id, wechat_id) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			is_admin = EXCLUDED.is_admin,
			is_owner = EXCLUDED.is_owner
	`)).
		WithArgs("group1", "wxid1", "Alice", true, false, &now).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.Upsert(context.Background(), &GroupMemberRow{
		GroupID: "group1", WeChatID: "wxid1", DisplayName: "Alice", IsAdmin: true, JoinedAt: &now,
	}); err != nil {
		t.Fatalf("Upsert error: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT group_id, wechat_id, display_name, is_admin, is_owner, joined_at
		FROM group_member WHERE group_id = $1
	`)).
		WithArgs("group1").
		WillReturnRows(sqlmock.NewRows([]string{
			"group_id", "wechat_id", "display_name", "is_admin", "is_owner", "joined_at",
		}).AddRow("group1", "wxid1", "Alice", true, false, now))
	rows, err := store.GetByGroup(context.Background(), "group1")
	if err != nil || len(rows) != 1 {
		t.Fatalf("GetByGroup error=%v len=%d", err, len(rows))
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM group_member WHERE group_id = $1 AND wechat_id = $2")).
		WithArgs("group1", "wxid1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.DeleteMember(context.Background(), "group1", "wxid1"); err != nil {
		t.Fatalf("DeleteMember error: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM group_member WHERE group_id = $1")).
		WithArgs("group1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.DeleteGroup(context.Background(), "group1"); err != nil {
		t.Fatalf("DeleteGroup error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMediaCacheProviderSessionAuditRateLimitStores(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mediaStore := &MediaCacheStore{db: db}
	sessionStore := &ProviderSessionStore{db: db}
	auditStore := &AuditLogStore{db: db}
	rateStore := &RateLimitStore{db: db}
	now := time.Now()

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO media_cache (wechat_media_id, matrix_mxc, mime_type, file_size, file_name)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (wechat_media_id) DO UPDATE SET
			matrix_mxc = EXCLUDED.matrix_mxc,
			mime_type = EXCLUDED.mime_type,
			file_size = EXCLUDED.file_size,
			file_name = EXCLUDED.file_name,
			cached_at = NOW()
	`)).
		WithArgs("media1", "mxc://media", "image/jpeg", int64(12), "pic.jpg").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := mediaStore.Put(context.Background(), &MediaCacheEntry{
		WeChatMediaID: "media1", MatrixMXC: "mxc://media", MimeType: "image/jpeg", FileSize: 12, FileName: "pic.jpg",
	}); err != nil {
		t.Fatalf("MediaCache Put error: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT wechat_media_id, matrix_mxc, mime_type, file_size, file_name, cached_at
		FROM media_cache WHERE wechat_media_id = $1
	`)).
		WithArgs("media1").
		WillReturnRows(sqlmock.NewRows([]string{
			"wechat_media_id", "matrix_mxc", "mime_type", "file_size", "file_name", "cached_at",
		}).AddRow("media1", "mxc://media", "image/jpeg", int64(12), "pic.jpg", now))
	entry, err := mediaStore.Get(context.Background(), "media1")
	if err != nil || entry == nil {
		t.Fatalf("MediaCache Get error=%v entry=%+v", err, entry)
	}

	sessionData := json.RawMessage(`{"token":"abc"}`)
	deviceInfo := json.RawMessage(`{"device":"ios"}`)
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO provider_session (bridge_user, provider_type, session_data, cookies, device_info, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (bridge_user) DO UPDATE SET
			provider_type = EXCLUDED.provider_type,
			session_data = EXCLUDED.session_data,
			cookies = EXCLUDED.cookies,
			device_info = EXCLUDED.device_info,
			updated_at = NOW()
	`)).
		WithArgs("@user:example.com", "padpro", sessionData, []byte("cookie"), deviceInfo).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := sessionStore.Upsert(context.Background(), &ProviderSessionRow{
		BridgeUser: "@user:example.com", ProviderType: "padpro", SessionData: sessionData, Cookies: []byte("cookie"), DeviceInfo: deviceInfo,
	}); err != nil {
		t.Fatalf("ProviderSession Upsert error: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT bridge_user, provider_type, session_data, cookies, device_info, updated_at
		FROM provider_session WHERE bridge_user = $1
	`)).
		WithArgs("@user:example.com").
		WillReturnRows(sqlmock.NewRows([]string{
			"bridge_user", "provider_type", "session_data", "cookies", "device_info", "updated_at",
		}).AddRow("@user:example.com", "padpro", sessionData, []byte("cookie"), deviceInfo, now))
	sess, err := sessionStore.Get(context.Background(), "@user:example.com")
	if err != nil || sess == nil {
		t.Fatalf("ProviderSession Get error=%v sess=%+v", err, sess)
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM provider_session WHERE bridge_user = $1")).
		WithArgs("@user:example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := sessionStore.Delete(context.Background(), "@user:example.com"); err != nil {
		t.Fatalf("ProviderSession Delete error: %v", err)
	}

	details := json.RawMessage(`{"ok":true}`)
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO bridge_audit_log (bridge_user, action, provider_type, details, ip_address)
		VALUES ($1, $2, $3, $4, $5::inet)
	`)).
		WithArgs("@user:example.com", "login", "padpro", details, "127.0.0.1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := auditStore.Log(context.Background(), &AuditLogEntry{
		BridgeUser: "@user:example.com", Action: "login", ProviderType: "padpro", Details: details, IPAddress: "127.0.0.1",
	}); err != nil {
		t.Fatalf("AuditLog Log error: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, bridge_user, action, provider_type, details, ip_address, created_at
		FROM bridge_audit_log
		WHERE bridge_user = $1
		ORDER BY created_at DESC LIMIT $2
	`)).
		WithArgs("@user:example.com", 5).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "bridge_user", "action", "provider_type", "details", "ip_address", "created_at",
		}).AddRow(int64(1), "@user:example.com", "login", "padpro", details, "127.0.0.1", now))
	entries, err := auditStore.Recent(context.Background(), "@user:example.com", 5)
	if err != nil || len(entries) != 1 {
		t.Fatalf("AuditLog Recent error=%v len=%d", err, len(entries))
	}

	windowStart := time.Now().Truncate(time.Minute)
	mock.ExpectQuery(regexp.QuoteMeta(`
		INSERT INTO rate_limit (bridge_user, window_start, message_count, media_count, api_call_count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_user, window_start) DO UPDATE SET
			message_count = rate_limit.message_count + EXCLUDED.message_count,
			media_count = rate_limit.media_count + EXCLUDED.media_count,
			api_call_count = rate_limit.api_call_count + EXCLUDED.api_call_count
		RETURNING bridge_user, window_start, message_count, media_count, api_call_count
	`)).
		WithArgs("@user:example.com", windowStart, 1, 2, 3).
		WillReturnRows(sqlmock.NewRows([]string{
			"bridge_user", "window_start", "message_count", "media_count", "api_call_count",
		}).AddRow("@user:example.com", windowStart, 1, 2, 3))
	rate, err := rateStore.Increment(context.Background(), "@user:example.com", 1, 2, 3)
	if err != nil || rate == nil {
		t.Fatalf("RateLimit Increment error=%v rate=%+v", err, rate)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT bridge_user, window_start, message_count, media_count, api_call_count
		FROM rate_limit WHERE bridge_user = $1 AND window_start = $2
	`)).
		WithArgs("@user:example.com", windowStart).
		WillReturnRows(sqlmock.NewRows([]string{
			"bridge_user", "window_start", "message_count", "media_count", "api_call_count",
		}).AddRow("@user:example.com", windowStart, 1, 2, 3))
	rate, err = rateStore.GetCurrent(context.Background(), "@user:example.com")
	if err != nil || rate == nil {
		t.Fatalf("RateLimit GetCurrent error=%v rate=%+v", err, rate)
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM rate_limit WHERE window_start < $1")).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := rateStore.Cleanup(context.Background(), time.Hour); err != nil {
		t.Fatalf("RateLimit Cleanup error: %v", err)
	}

	if got := nullString(""); got != nil {
		t.Fatalf("nullString empty = %#v, want nil", got)
	}
	if got := nullString("x"); got != "x" {
		t.Fatalf("nullString non-empty = %#v", got)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
