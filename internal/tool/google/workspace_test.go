package google

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSavedFilePath(t *testing.T) {
	t.Run("rewrites relative saved_file to an absolute path under memoryDir", func(t *testing.T) {
		raw := json.RawMessage(`{"bytes":29496,"mimeType":"application/pdf","saved_file":"download.pdf","status":"success"}`)

		got := resolveSavedFilePath(raw, "/data/tclaw/alice/memory")

		var payload map[string]any
		require.NoError(t, json.Unmarshal(got, &payload))
		require.Equal(t, filepath.Join("/data/tclaw/alice/memory", "download.pdf"), payload["saved_file"])
		require.Equal(t, "application/pdf", payload["mimeType"])
	})

	t.Run("no memoryDir leaves response unchanged", func(t *testing.T) {
		raw := json.RawMessage(`{"saved_file":"download.pdf"}`)

		got := resolveSavedFilePath(raw, "")

		require.JSONEq(t, string(raw), string(got))
	})

	t.Run("response without saved_file is unchanged", func(t *testing.T) {
		raw := json.RawMessage(`{"id":"abc123","name":"some message"}`)

		got := resolveSavedFilePath(raw, "/data/tclaw/alice/memory")

		require.JSONEq(t, string(raw), string(got))
	})

	t.Run("already-absolute saved_file is left alone", func(t *testing.T) {
		raw := json.RawMessage(`{"saved_file":"/already/absolute/download.pdf"}`)

		got := resolveSavedFilePath(raw, "/data/tclaw/alice/memory")

		require.JSONEq(t, string(raw), string(got))
	})

	t.Run("non-object JSON is returned unchanged", func(t *testing.T) {
		raw := json.RawMessage(`[{"id":"abc123"}]`)

		got := resolveSavedFilePath(raw, "/data/tclaw/alice/memory")

		require.JSONEq(t, string(raw), string(got))
	})

	t.Run("relative saved_file with subdirectory joins correctly", func(t *testing.T) {
		raw := json.RawMessage(`{"saved_file":"media/download.pdf"}`)

		got := resolveSavedFilePath(raw, "/data/tclaw/alice/memory")

		var payload map[string]any
		require.NoError(t, json.Unmarshal(got, &payload))
		require.Equal(t, filepath.Join("/data/tclaw/alice/memory", "media", "download.pdf"), payload["saved_file"])
	})
}
