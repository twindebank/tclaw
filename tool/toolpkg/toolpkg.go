// Package toolpkg defines the standard interface for MCP tool packages.
//
// Every tool package (tfl, banking, devtools, etc.) implements the Package
// interface. The Registry collects all packages and handles registration,
// secret seeding, and info tool generation automatically.
//
// To add a new tool package:
//  1. Create a new directory under tool/ (e.g. tool/mytools/)
//  2. Implement the Package interface
//  3. Add the package to the registry in router.go
//
// That's it — the registry handles secret seeding, info tool registration,
// and toolgroup wiring.
package toolpkg

import (
	"context"
	"encoding/json"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/libraries/store"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/toolgroup"
	"tclaw/user"
)

// Package is the standard interface every tool package implements.
// It declares what the package provides (tools, group, secrets) and
// how to register its tools on an MCP handler.
type Package interface {
	// Name returns a stable identifier (e.g. "tfl", "banking", "devtools").
	Name() string

	// Description returns a human-readable summary of what this package does.
	Description() string

	// Group returns the toolgroup this package belongs to.
	Group() toolgroup.ToolGroup

	// ToolPatterns returns the Claude CLI tool patterns this package registers
	// (e.g. "mcp__tclaw__tfl_*"). Used to build the toolgroup -> tools map.
	ToolPatterns() []claudecli.Tool

	// RequiredSecrets declares what secrets this package needs. Used for
	// automatic seeding from environment variables and for the info tool's
	// credential status display.
	RequiredSecrets() []SecretSpec

	// Info returns structured metadata about this package: what it does,
	// what group it belongs to, what credentials it needs and their current
	// status. Called by the auto-registered <name>_info tool and available
	// to the registry for introspection.
	Info(ctx context.Context, secretStore secret.Store) (*PackageInfo, error)

	// Register registers this package's tools on the handler. Called once
	// during user startup.
	Register(handler *mcp.Handler, ctx RegistrationContext) error
}

// SecretSpec declares a single secret a package needs.
type SecretSpec struct {
	// StoreKey is the key in the secret store (e.g. "tfl_api_key").
	StoreKey string

	// EnvVarPrefix is the env var prefix for seeding from Fly secrets.
	// Combined with the user ID to form the full env var name:
	//   EnvVarPrefix + "_" + UPPER(userID)
	// Example: "TFL_API_KEY" -> "TFL_API_KEY_THEO"
	EnvVarPrefix string

	// Required indicates whether the package needs this secret to function.
	// If false, the package works in a degraded mode without it (e.g. TfL
	// works without an API key but is rate-limited).
	Required bool

	// Label is a human-readable name shown in the info tool output
	// (e.g. "TfL API Key").
	Label string

	// Description is shown in the info tool to help the user understand
	// what this secret is and how to get it.
	Description string
}

// CredentialStatus reports the current state of a single required secret.
type CredentialStatus struct {
	StoreKey    string `json:"store_key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Configured  bool   `json:"configured"`
	Required    bool   `json:"required"`
}

// PackageInfo is returned by Info() and by the standard info tool. It gives
// the agent a consistent structured view of any tool package.
type PackageInfo struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Group       toolgroup.ToolGroup `json:"group"`
	GroupInfo   toolgroup.GroupInfo `json:"group_info"`
	Credentials []CredentialStatus  `json:"credentials"`
	Tools       []string            `json:"tools"`

	// RedirectURL is the OAuth callback URL to configure in the provider's
	// developer portal before creating OAuth credentials. Only set for
	// packages that require OAuth.
	RedirectURL string `json:"redirect_url,omitempty"`
}

// RegistrationContext carries shared dependencies from the router to each
// package's Register method. Packages take what they need and ignore the rest.
type RegistrationContext struct {
	SecretStore secret.Store
	StateStore  store.Store
	Callback    *oauth.CallbackServer
	UserDir     string
	UserID      user.ID
	Env         string
	ConfigPath  string

	// Extra holds package-specific dependencies that don't fit the common
	// fields. Packages that need special deps (e.g. channeltools needs a
	// channel registry) put them here as typed values.
	Extra map[string]any
}

// CheckCredentialStatus checks the secret store for each SecretSpec and returns
// the status. Convenience function for Package.Info() implementations.
func CheckCredentialStatus(ctx context.Context, secretStore secret.Store, specs []SecretSpec) []CredentialStatus {
	statuses := make([]CredentialStatus, len(specs))
	for i, spec := range specs {
		configured := false
		val, err := secretStore.Get(ctx, spec.StoreKey)
		if err == nil && val != "" {
			configured = true
		}
		statuses[i] = CredentialStatus{
			StoreKey:    spec.StoreKey,
			Label:       spec.Label,
			Description: spec.Description,
			Configured:  configured,
			Required:    spec.Required,
		}
	}
	return statuses
}

// InfoToolDef returns the MCP tool definition for the standard <name>_info tool.
func InfoToolDef(pkg Package) mcp.ToolDef {
	schema := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	return mcp.ToolDef{
		Name:        pkg.Name() + "_info",
		Description: "Show information about the " + pkg.Name() + " tool package: description, required credentials and their status, available tools.",
		InputSchema: schema,
	}
}

// InfoToolHandler returns the MCP handler for the standard <name>_info tool.
// It calls pkg.Info() and returns the result as JSON. For CredentialProvider
// packages with OAuth, it includes the redirect URL so the agent can tell the
// user what to configure in their developer portal.
func InfoToolHandler(pkg Package, secretStore secret.Store, callbackURL string) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		info, err := pkg.Info(ctx, secretStore)
		if err != nil {
			return nil, err
		}

		// Add redirect URL for OAuth packages.
		if cp, ok := pkg.(CredentialProvider); ok && callbackURL != "" {
			if cp.CredentialSpec().NeedsOAuth() {
				info.RedirectURL = callbackURL
			}
		}

		return json.Marshal(info)
	}
}
