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

	if s.verifier != nil {
		for _, id := range []string{sourceID, targetID} {
			participant, err := s.verifier.GetParticipant(ctx, id)
			if err != nil {
				return Result{}, fmt.Errorf("verify participant: %w", err)
			}
			if !participant.Active {
				return Result{}, ErrParticipantInactive
			}
		}
	}

	current, opposite, changed, err := s.repository.UpdatePair(ctx, sourceID, targetID,
		func(current, opposite Reaction) (Reaction, bool, error) {
			next, transitionErr := ProcessReaction(current, action)
			if errors.Is(transitionErr, ErrAlreadyExists) {
				return current, false, nil
			}
			if transitionErr != nil {
				return Reaction{}, false, transitionErr
			}
			return next, true, nil
		})
	if err != nil {
		return Result{}, fmt.Errorf("update interaction pair: %w", err)
	}

	return s.result(current, opposite, changed), nil
}

func (s *Service) Inspect(ctx context.Context, sourceID, targetID string) (Result, error) {
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return Result{}, ErrInvalidParticipant
	}
	current, opposite, err := s.repository.GetPair(ctx, sourceID, targetID)
	if err != nil {
		return Result{}, fmt.Errorf("read interaction pair: %w", err)
	}
	return s.result(current, opposite, false), nil
}

func (s *Service) result(reaction, opposite Reaction, changed bool) Result {
	matched := reaction.Status == StatusPending && opposite.Status == StatusPending
	return Result{
		Reaction:   reaction,
		Opposite:   opposite,
		Matched:    matched,
		MatchedNow: changed && matched,
		Changed:    changed,
	}
}
