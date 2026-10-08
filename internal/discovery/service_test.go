package discovery

import (
	"errors"
	"testing"
)

var ErrNotFound = errors.New("profile not found")

type profileReaderStub struct {
	getErr  error
	listErr error
}

func (s *profileReaderStub) Get(id string) (Profile, error) {
	if s.getErr != nil {
		return Profile{}, s.getErr
	}
	return Profile{ID: id, Active: true, Interests: []string{"go"}}, nil
}

func (s *profileReaderStub) List() ([]Profile, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return []Profile{}, nil
}

func TestService_Recommend_DependencyFailure(t *testing.T) {
	t.Run("returns wrapped error when Get fails", func(t *testing.T) {
		stub := &profileReaderStub{getErr: ErrNotFound}
		svc := NewService(stub)

		_, err := Recommend(svc, "u1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected cause %v, got %v", ErrNotFound, err)
		}
	})
}
