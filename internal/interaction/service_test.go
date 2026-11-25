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
	service := interaction.NewService(repository, nil)

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
	service := interaction.NewService(repository, nil)
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
	service := interaction.NewService(repository, nil)
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

func TestServiceStopsBeforeSaveWhenReadFails(t *testing.T) {
	readErr := errors.New("read failed")
	repository := failedPairReadRepository{InteractionRepository: memory.NewInteractions(), err: readErr}
	service := interaction.NewService(repository, nil)
	_, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if !errors.Is(err, readErr) {
		t.Fatalf("got err=%v; expected wrapped read error", err)
	}
}

func TestServiceDoesNotSaveOnRepeat(t *testing.T) {
	repository := memory.NewInteractions()
	service := interaction.NewService(repository, nil)
	_, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if err != nil {
		t.Fatalf("first reaction: %v", err)
	}
	repository.SetSaveError(errors.New("unexpected save"))
	result, err := service.React(context.Background(), "u1", "u2", interaction.StatusPending)
	if err != nil || result.Changed {
		t.Fatalf("repeat: result=%#v, err=%v", result, err)
	}
}

type failedPairReadRepository struct {
	*memory.InteractionRepository
	err error
}

func (r failedPairReadRepository) UpdatePair(context.Context, string, string, func(interaction.Reaction, interaction.Reaction) (interaction.Reaction, bool, error)) (interaction.Reaction, interaction.Reaction, bool, error) {
	return interaction.Reaction{}, interaction.Reaction{}, false, r.err
}

func TestConcurrentOppositeLikesCreateOneMatch(t *testing.T) {
	repository := memory.NewInteractions()
	service := interaction.NewService(repository, nil)
	start := make(chan struct{})
	results := make(chan interaction.Result, 2)
	errorsFound := make(chan error, 2)
	for _, users := range [][2]string{{"u1", "u2"}, {"u2", "u1"}} {
		users := users
		go func() {
			<-start
			result, err := service.React(context.Background(), users[0], users[1], interaction.StatusPending)
			results <- result
			errorsFound <- err
		}()
	}
	close(start)
	matches := 0
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatal(err)
		}
		if (<-results).MatchedNow {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("got MatchedNow %d times, want exactly once", matches)
	}
	state, err := service.Inspect(context.Background(), "u1", "u2")
	if err != nil || !state.Matched {
		t.Fatalf("final pair state: %#v, %v", state, err)
	}
}
