package mysql

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
	"github.com/RaafidAfraazG/NodeWatch/internal/storage"
	driver "github.com/go-sql-driver/mysql"
)

//go:embed migrations/001_initial.sql
var initialSchema string

type Store struct {
	db      *sql.DB
	timeout time.Duration
}

func Open(c config.Collector) (*Store, error) {
	dsn := driver.NewConfig()
	dsn.Net = "tcp"
	dsn.Addr = c.MySQLAddr
	dsn.User = c.MySQLUser
	dsn.Passwd = c.MySQLPassword
	dsn.DBName = c.MySQLDatabase
	dsn.Timeout = c.DependencyTimeout
	dsn.ReadTimeout = c.DependencyTimeout
	dsn.WriteTimeout = c.DependencyTimeout
	dsn.ParseTime = true
	dsn.Loc = time.UTC
	connector, err := driver.NewConnector(dsn)
	if err != nil {
		return nil, errors.New("invalid MySQL configuration")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(c.MySQLMaxOpen)
	db.SetMaxIdleConns(c.MySQLMaxIdle)
	db.SetConnMaxLifetime(c.MySQLConnLifetime)
	return &Store{db: db, timeout: c.DependencyTimeout}, nil
}
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies connectivity AND the schema version required by this binary.
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return errors.New("unsupported database schema version")
	}
	return nil
}

// Migrate uses a connection-scoped lock. MySQL DDL commits implicitly; the
// version is recorded only after replayable CREATE IF NOT EXISTS statements.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT('nodewatch:', LEFT(SHA2(DATABASE(), 256), 40)), 10)").Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return errors.New("migration lock unavailable")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, "DO RELEASE_LOCK(CONCAT('nodewatch:', LEFT(SHA2(DATABASE(), 256), 40)))")
	}()
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INT PRIMARY KEY, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		return err
	}
	var version int
	if err = conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return errors.New("database schema newer than this binary")
	}
	if version == 1 {
		return nil
	}
	for _, statement := range strings.Split(initialSchema, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err = conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", 1)
	return err
}

func Canonical(s metrics.Snapshot) ([]byte, [32]byte, error) {
	s.CollectedAt = s.CollectedAt.UTC()
	data, err := json.Marshal(s)
	return data, sha256.Sum256(data), err
}

func (s *Store) Accept(ctx context.Context, snapshot metrics.Snapshot, seen time.Time) (storage.Host, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	data, hash, err := Canonical(snapshot)
	if err != nil {
		return storage.Host{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storage.Host{}, false, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "INSERT INTO snapshots (host_id, collected_ns, payload_hash, payload) VALUES (?, ?, ?, ?)", snapshot.NodeID, snapshot.CollectedAt.UnixNano(), hash[:], data)
	duplicate := false
	if err != nil {
		var dbErr *driver.MySQLError
		if !errors.As(err, &dbErr) || dbErr.Number != 1062 {
			return storage.Host{}, false, err
		}
		var storedHash []byte
		if err = tx.QueryRowContext(ctx, "SELECT payload_hash FROM snapshots WHERE host_id = ? AND collected_ns = ?", snapshot.NodeID, snapshot.CollectedAt.UnixNano()).Scan(&storedHash); err != nil {
			return storage.Host{}, false, err
		}
		if !bytes.Equal(storedHash, hash[:]) {
			return storage.Host{}, false, storage.ErrConflict
		}
		duplicate = true
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO hosts (host_id, latest_ns, last_seen_ns) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE latest_ns = GREATEST(latest_ns, ?), last_seen_ns = GREATEST(last_seen_ns, ?)`, snapshot.NodeID, snapshot.CollectedAt.UnixNano(), seen.UnixNano(), snapshot.CollectedAt.UnixNano(), seen.UnixNano())
	if err != nil {
		return storage.Host{}, false, err
	}
	// A current locking read avoids a stale REPEATABLE READ snapshot created
	// by the duplicate hash check. Shared locks avoid snapshot lock upgrades
	// when multiple identical retries are waiting on the same host row.
	host, err := scanHost(tx.QueryRowContext(ctx, hostSelect+" WHERE h.host_id = ? FOR SHARE", snapshot.NodeID))
	if err != nil {
		return storage.Host{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return storage.Host{}, false, err
	}
	return host, duplicate, nil
}

const hostSelect = `SELECT h.host_id, s.payload, h.last_seen_ns FROM hosts h
 JOIN snapshots s ON s.host_id = h.host_id AND s.collected_ns = h.latest_ns`

type scanner interface{ Scan(...any) error }

func scanHost(row scanner) (storage.Host, error) {
	h, _, err := scanHostSize(row)
	return h, err
}

func scanHostSize(row scanner) (storage.Host, int, error) {
	var h storage.Host
	var payload []byte
	var seen int64
	if err := row.Scan(&h.ID, &payload, &seen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return h, 0, storage.ErrNotFound
		}
		return h, 0, err
	}
	if err := json.Unmarshal(payload, &h.Latest); err != nil {
		return h, 0, fmt.Errorf("invalid stored snapshot: %w", err)
	}
	h.LastSeen = time.Unix(0, seen).UTC()
	h.Status = "unknown"
	return h, len(payload), nil
}

func (s *Store) Host(ctx context.Context, id string) (storage.Host, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return scanHost(s.db.QueryRowContext(ctx, hostSelect+" WHERE h.host_id = ?", id))
}
func (s *Store) List(ctx context.Context, after string, limit int) ([]storage.Host, error) {
	if limit < 1 || limit > 200 {
		return nil, errors.New("invalid host query limit")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, hostSelect+" WHERE h.host_id > ? ORDER BY h.host_id ASC LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := make([]storage.Host, 0)
	bytesRead := 0
	for rows.Next() {
		h, size, err := scanHostSize(rows)
		if err != nil {
			return nil, err
		}
		if bytesRead+size > maxPageBytes && len(hosts) > 0 {
			break
		}
		bytesRead += size
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}
func (s *Store) Query(ctx context.Context, id string, q storage.Query) ([]metrics.Snapshot, error) {
	if q.Limit < 1 || q.Limit > 1000 || !q.From.Before(q.To) || q.To.Sub(q.From) > 31*24*time.Hour {
		return nil, errors.New("invalid history bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	query := "SELECT payload FROM snapshots WHERE host_id = ? AND collected_ns >= ? AND collected_ns < ?"
	args := []any{id, q.From.UnixNano(), q.To.UnixNano()}
	if q.Before != nil {
		query += " AND collected_ns < ?"
		args = append(args, q.Before.UnixNano())
	}
	query += " ORDER BY collected_ns DESC LIMIT ?"
	args = append(args, q.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]metrics.Snapshot, 0)
	bytesRead := 0
	for rows.Next() {
		var data []byte
		var m metrics.Snapshot
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if bytesRead+len(data) > maxPageBytes && len(result) > 0 {
			break
		}
		bytesRead += len(data)
		if err = json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// Pages stop before exceeding this payload budget as well as their row limit.
// A continuation cursor always points at the last returned row.
const maxPageBytes = 8 * 1024 * 1024
