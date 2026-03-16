package modeltools

import (
	"context"
	"encoding/json"

	"tclaw/claudecli"
	"tclaw/mcp"
)

func modelListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "model_list",
		Description: "List all available Claude models with their short names and full identifiers.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

type modelEntry struct {
	ShortName string `json:"short_name"`
	ModelID   string `json:"model_id"`
}

func modelListHandler() mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		models := claudecli.ValidModels()
		entries := make([]modelEntry, 0, len(models)+1)

		// "auto" is always first — it means let the CLI choose.
		entries = append(entries, modelEntry{
			ShortName: "auto",
			ModelID:   "(CLI default — no --model flag)",
		})

		for _, m := range models {
			entries = append(entries, modelEntry{
				ShortName: m.ShortName(),
				ModelID:   string(m),
			})
		}

		return json.Marshal(entries)
	}
}
