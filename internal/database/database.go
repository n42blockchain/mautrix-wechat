package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Database wraps the SQL database connection and provides typed query methods.
type Database struct {
	db *sql.DB

	User           *UserStore
	BridgeUser     *BridgeUserStore
	RoomMapping    *RoomMappingStore
	MessageMapping *MessageMappingStore
	GroupMember    *GroupMemberStore
	MediaCache     *MediaCacheStore
	ProviderSession *ProviderSessionStore
	AuditLog       *AuditLogStore
	RateLimit      *RateLimitStore
}

// New creates a new Database instance and runs migrations.
func New(driverName, dataSourceName string, maxOpen, maxIdle int) (*Database, error) {
	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	d := &Database{db: db}
	d.User = &UserStore{db: db}
	d.BridgeUser = &BridgeUserStore{db: db}
	d.RoomMapping = &RoomMappingStore{db: db}
	d.MessageMapping = &MessageMappingStore{db: db}
	d.GroupMember = &GroupMemberStore{db: db}
	d.MediaCache = &MediaCacheStore{db: db}
	d.ProviderSession = &ProviderSessionStore{db: db}
	d.AuditLog = &AuditLogStore{db: db}
	d.RateLimit = &RateLimitStore{db: db}

	return d, nil
}

// RunMigrations executes all pending database migrations.
func (d *Database) RunMigrations(ctx context.Context) error {
	// Create migrations tracking table
	_, err := d.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// Get current version
	var currentVersion int
	err = d.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("get current migration version: %w", err)
	}

	// Read and apply migrations
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		var version int
		if _, err := fmt.Sscanf(entry.Name(), "%04d_", &version); err != nil {
			continue
		}

		if version <= currentVersion {
			continue
		}

		data, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin transaction for migration %d: %w", version, err)
		}

		if _, err := tx.ExecContext(ctx, string(data)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("execute migration %s: %w", entry.Name(), err)
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}

	return nil
}

// Close closes the database connection.
func (d *Database) Close() error {
	return d.db.Close()
}

// DB returns the underlying *sql.DB for advanced usage.
func (d *Database) DB() *sql.DB {
	return d.db
}
