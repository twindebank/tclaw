package google

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/connection"
	"tclaw/gws"
	"tclaw/mcp"
)

type workspaceArgs struct {
	Connection string `json:"connection"`
	Command    string `json:"command"`
	Params     string `json:"params"`
	Body       string `json:"body"`
}

func workspaceHandler(connMap map[connection.ConnectionID]Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a workspaceArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(connMap, a.Connection)
		if err != nil {
			return nil, err
		}

		if a.Command == "" {
			return nil, fmt.Errorf("command is required (e.g. 'gmail users messages list')")
		}

		return runGWS(ctx, deps, gws.Raw(a.Command, a.Params, a.Body))
	}
}
