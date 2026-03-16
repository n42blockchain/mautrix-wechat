package database

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func bridgeUserMockRows() *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"matrix_user_id", "wechat_id", "provider_type", "login_state",
		"management_room", "space_room", "last_login", "created_at",
	}).AddRow("@user:example.com", "wxid_test", "padpro", 3, "!mgmt:example.com", "!space:example.com", now, now)
}

func TestBridgeUserStore_UpsertAndGetByMatrixID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := NewBridgeUserStore(db)
	now := time.Now()

	mock.ExpectExec(regexp.QuoteMeta(`
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
	`)).
		WithArgs("@user:example.com", "wxid_test", "padpro", 3, "!mgmt:example.com", "!space:example.com", now).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Upsert(context.Background(), &BridgeUser{
		MatrixUserID:   "@user:example.com",
		WeChatID:       "wxid_test",
		ProviderType:   "padpro",
		LoginState:     3,
		ManagementRoom: "!mgmt:example.com",
		SpaceRoom:      "!space:example.com",
		LastLogin:      &now,
	})
	if err != nil {
		t.Fatalf("Upsert error: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + bridgeUserColumns + ` FROM bridge_user WHERE matrix_user_id = $1`)).
		WithArgs("@user:example.com").
		WillReturnRows(bridgeUserMockRows())

	user, err := store.GetByMatrixID(context.Background(), "@user:example.com")
	if err != nil {
		t.Fatalf("GetByMatrixID error: %v", err)
	}
	if user == nil || user.WeChatID != "wxid_test" {
		t.Fatalf("unexpected user: %+v", user)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestBridgeUserStore_GetByWeChatIDAndGetAll(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := NewBridgeUserStore(db)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + bridgeUserColumns + ` FROM bridge_user WHERE wechat_id = $1`)).
		WithArgs("wxid_test").
		WillReturnRows(bridgeUserMockRows())

	user, err := store.GetByWeChatID(context.Background(), "wxid_test")
	if err != nil {
		t.Fatalf("GetByWeChatID error: %v", err)
	}
	if user == nil || user.MatrixUserID != "@user:example.com" {
		t.Fatalf("unexpected user: %+v", user)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + bridgeUserColumns + ` FROM bridge_user`)).
		WillReturnRows(bridgeUserMockRows())

	users, err := store.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll error: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("users len = %d, want 1", len(users))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestBridgeUserStore_UpdateLoginStateAndDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := NewBridgeUserStore(db)

	mock.ExpectExec(regexp.QuoteMeta("UPDATE bridge_user SET login_state = $1 WHERE matrix_user_id = $2")).
		WithArgs(4, "@user:example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.UpdateLoginState(context.Background(), "@user:example.com", 4); err != nil {
		t.Fatalf("UpdateLoginState error: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM bridge_user WHERE matrix_user_id = $1")).
		WithArgs("@user:example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.Delete(context.Background(), "@user:example.com"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
