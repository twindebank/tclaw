package repotools_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/repo"
	"tclaw/tool/repotools"

	"github.com/stretchr/testify/require"
)

func TestRepoAdd_Basic(t *testing.T) {
	h, _, userDir := setup(t)

	result := callTool(t, h, "repo_add", map[string]string{
		"name": "myrepo",
		"url":  "https://github.com/user/repo",
	})

	var got map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	require.Equal(t, "myrepo", got["name"])
	require.Equal(t, "https://github.com/user/repo", got["url"])
	require.Equal(t, "main", got["branch"], "should default to main")

	// Directories should be created.
	_, err := os.Stat(filepath.Join(userDir, "repos", "myrepo", "bare"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(userDir, "repos", "myrepo", "checkout"))
	require.NoError(t, err)
}

func TestRepoAdd_CustomBranch(t *testing.T) {
	h, _, _ := setup(t)

	result := callTool(t, h, "repo_add", map[string]string{
		"name":   "myrepo",
		"url":    "https://github.com/user/repo",
		"branch": "develop",
	})

	var got map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	require.Equal(t, "develop", got["branch"])
}

func TestRepoAdd_Validation(t *testing.T) {
	h, _, _ := setup(t)

	t.Run("rejects empty name", func(t *testing.T) {
		callToolExpectError(t, h, "repo_add", map[string]string{
			"name": "",
			"url":  "https://github.com/user/repo",
		})
	})

	t.Run("rejects empty url", func(t *testing.T) {
		callToolExpectError(t, h, "repo_add", map[string]string{
			"name": "test",
			"url":  "",
		})
	})

	t.Run("rejects non-HTTPS url", func(t *testing.T) {
		callToolExpectError(t, h, "repo_add", map[string]string{
			"name": "test",
			"url":  "http://github.com/user/repo",
		})
	})

	t.Run("rejects invalid name characters", func(t *testing.T) {
		callToolExpectError(t, h, "repo_add", map[string]string{
			"name": "my repo!",
			"url":  "https://github.com/user/repo",
		})
	})

	t.Run("rejects name starting with hyphen", func(t *testing.T) {
		callToolExpectError(t, h, "repo_add", map[string]string{
			"name": "-bad",
			"url":  "https://github.com/user/repo",
		})
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		callTool(t, h, "repo_add", map[string]string{
			"name": "dup",
			"url":  "https://github.com/user/repo",
		})
		callToolExpectError(t, h, "repo_add", map[string]string{
			"name": "dup",
			"url":  "https://github.com/user/other",
		})
	})
}

func TestRepoList_Empty(t *testing.T) {
	h, _, _ := setup(t)

	result := callTool(t, h, "repo_list", map[string]any{})

	var got map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	repos := got["repos"].([]any)
	require.Empty(t, repos)
	require.Contains(t, got["message"], "No tracked repos")
}

func TestRepoList_ShowsAddedRepos(t *testing.T) {
	h, _, _ := setup(t)

	callTool(t, h, "repo_add", map[string]string{
		"name": "alpha",
		"url":  "https://github.com/user/alpha",
	})
	callTool(t, h, "repo_add", map[string]string{
		"name": "beta",
		"url":  "https://github.com/user/beta",
	})

	result := callTool(t, h, "repo_list", map[string]any{})

	var got map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	repos := got["repos"].([]any)
	require.Len(t, repos, 2)
	require.Contains(t, got["message"], "2 tracked repo(s)")
}

func TestRepoList_ShowsNotSyncedStatus(t *testing.T) {
	h, _, _ := setup(t)

	callTool(t, h, "repo_add", map[string]string{
		"name": "fresh",
		"url":  "https://github.com/user/fresh",
	})

	result := callTool(t, h, "repo_list", map[string]any{})

	var got map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	repos := got["repos"].([]any)
	first := repos[0].(map[string]any)
	require.Equal(t, "never", first["last_synced"])
	require.Equal(t, "(not synced)", first["last_seen_commit"])
}

func TestRepoRemove(t *testing.T) {
	h, repoStore, userDir := setup(t)

	callTool(t, h, "repo_add", map[string]string{
		"name": "gonzo",
		"url":  "https://github.com/user/gonzo",
	})

	// Verify it exists.
	got, err := repoStore.Get(context.Background(), "gonzo")
	require.NoError(t, err)
	require.NotNil(t, got)

	callTool(t, h, "repo_remove", map[string]string{"name": "gonzo"})

	// Store entry should be gone.
	got, err = repoStore.Get(context.Background(), "gonzo")
	require.NoError(t, err)
	require.Nil(t, got)

	// Directories should be gone.
	_, err = os.Stat(filepath.Join(userDir, "repos", "gonzo"))
	require.True(t, os.IsNotExist(err))
}

func TestRepoRemove_NotFound(t *testing.T) {
	h, _, _ := setup(t)

	callToolExpectError(t, h, "repo_remove", map[string]string{"name": "nope"})
}

func TestRepoSync_FullLifecycle(t *testing.T) {
	// End-to-end: add → sync (first) → add commits → sync (incremental).
	h, repoStore, userDir := setup(t)
	remote := createTestRemote(t, "main")

	// Register the repo directly in the store (repo_add rejects non-HTTPS URLs,
	// but we need a local path for integration tests).
	addLocalRepo(t, repoStore, userDir, "lifecycle", remote, "main")

	// First sync — should clone and show initial commits.
	result := callTool(t, h, "repo_sync", map[string]string{"name": "lifecycle"})
	var syncResult map[string]any
	require.NoError(t, json.Unmarshal(result, &syncResult))
	require.Equal(t, "lifecycle", syncResult["name"])
	require.NotEmpty(t, syncResult["head_commit"])
	require.NotEmpty(t, syncResult["new_commits"], "first sync should show initial commits")

	// Checkout should contain the file from the initial commit.
	checkoutDir := filepath.Join(userDir, "repos", "lifecycle", "checkout")
	_, err := os.Stat(filepath.Join(checkoutDir, "init.txt"))
	require.NoError(t, err)

	// Store should have LastSeenCommit set.
	tracked, err := repoStore.Get(context.Background(), "lifecycle")
	require.NoError(t, err)
	require.NotEmpty(t, tracked.LastSeenCommit)
	require.False(t, tracked.LastSyncedAt.IsZero())

	firstHead := tracked.LastSeenCommit

	// Add new commits to remote.
	addCommitToRemote(t, remote, "main", "feature.txt", "add feature")

	// Incremental sync.
	result = callTool(t, h, "repo_sync", map[string]string{"name": "lifecycle"})
	require.NoError(t, json.Unmarshal(result, &syncResult))

	newCount := int(syncResult["new_commit_count"].(float64))
	require.Equal(t, 1, newCount)
	require.Contains(t, syncResult["new_commits"], "add feature")
	require.NotEqual(t, firstHead, syncResult["head_commit"])

	// New file should appear in checkout.
	_, err = os.Stat(filepath.Join(checkoutDir, "feature.txt"))
	require.NoError(t, err)
}

func TestRepoSync_NoNewCommits(t *testing.T) {
	h, repoStore, userDir := setup(t)
	remote := createTestRemote(t, "main")

	addLocalRepo(t, repoStore, userDir, "stable", remote, "main")

	// First sync.
	callTool(t, h, "repo_sync", map[string]string{"name": "stable"})

	// Second sync without any new commits.
	result := callTool(t, h, "repo_sync", map[string]string{"name": "stable"})
	var syncResult map[string]any
	require.NoError(t, json.Unmarshal(result, &syncResult))
	require.Equal(t, float64(0), syncResult["new_commit_count"])
	require.Contains(t, syncResult["message"].(string), "no new commits")
}

func TestRepoSync_ResolvesOnlyRepo(t *testing.T) {
	// When only one repo is tracked, name can be omitted.
	h, repoStore, userDir := setup(t)
	remote := createTestRemote(t, "main")

	addLocalRepo(t, repoStore, userDir, "solo", remote, "main")

	// Sync without specifying name.
	result := callTool(t, h, "repo_sync", map[string]any{})
	var syncResult map[string]any
	require.NoError(t, json.Unmarshal(result, &syncResult))
	require.Equal(t, "solo", syncResult["name"])
}

func TestRepoSync_AmbiguousWithoutName(t *testing.T) {
	h, _, _ := setup(t)

	callTool(t, h, "repo_add", map[string]string{
		"name": "repo-a",
		"url":  "https://github.com/user/a",
	})
	callTool(t, h, "repo_add", map[string]string{
		"name": "repo-b",
		"url":  "https://github.com/user/b",
	})

	// Should fail because name is ambiguous.
	callToolExpectError(t, h, "repo_sync", map[string]any{})
}

func TestRepoSync_NoTrackedRepos(t *testing.T) {
	h, _, _ := setup(t)

	callToolExpectError(t, h, "repo_sync", map[string]any{})
}

func TestRepoLog_RequiresSync(t *testing.T) {
	h, _, _ := setup(t)

	callTool(t, h, "repo_add", map[string]string{
		"name": "unsynced",
		"url":  "https://github.com/user/repo",
	})

	callToolExpectError(t, h, "repo_log", map[string]string{"name": "unsynced"})
}

func TestRepoLog_AfterSync(t *testing.T) {
	h, repoStore, userDir := setup(t)
	remote := createTestRemote(t, "main")

	addLocalRepo(t, repoStore, userDir, "logged", remote, "main")
	callTool(t, h, "repo_sync", map[string]string{"name": "logged"})

	result := callTool(t, h, "repo_log", map[string]string{"name": "logged"})
	var logResult map[string]any
	require.NoError(t, json.Unmarshal(result, &logResult))
	require.Equal(t, "logged", logResult["name"])
	require.NotEmpty(t, logResult["log"])
}

// setup creates an MCP handler with repo tools wired to a temp directory.
func setup(t *testing.T) (*mcp.Handler, *repo.Store, string) {
	t.Helper()
	userDir := t.TempDir()

	s, err := store.NewFS(filepath.Join(userDir, "state"))
	require.NoError(t, err)

	repoStore := repo.NewStore(s)
	secrets := &memorySecretStore{data: make(map[string]string)}

	handler := mcp.NewHandler()
	repotools.RegisterTools(handler, repotools.Deps{
		Store:       repoStore,
		SecretStore: secrets,
		UserDir:     userDir,
	})

	return handler, repoStore, userDir
}

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := h.Call(context.Background(), name, argsJSON)
	require.NoError(t, err, "call %s", name)
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), name, argsJSON)
	require.Error(t, err, "expected error from %s", name)
	return err
}

