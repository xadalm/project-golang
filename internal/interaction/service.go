package interaction

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInteractionNotFound = errors.New("interaction not found")
	ErrInvalidParticipant  = errors.New("source and target must be different")
)

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) React(ctx context.Context, sourceID, targetID string, action Status) (Result, error) {
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return Result{}, ErrInvalidParticipant
	}

	current, err := s.read(ctx, sourceID, targetID)
	if err != nil {
		return Result{}, fmt.Errorf("read current interaction: %w", err)
	}
	opposite, err := s.read(ctx, targetID, sourceID)
	if err != nil {
		return Result{}, fmt.Errorf("read opposite interaction: %w", err)
	}

	next, transitionErr := ProcessReaction(current, action)
	if errors.Is(transitionErr, ErrAlreadyExists) {
		return s.result(current, opposite, false), nil
	}
	if transitionErr != nil {
		return Result{}, transitionErr
	}

	if err := s.repository.Save(ctx, next); err != nil {
		return Result{}, fmt.Errorf("save interaction: %w", err)
	}

	return s.result(next, opposite, true), nil
}

func (s *Service) read(ctx context.Context, sourceID, targetID string) (Reaction, error) {
	reaction, err := s.repository.Get(ctx, sourceID, targetID)
	if errors.Is(err, ErrInteractionNotFound) {
		return Reaction{SourceID: sourceID, TargetID: targetID, Status: StatusNone}, nil
	}
	if err != nil {
		return Reaction{}, err
	}
	return reaction, nil
}

func (s *Service) result(reaction, opposite Reaction, changed bool) Result {
	return Result{
		Reaction: reaction,
		Opposite: opposite,
		Matched:  reaction.Status == StatusPending && opposite.Status == StatusPending,
		Changed:  changed,
	}
}
