package interaction

import "context"

// Repository описывает контракт сохранения и чтения взаимодействий
type Repository interface {
	Save(ctx context.Context, reaction Reaction) error
	Get(ctx context.Context, sourceID, targetID string) (Reaction, error)
}