// createTestRemote creates a local git repo with one commit, usable as a
// "remote" for clone/fetch.
func createTestRemote(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()

	gitRun(t, dir, "init", "--initial-branch", branch)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.txt"), []byte("hello"), 0o644))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "initial commit")

	return dir
}

func addCommitToRemote(t *testing.T, remoteDir string, _ string, filename string, message string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(remoteDir, filename), []byte(message), 0o644))
	gitRun(t, remoteDir, "add", filename)
	gitRun(t, remoteDir, "commit", "-m", message)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, string(out))
}

// addLocalRepo registers a repo in the store using a local path as URL,
// bypassing repo_add's HTTPS validation. Used for integration tests.
func addLocalRepo(t *testing.T, repoStore *repo.Store, userDir string, name string, localRemote string, branch string) {
	t.Helper()
	repoDir := filepath.Join(userDir, "repos", name, "bare")
	checkoutDir := filepath.Join(userDir, "repos", name, "checkout")

	// Only create the bare dir — readOnlyCheckout creates the checkout dir via
	// git worktree add. Pre-creating it would make readOnlyCheckout think the
	// worktree already exists and try git checkout instead.
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	require.NoError(t, repoStore.Put(context.Background(), repo.TrackedRepo{
		Name:        name,
		URL:         localRemote,
		Branch:      branch,
		RepoDir:     repoDir,
		WorktreeDir: checkoutDir,
		AddedAt:     time.Now(),
	}))
}

// memorySecretStore is an in-memory secret.Store for testing.
type memorySecretStore struct {
	data map[string]string
}

func (m *memorySecretStore) Get(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func (m *memorySecretStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *memorySecretStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}
