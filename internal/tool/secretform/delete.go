package secretform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/internal/mcp"
)

const ToolDelete = "secret_form_delete"

// DeleteRequest is a proposed deletion, passed to the router to put to the user.
type DeleteRequest struct {
	Key    string
	Reason string
}

// ValidateDeletableKey returns an error when a key does not name a secret the
// agent may ask to have deleted.
func ValidateDeletableKey(key string, collected map[string]bool) error {
	if err := validateKeyShape(key); err != nil {
		return err
	}
	if collected == nil {
		// An allowlist that is not known allows nothing. Both the tool and the
		// router inherit this, so neither can turn into a no-op gate.
		return fmt.Errorf("the set of user-supplied secrets is not known here, so nothing can be deleted")
	}
	if !collected[key] {
		return fmt.Errorf("%q was not collected from you through a secret form, so it is not the agent's to delete — "+
			"a tool package's own key is cleared through that package, and a credential slot through credential_clear", key)
	}
	return nil
}

func deleteDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolDelete,
		Description: "Ask the user to delete a stored secret. Use it to clear a value that is no longer " +
			"needed — a credential replaced by another, or one collected by mistake. The user is asked in " +
			"the chat and the secret is removed only when they reply; you cannot delete one on your own, " +
			"and the value cannot be recovered afterwards. Only secrets named by a plain key are reachable: " +
			"credential slots (credential_clear), channel tokens and remote MCP auth are not.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"key": {
					"type": "string",
					"description": "The secret store key to delete, e.g. 'old_api_token'. Lowercase letters, digits and underscores."
				},
				"reason": {
					"type": "string",
					"description": "Why it should go, in one line. The user sees this in the confirmation prompt, so it is what they decide on."
				}
			},
			"required": ["key", "reason"]
		}`),
	}
}

// collectedForDeps reads the allowlist, returning nil only alongside an error so
// a missing state store can never read as "nothing is protected".
func collectedForDeps(ctx context.Context, deps Deps) (map[string]bool, error) {
	if deps.StateStore == nil {
		return nil, fmt.Errorf("no state store here, so which secrets you supplied is unknown and none can be deleted")
	}
	return CollectedKeys(ctx, deps.StateStore)
}

type deleteArgs struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

func deleteHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a deleteArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		collected, err := collectedForDeps(ctx, deps)
		if err != nil {
			return nil, err
		}
		if err := ValidateDeletableKey(a.Key, collected); err != nil {
			return nil, err
		}
		reason := strings.TrimSpace(a.Reason)
		if reason == "" {
			return nil, fmt.Errorf("reason is required — the user decides on it, so it has to say why the secret should go")
		}
		if deps.ArmSecretDelete == nil {
			return nil, fmt.Errorf("no channel can be asked to confirm a deletion on this instance")
		}

		if err := deps.ArmSecretDelete(ctx, DeleteRequest{Key: a.Key, Reason: reason}); err != nil {
			return nil, fmt.Errorf("ask for confirmation: %w", err)
		}

		return json.Marshal(map[string]any{
			"key":    a.Key,
			"status": "awaiting_confirmation",
			"message": fmt.Sprintf("Asked the user to confirm deleting %q. It is removed only when they reply, "+
				"and you will not be told again — carry on with something else.", a.Key),
		})
	}
}
