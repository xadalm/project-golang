package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"example.com/linkup/internal/interaction"
)

var _ interaction.Repository = (*Store)(nil)

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) ReadPair(ctx context.Context, sourceID, targetID string) (interaction.Pair, error) {
	queryCtx, cancel := s.operationContext(ctx)
	defer cancel()
	pair, err := readPair(queryCtx, s.db, sourceID, targetID)
	if err != nil {
		return interaction.Pair{}, classifyError("read interaction pair", err)
	}
	return pair, nil
}

func (s *Store) UpdatePair(
	ctx context.Context,
	sourceID, targetID string,
	update func(interaction.Pair) (interaction.Pair, error),
) (interaction.Pair, bool, error) {
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return interaction.Pair{}, false, interaction.ErrInvalidParticipant
	}
	txCtx, cancel := s.operationContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return interaction.Pair{}, false, classifyError("begin pair transaction", err)
	}
	defer tx.Rollback()
	if err := setTransactionTimeouts(txCtx, tx, s.lockTimeout); err != nil {
		return interaction.Pair{}, false, err
	}
	if _, err := tx.ExecContext(txCtx, `
		INSERT INTO interaction_pairs (user_low, user_high)
		VALUES (LEAST($1::text, $2::text), GREATEST($1::text, $2::text))
		ON CONFLICT (user_low, user_high) DO NOTHING`, sourceID, targetID); err != nil {
		return interaction.Pair{}, false, classifyError("create pair lock row", err)
	}
	var blockedBy string
	if err := tx.QueryRowContext(txCtx, `
		SELECT COALESCE(blocked_by, '')
		FROM interaction_pairs
		WHERE user_low = LEAST($1::text, $2::text)
		  AND user_high = GREATEST($1::text, $2::text)
		FOR UPDATE`, sourceID, targetID).Scan(&blockedBy); err != nil {
		return interaction.Pair{}, false, classifyError("lock interaction pair", err)
	}
	current, err := readPair(txCtx, tx, sourceID, targetID)
	if err != nil {
		return interaction.Pair{}, false, classifyError("read locked interaction pair", err)
	}
	next, err := update(current)
	if err != nil {
		return interaction.Pair{}, false, err
	}
	if !validPair(next, sourceID, targetID) {
		return interaction.Pair{}, false, errors.New("invalid interaction pair update")
	}
	if equalPair(current, next) {
		if err := tx.Commit(); err != nil {
			return interaction.Pair{}, false, fmt.Errorf("%w: %w", ErrCommitUnconfirmed, classifyError("commit no-op pair transaction", err))
		}
		return current, false, nil
	}
	if next.BlockedBy != blockedBy && next.BlockedBy != "" {
		if _, err := tx.ExecContext(txCtx, `
			UPDATE interaction_pairs SET blocked_by = $3
			WHERE user_low = LEAST($1::text, $2::text)
			  AND user_high = GREATEST($1::text, $2::text)
			  AND blocked_by IS NULL`, sourceID, targetID, next.BlockedBy); err != nil {
			return interaction.Pair{}, false, classifyError("persist pair block", err)
		}
	}
	if err := persistReaction(txCtx, tx, current.Reaction, next.Reaction); err != nil {
		return interaction.Pair{}, false, err
	}
	if err := persistReaction(txCtx, tx, current.Opposite, next.Opposite); err != nil {
		return interaction.Pair{}, false, err
	}
	if current.Matched != next.Matched {
		if next.Matched {
			if _, err := tx.ExecContext(txCtx, `
				INSERT INTO matches (user_low, user_high, active)
				VALUES (LEAST($1::text, $2::text), GREATEST($1::text, $2::text), true)`,
				sourceID, targetID); err != nil {
				return interaction.Pair{}, false, classifyError("create match", err)
			}
		} else {
			result, err := tx.ExecContext(txCtx, `
				UPDATE matches SET active = false, deactivated_at = now()
				WHERE user_low = LEAST($1::text, $2::text)
				  AND user_high = GREATEST($1::text, $2::text)
				  AND active`, sourceID, targetID)
			if err != nil {
				return interaction.Pair{}, false, classifyError("deactivate match", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return interaction.Pair{}, false, classifyError("check match deactivation", err)
			}
			if affected != 1 {
				return interaction.Pair{}, false, fmt.Errorf("%w: active match row missing", ErrDatabaseFailure)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return interaction.Pair{}, false, fmt.Errorf("%w: %w", ErrCommitUnconfirmed, classifyError("commit interaction pair", err))
	}
	return next, true, nil
}

func readPair(ctx context.Context, queryer rowQueryer, sourceID, targetID string) (interaction.Pair, error) {
	var blockedBy string
	var matched bool
	var currentStatus, oppositeStatus string
	err := queryer.QueryRowContext(ctx, `
		SELECT COALESCE(p.blocked_by, ''),
		       COALESCE(m.active, false),
		       COALESCE(r.status, 'none'),
		       COALESCE(o.status, 'none')
		FROM (SELECT $1::text AS source_id, $2::text AS target_id) q
		LEFT JOIN interaction_pairs p
		  ON p.user_low = LEAST(q.source_id, q.target_id)
		 AND p.user_high = GREATEST(q.source_id, q.target_id)
		LEFT JOIN matches m
		  ON m.user_low = p.user_low AND m.user_high = p.user_high
		LEFT JOIN reactions r ON r.source_id = q.source_id AND r.target_id = q.target_id
		LEFT JOIN reactions o ON o.source_id = q.target_id AND o.target_id = q.source_id`,
		sourceID, targetID).Scan(&blockedBy, &matched, &currentStatus, &oppositeStatus)
	if err != nil {
		return interaction.Pair{}, err
	}
	return interaction.Pair{
		Reaction: interaction.Reaction{SourceID: sourceID, TargetID: targetID, Status: interaction.Status(currentStatus)},
		Opposite: interaction.Reaction{SourceID: targetID, TargetID: sourceID, Status: interaction.Status(oppositeStatus)},
		Matched:  matched, BlockedBy: blockedBy,
	}, nil
}

func persistReaction(ctx context.Context, tx *sql.Tx, current, next interaction.Reaction) error {
	if current == next {
		return nil
	}
	if next.Status == interaction.StatusNone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM reactions WHERE source_id = $1 AND target_id = $2`, current.SourceID, current.TargetID); err != nil {
			return classifyError("delete reaction", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reactions (source_id, target_id, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (source_id, target_id) DO UPDATE SET status = EXCLUDED.status`,
		next.SourceID, next.TargetID, next.Status); err != nil {
		return classifyError("persist reaction", err)
	}
	return nil
}

func validPair(pair interaction.Pair, sourceID, targetID string) bool {
	return pair.Reaction.SourceID == sourceID && pair.Reaction.TargetID == targetID &&
		pair.Opposite.SourceID == targetID && pair.Opposite.TargetID == sourceID &&
		validStatus(pair.Reaction.Status) && validStatus(pair.Opposite.Status) &&
		(pair.BlockedBy == "" || pair.BlockedBy == sourceID || pair.BlockedBy == targetID) &&
		(!pair.Matched || (pair.BlockedBy == "" &&
			pair.Reaction.Status == interaction.StatusPending && pair.Opposite.Status == interaction.StatusPending))
}

func validStatus(status interaction.Status) bool {
	return status == interaction.StatusNone || status == interaction.StatusPending || status == interaction.StatusAccepted
}

func equalPair(left, right interaction.Pair) bool {
	return left.Reaction == right.Reaction && left.Opposite == right.Opposite &&
		left.Matched == right.Matched && left.BlockedBy == right.BlockedBy
}
