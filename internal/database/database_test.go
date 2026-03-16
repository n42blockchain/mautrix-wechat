package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestNewInitializesStoresAndClose(t *testing.T) {
	dsn := fmt.Sprintf("sqlmock_database_new_%d", time.Now().UnixNano())
	seedDB, mock, err := sqlmock.NewWithDSN(dsn, sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock.NewWithDSN: %v", err)
	}

	mock.ExpectPing()
	db, err := New("sqlmock", dsn, 7, 3)
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	if db.DB() == nil {
		t.Fatal("DB() should return underlying handle")
	}
	if db.DB().Stats().MaxOpenConnections != 7 {
		t.Fatalf("max open connections = %d", db.DB().Stats().MaxOpenConnections)
	}
	if db.User == nil || db.BridgeUser == nil || db.RoomMapping == nil || db.MessageMapping == nil ||
		db.GroupMember == nil || db.MediaCache == nil || db.ProviderSession == nil ||
		db.AuditLog == nil || db.RateLimit == nil || db.NodeAssignment == nil {
		t.Fatal("expected typed stores to be initialized")
	}

	mock.ExpectClose()
	if err := db.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	mock.ExpectClose()
	if err := seedDB.Close(); err != nil {
		t.Fatalf("seed close error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestNewClosesHandleWhenPingFails(t *testing.T) {
	dsn := fmt.Sprintf("sqlmock_database_ping_fail_%d", time.Now().UnixNano())
	seedDB, mock, err := sqlmock.NewWithDSN(dsn, sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock.NewWithDSN: %v", err)
	}

	mock.ExpectPing().WillReturnError(errors.New("ping failed"))
	mock.ExpectClose()

	db, err := New("sqlmock", dsn, 5, 2)
	if err == nil || !regexp.MustCompile(`ping database: ping failed`).MatchString(err.Error()) {
		t.Fatalf("New error = %v, db = %+v", err, db)
	}

	mock.ExpectClose()
	if err := seedDB.Close(); err != nil {
		t.Fatalf("seed close error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestRunMigrationsAppliesPendingVersions(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Database{db: db}
	ctx := context.Background()

	mock.ExpectExec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)
	`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(0))

	for _, migration := range []struct {
		version int
		file    string
	}{
		{version: 1, file: "migrations/0001_initial_schema.sql"},
		{version: 2, file: "migrations/0002_multi_tenant.sql"},
	} {
		data, readErr := migrationFS.ReadFile(migration.file)
		if readErr != nil {
			t.Fatalf("read migration %s: %v", migration.file, readErr)
		}

		mock.ExpectBegin()
		mock.ExpectExec(string(data)).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`INSERT INTO schema_migrations (version) VALUES ($1)`).
			WithArgs(migration.version).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}

	if err := d.RunMigrations(ctx); err != nil {
		t.Fatalf("RunMigrations error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestRunMigrationsSkipsAlreadyAppliedVersions(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Database{db: db}

	mock.ExpectExec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)
	`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(2))

	if err := d.RunMigrations(context.Background()); err != nil {
		t.Fatalf("RunMigrations error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestRunMigrationsRollsBackOnMigrationFailure(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	d := &Database{db: db}
	migrationData, err := migrationFS.ReadFile("migrations/0001_initial_schema.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	mock.ExpectExec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)
	`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectExec(string(migrationData)).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	err = d.RunMigrations(context.Background())
	if err == nil || !regexp.MustCompile(`execute migration 0001_initial_schema\.sql: boom`).MatchString(err.Error()) {
		t.Fatalf("RunMigrations error = %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
