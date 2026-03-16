package database

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func roomMappingMockRows() *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"wechat_chat_id", "matrix_room_id", "bridge_user", "is_group",
		"name", "avatar_mxc", "topic", "encrypted", "name_set", "avatar_set", "created_at",
	}).AddRow("group1", "!room:example.com", "@user:example.com", true, "Group", "mxc://avatar", "topic", true, true, true, now)
}

func TestRoomMappingStore_CRUD(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := &RoomMappingStore{db: db}
	mock.ExpectExec(regexp.QuoteMeta(`
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
	`)).
		WithArgs("group1", "!room:example.com", "@user:example.com", true, "Group", "mxc://avatar", "topic", true, true, true).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.Upsert(context.Background(), &RoomMapping{
		WeChatChatID: "group1",
		MatrixRoomID: "!room:example.com",
		BridgeUser:   "@user:example.com",
		IsGroup:      true,
		Name:         "Group",
		AvatarMXC:    "mxc://avatar",
		Topic:        "topic",
		Encrypted:    true,
		NameSet:      true,
		AvatarSet:    true,
	}); err != nil {
		t.Fatalf("Upsert error: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + roomMappingColumns + ` FROM room_mapping WHERE matrix_room_id = $1`)).
		WithArgs("!room:example.com").
		WillReturnRows(roomMappingMockRows())
	room, err := store.GetByMatrixRoomID(context.Background(), "!room:example.com")
	if err != nil || room == nil {
		t.Fatalf("GetByMatrixRoomID error=%v room=%+v", err, room)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT `+roomMappingColumns+` FROM room_mapping WHERE wechat_chat_id = $1 AND bridge_user = $2`)).
		WithArgs("group1", "@user:example.com").
		WillReturnRows(roomMappingMockRows())
	room, err = store.GetByWeChatChat(context.Background(), "group1", "@user:example.com")
	if err != nil || room == nil {
		t.Fatalf("GetByWeChatChat error=%v room=%+v", err, room)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + roomMappingColumns + ` FROM room_mapping WHERE bridge_user = $1 ORDER BY created_at`)).
		WithArgs("@user:example.com").
		WillReturnRows(roomMappingMockRows())
	rooms, err := store.GetAllForUser(context.Background(), "@user:example.com")
	if err != nil || len(rooms) != 1 {
		t.Fatalf("GetAllForUser error=%v rooms=%d", err, len(rooms))
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM room_mapping WHERE wechat_chat_id = $1 AND bridge_user = $2")).
		WithArgs("group1", "@user:example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.Delete(context.Background(), "group1", "@user:example.com"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
