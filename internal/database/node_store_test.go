package database

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestNewNodeAssignmentStore(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := NewNodeAssignmentStore(db)
	if store == nil {
		t.Fatal("expected store")
	}
}

func TestNodeAssignmentStore_GetByBridgeUser_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := NewNodeAssignmentStore(db)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT bridge_user, node_id, assigned_at, last_active, wechat_id, login_state
		FROM node_assignment WHERE bridge_user = $1
	`)).
		WithArgs("@user:example.com").
		WillReturnRows(sqlmock.NewRows([]string{
			"bridge_user", "node_id", "assigned_at", "last_active", "wechat_id", "login_state",
		}))

	assignment, err := store.GetByBridgeUser(context.Background(), "@user:example.com")
	if err != nil {
		t.Fatalf("GetByBridgeUser error: %v", err)
	}
	if assignment != nil {
		t.Fatal("expected nil assignment when no row exists")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestNodeAssignmentStore_DeleteExceptLoginState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := NewNodeAssignmentStore(db)
	mock.ExpectExec(regexp.QuoteMeta(`
		DELETE FROM node_assignment WHERE login_state <> $1
	`)).
		WithArgs(3).
		WillReturnResult(sqlmock.NewResult(0, 2))

	rows, err := store.DeleteExceptLoginState(context.Background(), 3)
	if err != nil {
		t.Fatalf("DeleteExceptLoginState error: %v", err)
	}
	if rows != 2 {
		t.Fatalf("deleted rows = %d, want 2", rows)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
