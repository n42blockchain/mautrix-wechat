package database

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func wechatUserMockRows() *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"wechat_id", "alias", "nickname", "avatar_url", "avatar_mxc", "gender",
		"province", "city", "signature", "matrix_user_id", "name_set", "avatar_set",
		"contact_info_set", "last_sync", "created_at", "updated_at",
	}).AddRow("wxid_test", "alias", "Tester", "https://example.com/avatar.jpg", "mxc://avatar", 1,
		"ON", "Toronto", "sig", "@wechat_wxid_test:example.com", true, true, true, now, now, now)
}

func TestUserStore_UpsertAndLookups(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := NewUserStore(db)
	now := time.Now()

	mock.ExpectExec(regexp.QuoteMeta(`
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
	`)).
		WithArgs("wxid_test", "alias", "Tester", "https://example.com/avatar.jpg", "mxc://avatar", 1,
			"ON", "Toronto", "sig", "@wechat_wxid_test:example.com", true, true, true, &now).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Upsert(context.Background(), &WeChatUser{
		WeChatID:       "wxid_test",
		Alias:          "alias",
		Nickname:       "Tester",
		AvatarURL:      "https://example.com/avatar.jpg",
		AvatarMXC:      "mxc://avatar",
		Gender:         1,
		Province:       "ON",
		City:           "Toronto",
		Signature:      "sig",
		MatrixUserID:   "@wechat_wxid_test:example.com",
		NameSet:        true,
		AvatarSet:      true,
		ContactInfoSet: true,
		LastSync:       &now,
	})
	if err != nil {
		t.Fatalf("Upsert error: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + wechatUserColumns + ` FROM wechat_user WHERE wechat_id = $1`)).
		WithArgs("wxid_test").
		WillReturnRows(wechatUserMockRows())
	user, err := store.GetByWeChatID(context.Background(), "wxid_test")
	if err != nil || user == nil {
		t.Fatalf("GetByWeChatID error=%v user=%+v", err, user)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + wechatUserColumns + ` FROM wechat_user WHERE matrix_user_id = $1`)).
		WithArgs("@wechat_wxid_test:example.com").
		WillReturnRows(wechatUserMockRows())
	user, err = store.GetByMatrixID(context.Background(), "@wechat_wxid_test:example.com")
	if err != nil || user == nil {
		t.Fatalf("GetByMatrixID error=%v user=%+v", err, user)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + wechatUserColumns + ` FROM wechat_user ORDER BY nickname`)).
		WillReturnRows(wechatUserMockRows())
	users, err := store.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll error: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("users len = %d, want 1", len(users))
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM wechat_user WHERE wechat_id = $1")).
		WithArgs("wxid_test").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.Delete(context.Background(), "wxid_test"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
