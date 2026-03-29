package modeltools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/claudecli"
	"tclaw/mcp"
)

const ToolSet = "model_set"

func modelSetDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolSet,
		Description: "Set the model for subsequent turns. Use a short name (e.g. 'opus-4.6', 'sonnet-4.6') or full model ID. Use 'auto' to let the CLI choose. Takes effect on the next turn.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"model": {
					"type": "string",
					"description": "Model short name (e.g. 'opus-4.6', 'sonnet-4.6', 'haiku-4.5') or full model ID, or 'auto' to clear override"
				}
			},
			"required": ["model"]
		}`),
	}
}

type modelSetParams struct {
	Model string `json:"model"`
}

func modelSetHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var params modelSetParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("parse params: %w", err)
		}

		input := strings.TrimSpace(params.Model)
		if input == "" {
			return nil, fmt.Errorf("model is required")
		}

		// "auto" clears the override.
		if strings.EqualFold(input, "auto") {
			if err := deps.Store.Set(ctx, storeKey, []byte("")); err != nil {
				return nil, fmt.Errorf("clear model override: %w", err)
			}
			return json.Marshal("Model set to auto (CLI will choose). Takes effect on the next turn.")
		}

		// Try short name lookup first, then treat as a full model ID.
		model, ok := claudecli.ShortNameToModel[strings.ToLower(input)]
		if !ok {
			model = claudecli.Model(input)
		}

		if !claudecli.ValidModel(model) {
			return nil, fmt.Errorf("unknown model %q — use model_list to see available models", input)
		}

		if err := deps.Store.Set(ctx, storeKey, []byte(model)); err != nil {
			return nil, fmt.Errorf("set model override: %w", err)
		}

		return json.Marshal(fmt.Sprintf("Model set to %s (%s). Takes effect on the next turn.", model.ShortName(), model))
	}
}
