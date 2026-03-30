package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"

	"tclaw/mcp"
)

const (
	// configSecretName is the GitHub secret that seeds tclaw.yaml on fresh deploys.
	// CI writes it to /etc/tclaw/tclaw.yaml in the image; on first boot, it's
	// copied to the persistent volume at /data/tclaw.yaml where all runtime
	// mutations happen.
	configSecretName = "TCLAW_YAML"
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
		Description: `Update the tclaw.yaml config and persist it to the TCLAW_YAML GitHub secret.

Takes the full YAML content as a string. The YAML is validated before writing.
Two things happen:
1. The file at the active config path is updated immediately (takes effect now, no restart needed for most changes).
2. The TCLAW_YAML secret on twindebank/tclaw is updated so the change survives the next deploy.

Requires:
- github_token in the secret store (from dev_start, or re-provide it here)
- repo_url in the dev store (from dev_start)

Secret store key: github_token`,
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

		token, err := deps.SecretStore.Get(ctx, githubTokenKey)
		if err != nil {
			return nil, fmt.Errorf("read github token: %w", err)
		}
		if token == "" {
			return nil, fmt.Errorf("github_token not set — run dev_start once to configure it")
		}

		repoURL, err := deps.Store.GetRepoURL(ctx)
		if err != nil {
			return nil, fmt.Errorf("read repo url: %w", err)
		}
		if repoURL == "" {
			return nil, fmt.Errorf("repo URL not configured — run dev_start once to set it up")
		}

		// Extract owner/repo from the GitHub URL (e.g. https://github.com/twindebank/tclaw).
		repoSlug := strings.TrimPrefix(repoURL, "https://github.com/")
		repoSlug = strings.TrimSuffix(repoSlug, ".git")
		if repoSlug == repoURL || !strings.Contains(repoSlug, "/") {
			return nil, fmt.Errorf("unexpected repo URL format %q — expected https://github.com/owner/repo", repoURL)
		}

		// Update the GitHub secret so the change survives the next deploy.
		if err := ghSecretSet(repoSlug, configSecretName, a.Content, token); err != nil {
			return nil, fmt.Errorf("update GitHub secret: %w", err)
		}

		// Write to the active config path so the change takes effect immediately
		// without waiting for a redeploy.
		if err := os.WriteFile(deps.ConfigPath, []byte(a.Content), 0o644); err != nil {
			return nil, fmt.Errorf("write config file: %w", err)
		}

		result := map[string]any{
			"path":    deps.ConfigPath,
			"repo":    repoSlug,
			"secret":  configSecretName,
			"message": fmt.Sprintf("Config written to %s and persisted to GitHub secret %s on %s. Local change takes effect immediately; the secret will be used on next deploy.", deps.ConfigPath, configSecretName, repoSlug),
		}
		return json.Marshal(result)
	}
}

// ghSecretSet updates a GitHub Actions secret using the gh CLI.
func ghSecretSet(repoSlug, secretName, value, token string) error {
	cmd := exec.Command("gh", "secret", "set", secretName,
		"--repo", repoSlug,
		"--body", value,
	)
	cmd.Env = ghEnv(token)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
