package credentialtools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/credential"
	"tclaw/mcp"
)

func credentialRemoveDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolCredentialRemove,
		Description: "Remove a credential set and delete all its stored secrets. The associated tools will become unavailable. Use credential_list to see existing sets.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"credential_set_id": {
					"type": "string",
					"description": "The credential set ID to remove (e.g. 'google/work'). Use credential_list to find IDs."
				}
			},
			"required": ["credential_set_id"]
		}`),
	}
}

type credentialRemoveArgs struct {
	CredentialSetID string `json:"credential_set_id"`
}

func credentialRemoveHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a credentialRemoveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.CredentialSetID == "" {
			return nil, fmt.Errorf("credential_set_id is required")
		}

		id := credential.CredentialSetID(a.CredentialSetID)

		// Look up the set to find its package name for the change callback.
		set, err := deps.CredentialManager.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("look up credential set: %w", err)
		}
		if set == nil {
			return nil, fmt.Errorf("credential set %q not found — use credential_list to see existing sets", id)
		}

		packageName := set.Package

		if err := deps.CredentialManager.Remove(ctx, id); err != nil {
			return nil, fmt.Errorf("remove credential set: %w", err)
		}

		if deps.OnCredentialChange != nil {
			deps.OnCredentialChange(packageName)
		}

		return json.Marshal(map[string]string{
			"status":  "removed",
			"message": fmt.Sprintf("Credential set %s removed. Associated tools are no longer available.", id),
		})
	}
}
