package profiles

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound        = errors.New("profile not found")
	ErrForbidden       = errors.New("profile edit forbidden")
	ErrVersionConflict = errors.New("profile version conflict")
	ErrInvalidProfile  = errors.New("invalid profile")
)

type Profile struct {
	ID        string
	Active    bool
	Interests []string
	Version   int64
}

type Repository interface {
	GetProfile(ctx context.Context, id string) (Profile, error)
	ListProfiles(ctx context.Context) ([]Profile, error)
	UpdateProfile(ctx context.Context, id string, expectedVersion int64, interests []string) (Profile, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Get(ctx context.Context, id string) (Profile, error) {
	return s.repository.GetProfile(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]Profile, error) {
	return s.repository.ListProfiles(ctx)
}

func (s *Service) Update(ctx context.Context, actorID, profileID string, expectedVersion int64, interests []string) (Profile, error) {
	if expectedVersion < 1 || len(interests) > 32 {
		return Profile{}, ErrInvalidProfile
	}
	normalized := make([]string, len(interests))
	seen := make(map[string]struct{}, len(interests))
	for i, interest := range interests {
		interest = strings.TrimSpace(interest)
		if interest == "" || len(interest) > 64 {
			return Profile{}, ErrInvalidProfile
		}
		if _, exists := seen[interest]; exists {
			return Profile{}, ErrInvalidProfile
		}
		seen[interest] = struct{}{}
		normalized[i] = interest
	}
	if _, err := s.repository.GetProfile(ctx, profileID); err != nil {
		return Profile{}, err
	}
	if actorID != profileID {
		return Profile{}, ErrForbidden
	}
	profile, err := s.repository.UpdateProfile(ctx, profileID, expectedVersion, normalized)
	if err != nil {
		return Profile{}, fmt.Errorf("update profile: %w", err)
	}
	return profile, nil
}
