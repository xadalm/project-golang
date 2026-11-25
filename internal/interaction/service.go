package interaction

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInteractionNotFound = errors.New("interaction not found")
	ErrInvalidParticipant  = errors.New("source and target must be different")
	ErrParticipantNotFound = errors.New("participant not found")
	ErrParticipantInactive = errors.New("participant is inactive")
	ErrDependencyFailure   = errors.New("profile dependency failed")
	ErrPairBlocked         = errors.New("interaction pair is blocked")
)

type ParticipantVerifier interface {
	GetParticipant(ctx context.Context, id string) (Participant, error)
}

type Service struct {
	repository Repository
	verifier   ParticipantVerifier
}

func NewService(repository Repository, verifier ParticipantVerifier) *Service {
	return &Service{repository: repository, verifier: verifier}
}

func (s *Service) React(ctx context.Context, sourceID, targetID string, action Status) (Result, error) {
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return Result{}, ErrInvalidParticipant
	}

	if action != StatusPending && action != StatusAccepted {
		return Result{}, ErrInvalidAction
	}

	if err := s.verifyParticipants(ctx, sourceID, targetID); err != nil {
		return Result{}, err
	}

	pair, changed, err := s.repository.UpdatePair(ctx, sourceID, targetID,
		func(pair Pair) (Pair, error) {
			if pair.BlockedBy != "" {
				return Pair{}, ErrPairBlocked
			}
			next, transitionErr := ProcessReaction(pair.Reaction, action)
			if errors.Is(transitionErr, ErrAlreadyExists) {
				return pair, nil
			}
			if transitionErr != nil {
				return Pair{}, transitionErr
			}
			pair.Reaction = next
			pair.Matched = pair.Reaction.Status == StatusPending && pair.Opposite.Status == StatusPending
			return pair, nil
		})
	if err != nil {
		return Result{}, fmt.Errorf("update interaction pair: %w", err)
	}

	return s.result(pair, changed), nil
}

func (s *Service) Block(ctx context.Context, sourceID, targetID string) (Result, error) {
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return Result{}, ErrInvalidParticipant
	}
	if err := s.verifyParticipants(ctx, sourceID, targetID); err != nil {
		return Result{}, err
	}
	pair, changed, err := s.repository.UpdatePair(ctx, sourceID, targetID, func(pair Pair) (Pair, error) {
		if pair.BlockedBy != "" {
			return pair, nil
		}
		pair.BlockedBy = sourceID
		pair.Matched = false
		return pair, nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("block interaction pair: %w", err)
	}
	return s.result(pair, changed), nil
}

func (s *Service) Inspect(ctx context.Context, sourceID, targetID string) (Result, error) {
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return Result{}, ErrInvalidParticipant
	}
	pair, err := s.repository.ReadPair(ctx, sourceID, targetID)
	if err != nil {
		return Result{}, fmt.Errorf("read interaction pair: %w", err)
	}
	return s.result(pair, false), nil
}

func (s *Service) verifyParticipants(ctx context.Context, sourceID, targetID string) error {
	if s.verifier == nil {
		return nil
	}
	for _, id := range []string{sourceID, targetID} {
		participant, err := s.verifier.GetParticipant(ctx, id)
		if err != nil {
			return fmt.Errorf("verify participant: %w", err)
		}
		if !participant.Active {
			return ErrParticipantInactive
		}
	}
	return nil
}

func (s *Service) result(pair Pair, changed bool) Result {
	return Result{
		Reaction:   pair.Reaction,
		Opposite:   pair.Opposite,
		Matched:    pair.Matched,
		MatchedNow: changed && pair.Matched,
		Blocked:    pair.BlockedBy != "",
		Changed:    changed,
	}
}
