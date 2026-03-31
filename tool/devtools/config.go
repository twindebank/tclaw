package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"tclaw/mcp"
)

const ToolConfigGet = "config_get"

func configGetDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolConfigGet,
		Description: `Read the active tclaw.yaml config file.

Returns the full YAML content of the running config. In production this is
/data/tclaw.yaml on the persistent Fly volume (seeded from the image on first
boot). Agent mutations (channel create/edit/delete) write here and survive
redeploys.

Use this to inspect current channel config, providers, users, or any other
settings before making changes with config_set.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

func configGetHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		if deps.ConfigPath == "" {
			return nil, fmt.Errorf("config path not set — cannot read config")
		}

		content, err := os.ReadFile(deps.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}

		result := map[string]any{
			"path":    deps.ConfigPath,
			"content": string(content),
		}
		return json.Marshal(result)
	}
}

const ToolConfigSet = "config_set"

func configSetDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolConfigSet,
		Description: `Update the active tclaw.yaml config file.

Takes the full YAML content as a string. The YAML is validated before writing.
The file is updated immediately — no restart needed for most changes.

The config lives on the persistent Fly volume and survives redeploys. A seed
copy is baked into the Docker image for first boot only. Use 'tclaw config push'
from the dev CLI to sync local changes to the remote volume, or
'tclaw config pull' to retrieve agent-made changes.`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"content": {
					"type": "string",
					"description": "Full YAML content for tclaw.yaml. Must be valid YAML."
				}
			},
			"required": ["content"]
		}`),
	}
}

type configSetArgs struct {
	Content string `json:"content"`
}

func configSetHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a configSetArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Content == "" {
			return nil, fmt.Errorf("content is required")
		}

		// Validate YAML before writing anything.
		var parsed any
		if err := yaml.Unmarshal([]byte(a.Content), &parsed); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}

		if deps.ConfigPath == "" {
			return nil, fmt.Errorf("config path not set — cannot write config")
		}

		if err := os.WriteFile(deps.ConfigPath, []byte(a.Content), 0o644); err != nil {
			return nil, fmt.Errorf("write config file: %w", err)
		}

		result := map[string]any{
			"path":    deps.ConfigPath,
			"message": fmt.Sprintf("Config written to %s.", deps.ConfigPath),
		}
		return json.Marshal(result)
	}
}
