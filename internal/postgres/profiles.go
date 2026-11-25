package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"example.com/linkup/internal/profiles"
)

var _ profiles.Repository = (*Store)(nil)

func (s *Store) GetProfile(ctx context.Context, id string) (profiles.Profile, error) {
	queryCtx, cancel := s.operationContext(ctx)
	defer cancel()
	var profile profiles.Profile
	var interests []byte
	err := s.db.QueryRowContext(queryCtx, `
		SELECT user_id, active, interests, version
		FROM profiles WHERE user_id = $1`, id).Scan(&profile.ID, &profile.Active, &interests, &profile.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return profiles.Profile{}, profiles.ErrNotFound
	}
	if err != nil {
		return profiles.Profile{}, classifyError("read profile", err)
	}
	if err := json.Unmarshal(interests, &profile.Interests); err != nil {
		return profiles.Profile{}, fmt.Errorf("%w: decode stored profile interests: %v", ErrDatabaseFailure, err)
	}
	if profile.Interests == nil {
		profile.Interests = []string{}
	}
	return profile, nil
}

func (s *Store) ListProfiles(ctx context.Context) ([]profiles.Profile, error) {
	queryCtx, cancel := s.operationContext(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(queryCtx, `
		SELECT user_id, active, interests, version
		FROM profiles ORDER BY user_id`)
	if err != nil {
		return nil, classifyError("list profiles", err)
	}
	result := make([]profiles.Profile, 0)
	for rows.Next() {
		var profile profiles.Profile
		var interests []byte
		if err := rows.Scan(&profile.ID, &profile.Active, &interests, &profile.Version); err != nil {
			_ = rows.Close()
			return nil, classifyError("scan profile", err)
		}
		if err := json.Unmarshal(interests, &profile.Interests); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: decode stored profile interests: %v", ErrDatabaseFailure, err)
		}
		if profile.Interests == nil {
			profile.Interests = []string{}
		}
		result = append(result, profile)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, classifyError("iterate profiles", err)
	}
	if err := rows.Close(); err != nil {
		return nil, classifyError("close profile rows", err)
	}
	return result, nil
}

func (s *Store) UpdateProfile(ctx context.Context, id string, expectedVersion int64, interests []string) (profiles.Profile, error) {
	txCtx, cancel := s.operationContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return profiles.Profile{}, classifyError("begin profile update", err)
	}
	defer tx.Rollback()
	if err := setTransactionTimeouts(txCtx, tx, s.lockTimeout); err != nil {
		return profiles.Profile{}, err
	}

	var profile profiles.Profile
	var currentInterests []byte
	err = tx.QueryRowContext(txCtx, `
		SELECT user_id, active, interests, version
		FROM profiles WHERE user_id = $1 FOR UPDATE`, id).
		Scan(&profile.ID, &profile.Active, &currentInterests, &profile.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return profiles.Profile{}, profiles.ErrNotFound
	}
	if err != nil {
		return profiles.Profile{}, classifyError("lock profile for edit", err)
	}
	if profile.Version != expectedVersion {
		return profiles.Profile{}, profiles.ErrVersionConflict
	}
	encoded, err := encodeInterests(interests)
	if err != nil {
		return profiles.Profile{}, err
	}
	err = tx.QueryRowContext(txCtx, `
		UPDATE profiles
		SET interests = $2::jsonb, version = version + 1
		WHERE user_id = $1
		RETURNING version`, id, encoded).Scan(&profile.Version)
	if err != nil {
		return profiles.Profile{}, classifyError("write profile edit", err)
	}
	profile.Interests = append([]string{}, interests...)
	if err := tx.Commit(); err != nil {
		return profiles.Profile{}, fmt.Errorf("%w: %w", ErrCommitUnconfirmed, classifyError("commit profile edit", err))
	}
	return profile, nil
}
