package interaction_test

import (
	"context"
	"errors"
	"testing"

	"example.com/linkup/internal/interaction"
	"example.com/linkup/internal/memory"
)

func TestServiceReactSequence(t *testing.T) {
	repository := memory.NewInteractions()
	service := interaction.NewService(repository)

	first, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if err != nil || !first.Changed || first.Matched {
		t.Fatalf("first reaction: %#v, %v", first, err)
	}
	repeat, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if err != nil || repeat.Changed {
		t.Fatalf("repeat: %#v, %v", repeat, err)
	}
	response, err := service.React(context.Background(), "u2", "u1", interaction.StatusPending)
	if err != nil || !response.Changed || !response.Matched {
		t.Fatalf("response: %#v, %v", response, err)
	}
	again, err := service.React(context.Background(), "u2", "u1", interaction.StatusPending)
	if err != nil || again.Changed || !again.Matched {
		t.Fatalf("response repeat: %#v, %v", again, err)
	}
	stored, err := repository.Get(context.Background(), "u1", "u2")
	if err != nil || stored.Status != interaction.StatusPending {
		t.Fatalf("stored first reaction: %#v, %v", stored, err)
	}
}

func TestServiceDoesNotSaveAfterReadFailureOrOnInvalidTransition(t *testing.T) {
	repository := memory.NewInteractions()
	service := interaction.NewService(repository)
	_, err := service.React(context.Background(), "u1", "u2", interaction.StatusAccepted)
	if !errors.Is(err, interaction.ErrInvalidAction) {
		t.Fatalf("expected invalid transition, got %v", err)
	}
	_, err = repository.Get(context.Background(), "u1", "u2")
	if !errors.Is(err, interaction.ErrInteractionNotFound) {
		t.Fatalf("state changed after rejected transition: %v", err)
	}
}

func TestServiceSaveFailurePreservesState(t *testing.T) {
	repository := memory.NewInteractions()
	service := interaction.NewService(repository)
	saveErr := errors.New("write failed")
	repository.SetSaveError(saveErr)
	_, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if !errors.Is(err, saveErr) {
		t.Fatalf("expected save error, got %v", err)
	}
	repository.SetSaveError(nil)
	result, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if err != nil || !result.Changed {
		t.Fatalf("retry: %#v, %v", result, err)
	}
}

type countingRepository struct {
	getErrAt  int
	getCalls  int
	saveCalls int
	reaction  *interaction.Reaction
}

func (r *countingRepository) Get(_ context.Context, sourceID, targetID string) (interaction.Reaction, error) {
	r.getCalls++
	if r.getErrAt != 0 && r.getCalls == r.getErrAt {
		return interaction.Reaction{}, errors.New("read failed")
	}
	if r.reaction != nil && r.reaction.SourceID == sourceID && r.reaction.TargetID == targetID {
		return *r.reaction, nil
	}
	return interaction.Reaction{
		SourceID: sourceID,
		TargetID: targetID,
		Status:   interaction.StatusNone,
	}, nil
}

func (r *countingRepository) Save(_ context.Context, reaction interaction.Reaction) error {
	r.saveCalls++
	copy := reaction
	r.reaction = &copy
	return nil
}

func TestServiceStopsBeforeSaveWhenReadFails(t *testing.T) {
	repository := &countingRepository{getErrAt: 2}
	service := interaction.NewService(repository)

	_, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if err == nil || repository.saveCalls != 0 {
		t.Fatalf("got err=%v, saveCalls=%d; expected read error and no save", err, repository.saveCalls)
	}
}

func TestServiceDoesNotSaveOnRepeat(t *testing.T) {
	repository := &countingRepository{}
	service := interaction.NewService(repository)

	_, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if err != nil || repository.saveCalls != 1 {
		t.Fatalf("first reaction: err=%v, saveCalls=%d", err, repository.saveCalls)
	}
	result, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if err != nil || result.Changed || repository.saveCalls != 1 {
		t.Fatalf("repeat: result=%#v, err=%v, saveCalls=%d", result, err, repository.saveCalls)
	}
}
