package knowledgeproxy_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/knowledgeproxy"
)

func TestServer_ForwardsAndInjectsAuth(t *testing.T) {
	t.Run("injects token and preserves path and query", func(t *testing.T) {
		upstream, seen := newUpstream(t)
		addr := startServer(t, "owner/knowledge-base", staticToken("s3cret"), upstream.URL)

		resp := get(t, fmt.Sprintf("http://%s/owner/knowledge-base.git/info/refs?service=git-upload-pack", addr))
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "upstream-ok", string(body))
		require.Equal(t, "token s3cret", seen.header("Authorization"))
		require.Equal(t, "/owner/knowledge-base.git/info/refs", seen.path())
		require.Equal(t, "git-upload-pack", seen.query("service"))
		// The token must never appear in what the agent-facing side receives.
		require.NotContains(t, string(body), "s3cret")
	})

	t.Run("allows receive-pack (push) endpoints", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := startServer(t, "owner/knowledge-base", staticToken("tok"), upstream.URL)

		resp := post(t, fmt.Sprintf("http://%s/owner/knowledge-base.git/git-receive-pack", addr))
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestServer_RejectsUnpinnedRequests(t *testing.T) {
	t.Run("rejects a different repo", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := startServer(t, "owner/knowledge-base", staticToken("tok"), upstream.URL)

		resp := get(t, fmt.Sprintf("http://%s/someone/other-repo.git/info/refs?service=git-upload-pack", addr))
		defer resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("rejects non git smart-http endpoints", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := startServer(t, "owner/knowledge-base", staticToken("tok"), upstream.URL)

		resp := get(t, fmt.Sprintf("http://%s/owner/knowledge-base.git/config", addr))
		defer resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("rejects unknown info/refs service", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := startServer(t, "owner/knowledge-base", staticToken("tok"), upstream.URL)

		resp := get(t, fmt.Sprintf("http://%s/owner/knowledge-base.git/info/refs?service=evil", addr))
		defer resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

func TestServer_TokenUnavailable(t *testing.T) {
	t.Run("returns 502 when the token source errors", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		failing := func(_ context.Context) (string, error) { return "", fmt.Errorf("boom") }
		addr := startServer(t, "owner/knowledge-base", failing, upstream.URL)

		resp := get(t, fmt.Sprintf("http://%s/owner/knowledge-base.git/info/refs?service=git-upload-pack", addr))
		defer resp.Body.Close()
		require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	})
}

func TestServer_RemoteURL(t *testing.T) {
	t.Run("builds a token-free http remote for the pinned repo", func(t *testing.T) {
		upstream, _ := newUpstream(t)
		addr := startServer(t, "owner/knowledge-base", staticToken("tok"), upstream.URL)

		s, err := knowledgeproxy.NewServer(knowledgeproxy.Config{
			RepoPath: "owner/knowledge-base",
			Token:    staticToken("tok"),
			Upstream: upstream.URL,
		})
		require.NoError(t, err)
		_ = addr

		// A non-started server has no address.
		require.Equal(t, "", s.RemoteURL())
	})
}

// --- helpers ---

func startServer(t *testing.T, repoPath string, token knowledgeproxy.TokenSource, upstream string) string {
	t.Helper()
	s, err := knowledgeproxy.NewServer(knowledgeproxy.Config{
		RepoPath: repoPath,
		Token:    token,
		Upstream: upstream,
	})
	require.NoError(t, err)

	addr, err := s.Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	require.Equal(t, fmt.Sprintf("http://%s/%s.git", addr, repoPath), s.RemoteURL())
	return addr
}

func staticToken(tok string) knowledgeproxy.TokenSource {
	return func(_ context.Context) (string, error) { return tok, nil }
}

// requestRecorder captures the last request the upstream received.
type requestRecorder struct {
	mu     sync.Mutex
	header http.Header
	url    string
	query  map[string][]string
}

func (r *requestRecorder) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.header = req.Header.Clone()
	r.url = req.URL.Path
	r.query = req.URL.Query()
}

// wrap around the recorder to expose typed getters used in assertions.
type recorderView struct{ r *requestRecorder }

func (v recorderView) header(k string) string {
	v.r.mu.Lock()
	defer v.r.mu.Unlock()
	return v.r.header.Get(k)
}

func (v recorderView) path() string {
	v.r.mu.Lock()
	defer v.r.mu.Unlock()
	return v.r.url
}

func (v recorderView) query(k string) string {
	v.r.mu.Lock()
	defer v.r.mu.Unlock()
	if vals := v.r.query[k]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func newUpstream(t *testing.T) (*httptest.Server, recorderView) {
	t.Helper()
	rec := &requestRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		// Echo a fixed body — never the token — so tests can assert passthrough.
		if strings.Contains(r.Header.Get("Authorization"), "tok") ||
			strings.Contains(r.Header.Get("Authorization"), "s3cret") {
			io.WriteString(w, "upstream-ok")
			return
		}
		io.WriteString(w, "upstream-noauth")
	}))
	t.Cleanup(srv.Close)
	return srv, recorderView{r: rec}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	return resp
}

func post(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/x-git-receive-pack-request", strings.NewReader(""))
	require.NoError(t, err)
	return resp
}
