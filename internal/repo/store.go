package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/internal/libraries/store"
)

const reposKey = "tracked_repos"

// Store manages tracked repo state.
type Store struct {
	store store.Store
}

// NewStore creates a Store backed by the given key-value store.
func NewStore(s store.Store) *Store {
	return &Store{store: s}
}

// List returns all tracked repos keyed by name.
func (s *Store) List(ctx context.Context) (map[string]TrackedRepo, error) {
	data, err := s.store.Get(ctx, reposKey)
	if err != nil {
		return nil, fmt.Errorf("read tracked repos: %w", err)
	}
	if data == nil {
		return make(map[string]TrackedRepo), nil
	}
	var repos map[string]TrackedRepo
	if err := json.Unmarshal(data, &repos); err != nil {
		return nil, fmt.Errorf("unmarshal tracked repos: %w", err)
	}
	return repos, nil
}

// Get returns a single tracked repo by name, or nil if not found.
func (s *Store) Get(ctx context.Context, name string) (*TrackedRepo, error) {
	repos, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	r, ok := repos[name]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

// Put creates or updates a tracked repo.
func (s *Store) Put(ctx context.Context, r TrackedRepo) error {
	repos, err := s.List(ctx)
	if err != nil {
		return err
	}
	repos[r.Name] = r
	return s.save(ctx, repos)
}

// Delete removes a tracked repo by name.
func (s *Store) Delete(ctx context.Context, name string) error {
	repos, err := s.List(ctx)
	if err != nil {
		return err
	}
	delete(repos, name)
	return s.save(ctx, repos)
}

// Resolve finds the repo to operate on. If name is non-empty, it looks up that
// specific repo. If empty and there's exactly one tracked repo, it returns that
// one. Returns an error if ambiguous or not found.
func (s *Store) Resolve(ctx context.Context, name string) (*TrackedRepo, error) {
	repos, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("no tracked repos — use repo_add first")
	}

	if name != "" {
		r, ok := repos[name]
		if !ok {
			return nil, fmt.Errorf("no tracked repo named %q", name)
		}
		return &r, nil
	}

	if len(repos) == 1 {
		for _, r := range repos {
			return &r, nil
		}
	}

	var names []string
	for n := range repos {
		names = append(names, n)
	}
	return nil, fmt.Errorf("multiple tracked repos — specify which one: %v", names)
}

func (s *Store) save(ctx context.Context, repos map[string]TrackedRepo) error {
	data, err := json.Marshal(repos)
	if err != nil {
		return fmt.Errorf("marshal tracked repos: %w", err)
	}
	return s.store.Set(ctx, reposKey, data)
}
