package remotemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"tclaw/internal/mcp"
)

const ToolRemoteMCPUpdate = "remote_mcp_update"

func remoteMCPUpdateDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolRemoteMCPUpdate,
		Description: "Change which channels a connected remote MCP server's tools reach. Send the complete " +
			"list of channels the server should reach: it replaces the stored list rather than adding to it, " +
			"so read the current list from remote_mcp_list first and include the channels you are keeping. " +
			"Use this instead of registering a second copy of a server another channel already has: a second " +
			"copy needs its own credentials and its own tool discovery. A name the server is already scoped " +
			"to is accepted even when that channel has since been deleted, so you can drop it by leaving it " +
			"out; the reply names any such channel, because it reaches nothing.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "The name of the remote MCP to change. Use remote_mcp_list to see names."
				},
				"channels": {
					"type": "array",
					"items": {"type": "string"},
					"description": "The complete list of channel names this server's tools should reach, replacing the stored list.",
					"minItems": 1
				}
			},
			"required": ["name", "channels"]
		}`),
	}
}

type remoteMCPUpdateArgs struct {
	Name     string   `json:"name"`
	Channels []string `json:"channels,omitempty"`
}

func remoteMCPUpdateHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a remoteMCPUpdateArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Name == "" {
			return nil, fmt.Errorf("name is required — use %s to see the connected servers", ToolRemoteMCPList)
		}
		if a.Channels == nil {
			return nil, fmt.Errorf("nothing to update — pass channels with the complete list this server's tools should reach")
		}

		entry, err := deps.Manager.GetRemoteMCP(ctx, a.Name)
		if err != nil {
			return nil, fmt.Errorf("read remote mcp: %w", err)
		}
		if entry == nil {
			return nil, fmt.Errorf("remote mcp %q not found — use %s to see the connected servers", a.Name, ToolRemoteMCPList)
		}

		scope, err := cleanChannels(cleanChannelsParams{
			ChannelNames:  deps.ChannelNames,
			AlreadyScoped: entry.Channels,
			Names:         a.Channels,
		})
		if err != nil {
			return nil, err
		}
		if len(scope.Unmatched) > 0 {
			slog.Warn("remote mcp keeps a channel name that no channel has",
				"name", a.Name, "channels", scope.Unmatched)
		}
		if err := deps.Manager.SetChannels(ctx, a.Name, scope.Channels); err != nil {
			return nil, fmt.Errorf("update remote mcp: %w", err)
		}

		if updateErr := deps.ConfigUpdater(ctx); updateErr != nil {
			return nil, fmt.Errorf("remote MCP %q updated but config update failed — the change won't apply until next restart: %w", a.Name, updateErr)
		}
		if deps.OnChannelChange != nil {
			deps.OnChannelChange()
		}

		result := map[string]any{
			"name":     a.Name,
			"channels": scope.Channels,
			"message": fmt.Sprintf("Remote MCP %q now reaches %d channel(s). Its tools will follow the new list from the next message.",
				a.Name, len(scope.Channels)-len(scope.Unmatched)),
		}
		if len(scope.Unmatched) > 0 {
			// Counting these as reach would tell the agent a dead name is live scope.
			result["channels_matching_nothing"] = scope.Unmatched
			result["message"] = fmt.Sprintf("%s These names match no channel and reach nothing, and were kept only so you can drop them by leaving them out: %s.",
				result["message"], strings.Join(quoteAll(scope.Unmatched), ", "))
		}
		return json.Marshal(result)
	}
}
