package telegramchannel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"
)

func TestDownloadMedia(t *testing.T) {
	t.Run("streams the file to disk and returns its path", func(t *testing.T) {
		want := []byte("hello attachment bytes")
		server := newFakeFileServer(t, fakeFile{
			filePath: "documents/report.pdf",
			fileSize: int64(len(want)),
			body:     want,
		})
		defer server.Close()

		mediaDir := t.TempDir()
		tg := NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{MediaDir: mediaDir})
		b := newBotForTest(t, server.URL)

		msg := &models.Message{ID: 7, Document: &models.Document{FileID: "doc1", FileName: "report.pdf"}}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)

		path, err := tg.downloadMedia(context.Background(), b, msg, att)
		require.NoError(t, err)

		// Saved under MediaDir with the document prefix and original extension.
		require.Equal(t, mediaDir, filepath.Dir(path))
		require.Contains(t, filepath.Base(path), "document_")
		require.True(t, strings.HasSuffix(path, "_7.pdf"))

		// The bytes on disk are exactly what the server served — nothing injected,
		// nothing truncated.
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("rejects a file that getFile reports as over the size cap", func(t *testing.T) {
		server := newFakeFileServer(t, fakeFile{
			filePath: "video/big.mp4",
			fileSize: maxMediaDownloadBytes + 1,
			body:     []byte("should never be downloaded"),
		})
		defer server.Close()

		mediaDir := t.TempDir()
		tg := NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{MediaDir: mediaDir})
		b := newBotForTest(t, server.URL)

		msg := &models.Message{ID: 9, Video: &models.Video{FileID: "vid1"}}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)

		path, err := tg.downloadMedia(context.Background(), b, msg, att)
		require.Error(t, err)
		require.Contains(t, err.Error(), "too large")
		require.Empty(t, path)

		// Nothing should have been written to the media dir.
		entries, readErr := os.ReadDir(mediaDir)
		require.NoError(t, readErr)
		require.Empty(t, entries)
	})

	t.Run("returns an error and leaves no file when the download 404s", func(t *testing.T) {
		server := newFakeFileServer(t, fakeFile{
			filePath:     "documents/gone.pdf",
			fileSize:     100,
			downloadCode: http.StatusNotFound,
		})
		defer server.Close()

		mediaDir := t.TempDir()
		tg := NewTelegram("fake-token", "test", "desc", "", []int64{1}, TelegramOptions{MediaDir: mediaDir})
		b := newBotForTest(t, server.URL)

		msg := &models.Message{ID: 11, Document: &models.Document{FileID: "doc1", FileName: "gone.pdf"}}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)

		path, err := tg.downloadMedia(context.Background(), b, msg, att)
		require.Error(t, err)
		require.Contains(t, err.Error(), "download status 404")
		require.Empty(t, path)

		entries, readErr := os.ReadDir(mediaDir)
		require.NoError(t, readErr)
		require.Empty(t, entries)
	})
}

func TestRemovePartialDownload(t *testing.T) {
	t.Run("removes an existing partial file", func(t *testing.T) {
		dir := t.TempDir()
		partial := filepath.Join(dir, "document_1_2.pdf")
		require.NoError(t, os.WriteFile(partial, []byte("half"), 0o644))

		removePartialDownload(partial)

		_, err := os.Stat(partial)
		require.True(t, os.IsNotExist(err), "partial file should have been removed")
	})

	t.Run("logs and does not panic when the file is already gone", func(t *testing.T) {
		// Best-effort cleanup must never itself fail loudly.
		require.NotPanics(t, func() {
			removePartialDownload(filepath.Join(t.TempDir(), "never-existed.pdf"))
		})
	})
}

// --- helpers ---

// fakeFile describes the single attachment a newFakeFileServer will serve.
type fakeFile struct {
	filePath string
	fileSize int64
	body     []byte

	// downloadCode overrides the HTTP status for the file-download request
	// (0 means 200 OK). Used to simulate a download failure.
	downloadCode int
}

// newFakeFileServer stubs the two bot API surfaces downloadMedia touches:
// getMe (issued by bot.New to validate the token), getFile (returns the file
// metadata), and the /file/bot<token>/<path> download endpoint.
func newFakeFileServer(t *testing.T, f fakeFile) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"bot","username":"fakebot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			_, _ = fmt.Fprintf(w,
				`{"ok":true,"result":{"file_id":"doc1","file_unique_id":"u1","file_size":%d,"file_path":%q}}`,
				f.fileSize, f.filePath)
		case strings.Contains(r.URL.Path, "/file/"):
			if f.downloadCode != 0 {
				w.WriteHeader(f.downloadCode)
				return
			}
			_, _ = w.Write(f.body)
		default:
			http.NotFound(w, r)
		}
	}))
}
