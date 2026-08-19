package documenttools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/documenttools"
)

func TestDocumentSendPDF(t *testing.T) {
	t.Run("renders the markdown and sends it as a pdf", func(t *testing.T) {
		h := setup(t)
		writeMarkdown(t, h.MemoryDir, "guide.md", "# Guide\n\nA line of text.\n\n- one\n- two\n")

		result := callTool(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "guide.md",
			"filename":      "guide.pdf",
			"caption":       "Here you go",
		})

		var got documenttools.SendPDFResponse
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "guide.pdf", got.Filename)
		require.Equal(t, "42", got.MessageID, "the transport's message id is passed back")
		require.Greater(t, len(h.Sent.calls[0].Content), 500, "a rendered guide should not be near-empty")

		require.Len(t, h.Sent.calls, 1, "exactly one file sent")
		require.Equal(t, "guide.pdf", h.Sent.calls[0].Filename)
		require.Equal(t, "Here you go", h.Sent.calls[0].Caption)
		require.True(t, h.Sent.calls[0].Opts.Notify, "a document the user asked for should ring")
		require.True(t, bytes.HasPrefix(h.Sent.calls[0].Content, []byte("%PDF-")), "content should be a PDF")
	})

	t.Run("fills a credential placeholder from the store", func(t *testing.T) {
		h := setup(t)
		h.Secrets.data["doc_wifi_password"] = "correct-horse-battery"
		writeMarkdown(t, h.MemoryDir, "guide.md", "# WiFi\n\nPassword: ${cred:wifi_password}\n")

		result := callTool(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "guide.md",
			"filename":      "wifi.pdf",
		})

		var got documenttools.SendPDFResponse
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, []string{"wifi_password"}, got.CredentialsSet, "the key is reported, never the value")

		onDisk, err := os.ReadFile(filepath.Join(h.MemoryDir, "guide.md"))
		require.NoError(t, err)
		require.Contains(t, string(onDisk), "${cred:wifi_password}", "the source file keeps the placeholder")
		require.Len(t, h.Sent.calls, 1, "one file sent")
	})

	t.Run("a different credential value produces a different pdf", func(t *testing.T) {
		markdown := "# WiFi\n\nPassword: ${cred:wifi_password}\n"

		first, second := documentBytesForSecret(t, markdown, "first-value-here"), documentBytesForSecret(t, markdown, "second-value-x")

		require.NotEqual(t, first, second, "the credential value must reach the rendered document")
	})

	t.Run("reports a credential the user has not set yet", func(t *testing.T) {
		h := setup(t)
		writeMarkdown(t, h.MemoryDir, "guide.md", "Password: ${cred:wifi_password}\n")

		err := callToolExpectError(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "guide.md",
			"filename":      "guide.pdf",
		})

		require.Equal(t,
			"no value set for doc_wifi_password — use secret_form_request with that exact key to have the user fill it in",
			err.Error())
	})

	t.Run("a key outside the document namespace cannot reach a system secret", func(t *testing.T) {
		h := setup(t)
		// the value a prompt injection would be after, stored under its real key
		h.Secrets.data["telegram_client_session"] = "full-account-access"
		writeMarkdown(t, h.MemoryDir, "guide.md", "Session: ${cred:telegram_client_session}\n")

		err := callToolExpectError(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "guide.md",
			"filename":      "guide.pdf",
		})

		require.Contains(t, err.Error(), "doc_telegram_client_session",
			"the placeholder only ever reaches the doc_ namespace")
		require.Empty(t, h.Sent.calls, "nothing is sent when the key does not resolve")
	})

	t.Run("refuses a filename that could inject into the upload headers", func(t *testing.T) {
		h := setup(t)
		writeMarkdown(t, h.MemoryDir, "guide.md", "# Guide\n")

		for _, name := range []string{"guide\r\ninjected.pdf", "../../etc/guide.pdf"} {
			err := callToolExpectError(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
				"markdown_path": "guide.md",
				"filename":      name,
			})
			require.Contains(t, err.Error(), "filename", "the error names the offending field")
		}
		require.Empty(t, h.Sent.calls, "nothing is sent for a bad filename")
	})

	t.Run("refuses a path outside the memory directory", func(t *testing.T) {
		h := setup(t)

		err := callToolExpectError(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "../escape.md",
			"filename":      "guide.pdf",
		})

		require.Equal(t, `path "../escape.md" points outside your memory directory`, err.Error(),
			"an escaping path says so, rather than reporting a missing file")
	})

	t.Run("refuses an absolute path", func(t *testing.T) {
		h := setup(t)

		err := callToolExpectError(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "/etc/passwd",
			"filename":      "guide.pdf",
		})

		require.Equal(t, `path "/etc/passwd" must be relative to your memory directory`, err.Error())
	})

	t.Run("refuses a filename that is not a pdf", func(t *testing.T) {
		h := setup(t)
		writeMarkdown(t, h.MemoryDir, "guide.md", "# Guide\n")

		err := callToolExpectError(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "guide.md",
			"filename":      "guide.docx",
		})

		require.Equal(t, `filename "guide.docx" must end in .pdf`, err.Error())
	})

	t.Run("says so when the channel cannot carry a file", func(t *testing.T) {
		dir := t.TempDir()
		writeMarkdown(t, dir, "guide.md", "# Guide\n")
		handler := mcp.NewHandler()
		documenttools.RegisterTools(handler, documenttools.Deps{
			MemoryDir:   dir,
			SecretStore: &memorySecretStore{data: map[string]string{}},
		})

		err := callToolExpectError(t, handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "guide.md",
			"filename":      "guide.pdf",
		})

		require.Equal(t, "this channel cannot receive files", err.Error())
	})

	t.Run("reports a character the pdf font cannot render", func(t *testing.T) {
		h := setup(t)
		writeMarkdown(t, h.MemoryDir, "guide.md", "# Feeding 🐈\n")

		err := callToolExpectError(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
			"markdown_path": "guide.md",
			"filename":      "guide.pdf",
		})

		require.Equal(t, "render guide.md: cannot render these characters: '🐈' (U+1F408)", err.Error())
	})
}

// --- helpers ---

// harness is one wired-up document tool package plus the fakes behind it.
type harness struct {
	Handler   *mcp.Handler
	Sent      *fileSpy
	Secrets   *memorySecretStore
	MemoryDir string
}

func setup(t *testing.T) harness {
	t.Helper()
	h := harness{
		Handler:   mcp.NewHandler(),
		Sent:      &fileSpy{},
		Secrets:   &memorySecretStore{data: map[string]string{}},
		MemoryDir: t.TempDir(),
	}
	documenttools.RegisterTools(h.Handler, documenttools.Deps{
		MemoryDir:   h.MemoryDir,
		SecretStore: h.Secrets,
		SendFile:    h.Sent.send,
	})
	return h
}

func documentBytesForSecret(t *testing.T, markdown, value string) []byte {
	t.Helper()
	h := setup(t)
	h.Secrets.data["doc_wifi_password"] = value
	writeMarkdown(t, h.MemoryDir, "guide.md", markdown)

	callTool(t, h.Handler, documenttools.ToolSendPDF, map[string]any{
		"markdown_path": "guide.md",
		"filename":      "wifi.pdf",
	})

	require.Len(t, h.Sent.calls, 1, "one file sent")
	return h.Sent.calls[0].Content
}

func writeMarkdown(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
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

type fileSpy struct {
	calls []channel.SendFileParams
}

func (f *fileSpy) send(_ context.Context, p channel.SendFileParams) (channel.MessageID, error) {
	f.calls = append(f.calls, p)
	return channel.MessageID("42"), nil
}

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
