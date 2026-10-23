package memory

import (
	"context"
	"errors"
	"testing"

	"example.com/linkup/internal/interaction"
)

func TestInteractionRepositoryStoresOnePairWithTwoDirections(t *testing.T) {
	repository := NewInteractions()
	ctx := context.Background()

	first := interaction.Reaction{SourceID: "u1", TargetID: "u2", Status: interaction.StatusPending}
	second := interaction.Reaction{SourceID: "u2", TargetID: "u1", Status: interaction.StatusPending}
	if err := repository.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(ctx, second); err != nil {
		t.Fatal(err)
	}

	gotFirst, err := repository.Get(ctx, "u1", "u2")
	if err != nil || gotFirst != first {
		t.Fatalf("first direction: %#v, %v", gotFirst, err)
	}
	gotSecond, err := repository.Get(ctx, "u2", "u1")
	if err != nil || gotSecond != second {
		t.Fatalf("second direction: %#v, %v", gotSecond, err)
	}
}

func TestInteractionRepositoryDistinguishesMissingState(t *testing.T) {
	repository := NewInteractions()
	_, err := repository.Get(context.Background(), "u1", "u2")
	if !errors.Is(err, interaction.ErrInteractionNotFound) {
		t.Fatalf("got %v, want ErrInteractionNotFound", err)
	}
}
