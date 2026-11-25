package memory

import (
	"testing"

	"example.com/linkup/internal/discovery"
)

func TestProfilesRepositoryCopiesNestedInterests(t *testing.T) {
	seed := []discovery.Profile{{ID: "u1", Active: true, Interests: []string{"go"}}}
	repository := NewProfiles(seed)
	seed[0].Interests[0] = "changed"

	profile, err := repository.Get("u1")
	if err != nil {
		t.Fatal(err)
	}
	profile.Interests[0] = "also changed"
	listed, err := repository.List()
	if err != nil {
		t.Fatal(err)
	}
	listed[0].Interests[0] = "changed again"

	stored, err := repository.Get("u1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Interests[0] != "go" {
		t.Fatalf("repository data changed without a write: %#v", stored.Interests)
	}
}
