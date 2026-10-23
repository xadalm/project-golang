package interaction

import (
	"errors"
	"reflect"
	"testing"
)

func TestProcessReaction(t *testing.T) {
	tests := []struct {
		name   string
		state  Reaction
		action Status
		want   Reaction
		err    error
	}{
		{"first reaction", Reaction{SourceID: "u1", TargetID: "u2", Status: StatusNone}, StatusPending, Reaction{SourceID: "u1", TargetID: "u2", Status: StatusPending}, nil},
		{"accepted transition", Reaction{SourceID: "u1", TargetID: "u2", Status: StatusPending}, StatusAccepted, Reaction{SourceID: "u1", TargetID: "u2", Status: StatusAccepted}, nil},
		{"repeat", Reaction{SourceID: "u1", TargetID: "u2", Status: StatusPending}, StatusPending, Reaction{SourceID: "u1", TargetID: "u2", Status: StatusPending}, ErrAlreadyExists},
		{"forbidden transition", Reaction{SourceID: "u1", TargetID: "u2", Status: StatusNone}, StatusAccepted, Reaction{}, ErrInvalidAction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProcessReaction(tt.state, tt.action)
			if !errors.Is(err, tt.err) || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, %v; want %#v, %v", got, err, tt.want, tt.err)
			}
		})
	}
}
