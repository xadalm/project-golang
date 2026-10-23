package interaction

type Status string

const (
	StatusNone     Status = "none"
	StatusPending  Status = "pending"  // Первая реакция (например, Like)
	StatusAccepted Status = "accepted" // Взаимность (Match)
)

type Reaction struct {
	SourceID string
	TargetID string
	Status   Status
}

type Result struct {
	Reaction Reaction
	Opposite Reaction
	Matched  bool
	Changed  bool
}
