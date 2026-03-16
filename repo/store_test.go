package repo_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tclaw/libraries/store"
	"tclaw/repo"

	"github.com/stretchr/testify/require"
)

func TestStore_PutAndGet(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	tracked := repo.TrackedRepo{
		Name:    "myrepo",
		URL:     "https://github.com/user/repo",
		Branch:  "main",
		AddedAt: time.Now(),
	}
	require.NoError(t, s.Put(ctx, tracked))

	got, err := s.Get(ctx, "myrepo")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "myrepo", got.Name)
	require.Equal(t, "https://github.com/user/repo", got.URL)
	require.Equal(t, "main", got.Branch)
}

func TestStore_GetNotFound(t *testing.T) {
	s := newStore(t)

	got, err := s.Get(context.Background(), "missing")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStore_Delete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "todelete"}))
	require.NoError(t, s.Delete(ctx, "todelete"))

	got, err := s.Get(ctx, "todelete")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStore_List(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "alpha", URL: "https://a"}))
	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "beta", URL: "https://b"}))

	repos, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, repos, 2)
	require.Contains(t, repos, "alpha")
	require.Contains(t, repos, "beta")
}

func TestStore_ListEmpty(t *testing.T) {
	s := newStore(t)

	repos, err := s.List(context.Background())
	require.NoError(t, err)
	require.NotNil(t, repos)
	require.Empty(t, repos)
}

func TestStore_Resolve_ByName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "alpha"}))
	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "beta"}))

	got, err := s.Resolve(ctx, "alpha")
	require.NoError(t, err)
	require.Equal(t, "alpha", got.Name)
}

func TestStore_Resolve_SingleRepoWithoutName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "onlyone"}))

	got, err := s.Resolve(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "onlyone", got.Name)
}

func TestStore_Resolve_AmbiguousWithoutName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "alpha"}))
	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "beta"}))

	_, err := s.Resolve(ctx, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple tracked repos")
}

func TestStore_Resolve_NoRepos(t *testing.T) {
	s := newStore(t)

	_, err := s.Resolve(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no tracked repos")
}

func TestStore_Resolve_NameNotFound(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "exists"}))

	_, err := s.Resolve(ctx, "nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), `no tracked repo named "nope"`)
}

func TestStore_PutOverwrites(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "myrepo", Branch: "main"}))
	require.NoError(t, s.Put(ctx, repo.TrackedRepo{Name: "myrepo", Branch: "develop"}))

	got, err := s.Get(ctx, "myrepo")
	require.NoError(t, err)
	require.Equal(t, "develop", got.Branch)
}

func newStore(t *testing.T) *repo.Store {
	t.Helper()
	s, err := store.NewFS(filepath.Join(t.TempDir(), "state"))
	require.NoError(t, err)
	return repo.NewStore(s)
}
