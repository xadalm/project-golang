package memory

import (
	"errors"
	"sync"

	"example.com/linkup/internal/discovery"
)

var ErrProfileNotFound = errors.New("profile not found in memory storage")

type ProfilesRepository struct {
	mu       sync.RWMutex
	profiles map[string]discovery.Profile
}

func NewProfiles(seed []discovery.Profile) *ProfilesRepository {
	m := make(map[string]discovery.Profile, len(seed))
	for _, p := range seed {
		m[p.ID] = cloneProfile(p)
	}

	return &ProfilesRepository{profiles: m}
}

func (r *ProfilesRepository) Get(id string) (discovery.Profile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.profiles[id]
	if !ok {
		return discovery.Profile{}, ErrProfileNotFound
	}
	return cloneProfile(p), nil
}

func (r *ProfilesRepository) List() ([]discovery.Profile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]discovery.Profile, 0, len(r.profiles))
	for _, p := range r.profiles {
		result = append(result, cloneProfile(p))
	}
	return result, nil
}

func cloneProfile(profile discovery.Profile) discovery.Profile {
	profile.Interests = append([]string(nil), profile.Interests...)
	return profile
}
