package google

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"tclaw/internal/credential"
	"tclaw/internal/gws"
	"tclaw/internal/mcp"
)

type workspaceArgs struct {
	CredentialSet string `json:"credential_set"`
	Command       string `json:"command"`
	Params        string `json:"params"`
	JSON          string `json:"json"`
}

func workspaceHandler(depsMap map[credential.CredentialSetID]Deps, memoryDir string) mcp.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var a workspaceArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		deps, err := resolveDeps(depsMap, a.CredentialSet)
		if err != nil {
			return nil, err
		}

		if a.Command == "" {
			return nil, fmt.Errorf("command is required (e.g. 'gmail users messages list')")
		}

		cmd := gws.Raw(a.Command, a.Params, a.JSON)
		// The gws CLI's own path validation requires any file it touches (downloaded
		// binaries, -a attachment sources) to resolve under its working directory. Pointing
		// Dir at the whole memory sandbox — rather than a narrower subdir — lets both
		// directions work: downloads land somewhere the agent can read, and attachments
		// already living anywhere under the memory dir (e.g. media/) pass gws's check.
		cmd.Dir = memoryDir

		result, err := runGWS(ctx, deps, cmd)
		if err != nil {
			return nil, err
		}

		return resolveSavedFilePath(result, memoryDir), nil
	}
}

// resolveSavedFilePath rewrites a gws response's "saved_file" field (a
// filename relative to the subprocess's working directory) into the absolute
// path the caller can actually read. Responses without a "saved_file" string
// field, or with no memoryDir configured, are returned unchanged.
func resolveSavedFilePath(raw json.RawMessage, memoryDir string) json.RawMessage {
	if memoryDir == "" {
		return raw
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		// Not a JSON object (e.g. an array response) — nothing to rewrite.
		return raw
	}

	savedFile, ok := payload["saved_file"].(string)
	if !ok || savedFile == "" || filepath.IsAbs(savedFile) {
		return raw
	}

	payload["saved_file"] = filepath.Join(memoryDir, savedFile)
	rewritten, err := json.Marshal(payload)
	if err != nil {
		// Marshaling a map we just unmarshaled cannot fail; fall back defensively.
		return raw
	}
	return rewritten
}
