package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/mcp"
)

type workspaceArgs struct {
	Connection string `json:"connection"`
	Command    string `json:"command"`
	Params     string `json:"params"`
	Body       string `json:"body"`
}

func workspaceHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a workspaceArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Command == "" {
			return nil, fmt.Errorf("command is required (e.g. 'gmail users messages list')")
		}

		// Split command string into args for exec.
		args := strings.Fields(a.Command)

		if a.Params != "" {
			args = append(args, "--params", a.Params)
		}
		if a.Body != "" {
			args = append(args, "--json", a.Body)
		}

		return runGWS(ctx, deps, args...)
	}
}
