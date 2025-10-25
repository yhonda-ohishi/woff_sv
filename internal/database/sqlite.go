package database

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

// DB is the database connection wrapper
type DB struct {
	conn *sql.DB
}

// NewDB creates a new database connection
func NewDB(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Printf("Connected to SQLite database: %s", dbPath)

	return &DB{conn: conn}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// GetConn returns the underlying sql.DB connection
func (db *DB) GetConn() *sql.DB {
	return db.conn
}

// Migrate runs database migrations
func (db *DB) Migrate() error {
	schema := `
	-- OAuth ユーザーテーブル (WOFF/LINE両方対応)
	CREATE TABLE IF NOT EXISTS woff_users (
		user_id TEXT PRIMARY KEY,
		provider TEXT NOT NULL DEFAULT 'woff',  -- 'woff' or 'line'
		user_name TEXT,
		display_name TEXT,
		refresh_token TEXT,
		deleted_at DATETIME DEFAULT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- WOFFユーザーロールテーブル
	CREATE TABLE IF NOT EXISTS woff_user_roles (
		user_id TEXT NOT NULL,
		role TEXT NOT NULL,
		PRIMARY KEY (user_id, role),
		FOREIGN KEY (user_id) REFERENCES woff_users(user_id) ON DELETE CASCADE
	);

	-- インデックス
	CREATE INDEX IF NOT EXISTS idx_users_user_name ON woff_users(user_name);
	CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON woff_users(deleted_at);
	`

	_, err := db.conn.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// 既存テーブルにproviderカラムがない場合は追加
	_, err = db.conn.Exec(`ALTER TABLE woff_users ADD COLUMN provider TEXT NOT NULL DEFAULT 'woff'`)
	if err != nil {
		// カラムが既に存在する場合はエラーを無視
		if !contains(err.Error(), "duplicate column") {
			log.Printf("Warning: Could not add provider column (may already exist): %v", err)
		}
	}

	log.Println("Database migrations completed successfully")
	return nil
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
