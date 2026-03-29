package credentialtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"tclaw/credential"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
)

func credentialListDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolCredentialList,
		Description: `List all credential sets across tool packages, showing their package, label, channel scope, field status, and readiness.

Each credential set shows:
- Which tool package it belongs to (e.g. "google", "tfl", "monzo")
- Its label (e.g. "work", "personal", "default")
- Which channel it's scoped to (empty = available everywhere)
- Whether all required fields are configured
- Whether OAuth tokens are present (for OAuth packages)
- What tools become available when the set is ready`,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"package": {
					"type": "string",
					"description": "Filter by package name (e.g. 'google'). Omit to list all."
				}
			}
		}`),
	}
}

type credentialListArgs struct {
	Package string `json:"package"`
}

type credentialSetInfo struct {
	ID      credential.CredentialSetID `json:"id"`
	Package string                     `json:"package"`
	Label   string                     `json:"label"`
	Channel string                     `json:"channel,omitempty"`
	Ready   bool                       `json:"ready"`
	Fields  []fieldStatus              `json:"fields"`
}

type fieldStatus struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Required   bool   `json:"required"`
	Configured bool   `json:"configured"`
}

func credentialListHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a credentialListArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		var sets []credential.CredentialSet
		var err error
		if a.Package != "" {
			sets, err = deps.CredentialManager.ListByPackage(ctx, a.Package)
		} else {
			sets, err = deps.CredentialManager.List(ctx)
		}
		if err != nil {
			return nil, fmt.Errorf("list credential sets: %w", err)
		}

		// Build a map of package name → CredentialSpec for field info.
		specMap := make(map[string]toolpkg.CredentialSpec)
		for _, cp := range deps.Registry.CredentialProviders() {
			specMap[cp.Name()] = cp.CredentialSpec()
		}

		result := make([]credentialSetInfo, 0, len(sets))
		for _, s := range sets {
			info := credentialSetInfo{
				ID:      s.ID,
				Package: s.Package,
				Label:   s.Label,
				Channel: s.Channel,
			}

			spec, hasSpec := specMap[s.Package]
			if hasSpec {
				// Check each declared field.
				for _, f := range spec.Fields {
					val, fieldErr := deps.CredentialManager.GetField(ctx, s.ID, f.Key)
					if fieldErr != nil {
						slog.Warn("failed to check credential field", "set", s.ID, "field", f.Key, "err", fieldErr)
					}
					info.Fields = append(info.Fields, fieldStatus{
						Key:        f.Key,
						Label:      f.Label,
						Required:   f.Required,
						Configured: val != "",
					})
				}

				ready, readyErr := deps.CredentialManager.IsReady(
					ctx, s.ID, spec.RequiredFieldKeys(), spec.NeedsOAuth(),
				)
				if readyErr != nil {
					slog.Warn("failed to check credential readiness", "set", s.ID, "err", readyErr)
				}
				info.Ready = ready
			}

			result = append(result, info)
		}

		return json.Marshal(result)
	}
}
