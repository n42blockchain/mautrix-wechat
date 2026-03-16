package database

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func messageMappingMockRows() *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"wechat_msg_id", "matrix_event_id", "matrix_room_id", "sender", "msg_type", "timestamp", "created_at",
	}).AddRow("wxmsg1", "$event1", "!room:example.com", "@user:example.com", 1, now, now)
}

func TestMessageMappingStore_CRUD(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := &MessageMappingStore{db: db}
	now := time.Now()
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO message_mapping (wechat_msg_id, matrix_event_id, matrix_room_id, sender, msg_type, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (wechat_msg_id, matrix_room_id) DO NOTHING
	`)).
		WithArgs("wxmsg1", "$event1", "!room:example.com", "@user:example.com", 1, now).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.Insert(context.Background(), &MessageMapping{
		WeChatMsgID:   "wxmsg1",
		MatrixEventID: "$event1",
		MatrixRoomID:  "!room:example.com",
		Sender:        "@user:example.com",
		MsgType:       1,
		Timestamp:     now,
	}); err != nil {
		t.Fatalf("Insert error: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT `+messageMappingColumns+` FROM message_mapping WHERE wechat_msg_id = $1 AND matrix_room_id = $2`)).
		WithArgs("wxmsg1", "!room:example.com").
		WillReturnRows(messageMappingMockRows())
	mapping, err := store.GetByWeChatMsgID(context.Background(), "wxmsg1", "!room:example.com")
	if err != nil || mapping == nil {
		t.Fatalf("GetByWeChatMsgID error=%v mapping=%+v", err, mapping)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + messageMappingColumns + ` FROM message_mapping WHERE matrix_event_id = $1`)).
		WithArgs("$event1").
		WillReturnRows(messageMappingMockRows())
	mapping, err = store.GetByMatrixEventID(context.Background(), "$event1")
	if err != nil || mapping == nil {
		t.Fatalf("GetByMatrixEventID error=%v mapping=%+v", err, mapping)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT ` + messageMappingColumns + ` FROM message_mapping WHERE wechat_msg_id = $1 ORDER BY created_at DESC LIMIT 1`)).
		WithArgs("wxmsg1").
		WillReturnRows(messageMappingMockRows())
	mapping, err = store.GetLatestByWeChatMsgID(context.Background(), "wxmsg1")
	if err != nil || mapping == nil {
		t.Fatalf("GetLatestByWeChatMsgID error=%v mapping=%+v", err, mapping)
	}

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM message_mapping WHERE matrix_room_id = $1")).
		WithArgs("!room:example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.DeleteByRoom(context.Background(), "!room:example.com"); err != nil {
		t.Fatalf("DeleteByRoom error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
