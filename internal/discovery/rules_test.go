package discovery

import (
	"reflect"
	"testing"
)

func TestDiscover(t *testing.T) {
	subject := Profile{ID: "u1", Active: true, Interests: []string{"go", "backend"}}

	t.Run("happy path - finds matching active candidate", func(t *testing.T) {
		candidates := []Profile{
			{ID: "u2", Active: true, Interests: []string{"go", "frontend"}},
		}

		got := Discover(subject, candidates)
		want := []string{"u2"}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("filters out self and inactive candidates", func(t *testing.T) {
		candidates := []Profile{
			{ID: "u1", Active: true, Interests: []string{"go"}},
			{ID: "u3", Active: false, Interests: []string{"go"}},
		}

		got := Discover(subject, candidates)

		if len(got) != 0 {
			t.Fatalf("expected empty slice, got %v", got)
		}
	})

	t.Run("handles multiple interest matches without duplicating ID", func(t *testing.T) {
		candidates := []Profile{
			{ID: "u4", Active: true, Interests: []string{"go", "backend"}},
		}

		got := Discover(subject, candidates)
		want := []string{"u4"}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("does not mutate input slice or profiles", func(t *testing.T) {
		candidates := []Profile{
			{ID: "u2", Active: true, Interests: []string{"go"}},
		}

		originalSubject := subject
		originalCandidate := candidates[0]

		_ = Discover(subject, candidates)

		if !reflect.DeepEqual(subject, originalSubject) || !reflect.DeepEqual(candidates[0], originalCandidate) {
			t.Fatal("input data was mutated during Discover call")
		}
	})
}
