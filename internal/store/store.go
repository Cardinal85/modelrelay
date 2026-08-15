// Package store 提供 Relay 的 SQLite 持久化：
// 管理员账号、证书元数据与吊销记录、审计日志、可选请求摘要。
//
// 仅使用纯 Go 驱动（modernc.org/sqlite），支持无 CGO 交叉编译。
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store 是 SQLite 持久化句柄。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库并执行迁移。
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil && filepath.Dir(path) != "." {
		return nil, fmt.Errorf("store: mkdir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// 并发安全 + WAL（现代 SQLite 驱动默认）。
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

// migrate 执行 schema 迁移（版本表 + 递增迁移）。
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create schema_version: %w", err)
	}
	var version int
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&version)

	migrations := []string{
		// v1: 基础表。
		`CREATE TABLE IF NOT EXISTS admin_users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'readonly',
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS certs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			serial TEXT NOT NULL UNIQUE,
			subject TEXT NOT NULL,
			issuer TEXT NOT NULL,
			not_before TEXT NOT NULL,
			not_after TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			fingerprint TEXT NOT NULL DEFAULT '',
			revoked_at TEXT,
			revoke_reason TEXT,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS audit_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS request_summaries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts TEXT NOT NULL,
			request_id TEXT NOT NULL,
			path TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			node TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL,
			ttft_ms INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			error_code TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts);
		CREATE INDEX IF NOT EXISTS idx_reqsum_ts ON request_summaries(ts);
		CREATE INDEX IF NOT EXISTS idx_certs_node ON certs(node_id);`,
	}
	for i, m := range migrations {
		if i+1 <= version {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migrate v%d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES(?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Version 返回当前 schema 版本。
func (s *Store) Version() (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&v)
	return v, err
}

func nowStr() string { return time.Now().UTC().Format(time.RFC3339Nano) }
