package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// emailBodyDir is the subdirectory under the user's memory dir where full
	// email bodies are written for the agent to read on demand.
	emailBodyDir = "emails"

	// bodyPreviewChars is how many characters of the body are inlined into the
	// notification. Enough to triage/classify most email without opening the
	// file, small enough to keep the agent turn cheap.
	bodyPreviewChars = 500

	// emailBodyRetention bounds how long body files linger before pruning, so
	// the emails dir doesn't grow without limit.
	emailBodyRetention = 7 * 24 * time.Hour
)

// buildNotificationText produces the notification body for a single new email.
// It deterministically fetches the full message once, writes the body to a file
// the agent can read, and returns a compact summary that carries the exact
// message_id/thread_id plus a preview and the file path — so the agent never has
// to reverse-search Gmail. On any fetch failure it degrades to an ID-only
// notification (never dropped) so the agent can still read it with google_gmail_read.
func (n *notifier) buildNotificationText(ctx context.Context, deps Deps, messageID string) string {
	rsp, err := readFullMessage(ctx, deps, messageID)
	if err != nil {
		slog.Warn("gmail notifier: full fetch failed, degrading to id-only notification",
			"message_id", messageID, "error", err)
		return formatDegradedNotification(messageID, err)
	}

	bodyPath := ""
	if n.memoryDir != "" {
		path, writeErr := n.writeEmailBodyFile(rsp)
		if writeErr != nil {
			slog.Warn("gmail notifier: failed to write email body file",
				"message_id", messageID, "error", writeErr)
		} else {
			bodyPath = path
		}
	}

	return formatEmailNotification(rsp, bodyPath)
}

// writeEmailBodyFile writes the full email as a markdown file with a
// JSON-encoded (YAML-compatible) frontmatter block, and returns its path.
func (n *notifier) writeEmailBodyFile(rsp gmailReadResponse) (string, error) {
	dir := filepath.Join(n.memoryDir, emailBodyDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create emails dir: %w", err)
	}

	// Best-effort prune before writing so the directory stays bounded.
	n.pruneEmailBodyFiles(dir)

	path := filepath.Join(dir, rsp.ID+".md")
	if err := os.WriteFile(path, []byte(renderEmailFile(rsp)), 0o600); err != nil {
		return "", fmt.Errorf("write email body file: %w", err)
	}
	return path, nil
}

// pruneEmailBodyFiles removes body files older than emailBodyRetention. Failures
// are logged, not returned — pruning is best-effort and must never block a write.
func (n *notifier) pruneEmailBodyFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("gmail notifier: failed to read emails dir for pruning", "dir", dir, "error", err)
		return
	}

	cutoff := time.Now().Add(-emailBodyRetention)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			slog.Warn("gmail notifier: failed to stat email body file", "name", entry.Name(), "error", infoErr)
			continue
		}
		if info.ModTime().Before(cutoff) {
			if rmErr := os.Remove(filepath.Join(dir, entry.Name())); rmErr != nil {
				slog.Warn("gmail notifier: failed to prune email body file", "name", entry.Name(), "error", rmErr)
			}
		}
	}
}

// renderEmailFile builds the markdown file contents: a frontmatter block with
// the headers and threading identifiers, followed by the plain-text body.
func renderEmailFile(rsp gmailReadResponse) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeFrontmatterField(&b, "from", rsp.From)
	writeFrontmatterField(&b, "to", rsp.To)
	writeFrontmatterField(&b, "subject", rsp.Subject)
	writeFrontmatterField(&b, "date", rsp.Date)
	writeFrontmatterField(&b, "gmail_message_id", rsp.ID)
	writeFrontmatterField(&b, "thread_id", rsp.ThreadID)
	writeFrontmatterField(&b, "message_id_header", rsp.MessageID)
	writeFrontmatterField(&b, "references", rsp.References)
	b.WriteString("---\n\n")
	b.WriteString(rsp.Body)
	if !strings.HasSuffix(rsp.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// writeFrontmatterField writes a `key: "json-escaped-value"` line. A
// double-quoted JSON string is valid YAML and safely handles colons, quotes, and
// unicode in header values. HTML escaping is disabled so angle brackets in the
// Message-ID header stay human-readable.
func writeFrontmatterField(b *strings.Builder, key, value string) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		// Encoding a string never fails; fall back defensively.
		buf.Reset()
		buf.WriteString(`""`)
	}
	// Encoder.Encode appends a trailing newline — trim it before composing.
	fmt.Fprintf(b, "%s: %s\n", key, strings.TrimRight(buf.String(), "\n"))
}

func formatEmailNotification(rsp gmailReadResponse, bodyPath string) string {
	var b strings.Builder
	b.WriteString("📧 New email\n")
	fmt.Fprintf(&b, "From: %s\n", rsp.From)
	fmt.Fprintf(&b, "Subject: %s\n", rsp.Subject)
	if rsp.Date != "" {
		fmt.Fprintf(&b, "Date: %s\n", rsp.Date)
	}
	fmt.Fprintf(&b, "gmail_message_id: %s\nthread_id: %s\n", rsp.ID, rsp.ThreadID)

	preview := truncatePreview(rsp.Body, bodyPreviewChars)
	if preview != "" {
		fmt.Fprintf(&b, "\nPreview:\n%s\n", preview)
	}

	if bodyPath != "" {
		fmt.Fprintf(&b, "\nFull email saved to: %s\n", bodyPath)
		b.WriteString("Read that file for the complete body. Use google_gmail_read with the message_id " +
			"only if you need raw HTML or attachments. Do not reverse-search Gmail — you already have the exact IDs.")
	} else {
		b.WriteString("\nUse google_gmail_read with the message_id above to read the full body. " +
			"Do not reverse-search Gmail — you already have the exact IDs.")
	}

	return b.String()
}

func formatDegradedNotification(messageID string, err error) string {
	return fmt.Sprintf("📧 New email (could not fetch details: %v)\n"+
		"gmail_message_id: %s\n"+
		"Use google_gmail_read with this message_id to read it.", err, messageID)
}

// truncatePreview returns at most max runes of s (whitespace-collapsed), with an
// ellipsis appended when truncated.
func truncatePreview(s string, max int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	runes := []rune(collapsed)
	if len(runes) <= max {
		return collapsed
	}
	return string(runes[:max]) + "…"
}
