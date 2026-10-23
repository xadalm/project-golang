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

	r.mu.Lock()
	defer r.mu.Unlock()
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

func (r *InteractionRepository) SetSaveError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveErr = err
}

func makePairKey(sourceID, targetID string) pairKey {
	if sourceID < targetID {
		return pairKey{left: sourceID, right: targetID}
	}
	return pairKey{left: targetID, right: sourceID}
}
