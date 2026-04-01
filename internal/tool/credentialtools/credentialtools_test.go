package credentialtools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/claudecli"
	"tclaw/internal/credential"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/tool/credentialtools"
	"tclaw/internal/tool/toolpkg"
	"tclaw/internal/toolgroup"
)

func TestCredentialList_Empty(t *testing.T) {
	h, _ := setup(t)

	result := callTool(t, h, "credential_list", map[string]any{})

	var got []map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	require.Empty(t, got)
}

func TestCredentialList_ShowsSets(t *testing.T) {
	h, credMgr := setup(t)
	ctx := context.Background()

	_, err := credMgr.Add(ctx, "stub_api", "default", "")
	require.NoError(t, err)

	result := callTool(t, h, "credential_list", map[string]any{})

	var got []map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	require.Len(t, got, 1)
	require.Equal(t, "stub_api/default", got[0]["id"])
	require.Equal(t, "stub_api", got[0]["package"])
	require.Equal(t, false, got[0]["ready"])
}

func TestCredentialList_FilterByPackage(t *testing.T) {
	h, credMgr := setup(t)
	ctx := context.Background()

	_, err := credMgr.Add(ctx, "stub_api", "default", "")
	require.NoError(t, err)
	_, err = credMgr.Add(ctx, "stub_oauth", "work", "admin")
	require.NoError(t, err)

	result := callTool(t, h, "credential_list", map[string]any{
		"package": "stub_api",
	})

	var got []map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	require.Len(t, got, 1)
	require.Equal(t, "stub_api", got[0]["package"])
}

func TestCredentialAdd_APIKey(t *testing.T) {
	h, _ := setup(t)

	// credential_add for an API key package should return CREDENTIALS_NEEDED.
	err := callToolExpectError(t, h, "credential_add", map[string]any{
		"package": "stub_api",
		"label":   "default",
	})
	require.Contains(t, err.Error(), "CREDENTIALS_NEEDED")
	require.Contains(t, err.Error(), "api_key")
}

func TestCredentialAdd_UnknownPackage(t *testing.T) {
	h, _ := setup(t)

	err := callToolExpectError(t, h, "credential_add", map[string]any{
		"package": "nonexistent",
		"label":   "default",
	})
	require.Contains(t, err.Error(), "does not support credentials")
}

func TestCredentialAdd_OAuth_MissingSetupFields(t *testing.T) {
	h, _ := setup(t)

	// OAuth package without setup fields should return CREDENTIALS_NEEDED
	// for the client_id and client_secret.
	err := callToolExpectError(t, h, "credential_add", map[string]any{
		"package": "stub_oauth",
		"label":   "work",
	})
	require.Contains(t, err.Error(), "CREDENTIALS_NEEDED")
	require.Contains(t, err.Error(), "client_id")
}

func TestCredentialRemove(t *testing.T) {
	h, credMgr := setup(t)
	ctx := context.Background()

	_, err := credMgr.Add(ctx, "stub_api", "default", "")
	require.NoError(t, err)

	result := callTool(t, h, "credential_remove", map[string]any{
		"credential_set_id": "stub_api/default",
	})

	var got map[string]any
	require.NoError(t, json.Unmarshal(result, &got))
	require.Equal(t, "removed", got["status"])

	// Verify it's actually gone.
	set, err := credMgr.Get(ctx, "stub_api/default")
	require.NoError(t, err)
	require.Nil(t, set)
}

func TestCredentialRemove_NotFound(t *testing.T) {
	h, _ := setup(t)

	err := callToolExpectError(t, h, "credential_remove", map[string]any{
		"credential_set_id": "nonexistent/default",
	})
	require.Contains(t, err.Error(), "not found")
}

// --- helpers ---

