package gitproxy_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/gitproxy"
	"tclaw/internal/repo"
)

func TestProxy_Fetch(t *testing.T) {
	t.Run("is allowed at every tier and injects auth", func(t *testing.T) {
		for _, access := range repo.ValidAccessTiers() {
			t.Run(string(access), func(t *testing.T) {
				upstream, seen := newUpstream(t)
				addr := start(t, upstream.URL, gitproxy.Repo{
					Path: "owner/config", Access: access, DefaultBranch: "main", Token: "s3cret",
				})

				resp := get(t, fmt.Sprintf("http://%s/ha.git/info/refs?service=git-upload-pack", addr))
				defer resp.Body.Close()

				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, "/owner/config.git/info/refs", seen.path())
				require.Equal(t, "s3cret", seen.authUser(), "the token must be injected server-side")
			})
		}
	})

	t.Run("rewrites the proxy name to the real repo path", func(t *testing.T) {
		upstream, seen := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/renamed", Access: repo.AccessReadOnly, DefaultBranch: "main",
		})

		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-upload-pack", addr), nil, "")
		defer resp.Body.Close()

		require.Equal(t, "/owner/renamed.git/git-upload-pack", seen.path())
	})
}

func TestProxy_PushByTier(t *testing.T) {
	pushBody := pktLines("0000000000000000000000000000000000000000 " +
		"1111111111111111111111111111111111111111 refs/heads/feature\x00report-status")

	t.Run("read_only refuses the push advertisement", func(t *testing.T) {
		// Refusing at the advertisement means git fails before building a
		// packfile, so the agent gets a clear message rather than a late error.
		upstream, _ := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/config", Access: repo.AccessReadOnly, DefaultBranch: "main",
		})

		resp := get(t, fmt.Sprintf("http://%s/ha.git/info/refs?service=git-receive-pack", addr))
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		require.Contains(t, readBody(t, resp), "read-only")
	})

	t.Run("read_only refuses the push itself", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/config", Access: repo.AccessReadOnly, DefaultBranch: "main",
		})

		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(pushBody), "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("pull_requests_only allows a feature branch", func(t *testing.T) {
		upstream, seen := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/config", Access: repo.AccessPullRequestsOnly, DefaultBranch: "main",
		})

		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(pushBody), "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, pushBody, seen.body(), "the inspected bytes must still reach the upstream intact")
	})

	t.Run("pull_requests_only forwards the packfile that follows the commands intact", func(t *testing.T) {
		// A real push always has packfile bytes after the flush packet. The
		// command-parsing read must not consume or drop any of them.
		upstream, seen := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/config", Access: repo.AccessPullRequestsOnly, DefaultBranch: "main",
		})

		packfile := strings.Repeat("PACK-not-a-real-packfile-but-stands-in-for-one", 20)
		body := pushBody + packfile
		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(body), "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, body, seen.body(), "the packfile bytes after the flush packet must reach the upstream intact")
	})

	t.Run("pull_requests_only refuses the default branch", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/config", Access: repo.AccessPullRequestsOnly, DefaultBranch: "main",
		})

		body := pktLines("1111111111111111111111111111111111111111 " +
			"2222222222222222222222222222222222222222 refs/heads/main")
		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(body), "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		require.Contains(t, readBody(t, resp), "pull request")
	})

	t.Run("pull_requests_only refuses deleting the default branch", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/config", Access: repo.AccessPullRequestsOnly, DefaultBranch: "main",
		})

		body := pktLines("1111111111111111111111111111111111111111 " +
			"0000000000000000000000000000000000000000 refs/heads/main")
		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(body), "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("pull_requests_only refuses a default branch hidden behind a feature branch", func(t *testing.T) {
		// A multi-command push must be judged on every command, not the first.
		upstream, _ := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/config", Access: repo.AccessPullRequestsOnly, DefaultBranch: "main",
		})

		body := pktLines(
			"0000000000000000000000000000000000000000 1111111111111111111111111111111111111111 refs/heads/feature\x00report-status",
			"1111111111111111111111111111111111111111 2222222222222222222222222222222222222222 refs/heads/main",
		)
		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(body), "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("pull_requests_only refuses tags", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/config", Access: repo.AccessPullRequestsOnly, DefaultBranch: "main",
		})

		body := pktLines("0000000000000000000000000000000000000000 " +
			"1111111111111111111111111111111111111111 refs/tags/v1.0")
		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(body), "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		require.Contains(t, readBody(t, resp), "only branches")
	})

	t.Run("full_write allows the default branch", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := start(t, upstream.URL, gitproxy.Repo{
			Path: "owner/vault", Access: repo.AccessFullWrite, DefaultBranch: "main",
		})

		body := pktLines("1111111111111111111111111111111111111111 " +
			"2222222222222222222222222222222222222222 refs/heads/main")
		resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(body), "")
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestProxy_FailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		encoding string
	}{
		{name: "truncated length prefix", body: "00"},
		{name: "malformed length prefix", body: "zzzz"},
		{name: "payload shorter than its length", body: "0040short"},
		{name: "command with too few fields", body: pktLines("1111111111111111111111111111111111111111 refs/heads/feature")},
		{name: "no commands at all", body: "0000"},
		{name: "gzipped body", body: pktLines("0000000000000000000000000000000000000000 1111111111111111111111111111111111111111 refs/heads/feature"), encoding: "gzip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream, seen := newUpstream(t)
			addr := start(t, upstream.URL, gitproxy.Repo{
				Path: "owner/config", Access: repo.AccessPullRequestsOnly, DefaultBranch: "main",
			})

			resp := post(t, fmt.Sprintf("http://%s/ha.git/git-receive-pack", addr), []byte(tt.body), tt.encoding)
			defer resp.Body.Close()

			require.Equal(t, http.StatusForbidden, resp.StatusCode,
				"a push we cannot fully read must be refused")
			require.Empty(t, seen.path(), "nothing should reach the upstream")
		})
	}
}

