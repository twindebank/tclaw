package credentialtools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
)

const authWaitTimeout = 5 * time.Minute

func credentialAuthWaitDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolCredentialAuthWait,
		Description: "Wait for a pending OAuth authorization to complete. Call this after sending the auth URL to the user. Blocks until the user finishes authorizing (up to 5 minutes).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"credential_set_id": {
					"type": "string",
					"description": "The credential set ID to wait for (e.g. 'google/work')."
				}
			},
			"required": ["credential_set_id"]
		}`),
	}
}

type credentialAuthWaitArgs struct {
	CredentialSetID string `json:"credential_set_id"`
}

func credentialAuthWaitHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a credentialAuthWaitArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.CredentialSetID == "" {
			return nil, fmt.Errorf("credential_set_id is required")
		}

		setID := credential.CredentialSetID(a.CredentialSetID)

		// Check if OAuth tokens already exist (callback already fired).
		tokens, err := deps.CredentialManager.GetOAuthTokens(ctx, setID)
		if err != nil {
			return nil, fmt.Errorf("check oauth tokens: %w", err)
		}
		if tokens != nil && tokens.AccessToken != "" {
			return json.Marshal(map[string]string{
				"credential_set_id": string(setID),
				"status":            "authorized",
				"message":           fmt.Sprintf("Credential set %s is authorized and ready to use.", setID),
			})
		}

		// Poll for tokens until timeout or context cancellation.
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		deadline := time.After(authWaitTimeout)

		for {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("authorization wait cancelled")

			case <-deadline:
				return json.Marshal(map[string]string{
					"credential_set_id": string(setID),
					"status":            "timeout",
					"message":           fmt.Sprintf("Authorization timed out after %s. The user may not have completed the OAuth flow. They can try again with credential_add.", authWaitTimeout),
				})

			case <-ticker.C:
				tokens, err := deps.CredentialManager.GetOAuthTokens(ctx, setID)
				if err != nil {
					return nil, fmt.Errorf("check oauth tokens: %w", err)
				}
				if tokens != nil && tokens.AccessToken != "" {
					return json.Marshal(map[string]string{
						"credential_set_id": string(setID),
						"status":            "authorized",
						"message":           fmt.Sprintf("Credential set %s is now authorized and ready to use!", setID),
					})
				}
			}
		}
	}
}