func setup(t *testing.T) (*mcp.Handler, *credential.Manager) {
	t.Helper()

	s, err := store.NewFS(t.TempDir())
	require.NoError(t, err)
	sec := &memorySecretStore{data: make(map[string]string)}
	credMgr := credential.NewManager(s, sec)

	registry := toolpkg.NewRegistry(
		&stubAPIPackage{},
		&stubOAuthPackage{},
	)

	handler := mcp.NewHandler()
	credentialtools.RegisterTools(handler, credentialtools.Deps{
		CredentialManager: credMgr,
		Registry:          registry,
	})

	return handler, credMgr
}

func callTool(t *testing.T, h *mcp.Handler, name string, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	result, err := h.Call(context.Background(), name, argsJSON)
	require.NoError(t, err, "call %s", name)
	return result
}

func callToolExpectError(t *testing.T, h *mcp.Handler, name string, args any) error {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	require.NoError(t, err)
	_, err = h.Call(context.Background(), name, argsJSON)
	require.Error(t, err, "expected error from %s", name)
	return err
}

// stubAPIPackage is a minimal CredentialProvider for testing API key flows.
type stubAPIPackage struct{}

func (p *stubAPIPackage) Name() string                                         { return "stub_api" }
func (p *stubAPIPackage) Description() string                                  { return "Stub API key package for testing" }
func (p *stubAPIPackage) Group() toolgroup.ToolGroup                           { return "" }
func (p *stubAPIPackage) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool { return nil }
func (p *stubAPIPackage) RequiredSecrets() []toolpkg.SecretSpec                { return nil }
func (p *stubAPIPackage) Info(_ context.Context, _ secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{Name: "stub_api"}, nil
}
func (p *stubAPIPackage) Register(_ *mcp.Handler, _ toolpkg.RegistrationContext) error { return nil }

func (p *stubAPIPackage) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthAPIKey,
		Fields: []toolpkg.CredentialField{
			{Key: "api_key", Label: "API Key", Required: true},
		},
	}
}

func (p *stubAPIPackage) OnCredentialSetChange(_ *mcp.Handler, _ toolpkg.RegistrationContext, _ []toolpkg.ResolvedCredentialSet) error {
	return nil
}

// stubOAuthPackage is a minimal CredentialProvider for testing OAuth flows.
type stubOAuthPackage struct{}

func (p *stubOAuthPackage) Name() string                                         { return "stub_oauth" }
func (p *stubOAuthPackage) Description() string                                  { return "Stub OAuth package for testing" }
func (p *stubOAuthPackage) Group() toolgroup.ToolGroup                           { return "" }
func (p *stubOAuthPackage) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool { return nil }
func (p *stubOAuthPackage) RequiredSecrets() []toolpkg.SecretSpec                { return nil }
func (p *stubOAuthPackage) Info(_ context.Context, _ secret.Store) (*toolpkg.PackageInfo, error) {
	return &toolpkg.PackageInfo{Name: "stub_oauth"}, nil
}
func (p *stubOAuthPackage) Register(_ *mcp.Handler, _ toolpkg.RegistrationContext) error { return nil }

func (p *stubOAuthPackage) CredentialSpec() toolpkg.CredentialSpec {
	return toolpkg.CredentialSpec{
		AuthType: toolpkg.AuthOAuth2,
		Fields: []toolpkg.CredentialField{
			{Key: "client_id", Label: "Client ID", Required: true},
			{Key: "client_secret", Label: "Client Secret", Required: true},
		},
		OAuth: &toolpkg.OAuthSpec{
			AuthURL:  "https://example.com/auth",
			TokenURL: "https://example.com/token",
		},
		SupportsMultiple: true,
	}
}

func (p *stubOAuthPackage) OnCredentialSetChange(_ *mcp.Handler, _ toolpkg.RegistrationContext, _ []toolpkg.ResolvedCredentialSet) error {
	return nil
}

type memorySecretStore struct {
	data map[string]string
}

func (m *memorySecretStore) Get(_ context.Context, key string) (string, error) {
	return m.data[key], nil
}

func (m *memorySecretStore) Set(_ context.Context, key, value string) error {
	m.data[key] = value
	return nil
}

func (m *memorySecretStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}
