package memory

import (
	"errors"

	"example.com/linkup/internal/discovery"
)

var ErrProfileNotFound = errors.New("profile not found in memory storage")

type ProfilesRepository struct {
	profiles map[string]discovery.Profile
}

func NewProfiles(seed []discovery.Profile) *ProfilesRepository {
	m := make(map[string]discovery.Profile, len(seed))
	for _, p := range seed {
		m[p.ID] = p
	}

	return &ProfilesRepository{profiles: m}
}

func (r *ProfilesRepository) Get(id string) (discovery.Profile, error) {
	p, ok := r.profiles[id]
	if !ok {
		return discovery.Profile{}, ErrProfileNotFound
	}
	return p, nil
}

func (r *ProfilesRepository) List() ([]discovery.Profile, error) {
	result := make([]discovery.Profile, 0, len(r.profiles))
	for _, p := range r.profiles {
		result = append(result, p)
	}

	return result, nil
}
