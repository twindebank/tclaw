package onboardingtools

import (
	"context"
	"encoding/json"
	"fmt"

	"tclaw/mcp"
	"tclaw/onboarding"
)

func onboardingSetInfoDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        ToolSetInfo,
		Description: "Record that a piece of user info has been gathered during onboarding. The actual info is stored in the user's CLAUDE.md memory — this just tracks progress. Call this after writing the info to memory.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"field": {
					"type": "string",
					"description": "The info field that was gathered.",
					"enum": ["name", "home_location", "work_location"]
				}
			},
			"required": ["field"]
		}`),
	}
}

type setInfoArgs struct {
	Field string `json:"field"`
}

func onboardingSetInfoHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a setInfoArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Field == "" {
			return nil, fmt.Errorf("field is required")
		}

		state, err := deps.Store.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("get onboarding state: %w", err)
		}
		if state == nil {
			return nil, fmt.Errorf("onboarding has not been initialized")
		}

		if state.InfoGathered == nil {
			state.InfoGathered = make(map[string]bool)
		}
		state.InfoGathered[a.Field] = true

		if err := deps.Store.Set(ctx, state); err != nil {
			return nil, fmt.Errorf("save onboarding state: %w", err)
		}

		// Check what's still missing.
		var missing []string
		for _, f := range onboarding.AllInfoFields {
			if !state.InfoGathered[f] {
				missing = append(missing, f)
			}
		}

		result := map[string]any{
			"recorded": a.Field,
			"missing":  missing,
			"message":  fmt.Sprintf("Recorded %s. %d info fields remaining.", a.Field, len(missing)),
		}
		return json.Marshal(result)
	}
}
