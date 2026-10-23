package interaction

import "errors"

var (
	ErrAlreadyExists = errors.New("interaction already exists in this state")
	ErrInvalidAction = errors.New("invalid transition action")
)

// ProcessReaction — чистая функция перехода состояния
// currentState — то, что нашли в базе (или пустое, если взаимодействия не было)
// action — какую реакцию хочет совершить пользователь
func ProcessReaction(currentState Reaction, action Status) (Reaction, error) {
	if action != StatusPending && action != StatusAccepted {
		return Reaction{}, ErrInvalidAction
	}

	// Правило no-op: если состояние уже совпадает с тем, что пытаются сделать
	if currentState.Status == action {
		return currentState, ErrAlreadyExists
	}

	// Логика переходов (например, pending -> accepted)
	if currentState.Status == StatusNone && action == StatusPending {
		return Reaction{
			SourceID: currentState.SourceID,
			TargetID: currentState.TargetID,
			Status:   action,
		}, nil
	}

	if currentState.Status == StatusPending && action == StatusAccepted {
		return Reaction{
			SourceID: currentState.SourceID,
			TargetID: currentState.TargetID,
			Status:   StatusAccepted,
		}, nil
	}

	return Reaction{}, ErrInvalidAction
}
