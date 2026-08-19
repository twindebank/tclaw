package documenttools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"tclaw/internal/channel"
	"tclaw/internal/libraries/markdownpdf"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
)

// ToolSendPDF renders a markdown file and delivers the PDF to the chat.
const ToolSendPDF = "document_send_pdf"

// DocumentKeyPrefix namespaces the only secrets a document may carry. Nothing
// tclaw provisions for itself is named this way.
const DocumentKeyPrefix = "doc_"

// Deps carries what the send-pdf handler needs from the router.
type Deps struct {
	MemoryDir   string
	SecretStore secret.Store
	SendFile    func(ctx context.Context, p channel.SendFileParams) (channel.MessageID, error)
}

// SendPDFParams is the tool's argument shape.
type SendPDFParams struct {
	MarkdownPath string `json:"markdown_path"`
	Filename     string `json:"filename"`
	Caption      string `json:"caption"`
	Title        string `json:"title"`
	AssetsDir    string `json:"assets_dir"`
}

// SendPDFResponse tells the agent what was sent without echoing the content.
type SendPDFResponse struct {
	Filename       string   `json:"filename"`
	Bytes          int      `json:"bytes"`
	MessageID      string   `json:"message_id"`
	CredentialsSet []string `json:"credentials_filled,omitempty"`
}

// RegisterTools registers this package's tools on the handler.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	handler.Register(sendPDFDef(), sendPDFHandler(deps))
}

func sendPDFDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolSendPDF,
		Description: "Render a markdown file from your memory directory into a PDF and send it to this chat. " +
			"Use for anything the user needs as a document rather than a message: a guide, a report, a summary to forward on.\n\n" +
			"Supported markdown: headings, paragraphs, bullet and numbered lists, tables, blockquotes, fenced code, " +
			"horizontal rules, images alone on a line, and inline bold, italic, code and links. A paragraph or blockquote " +
			"starting with a warning sign renders as a red callout; other blockquotes render amber.\n\n" +
			"To include a credential you are not allowed to read, write ${cred:some_key} in the markdown. " +
			"The value is filled in when the PDF is built and never reaches you or the filesystem. " +
			"Use secret_form_request to have the user set a key first.\n\n" +
			"Only characters in the Windows-1252 set render. Ordinary punctuation including em dashes, en dashes and " +
			"degree signs is fine; emoji are not, and the call fails naming the character rather than mangling it.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"markdown_path": {"type": "string", "description": "Path to the markdown file, relative to your memory directory."},
				"filename": {"type": "string", "description": "Filename the recipient sees. Must end in .pdf."},
				"caption": {"type": "string", "description": "Short message sent alongside the document."},
				"title": {"type": "string", "description": "PDF metadata title. Defaults to the filename."},
				"assets_dir": {"type": "string", "description": "Directory images resolve against, relative to your memory directory. Defaults to the markdown file's own directory."}
			},
			"required": ["markdown_path", "filename"],
			"additionalProperties": false
		}`),
	}
}

func sendPDFHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var p SendPDFParams
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("parse arguments: %w", err)
		}
		if deps.SendFile == nil {
			return nil, fmt.Errorf("this channel cannot receive files")
		}
		if strings.TrimSpace(p.MarkdownPath) == "" {
			return nil, fmt.Errorf("markdown_path is required")
		}
		if !strings.HasSuffix(p.Filename, ".pdf") {
			return nil, fmt.Errorf("filename %q must end in .pdf", p.Filename)
		}
		if err := checkFilename(p.Filename); err != nil {
			return nil, err
		}

		markdownPath, err := resolveInMemoryDir(deps.MemoryDir, p.MarkdownPath)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(markdownPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p.MarkdownPath, err)
		}

		credentials, err := readCredentials(ctx, deps, string(raw))
		if err != nil {
			return nil, err
		}

		assetsDir := filepath.Dir(markdownPath)
		if p.AssetsDir != "" {
			assetsDir, err = resolveInMemoryDir(deps.MemoryDir, p.AssetsDir)
			if err != nil {
				return nil, err
			}
		}

		title := p.Title
		if title == "" {
			title = strings.TrimSuffix(p.Filename, ".pdf")
		}

		pdf, err := markdownpdf.Render(markdownpdf.RenderParams{
			Markdown:    string(raw),
			Title:       title,
			AssetsDir:   assetsDir,
			Credentials: credentials,
		})
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", p.MarkdownPath, err)
		}

		messageID, err := deps.SendFile(ctx, channel.SendFileParams{
			Filename: p.Filename,
			Content:  pdf,
			Caption:  p.Caption,
			Opts:     channel.SendOpts{Notify: true},
		})
		if err != nil {
			return nil, fmt.Errorf("send %s: %w", p.Filename, err)
		}

		filled := make([]string, 0, len(credentials))
		for key := range credentials {
			filled = append(filled, key)
		}
		sort.Strings(filled)

		slog.Info("sent document", "filename", p.Filename, "bytes", len(pdf), "credentials", len(filled))

		return json.Marshal(SendPDFResponse{
			Filename:       p.Filename,
			Bytes:          len(pdf),
			MessageID:      string(messageID),
			CredentialsSet: filled,
		})
	}
}

// checkFilename rejects a name that would be more than a name. It reaches a
// multipart Content-Disposition header, which does not escape newlines.
func checkFilename(name string) error {
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("filename %q must be a plain name, with no path separators", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("filename %q must not contain control characters", name)
		}
	}
	return nil
}

// readCredentials resolves the keys a document references. The values are
// handed to the renderer and never returned to the agent.
func readCredentials(ctx context.Context, deps Deps, markdown string) (map[string]string, error) {
	keys := markdownpdf.CredentialKeys(markdown)
	if len(keys) == 0 {
		return nil, nil
	}
	if deps.SecretStore == nil {
		return nil, fmt.Errorf("document has credential placeholders but no secret store is configured")
	}

	var missing []string
	values := map[string]string{}
	for _, key := range keys {
		storeKey := DocumentKeyPrefix + key
		value, err := deps.SecretStore.Get(ctx, storeKey)
		if err != nil {
			return nil, fmt.Errorf("read credential %q: %w", storeKey, err)
		}
		if value == "" {
			missing = append(missing, storeKey)
			continue
		}
		values[key] = value
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("no value set for %s — use secret_form_request with that exact key to have the user fill it in",
			strings.Join(missing, ", "))
	}
	return values, nil
}

// resolveInMemoryDir turns an agent-supplied relative path into an absolute one
// inside the memory directory, refusing anything that points outside it.
func resolveInMemoryDir(memoryDir, relative string) (string, error) {
	if memoryDir == "" {
		return "", fmt.Errorf("no memory directory is configured, so %q cannot be resolved", relative)
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("path %q must be relative to your memory directory", relative)
	}

	// checked before touching the filesystem, so a path that escapes says so
	// rather than reporting whatever file happens not to exist
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q points outside your memory directory", relative)
	}

	root, err := filepath.EvalSymlinks(memoryDir)
	if err != nil {
		return "", fmt.Errorf("resolve memory directory: %w", err)
	}
	// symlinks are resolved on both sides so a link out of the tree cannot slip past
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", relative, err)
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q points outside your memory directory", relative)
	}
	return resolved, nil
}
