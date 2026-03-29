package google

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/credential"
	"tclaw/gws"
	"tclaw/mcp"
)

type schemaArgs struct {
	CredentialSet string `json:"credential_set"`
	Method        string `json:"method"`
}

func schemaHandler(depsMap map[credential.CredentialSetID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a schemaArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(depsMap, a.CredentialSet)
		if err != nil {
			return nil, err
		}

		if a.Method == "" {
			return nil, fmt.Errorf("method is required (e.g. 'gmail.users.messages.list')")
		}

		output, err := runGWSRaw(ctx, deps, gws.Schema(a.Method))
		if err != nil {
			return nil, err
		}

		return json.Marshal(output)
	}
}
