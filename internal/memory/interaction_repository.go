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
	matched   bool
	blockedBy string
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
	if reaction.Status != interaction.StatusPending && reaction.Status != interaction.StatusAccepted {
		return errors.New("invalid interaction status")
	}
	_, _, err := r.UpdatePair(ctx, reaction.SourceID, reaction.TargetID, func(pair interaction.Pair) (interaction.Pair, error) {
		if pair.BlockedBy != "" {
			return interaction.Pair{}, interaction.ErrPairBlocked
		}
		pair.Reaction = reaction
		pair.Matched = pair.Reaction.Status == interaction.StatusPending && pair.Opposite.Status == interaction.StatusPending
		return pair, nil
	})
	return err
}

func (r *InteractionRepository) Get(ctx context.Context, sourceID, targetID string) (interaction.Reaction, error) {
	if err := ctx.Err(); err != nil {
		return interaction.Reaction{}, err
	}

	pair, err := r.ReadPair(ctx, sourceID, targetID)
	if err != nil {
		return interaction.Reaction{}, err
	}
	if pair.Reaction.Status == interaction.StatusNone {
		return interaction.Reaction{}, interaction.ErrInteractionNotFound
	}
	return pair.Reaction, nil
}

func (r *InteractionRepository) GetPair(ctx context.Context, sourceID, targetID string) (interaction.Reaction, interaction.Reaction, error) {
	pair, err := r.ReadPair(ctx, sourceID, targetID)
	return pair.Reaction, pair.Opposite, err
}

func (r *InteractionRepository) ReadPair(ctx context.Context, sourceID, targetID string) (interaction.Pair, error) {
	if err := ctx.Err(); err != nil {
		return interaction.Pair{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	stored := r.pairs[makePairKey(sourceID, targetID)]
	return pairState(stored, sourceID, targetID), nil
}

func (r *InteractionRepository) UpdatePair(
	ctx context.Context,
	sourceID, targetID string,
	update func(interaction.Pair) (interaction.Pair, error),
) (interaction.Pair, bool, error) {
	if err := ctx.Err(); err != nil {
		return interaction.Pair{}, false, err
	}
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return interaction.Pair{}, false, errors.New("invalid interaction participants")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return interaction.Pair{}, false, err
	}

	key := makePairKey(sourceID, targetID)
	stored := r.pairs[key]
	current := pairState(stored, sourceID, targetID)
	next, err := update(current)
	if err != nil {
		return interaction.Pair{}, false, err
	}
	if !validPair(next, sourceID, targetID) {
		return interaction.Pair{}, false, errors.New("invalid interaction pair update")
	}
	if equalPair(current, next) {
		return current, false, nil
	}
	if r.saveErr != nil {
		return interaction.Pair{}, false, r.saveErr
	}
	if stored.reactions == nil {
		stored.reactions = make(map[string]interaction.Reaction)
	}
	if next.Reaction.Status == interaction.StatusNone {
		delete(stored.reactions, sourceID)
	} else {
		stored.reactions[sourceID] = next.Reaction
	}
	if next.Opposite.Status == interaction.StatusNone {
		delete(stored.reactions, targetID)
	} else {
		stored.reactions[targetID] = next.Opposite
	}
	stored.matched = next.Matched
	stored.blockedBy = next.BlockedBy
	r.pairs[key] = stored
	return next, true, nil
}

func (r *InteractionRepository) SetSaveError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveErr = err
}

func pairState(stored interactionPair, sourceID, targetID string) interaction.Pair {
	return interaction.Pair{
		Reaction:  reactionOrNone(stored, sourceID, targetID),
		Opposite:  reactionOrNone(stored, targetID, sourceID),
		Matched:   stored.matched,
		BlockedBy: stored.blockedBy,
	}
}

func reactionOrNone(pair interactionPair, sourceID, targetID string) interaction.Reaction {
	if reaction, ok := pair.reactions[sourceID]; ok {
		return reaction
	}
	return interaction.Reaction{SourceID: sourceID, TargetID: targetID, Status: interaction.StatusNone}
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

func makePairKey(sourceID, targetID string) pairKey {
	if sourceID < targetID {
		return pairKey{left: sourceID, right: targetID}
	}
	return pairKey{left: targetID, right: sourceID}
}
