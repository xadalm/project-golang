package interaction

import "context"

// Repository описывает контракт сохранения и чтения взаимодействий
type Repository interface {
	ReadPair(ctx context.Context, sourceID, targetID string) (Pair, error)
	// UpdatePair runs update atomically for both directions; update must be short and side-effect free.
	UpdatePair(ctx context.Context, sourceID, targetID string, update func(Pair) (Pair, error)) (Pair, bool, error)
}
