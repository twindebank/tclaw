package modeltools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/claudecli"
	"tclaw/mcp"
)

func modelGetDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "model_get",
		Description: "Get the currently configured model. Returns 'auto' if no override is set (CLI chooses the model).",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

type modelGetResponse struct {
	Model     string `json:"model"`
	ShortName string `json:"short_name"`
}

func modelGetHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		raw, err := deps.Store.Get(ctx, storeKey)
		if err != nil {
			return nil, fmt.Errorf("read model override: %w", err)
		}

		model := claudecli.Model(string(raw))
		if model == "" {
			return json.Marshal(modelGetResponse{
				Model:     "auto",
				ShortName: "auto",
			})
		}

		return json.Marshal(modelGetResponse{
			Model:     string(model),
			ShortName: model.ShortName(),
		})
	}
}
