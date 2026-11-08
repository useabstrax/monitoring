package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/useabstrax/abstrax/plugins/monitoring/internal/layout"

	_ "modernc.org/sqlite"
)

// Tiers is the retention schedule the maintenance loop enforces.
type Tiers struct {
	RawInterval  int64
	RawRetain    int64
	Short        int64
	ShortRetain  int64
	Medium       int64
	MediumRetain int64
	Long         int64
	LongRetain   int64
	Archive      int64
	MaxRetain    int64
	MaxBytes     int64
}

// Widths returns bucket widths from finest to coarsest. Raw is the host
// sampling interval; stored raw rows use ResRaw (0) so they never collide
// with a rollup width.
func (t Tiers) Widths() []int64 {
	return []int64{t.RawInterval, t.Short, t.Medium, t.Long, t.Archive}
}

// TierName labels a chosen bucket width.
func (t Tiers) TierName(width int64) string {
	switch width {
	case t.RawInterval:
		return "raw"
	case t.Short:
		return "short"
	case t.Medium:
		return "medium"
	case t.Long:
		return "long"
	case t.Archive:
		return "archive"
	default:
		return "custom"
	}
}

// DB is the monitoring history database. One connection and a mutex keep
// SQLite as the single writer, matching the Abstrax agent.
type DB struct {
	sql  *sql.DB
	path string

	mu    sync.Mutex
	tiers Tiers
}

// Open opens or creates the database at path.
func Open(path string, tiers Tiers) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := layout.EnsureDir(dir); err != nil {
			return nil, err
		}
	}
	sdb, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}
	sdb.SetMaxOpenConns(1)
	sdb.SetMaxIdleConns(1)

	db := &DB{sql: sdb, path: path, tiers: tiers}
	if err := db.configure(); err != nil {
		sdb.Close()
		return nil, err
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			_ = os.Chmod(p, 0o600)
		}
	}
	return db, nil
}

func (db *DB) configure() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, err := db.sql.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("store: busy_timeout: %w", err)
	}
	var tables int
	if err := db.sql.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='host_samples'`).Scan(&tables); err != nil {
		return fmt.Errorf("store: inspecting schema: %w", err)
	}
	if tables == 0 {
		// incremental auto-vacuum must be selected before tables exist so
		// retention deletes can return pages without a blocking VACUUM.
		if _, err := db.sql.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			return fmt.Errorf("store: auto_vacuum: %w", err)
		}
	}
	if _, err := db.sql.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		return fmt.Errorf("store: journal_mode: %w", err)
	}
	if _, err := db.sql.Exec(`PRAGMA synchronous = FULL`); err != nil {
		return fmt.Errorf("store: synchronous: %w", err)
	}
	return db.migrate()
}

func (db *DB) migrate() error {
	var current int
	if err := db.sql.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("store: reading schema version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("store: database schema version %d is newer than this plugin supports (%d)", current, schemaVersion)
	}
	if current == schemaVersion {
		return nil
	}
	if _, err := db.sql.Exec(schema); err != nil {
		return fmt.Errorf("store: applying schema: %w", err)
	}
	if _, err := db.sql.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("store: setting schema version: %w", err)
	}
	return nil
}

// Close checkpoints and closes the database.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, _ = db.sql.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return db.sql.Close()
}

// SetTiers replaces the retention schedule used by later maintenance passes.
func (db *DB) SetTiers(t Tiers) {
	db.mu.Lock()
	db.tiers = t
	db.mu.Unlock()
}

// Tiers returns a copy of the retention schedule.
func (db *DB) Tiers() Tiers {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.tiers
}

func (db *DB) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// SizeBytes is the database plus its WAL and shared-memory files.
func (db *DB) SizeBytes() int64 {
	var n int64
	for _, p := range []string{db.path, db.path + "-wal", db.path + "-shm"} {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}

func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func formatUnix(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}
