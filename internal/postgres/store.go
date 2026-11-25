package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"example.com/linkup/internal/discovery"
)

var (
	ErrDatabaseFailure      = errors.New("database failure")
	ErrDatabaseTimeout      = errors.New("database operation timed out")
	ErrTransactionRetryable = errors.New("transaction can be retried")
	ErrCommitUnconfirmed    = errors.New("database commit outcome is unconfirmed")
)

//go:embed migrations/001_initial.sql
var initialMigration string

type Store struct {
	db           *sql.DB
	queryTimeout time.Duration
	lockTimeout  time.Duration
}

func Open(ctx context.Context, dsn string, maxConnections int, queryTimeout, lockTimeout time.Duration) (*Store, error) {
	if strings.TrimSpace(dsn) == "" || maxConnections < 2 || queryTimeout <= 0 || lockTimeout <= 0 {
		return nil, errors.New("database URL, at least two connections, and positive query/lock timeouts are required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, classifyError("connect to PostgreSQL", err)
	}
	return &Store{db: db, queryTimeout: queryTimeout, lockTimeout: lockTimeout}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	txCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return classifyError("begin migrations", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(txCtx, `SELECT pg_advisory_xact_lock(72623859790382856)`); err != nil {
		return classifyError("lock migrations", err)
	}

	if _, err := tx.ExecContext(txCtx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version integer PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return classifyError("create migration table", err)
	}
	var applied bool
	if err := tx.QueryRowContext(txCtx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = 1)`).Scan(&applied); err != nil {
		return classifyError("read migration version", err)
	}
	if !applied {
		for _, statement := range strings.Split(initialMigration, ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if _, err := tx.ExecContext(txCtx, statement); err != nil {
				return classifyError("apply migration 1", err)
			}
		}
		if _, err := tx.ExecContext(txCtx, `INSERT INTO schema_migrations (version) VALUES (1)`); err != nil {
			return classifyError("record migration 1", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", ErrCommitUnconfirmed, classifyError("commit migrations", err))
	}
	return nil
}

func (s *Store) SeedProfiles(ctx context.Context, profiles []discovery.Profile) error {
	txCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return classifyError("begin profile fixtures", err)
	}
	defer tx.Rollback()
	for _, profile := range profiles {
		interests, err := encodeInterests(profile.Interests)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(txCtx, `
			INSERT INTO profiles (user_id, active, interests, version)
			VALUES ($1, $2, $3::jsonb, 1)
			ON CONFLICT (user_id) DO NOTHING`, profile.ID, profile.Active, interests); err != nil {
			return classifyError("seed profile", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", ErrCommitUnconfirmed, classifyError("commit profile fixtures", err))
	}
	return nil
}

func (s *Store) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.queryTimeout)
}

func setTransactionTimeouts(ctx context.Context, tx *sql.Tx, lockTimeout time.Duration) error {
	lockValue := fmt.Sprintf("%dms", max(1, lockTimeout.Milliseconds()))
	_, err := tx.ExecContext(ctx, `SELECT set_config('lock_timeout', $1, true)`, lockValue)
	if err != nil {
		return classifyError("set transaction lock timeout", err)
	}
	return nil
}

func classifyError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01", "55P03":
			return fmt.Errorf("%w: %s (%s)", ErrTransactionRetryable, operation, pgErr.Code)
		case "57014":
			return fmt.Errorf("%w: %s (%s)", ErrDatabaseTimeout, operation, pgErr.Code)
		}
		return fmt.Errorf("%w: %s (%s, constraint %s): %w", ErrDatabaseFailure, operation, pgErr.Code, pgErr.ConstraintName, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrDatabaseTimeout, err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%w: %s: %w", ErrDatabaseFailure, operation, err)
}

func encodeInterests(interests []string) (string, error) {
	data, err := json.Marshal(interests)
	if err != nil {
		return "", fmt.Errorf("encode interests: %w", err)
	}
	return string(data), nil
}
