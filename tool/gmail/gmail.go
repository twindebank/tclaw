package gmail

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"tclaw/connection"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/provider"

	"golang.org/x/oauth2"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// Deps holds dependencies for gmail tool handlers.
type Deps struct {
	ConnID   connection.ConnectionID
	Manager  *connection.Manager
	Provider *provider.Provider
}

// RegisterTools adds all gmail tools for a specific connection to the MCP handler.
func RegisterTools(h *mcp.Handler, deps Deps) {
	defs := ToolDefs(deps.ConnID)
	// defs order matches: search, read, send, reply, list_labels
	h.Register(defs[0], searchHandler(deps))
	h.Register(defs[1], readHandler(deps))
	h.Register(defs[2], sendHandler(deps))
	h.Register(defs[3], replyHandler(deps))
	h.Register(defs[4], listLabelsHandler(deps))
}

// gmailClient builds an authenticated Gmail API client for the connection,
// refreshing the token if needed.
func gmailClient(ctx context.Context, deps Deps) (*gmailapi.Service, error) {
	refreshFn := func(ctx context.Context, refreshToken string) (*connection.Credentials, error) {
		return oauth.RefreshToken(ctx, deps.Provider.OAuth2, refreshToken)
	}
	creds, err := deps.Manager.RefreshIfNeeded(ctx, deps.ConnID, refreshFn)
	if err != nil {
		return nil, fmt.Errorf("get credentials for %s: %w", deps.ConnID, err)
	}

	tokenSrc := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: creds.AccessToken,
	})
	svc, err := gmailapi.NewService(ctx, option.WithHTTPClient(oauth2.NewClient(ctx, tokenSrc)))
	if err != nil {
		return nil, fmt.Errorf("create gmail client: %w", err)
	}
	return svc, nil
}

// extractBody walks the MIME parts to find the plain text body.
// Falls back to HTML if no plain text part exists.
func extractBody(payload *gmailapi.MessagePart) string {
	if payload == nil {
		return ""
	}

	// Single-part message.
	if payload.MimeType == "text/plain" && payload.Body != nil && payload.Body.Data != "" {
		return decodeBase64URL(payload.Body.Data)
	}

	// Multipart — look for text/plain first, then text/html.
	var htmlBody string
	for _, part := range payload.Parts {
		switch part.MimeType {
		case "text/plain":
			if part.Body != nil && part.Body.Data != "" {
				return decodeBase64URL(part.Body.Data)
			}
		case "text/html":
			if part.Body != nil && part.Body.Data != "" {
				htmlBody = decodeBase64URL(part.Body.Data)
			}
		case "multipart/alternative", "multipart/mixed", "multipart/related":
			if body := extractBody(part); body != "" {
				return body
			}
		}
	}

	return htmlBody
}

func decodeBase64URL(data string) string {
	decoded, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		// Data is already readable text, return as-is.
		return data
	}
	return string(decoded)
}

// buildRawEmail constructs an RFC 2822 email message.
func buildRawEmail(to, subject, body, cc, bcc, threadID, inReplyTo string) string {
	var b strings.Builder
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if cc != "" {
		b.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	if bcc != "" {
		b.WriteString(fmt.Sprintf("Bcc: %s\r\n", bcc))
	}
	b.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	if inReplyTo != "" {
		b.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", inReplyTo))
		b.WriteString(fmt.Sprintf("References: %s\r\n", inReplyTo))
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}
