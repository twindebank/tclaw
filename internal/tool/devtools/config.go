package devtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"tclaw/internal/mcp"
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

Changes persist across redeploys (the config lives on the persistent volume).
Use config_get to read the current config before making changes.

Permission-bearing sections are off limits and rejected if changed:
credential_slots, repos, users, tool_groups, allowed_tools, creatable_groups.
Those are the operator's to set from their machine with "tclaw config push" —
ask them rather than editing. Use repo_request_access to ask for repo access.`,
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

		// Config is where permissions are declared, so an agent that can
		// rewrite it wholesale can grant itself anything — which would make
		// every confirmation gate elsewhere decorative. Compare the protected
		// sections against what's on disk and refuse any change to them.
		current, err := os.ReadFile(deps.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("read current config: %w", err)
		}
		if err := checkProtectedSections(current, []byte(a.Content)); err != nil {
			return nil, err
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

// protectedTopLevelSections are config keys the agent may never change: they
// declare what it is allowed to do. Editing them is the operator's job, from
// their own machine via "tclaw config push".
var protectedTopLevelSections = []string{"credential_slots"}

// protectedUserFields are per-user keys with the same problem — they decide the
// agent's own capabilities, so it must not be able to widen them.
var protectedUserFields = []string{
	"repos", "tool_groups", "allowed_tools", "disallowed_tools", "creatable_groups",
}

// checkProtectedSections reports whether a proposed config changes anything
// permission-bearing. It compares the parsed values rather than the raw text so
// reformatting, reordering or a comment edit elsewhere is not mistaken for a
// permission change.
//
// Anything it cannot parse is refused: a config whose permissions cannot be
// compared is one whose permissions cannot be shown to be unchanged.
func checkProtectedSections(current, proposed []byte) error {
	currentTree, err := parseConfigTree(current)
	if err != nil {
		return fmt.Errorf("parse current config: %w", err)
	}
	proposedTree, err := parseConfigTree(proposed)
	if err != nil {
		return fmt.Errorf("parse proposed config: %w", err)
	}

	// The file is keyed by environment at the top level, and each environment
	// carries its own users, so both levels are walked.
	for env, currentEnv := range allEnvironments(currentTree, proposedTree) {
		proposedEnv := proposedTree[env]

		for _, section := range protectedTopLevelSections {
			if !equalYAML(currentEnv[section], proposedEnv[section]) {
				return protectedSectionError(section)
			}
		}

		currentUsers := usersByID(currentEnv)
		proposedUsers := usersByID(proposedEnv)
		if len(currentUsers) != len(proposedUsers) {
			return protectedSectionError("users")
		}
		for id, currentUser := range currentUsers {
			proposedUser, ok := proposedUsers[id]
			if !ok {
				return protectedSectionError("users")
			}
			for _, field := range protectedUserFields {
				if !equalYAML(currentUser[field], proposedUser[field]) {
					return protectedSectionError(field)
				}
			}
		}
	}
	return nil
}

// protectedSectionError explains the refusal and points at the way to get the
// change made, so the agent asks rather than retrying.
func protectedSectionError(section string) error {
	return fmt.Errorf("config_set cannot change %q — it decides what you are allowed to do. "+
		"Ask the user to change it with \"tclaw config push\" from their machine. "+
		"For repo access specifically, use repo_request_access instead", section)
}

// parseConfigTree unmarshals the environment-keyed config into generic maps.
func parseConfigTree(raw []byte) (map[string]map[string]any, error) {
	var tree map[string]map[string]any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// allEnvironments returns every environment named by either config, so adding
// or removing one is not a way to slip a permission change past the check.
func allEnvironments(current, proposed map[string]map[string]any) map[string]map[string]any {
	all := make(map[string]map[string]any, len(current)+len(proposed))
	for env, section := range current {
		all[env] = section
	}
	for env := range proposed {
		if _, ok := all[env]; !ok {
			all[env] = nil
		}
	}
	return all
}

// usersByID indexes an environment's users by their id, so a reordered list
// isn't read as a permission change.
func usersByID(env map[string]any) map[string]map[string]any {
	users, _ := env["users"].([]any)
	byID := make(map[string]map[string]any, len(users))
	for _, entry := range users {
		user, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := user["id"].(string)
		byID[id] = user
	}
	return byID
}

// equalYAML compares two parsed config values by their canonical encoding,
// which ignores formatting and key order.
func equalYAML(a, b any) bool {
	encodedA, errA := yaml.Marshal(a)
	encodedB, errB := yaml.Marshal(b)
	if errA != nil || errB != nil {
		// Unencodable values cannot be shown equal, so treat them as differing
		// and let the caller refuse.
		return false
	}
	return string(encodedA) == string(encodedB)
}
