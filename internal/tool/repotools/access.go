package repotools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tclaw/internal/mcp"
	"tclaw/internal/repo"
)

const ToolRequestAccess = "repo_request_access"

func repoRequestAccessDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name: ToolRequestAccess,
		Description: "Change what you may do with a tracked repo's remote.\n\n" +
			"Access levels:\n" +
			"- read_only: fetch only. The clone is mounted read-only.\n" +
			"- pull_requests_only: push any branch except the default one, and open PRs. " +
			"Pushing to the default branch is refused by the transport, so changes reach it only through a reviewed PR.\n" +
			"- full_write: push anywhere, including the default branch.\n\n" +
			"Raising access needs the user's confirmation: this tool sends them a prompt and returns " +
			"status \"awaiting_confirmation\". Do NOT answer that prompt yourself and do not send further " +
			"messages about it — only their reply grants it, and the grant applies on their \"yes\". " +
			"Lowering access applies immediately.\n\n" +
			"Once granted, use ordinary git in the clone (pull, commit, push) and gh to open PRs.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {
					"type": "string",
					"description": "Repo name, as shown by repo_list."
				},
				"access": {
					"type": "string",
					"enum": ["read_only", "pull_requests_only", "full_write"],
					"description": "Access level being requested."
				},
				"reason": {
					"type": "string",
					"description": "Why you need it, shown to the user in the confirmation prompt so they are deciding on a described change."
				},
				"credential": {
					"type": "string",
					"description": "Optional git credential slot label to authenticate with (credential_list shows the declared ones). Omit to use the default."
				},
				"expires_in": {
					"type": "string",
					"description": "Optional: drop back to read_only after this long, e.g. '30d', '12h'. Omit for an open-ended grant."
				}
			},
			"required": ["name", "access", "reason"]
		}`),
	}
}

type repoRequestAccessArgs struct {
	Name       string `json:"name"`
	Access     string `json:"access"`
	Reason     string `json:"reason"`
	Credential string `json:"credential"`
	ExpiresIn  string `json:"expires_in"`
}

func repoRequestAccessHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a repoRequestAccessArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Name == "" {
			return nil, fmt.Errorf("name is required")
		}
		if a.Reason == "" {
			return nil, fmt.Errorf("reason is required — the user sees it when deciding")
		}

		access := repo.Access(a.Access)
		if !repo.ValidAccess(access) {
			return nil, fmt.Errorf("unknown access %q (known: %v)", a.Access, repo.ValidAccessTiers())
		}

		tracked, err := deps.Store.Get(ctx, a.Name)
		if err != nil {
			return nil, err
		}
		if tracked == nil {
			return nil, fmt.Errorf("no tracked repo named %q — use repo_list to see what's available", a.Name)
		}
		if _, ok := deps.visibleTo(*tracked); !ok {
			return nil, errNotVisible(a.Name)
		}

		expiresAt, err := parseExpiry(a.ExpiresIn)
		if err != nil {
			return nil, err
		}

		current := tracked.EffectiveAccess(time.Now())
		if !access.Exceeds(current) {
			// A downgrade takes nothing away from the user, so it applies
			// without asking.
			tracked.Access = access
			tracked.DropToReadOnlyAt = expiresAt
			if a.Credential != "" {
				tracked.Credential = a.Credential
			}
			if err := deps.Store.Put(ctx, *tracked); err != nil {
				return nil, fmt.Errorf("save access: %w", err)
			}
			return json.Marshal(map[string]any{
				"name":    a.Name,
				"access":  access,
				"status":  "applied",
				"message": fmt.Sprintf("Repo %q is now %s.", a.Name, access),
			})
		}

		if deps.ArmGrant == nil {
			return nil, fmt.Errorf("access grants are unavailable — no way to ask the user for confirmation")
		}
		if err := deps.ArmGrant(ctx, GrantRequest{
			Repo:             a.Name,
			Access:           access,
			Credential:       a.Credential,
			DropToReadOnlyAt: expiresAt,
			Reason:           a.Reason,
		}); err != nil {
			return nil, fmt.Errorf("ask for confirmation: %w", err)
		}

		return json.Marshal(map[string]any{
			"name":   a.Name,
			"access": access,
			"status": "awaiting_confirmation",
			"message": fmt.Sprintf("Asked the user to confirm %s access to %q. It applies only when they reply. "+
				"Say nothing further about it — do not answer the prompt yourself.", access, a.Name),
		})
	}
}

// parseExpiry converts a relative window such as "30d" or "12h" into the instant
// the grant lapses. Days are supported on top of Go's duration syntax because
// that is the unit a grant is naturally described in.
func parseExpiry(expiresIn string) (time.Time, error) {
	expiresIn = strings.TrimSpace(expiresIn)
	if expiresIn == "" {
		return time.Time{}, nil
	}

	if days, ok := strings.CutSuffix(expiresIn, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid expires_in %q: %w", expiresIn, err)
		}
		if n <= 0 {
			return time.Time{}, fmt.Errorf("invalid expires_in %q: must be positive", expiresIn)
		}
		return time.Now().Add(time.Duration(n) * 24 * time.Hour), nil
	}

	d, err := time.ParseDuration(expiresIn)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid expires_in %q (try '30d' or '12h'): %w", expiresIn, err)
	}
	if d <= 0 {
		return time.Time{}, fmt.Errorf("invalid expires_in %q: must be positive", expiresIn)
	}
	return time.Now().Add(d), nil
}
