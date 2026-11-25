//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"example.com/linkup/internal/demo"
	"example.com/linkup/internal/interaction"
	"example.com/linkup/internal/postgres"
	"example.com/linkup/internal/profiles"
)

type postgresFixture struct {
	admin       *sql.DB
	store       *postgres.Store
	schema      string
	application string
}

func openPostgresFixture(t *testing.T) *postgresFixture {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("connect to test database: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	schema := "lr4_" + suffix
	application := "lr4_test_" + suffix
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	})

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", application)
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, parsed.String(), 8, 10*time.Second, 8*time.Second)
	if err != nil {
		t.Fatalf("open PostgreSQL store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close PostgreSQL store: %v", err)
		}
	})
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("run migration: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("run migration again: %v", err)
	}
	if err := store.SeedProfiles(ctx, demo.Profiles()); err != nil {
		t.Fatalf("seed profiles: %v", err)
	}
	return &postgresFixture{admin: admin, store: store, schema: schema, application: application}
}

func TestPostgresAtomicMatchAndRollback(t *testing.T) {
	fixture := openPostgresFixture(t)
	ctx := context.Background()
	service := interaction.NewService(fixture.store, nil)

	if _, err := service.React(ctx, "u1", "u2", interaction.StatusPending); err != nil {
		t.Fatalf("first reaction: %v", err)
	}
	result, err := service.React(ctx, "u2", "u1", interaction.StatusPending)
	if err != nil {
		t.Fatalf("reciprocal reaction: %v", err)
	}
	if !result.MatchedNow || !result.Matched {
		t.Fatalf("reciprocal reaction did not create match: %+v", result)
	}
	pair, err := service.Inspect(ctx, "u1", "u2")
	if err != nil || !pair.Matched {
		t.Fatalf("read match after commit: result=%+v err=%v", pair, err)
	}

	if _, err := service.React(ctx, "u1", "u3", interaction.StatusPending); err != nil {
		t.Fatalf("seed first reaction for rollback test: %v", err)
	}
	if _, err := fixture.admin.ExecContext(ctx, `
		CREATE FUNCTION fail_match_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'injected transaction failure' USING ERRCODE = 'P0001';
		END; $$`); err != nil {
		t.Fatalf("create rollback trigger function: %v", err)
	}
	if _, err := fixture.admin.ExecContext(ctx, `CREATE TRIGGER fail_match_insert BEFORE INSERT ON `+fixture.schema+`.matches
		FOR EACH ROW EXECUTE FUNCTION `+fixture.schema+`.fail_match_insert()`); err != nil {
		t.Fatalf("install rollback trigger: %v", err)
	}
	if _, err := service.React(ctx, "u3", "u1", interaction.StatusPending); !errors.Is(err, postgres.ErrDatabaseFailure) {
		t.Fatalf("expected injected database failure, got %v", err)
	}
	pair, err = service.Inspect(ctx, "u3", "u1")
	if err != nil {
		t.Fatalf("inspect rolled-back pair: %v", err)
	}
	if pair.Reaction.Status != interaction.StatusNone || pair.Opposite.Status != interaction.StatusPending || pair.Matched {
		t.Fatalf("failed transaction left partial state: %+v", pair)
	}
	var rows int
	if err := fixture.admin.QueryRowContext(ctx, `SELECT count(*) FROM `+fixture.schema+`.matches WHERE user_low = 'u1' AND user_high = 'u3'`).Scan(&rows); err != nil {
		t.Fatalf("check match rollback: %v", err)
	}
	if rows != 0 {
		t.Fatalf("match row survived rollback: %d", rows)
	}
}

