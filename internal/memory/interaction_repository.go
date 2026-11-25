package memory

import (
	"context"
	"errors"
	"sync"

	"example.com/linkup/internal/interaction"
)

var ErrInteractionStorageUnavailable = errors.New("interaction storage unavailable")

type interactionPair struct {
	reactions map[string]interaction.Reaction
}

type InteractionRepository struct {
	mu      sync.RWMutex
	pairs   map[pairKey]interactionPair
	saveErr error
}

type pairKey struct {
	left  string
	right string
}

func NewInteractions() *InteractionRepository {
	return &InteractionRepository{pairs: make(map[pairKey]interactionPair)}
}

func (r *InteractionRepository) Save(ctx context.Context, reaction interaction.Reaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if reaction.SourceID == "" || reaction.TargetID == "" || reaction.SourceID == reaction.TargetID {
		return errors.New("invalid interaction participants")
	}
	if reaction.Status != interaction.StatusPending && reaction.Status != interaction.StatusAccepted {
		return errors.New("invalid interaction status")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.saveErr != nil {
		return r.saveErr
	}

	key := makePairKey(reaction.SourceID, reaction.TargetID)
	pair := r.pairs[key]
	if pair.reactions == nil {
		pair.reactions = make(map[string]interaction.Reaction)
	}
	pair.reactions[reaction.SourceID] = reaction
	r.pairs[key] = pair
	return nil
}

func (r *InteractionRepository) Get(ctx context.Context, sourceID, targetID string) (interaction.Reaction, error) {
	if err := ctx.Err(); err != nil {
		return interaction.Reaction{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	pair, ok := r.pairs[makePairKey(sourceID, targetID)]
	if !ok {
		return interaction.Reaction{}, interaction.ErrInteractionNotFound
	}
	reaction, ok := pair.reactions[sourceID]
	if !ok {
		return interaction.Reaction{}, interaction.ErrInteractionNotFound
	}
	return reaction, nil
}

func (r *InteractionRepository) GetPair(ctx context.Context, sourceID, targetID string) (interaction.Reaction, interaction.Reaction, error) {
	if err := ctx.Err(); err != nil {
		return interaction.Reaction{}, interaction.Reaction{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	pair := r.pairs[makePairKey(sourceID, targetID)]
	return reactionOrNone(pair, sourceID, targetID), reactionOrNone(pair, targetID, sourceID), nil
}

func (r *InteractionRepository) UpdatePair(
	ctx context.Context,
	sourceID, targetID string,
	update func(interaction.Reaction, interaction.Reaction) (interaction.Reaction, bool, error),
) (interaction.Reaction, interaction.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return interaction.Reaction{}, interaction.Reaction{}, false, err
	}
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return interaction.Reaction{}, interaction.Reaction{}, false, errors.New("invalid interaction participants")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return interaction.Reaction{}, interaction.Reaction{}, false, err
	}

	key := makePairKey(sourceID, targetID)
	pair := r.pairs[key]
	current := reactionOrNone(pair, sourceID, targetID)
	opposite := reactionOrNone(pair, targetID, sourceID)
	next, changed, err := update(current, opposite)
	if err != nil {
		return interaction.Reaction{}, interaction.Reaction{}, false, err
	}
	if !changed {
		return current, opposite, false, nil
	}
	if next.SourceID != sourceID || next.TargetID != targetID ||
		(next.Status != interaction.StatusPending && next.Status != interaction.StatusAccepted) {
		return interaction.Reaction{}, interaction.Reaction{}, false, errors.New("invalid interaction update")
	}
	if r.saveErr != nil {
		return interaction.Reaction{}, interaction.Reaction{}, false, r.saveErr
	}
	if pair.reactions == nil {
		pair.reactions = make(map[string]interaction.Reaction)
	}
	pair.reactions[sourceID] = next
	r.pairs[key] = pair
	return next, opposite, true, nil
}

func (r *InteractionRepository) SetSaveError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveErr = err
}

func reactionOrNone(pair interactionPair, sourceID, targetID string) interaction.Reaction {
	if reaction, ok := pair.reactions[sourceID]; ok {
		return reaction
	}
	return interaction.Reaction{SourceID: sourceID, TargetID: targetID, Status: interaction.StatusNone}
}

func makePairKey(sourceID, targetID string) pairKey {
	if sourceID < targetID {
		return pairKey{left: sourceID, right: targetID}
	}
	return pairKey{left: targetID, right: sourceID}
}
