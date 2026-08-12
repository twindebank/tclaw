package credentialtools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
)

const ToolCredentialClear = "credential_clear"

func credentialClearDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolCredentialClear,
		Description: "Clear the stored value of one credential field, leaving the credential itself declared. " +
			"Use this to revoke a token without losing the declaration — anything referencing it keeps working once refilled. " +
			"To fill it again, call secret_form_request with a credential target of the same type, label and field. " +
			"To delete the credential outright, use credential_remove instead.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"credential_set_id": {
					"type": "string",
					"description": "Credential set ID as '<type>/<label>' (e.g. 'git/homeassistant'). credential_list shows the available ones."
				},
				"field": {
					"type": "string",
					"description": "Field to clear (e.g. 'token')."
				}
			},
			"required": ["credential_set_id", "field"]
		}`),
	}
}

type credentialClearArgs struct {
	CredentialSetID string `json:"credential_set_id"`
	Field           string `json:"field"`
}

func credentialClearHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a credentialClearArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.CredentialSetID == "" {
			return nil, fmt.Errorf("credential_set_id is required")
		}
		if a.Field == "" {
			return nil, fmt.Errorf("field is required")
		}

		id := credential.CredentialSetID(a.CredentialSetID)
		set, err := deps.CredentialManager.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("look up credential set: %w", err)
		}
		if set == nil {
			return nil, fmt.Errorf("credential set %q not found — use credential_list to see existing sets", id)
		}

		// Writing an empty value rather than deleting the key keeps the field
		// visible as declared-but-unset, which is what credential_list reports.
		if err := deps.CredentialManager.SetField(ctx, id, a.Field, ""); err != nil {
			return nil, fmt.Errorf("clear field %s on %s: %w", a.Field, id, err)
		}

		if deps.OnCredentialChange != nil {
			deps.OnCredentialChange(set.Package)
		}

		return json.Marshal(map[string]string{
			"status": "cleared",
			"message": fmt.Sprintf("Field %q on %s cleared. The credential is still declared — refill it with "+
				"secret_form_request using credential {type: %q, label: %q, field: %q}.",
				a.Field, id, set.Package, set.Label, a.Field),
		})
	}
}
