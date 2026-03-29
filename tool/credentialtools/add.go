package credentialtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/credential"
	"tclaw/libraries/credentialerror"
	"tclaw/mcp"
	"tclaw/oauth"
	"tclaw/tool/toolpkg"
)

func credentialAddDef(registry *toolpkg.Registry) mcp.ToolDef {
	// Build the enum of packages that support credentials.
	var packageNames []string
	for _, cp := range registry.CredentialProviders() {
		packageNames = append(packageNames, cp.Name())
	}

	enumJSON := "[]"
	if len(packageNames) > 0 {
		quoted := make([]string, len(packageNames))
		for i, n := range packageNames {
			quoted[i] = `"` + n + `"`
		}
		enumJSON = "[" + strings.Join(quoted, ", ") + "]"
	}

	return mcp.ToolDef{
		Name: ToolCredentialAdd,
		Description: `Add a new credential set for a tool package.

For API key packages: returns CREDENTIALS_NEEDED so you can collect secrets via secret_form_request. The user enters values on a secure web form — you never see the actual secret values.

For OAuth packages: if the setup fields (e.g. client_id, client_secret) are already configured (from tclaw.yaml or a previous secret form), starts the OAuth flow and returns an authorization URL. If setup fields are missing, returns CREDENTIALS_NEEDED first.

After creating a set, call credential_list to verify readiness.`,
		InputSchema: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"properties": {
				"package": {
					"type": "string",
					"enum": %s,
					"description": "Tool package name (e.g. 'google', 'tfl', 'monzo')"
				},
				"label": {
					"type": "string",
					"description": "Label to identify this credential set (e.g. 'work', 'personal', 'default'). Must be unique per package."
				},
				"channel": {
					"type": "string",
					"description": "Optional channel name to scope this credential set to. When set, the package's tools are only available on that channel. Leave empty for all channels."
				}
			},
			"required": ["package", "label"]
		}`, enumJSON)),
	}
}

type credentialAddArgs struct {
	Package string `json:"package"`
	Label   string `json:"label"`
	Channel string `json:"channel"`
}

func credentialAddHandler(deps Deps) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a credentialAddArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Package == "" || len(a.Package) > 64 {
			return nil, fmt.Errorf("package is required and must be under 64 characters")
		}
		if a.Label == "" || len(a.Label) > 64 {
			return nil, fmt.Errorf("label is required and must be under 64 characters")
		}

		// Find the CredentialProvider for this package.
		var cp toolpkg.CredentialProvider
		for _, p := range deps.Registry.CredentialProviders() {
			if p.Name() == a.Package {
				cp = p
				break
			}
		}
		if cp == nil {
			return nil, fmt.Errorf("package %q does not support credentials — use credential_list to see available packages", a.Package)
		}

		spec := cp.CredentialSpec()

		// Create the credential set.
		set, err := deps.CredentialManager.Add(ctx, a.Package, a.Label, a.Channel)
		if err != nil {
			return nil, fmt.Errorf("add credential set: %w", err)
		}

		switch spec.AuthType {
		case toolpkg.AuthAPIKey:
			return handleAPIKeyAdd(spec, set)

		case toolpkg.AuthOAuth2:
			return handleOAuthCredentialAdd(ctx, deps, spec, set)

		default:
			return nil, fmt.Errorf("unsupported auth type %q for package %s", spec.AuthType, a.Package)
		}
	}
}

// handleAPIKeyAdd returns CREDENTIALS_NEEDED so the agent triggers a secret
// form to collect the required fields. The agent never sees actual values.
func handleAPIKeyAdd(spec toolpkg.CredentialSpec, set *credential.CredentialSet) (json.RawMessage, error) {
	fields := make([]credentialerror.Field, 0, len(spec.Fields))
	for _, f := range spec.Fields {
		fields = append(fields, credentialerror.Field{
			Key:         credentialFieldStoreKey(set.ID, f.Key),
			Label:       f.Label,
			Description: f.Description,
		})
	}

	return nil, credentialerror.New(
		set.Package+" Credentials",
		fmt.Sprintf("Credential set %s created. Please provide the required credentials.", set.ID),
		fields...,
	)
}

// handleOAuthCredentialAdd checks for setup fields (client_id/secret), then
// either starts the OAuth flow or returns CREDENTIALS_NEEDED for them.
func handleOAuthCredentialAdd(ctx context.Context, deps Deps, spec toolpkg.CredentialSpec, set *credential.CredentialSet) (json.RawMessage, error) {
	// Check if setup fields are already populated (from config seeding or previous form).
	allFieldsPresent := true
	var missingFields []credentialerror.Field
	for _, f := range spec.Fields {
		val, err := deps.CredentialManager.GetField(ctx, set.ID, f.Key)
		if err != nil {
			return nil, fmt.Errorf("check field %s: %w", f.Key, err)
		}
		if val == "" && f.Required {
			allFieldsPresent = false
			missingFields = append(missingFields, credentialerror.Field{
				Key:         credentialFieldStoreKey(set.ID, f.Key),
				Label:       f.Label,
				Description: f.Description,
			})
		}
	}

	if !allFieldsPresent {
		// Need setup fields first — return CREDENTIALS_NEEDED.
		return nil, credentialerror.New(
			set.Package+" OAuth Setup",
			fmt.Sprintf("Credential set %s created. OAuth client credentials are needed before the authorization flow can start. After providing them, call credential_add again with the same package and label.", set.ID),
			missingFields...,
		)
	}

	// Setup fields present — start the OAuth flow.
	if deps.Callback == nil {
		return nil, fmt.Errorf("OAuth is not configured — set the server public_url in tclaw.yaml")
	}

	// Build OAuth2Config from the spec + stored fields.
	clientID, err := deps.CredentialManager.GetField(ctx, set.ID, "client_id")
	if err != nil {
		return nil, fmt.Errorf("read client_id: %w", err)
	}
	clientSecret, err := deps.CredentialManager.GetField(ctx, set.ID, "client_secret")
	if err != nil {
		return nil, fmt.Errorf("read client_secret: %w", err)
	}

	oauthCfg := &oauth.OAuth2Config{
		AuthURL:      spec.OAuth.AuthURL,
		TokenURL:     spec.OAuth.TokenURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       spec.OAuth.Scopes,
		ExtraParams:  spec.OAuth.ExtraParams,
	}

	flow := &credentialPendingFlow{
		setID:    set.ID,
		oauthCfg: oauthCfg,
		credMgr:  deps.CredentialManager,
		onChange: deps.OnCredentialChange,
		pkgName:  set.Package,
		done:     make(chan struct{}),
	}

	state, err := deps.Callback.RegisterFlow(flow)
	if err != nil {
		return nil, fmt.Errorf("start oauth flow: %w", err)
	}

	authURL := oauth.BuildAuthURL(oauthCfg, state, deps.Callback.CallbackURL())

	result := map[string]any{
		"credential_set_id": set.ID,
		"status":            "pending_auth",
		"auth_url":          authURL,
		"message":           fmt.Sprintf("Send this authorization URL to the user. Then IMMEDIATELY call credential_auth_wait with credential_set_id=%q — do NOT end the turn without calling it.", set.ID),
	}
	return json.Marshal(result)
}

// credentialFieldStoreKey builds the secret store key for a credential field.
// This is the key the secret form will write to, matching what the credential
// manager reads with GetField.
func credentialFieldStoreKey(id credential.CredentialSetID, field string) string {
	return "cred/" + string(id) + "/" + field
}
