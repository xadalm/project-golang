package memory

import (
	"context"
	"errors"
	"sync"

	"example.com/linkup/internal/discovery"
	"example.com/linkup/internal/profiles"
)

var ErrProfileNotFound = errors.New("profile not found in memory storage")

type ProfilesRepository struct {
	mu       sync.RWMutex
	profiles map[string]discovery.Profile
	versions map[string]int64
}

func NewProfiles(seed []discovery.Profile) *ProfilesRepository {
	m := make(map[string]discovery.Profile, len(seed))
	versions := make(map[string]int64, len(seed))
	for _, p := range seed {
		m[p.ID] = cloneProfile(p)
		versions[p.ID] = 1
	}

	return &ProfilesRepository{profiles: m, versions: versions}
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

func (r *ProfilesRepository) GetProfile(ctx context.Context, id string) (profiles.Profile, error) {
	if err := ctx.Err(); err != nil {
		return profiles.Profile{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	profile, ok := r.profiles[id]
	if !ok {
		return profiles.Profile{}, profiles.ErrNotFound
	}
	return profiles.Profile{
		ID: profile.ID, Active: profile.Active, Interests: append([]string{}, profile.Interests...),
		Version: r.versions[id],
	}, nil
}

func (r *ProfilesRepository) ListProfiles(ctx context.Context) ([]profiles.Profile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]profiles.Profile, 0, len(r.profiles))
	for _, profile := range r.profiles {
		result = append(result, profiles.Profile{
			ID: profile.ID, Active: profile.Active, Interests: append([]string{}, profile.Interests...),
			Version: r.versions[profile.ID],
		})
	}
	return result, nil
}

func (r *ProfilesRepository) UpdateProfile(ctx context.Context, id string, expectedVersion int64, interests []string) (profiles.Profile, error) {
	if err := ctx.Err(); err != nil {
		return profiles.Profile{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return profiles.Profile{}, err
	}
	profile, ok := r.profiles[id]
	if !ok {
		return profiles.Profile{}, profiles.ErrNotFound
	}
	if r.versions[id] != expectedVersion {
		return profiles.Profile{}, profiles.ErrVersionConflict
	}
	profile.Interests = append([]string{}, interests...)
	r.profiles[id] = profile
	r.versions[id]++
	return profiles.Profile{
		ID: profile.ID, Active: profile.Active, Interests: append([]string{}, profile.Interests...),
		Version: r.versions[id],
	}, nil
}