func TestProxy_RejectsNonGitRequests(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "repo config", path: "/ha.git/config"},
		{name: "arbitrary api path", path: "/ha.git/../../user/repos"},
		{name: "unknown info/refs service", path: "/ha.git/info/refs?service=evil"},
		{name: "no repo name", path: "/.git/info/refs?service=git-upload-pack"},
		{name: "name containing a path", path: "/owner/other.git/info/refs?service=git-upload-pack"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream, seen := newUpstream(t)
			addr := start(t, upstream.URL, gitproxy.Repo{
				Path: "owner/config", Access: repo.AccessFullWrite, DefaultBranch: "main",
			})

			resp := get(t, "http://"+addr+tt.path)
			defer resp.Body.Close()

			require.NotEqual(t, http.StatusOK, resp.StatusCode)
			require.Empty(t, seen.path(), "nothing should reach the upstream")
		})
	}
}

func TestProxy_UnknownRepo(t *testing.T) {
	t.Run("is not found, without saying whether the name exists", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		server, err := gitproxy.NewServer(gitproxy.Config{
			Upstream: upstream.URL,
			Repos: func(_ context.Context, _ string) (*gitproxy.Repo, error) {
				return nil, nil
			},
		})
		require.NoError(t, err)
		addr, err := server.Start("127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, server.Stop(context.Background())) })

		resp := get(t, fmt.Sprintf("http://%s/whatever.git/info/refs?service=git-upload-pack", addr))
		defer resp.Body.Close()

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestServer_RemoteURL(t *testing.T) {
	t.Run("addresses the repo by its tracked name", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		server, err := gitproxy.NewServer(gitproxy.Config{
			Upstream: upstream.URL,
			Repos:    func(context.Context, string) (*gitproxy.Repo, error) { return nil, nil },
		})
		require.NoError(t, err)
		addr, err := server.Start("127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, server.Stop(context.Background())) })

		require.Equal(t, fmt.Sprintf("http://%s/ha-config.git", addr), server.RemoteURL("ha-config"))
	})
}

// --- helpers ---

// pktLines encodes payloads as git pkt-lines followed by a flush packet.
func pktLines(payloads ...string) string {
	var b strings.Builder
	for _, p := range payloads {
		b.WriteString(fmt.Sprintf("%04x%s", len(p)+4, p))
	}
	b.WriteString("0000")
	return b.String()
}

// start runs a proxy serving one repo and returns its address.
func start(t *testing.T, upstreamURL string, r gitproxy.Repo) string {
	t.Helper()
	server, err := gitproxy.NewServer(gitproxy.Config{
		Upstream: upstreamURL,
		Repos: func(_ context.Context, _ string) (*gitproxy.Repo, error) {
			resolved := r
			return &resolved, nil
		},
	})
	require.NoError(t, err)

	addr, err := server.Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop(context.Background())) })
	return addr
}

// upstreamRecord captures what the fake GitHub received.
type upstreamRecord struct {
	mu       sync.Mutex
	gotPath  string
	gotUser  string
	gotBody  string
	gotQuery url.Values
}

func (u *upstreamRecord) path() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.gotPath
}

func (u *upstreamRecord) authUser() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.gotUser
}

func (u *upstreamRecord) body() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.gotBody
}

func newUpstream(t *testing.T) (*httptest.Server, *upstreamRecord) {
	t.Helper()
	record := &upstreamRecord{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		user, _, _ := r.BasicAuth()

		record.mu.Lock()
		record.gotPath = r.URL.Path
		record.gotUser = user
		record.gotBody = string(body)
		record.gotQuery = r.URL.Query()
		record.mu.Unlock()

		fmt.Fprint(w, "upstream-ok")
	}))
	t.Cleanup(server.Close)
	return server, record
}

func get(t *testing.T, rawURL string) *http.Response {
	t.Helper()
	resp, err := http.Get(rawURL)
	require.NoError(t, err)
	return resp
}

func post(t *testing.T, rawURL string, body []byte, encoding string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}