func TestPostgresBlockAndLikeSerializeInBothOrders(t *testing.T) {
	fixture := openPostgresFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	service := interaction.NewService(fixture.store, nil)

	if _, err := service.React(ctx, "u2", "u1", interaction.StatusPending); err != nil {
		t.Fatalf("seed pending reaction: %v", err)
	}
	reactionLock := time.Now().UnixNano()
	if _, err := fixture.admin.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION wait_on_reaction() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NEW;
		END; $$`, reactionLock)); err != nil {
		t.Fatalf("create reaction barrier function: %v", err)
	}
	if _, err := fixture.admin.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER wait_on_reaction BEFORE INSERT ON %s.reactions
		FOR EACH ROW EXECUTE FUNCTION %s.wait_on_reaction()`, fixture.schema, fixture.schema)); err != nil {
		t.Fatalf("install reaction barrier: %v", err)
	}
	conn, err := fixture.admin.Conn(ctx)
	if err != nil {
		t.Fatalf("open advisory lock connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, reactionLock); err != nil {
		t.Fatalf("hold reaction advisory lock: %v", err)
	}
	likeDone := make(chan error, 1)
	go func() {
		_, err := service.React(ctx, "u1", "u2", interaction.StatusPending)
		likeDone <- err
	}()
	waitForActivity(t, ctx, fixture, "%pg_advisory_xact_lock%")

	blockDone := make(chan error, 1)
	go func() {
		_, err := service.Block(ctx, "u1", "u2")
		blockDone <- err
	}()
	waitForActivity(t, ctx, fixture, "%FOR UPDATE%")
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, reactionLock); err != nil {
		t.Fatalf("release reaction advisory lock: %v", err)
	}
	if err := <-likeDone; err != nil {
		t.Fatalf("Like should commit before the waiting block: %v", err)
	}
	if err := <-blockDone; err != nil {
		t.Fatalf("block after Like: %v", err)
	}
	pair, err := service.Inspect(ctx, "u1", "u2")
	if err != nil || !pair.Blocked || pair.Matched {
		t.Fatalf("block did not deactivate the newly committed match: pair=%+v err=%v", pair, err)
	}

	if _, err := fixture.admin.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER wait_on_reaction ON %s.reactions`, fixture.schema)); err != nil {
		t.Fatalf("remove reaction barrier trigger: %v", err)
	}
	if _, err := fixture.admin.ExecContext(ctx, fmt.Sprintf(`DROP FUNCTION %s.wait_on_reaction()`, fixture.schema)); err != nil {
		t.Fatalf("remove reaction barrier: %v", err)
	}
	if _, err := fixture.admin.ExecContext(ctx, `INSERT INTO `+fixture.schema+`.interaction_pairs (user_low, user_high) VALUES ('u3', 'u4')`); err != nil {
		t.Fatalf("create pair lock row for block-first case: %v", err)
	}
	blockLock := time.Now().UnixNano()
	if _, err := fixture.admin.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION wait_on_block() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			PERFORM pg_advisory_xact_lock(%d);
			RETURN NEW;
		END; $$`, blockLock)); err != nil {
		t.Fatalf("create block barrier function: %v", err)
	}
	if _, err := fixture.admin.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER wait_on_block BEFORE UPDATE OF blocked_by ON %s.interaction_pairs
		FOR EACH ROW EXECUTE FUNCTION %s.wait_on_block()`, fixture.schema, fixture.schema)); err != nil {
		t.Fatalf("install block barrier: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, blockLock); err != nil {
		t.Fatalf("hold block advisory lock: %v", err)
	}
	blockDone = make(chan error, 1)
	go func() {
		_, err := service.Block(ctx, "u3", "u4")
		blockDone <- err
	}()
	waitForActivity(t, ctx, fixture, "%pg_advisory_xact_lock%")
	likeDone = make(chan error, 1)
	go func() {
		_, err := service.React(ctx, "u3", "u4", interaction.StatusPending)
		likeDone <- err
	}()
	waitForActivity(t, ctx, fixture, "%FOR UPDATE%")
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, blockLock); err != nil {
		t.Fatalf("release block advisory lock: %v", err)
	}
	if err := <-blockDone; err != nil {
		t.Fatalf("block should commit before the waiting Like: %v", err)
	}
	if err := <-likeDone; !errors.Is(err, interaction.ErrPairBlocked) {
		t.Fatalf("Like after block should be rejected, got %v", err)
	}
	pair, err = service.Inspect(ctx, "u3", "u4")
	if err != nil || !pair.Blocked || pair.Reaction.Status != interaction.StatusNone {
		t.Fatalf("Like changed state after block: pair=%+v err=%v", pair, err)
	}
}

func waitForActivity(t *testing.T, ctx context.Context, fixture *postgresFixture, queryPattern string) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := fixture.admin.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name = $1 AND wait_event_type = 'Lock' AND query ILIKE $2
			)`, fixture.application, queryPattern).Scan(&waiting)
		if err != nil {
			t.Fatalf("observe PostgreSQL lock wait: %v", err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for PostgreSQL query %q to block: %v", queryPattern, ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPostgresConcurrentProfileEditsUseVersion(t *testing.T) {
	fixture := openPostgresFixture(t)
	ctx := context.Background()
	service := profiles.NewService(fixture.store)
	start := make(chan struct{})
	type outcome struct {
		profile profiles.Profile
		err     error
	}
	results := make(chan outcome, 2)
	var workers sync.WaitGroup
	for _, interests := range [][]string{{"go", "systems"}, {"go", "music"}} {
		interests := interests
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			profile, err := service.Update(ctx, "u1", "u1", 1, interests)
			results <- outcome{profile: profile, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			if result.profile.Version != 2 {
				t.Errorf("successful edit has version %d, want 2", result.profile.Version)
			}
		case errors.Is(result.err, profiles.ErrVersionConflict):
			conflicts++
		default:
			t.Errorf("unexpected profile update error: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("same-version concurrent edits: successes=%d conflicts=%d", successes, conflicts)
	}
	current, err := service.Get(ctx, "u1")
	if err != nil || current.Version != 2 {
		t.Fatalf("read updated profile: profile=%+v err=%v", current, err)
	}
}

func TestPostgresSchemaConstraints(t *testing.T) {
	fixture := openPostgresFixture(t)
	ctx := context.Background()
	assertSQLState := func(t *testing.T, query string, args ...any) string {
		t.Helper()
		_, err := fixture.admin.ExecContext(ctx, query, args...)
		if err == nil {
			t.Fatal("expected schema constraint violation")
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Fatalf("expected PostgreSQL error, got %T: %v", err, err)
		}
		return pgErr.Code
	}

	if code := assertSQLState(t, `INSERT INTO `+fixture.schema+`.interaction_pairs (user_low, user_high) VALUES ('u2', 'u1')`); code != "23514" {
		t.Errorf("noncanonical pair SQLSTATE = %s, want 23514", code)
	}
	if code := assertSQLState(t, `INSERT INTO `+fixture.schema+`.interaction_pairs (user_low, user_high) VALUES ('missing', 'u1')`); code != "23503" {
		t.Errorf("unknown profile SQLSTATE = %s, want 23503", code)
	}
	if code := assertSQLState(t, `UPDATE `+fixture.schema+`.profiles SET interests = '[42]'::jsonb WHERE user_id = 'u1'`); code != "23514" {
		t.Errorf("non-string profile interest SQLSTATE = %s, want 23514", code)
	}
	if _, err := fixture.admin.ExecContext(ctx, `INSERT INTO `+fixture.schema+`.interaction_pairs (user_low, user_high) VALUES ('u1', 'u2')`); err != nil {
		t.Fatalf("insert canonical pair: %v", err)
	}
	if code := assertSQLState(t, `INSERT INTO `+fixture.schema+`.reactions (source_id, target_id, status) VALUES ('u1', 'u2', 'invalid')`); code != "23514" {
		t.Errorf("invalid reaction status SQLSTATE = %s, want 23514", code)
	}
	if _, err := fixture.admin.ExecContext(ctx, `INSERT INTO `+fixture.schema+`.reactions (source_id, target_id, status) VALUES ('u1', 'u2', 'pending')`); err != nil {
		t.Fatalf("insert valid reaction: %v", err)
	}
	if code := assertSQLState(t, `INSERT INTO `+fixture.schema+`.reactions (source_id, target_id, status) VALUES ('u1', 'u2', 'accepted')`); code != "23505" {
		t.Errorf("duplicate directed reaction SQLSTATE = %s, want 23505", code)
	}
	if _, err := fixture.admin.ExecContext(ctx, `INSERT INTO `+fixture.schema+`.matches (user_low, user_high) VALUES ('u1', 'u2')`); err != nil {
		t.Fatalf("insert match: %v", err)
	}
	if code := assertSQLState(t, `INSERT INTO `+fixture.schema+`.matches (user_low, user_high) VALUES ('u1', 'u2')`); code != "23505" {
		t.Errorf("duplicate match SQLSTATE = %s, want 23505", code)
	}
	if _, err := fixture.admin.ExecContext(ctx, `INSERT INTO `+fixture.schema+`.interaction_pairs (user_low, user_high) VALUES ('u1', 'u5')`); err != nil {
		t.Fatalf("insert pair for match-state constraint: %v", err)
	}
	if code := assertSQLState(t, `INSERT INTO `+fixture.schema+`.matches (user_low, user_high, active) VALUES ('u1', 'u5', false)`); code != "23514" {
		t.Errorf("inactive match without deactivation time SQLSTATE = %s, want 23514", code)
	}
	if code := assertSQLState(t, `UPDATE `+fixture.schema+`.profiles SET version = 0 WHERE user_id = 'u1'`); code != "23514" {
		t.Errorf("nonpositive profile version SQLSTATE = %s, want 23514", code)
	}
}
