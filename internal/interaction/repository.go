package interaction

import "context"

// Repository описывает контракт сохранения и чтения взаимодействий
type Repository interface {
	Save(ctx context.Context, reaction Reaction) error
	Get(ctx context.Context, sourceID, targetID string) (Reaction, error)
	GetPair(ctx context.Context, sourceID, targetID string) (Reaction, Reaction, error)
	// UpdatePair runs update atomically for both directions; update must be short and side-effect free.
	UpdatePair(ctx context.Context, sourceID, targetID string, update func(Reaction, Reaction) (Reaction, bool, error)) (Reaction, Reaction, bool, error)
}
