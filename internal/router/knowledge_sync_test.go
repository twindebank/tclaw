package router

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/outbox"
	"tclaw/internal/libraries/store"
)

func TestSyncKnowledgeVault(t *testing.T) {
	t.Run("commits local edits, rebases, and pushes", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		// Non-bare remotes refuse a push to the checked-out branch by default —
		// updateInstead lets the push land and update the remote's working tree,
		// so we can assert on it afterwards.
		gitRun(t, remote, "config", "receive.denyCurrentBranch", "updateInstead")

		dir := filepath.Join(t.TempDir(), "knowledge")
		cloneVault(t, dir, remote)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("hello"), 0o644))

		syncKnowledgeVault(context.Background(), knowledgeSyncParams{
			Dir: dir, UserID: "u1", ChannelName: "irrelevant",
		})

		require.Empty(t, gitOutput(t, dir, "status", "--porcelain"), "working tree should be clean after sync")
		require.Contains(t, gitOutput(t, dir, "log", "--oneline"), "Auto-sync from tclaw")
		require.Equal(t, "0", trimmed(gitOutput(t, dir, "rev-list", "--count", "@{u}..HEAD")), "local should not be ahead after a successful push")
		require.FileExists(t, filepath.Join(remote, "note.md"))
	})

	t.Run("pulls remote changes when there is nothing local to commit", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		dir := filepath.Join(t.TempDir(), "knowledge")
		cloneVault(t, dir, remote)

		// Someone else advances the vault directly on the remote.
		require.NoError(t, os.WriteFile(filepath.Join(remote, "other.md"), []byte("from elsewhere"), 0o644))
		gitRun(t, remote, "add", "other.md")
		gitRun(t, remote, "commit", "-m", "someone else's note")

		syncKnowledgeVault(context.Background(), knowledgeSyncParams{
			Dir: dir, UserID: "u1", ChannelName: "irrelevant",
		})

		require.FileExists(t, filepath.Join(dir, "other.md"))
		require.NotContains(t, gitOutput(t, dir, "log", "--oneline"), "Auto-sync from tclaw",
			"nothing local was dirty, so no auto-sync commit should have been created")
	})

	t.Run("aborts a conflicted rebase and sends a notification-only alert", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		dir := filepath.Join(t.TempDir(), "knowledge")
		cloneVault(t, dir, remote)

		// Remote and local both edit the same line of the same file, guaranteeing
		// a rebase conflict rather than a clean fast-forward or auto-merge.
		require.NoError(t, os.WriteFile(filepath.Join(remote, "index.md"), []byte("# vault (remote edit)\n"), 0o644))
		gitRun(t, remote, "add", "index.md")
		gitRun(t, remote, "commit", "-m", "remote edit")

		require.NoError(t, os.WriteFile(filepath.Join(dir, "index.md"), []byte("# vault (local edit)\n"), 0o644))

		rec := newConflictRecordingChannel("dev-vault-autosync")
		channelsFunc := func() map[channel.ChannelID]channel.Channel {
			return map[channel.ChannelID]channel.Channel{"chan-1": rec}
		}
		ob := outbox.New(outbox.Params{Store: mustFSStore(t), Channels: channelsFunc})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ob.Start(ctx)

		syncKnowledgeVault(ctx, knowledgeSyncParams{
			Dir:          dir,
			UserID:       "u1",
			ChannelName:  "dev-vault-autosync",
			Outbox:       ob,
			ChannelsFunc: channelsFunc,
		})
		require.NoError(t, ob.Flush(ctx))

		require.False(t, gitRebaseInProgress(dir), "conflicted rebase should have been aborted, not left in progress")
		require.Contains(t, gitOutput(t, dir, "log", "--oneline"), "Auto-sync from tclaw",
			"the local commit made before the failed rebase should survive the abort")
		content, err := os.ReadFile(filepath.Join(dir, "index.md"))
		require.NoError(t, err)
		require.Equal(t, "# vault (local edit)\n", string(content), "abort should restore the pre-rebase local content")

		calls := rec.Calls()
		require.Len(t, calls, 1)
		require.Contains(t, calls[0].text, "conflict")
		require.True(t, calls[0].notify)
	})
}

func TestGitRebaseInProgress(t *testing.T) {
	t.Run("false for an ordinary clone", func(t *testing.T) {
		remote := createTestRemote(t, "main")
		dir := filepath.Join(t.TempDir(), "knowledge")
		cloneVault(t, dir, remote)

		require.False(t, gitRebaseInProgress(dir))
	})
}

// --- helpers ---

func trimmed(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func mustFSStore(t *testing.T) *store.FS {
	t.Helper()
	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	return s
}

// conflictRecordedCall captures a single Send call observed by conflictRecordingChannel.
type conflictRecordedCall struct {
	text   string
	notify bool
}

// conflictRecordingChannel is a minimal channel.Channel that records Send calls,
// used to assert the conflict alert is delivered to the right channel with the
// right notify hint — without a full transport.
type conflictRecordingChannel struct {
	name  string
	calls []conflictRecordedCall
}

func newConflictRecordingChannel(name string) *conflictRecordingChannel {
	return &conflictRecordingChannel{name: name}
}

func (c *conflictRecordingChannel) Calls() []conflictRecordedCall {
	return c.calls
}

func (c *conflictRecordingChannel) Info() channel.Info {
	return channel.Info{ID: channel.ChannelID(c.name), Name: c.name, Type: channel.TypeSocket}
}
func (c *conflictRecordingChannel) Messages(context.Context) <-chan string { return make(chan string) }
func (c *conflictRecordingChannel) Send(_ context.Context, text string, opts channel.SendOpts) (channel.MessageID, error) {
	c.calls = append(c.calls, conflictRecordedCall{text: text, notify: opts.Notify})
	return "msg-1", nil
}
func (c *conflictRecordingChannel) Edit(context.Context, channel.MessageID, string) error { return nil }
func (c *conflictRecordingChannel) Done(context.Context) error                            { return nil }
func (c *conflictRecordingChannel) SplitStatusMessages() bool                             { return false }
func (c *conflictRecordingChannel) Markup() channel.Markup                                { return channel.MarkupMarkdown }
func (c *conflictRecordingChannel) StatusWrap() channel.StatusWrap                        { return channel.StatusWrap{} }

// cloneVault clones a test remote into dir with the commit identity set,
// standing in for what boot provisioning does for the vault repo.
func cloneVault(t *testing.T, dir, remote string) {
	t.Helper()
	out, err := exec.Command("git", "-c", "core.hooksPath=/dev/null",
		"clone", "--branch", "main", remote, dir).CombinedOutput()
	require.NoError(t, err, "git clone: %s", string(out))
	require.NoError(t, configureGitIdentity(dir, "", ""))
}
