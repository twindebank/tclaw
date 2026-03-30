package toolpkg_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/claudecli"
	"tclaw/libraries/secret"
	"tclaw/mcp"
	"tclaw/tool/toolpkg"
	"tclaw/toolgroup"
)

func TestRegistry_RegisterAll(t *testing.T) {
	t.Run("registers tools and info tools", func(t *testing.T) {
		handler := mcp.NewHandler()
		reg := toolpkg.NewRegistry(&stubPackage{name: "alpha"}, &stubPackage{name: "beta"})

		err := reg.RegisterAll(handler, toolpkg.RegistrationContext{
			SecretStore: &memorySecretStore{data: make(map[string]string)},
		})
		require.NoError(t, err)

		tools := handler.ListTools()
		names := toolNames(tools)

		// Each stub registers one tool named <name>_do_thing.
		require.Contains(t, names, "alpha_do_thing")
		require.Contains(t, names, "beta_do_thing")

		// Each package gets an auto-generated info tool.
		require.Contains(t, names, "alpha_info")
		require.Contains(t, names, "beta_info")
	})

	t.Run("info tool returns structured response", func(t *testing.T) {
		handler := mcp.NewHandler()
		secrets := &memorySecretStore{data: map[string]string{"alpha_key": "configured"}}
		reg := toolpkg.NewRegistry(&stubPackage{
			name: "alpha",
			secrets: []toolpkg.SecretSpec{
				{StoreKey: "alpha_key", Label: "Alpha Key", Required: true},
			},
		})

		err := reg.RegisterAll(handler, toolpkg.RegistrationContext{
			SecretStore: secrets,
		})
		require.NoError(t, err)

		result, err := handler.Call(context.Background(), "alpha_info", json.RawMessage(`{}`))
		require.NoError(t, err)

		var info toolpkg.PackageInfo
		require.NoError(t, json.Unmarshal(result, &info))
		require.Equal(t, "alpha", info.Name)
		require.Len(t, info.Credentials, 1)
		require.True(t, info.Credentials[0].Configured)
		require.Equal(t, "Alpha Key", info.Credentials[0].Label)
	})
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	require.Panics(t, func() {
		toolpkg.NewRegistry(&stubPackage{name: "dup"}, &stubPackage{name: "dup"})
	})
}

func TestRegistry_SeedAllSecrets(t *testing.T) {
	t.Run("seeds env vars from package specs", func(t *testing.T) {
		secrets := &memorySecretStore{data: make(map[string]string)}
		reg := toolpkg.NewRegistry(&stubPackage{
			name: "alpha",
			secrets: []toolpkg.SecretSpec{
				{StoreKey: "alpha_key", EnvVarPrefix: "ALPHA_KEY"},
			},
		})

		os.Setenv("ALPHA_KEY_TESTUSER", "secret-value")
		t.Cleanup(func() { os.Unsetenv("ALPHA_KEY_TESTUSER") })

		err := reg.SeedAllSecrets(context.Background(), "testuser", secrets)
		require.NoError(t, err)
		require.Equal(t, "secret-value", secrets.data["alpha_key"])
		require.Empty(t, os.Getenv("ALPHA_KEY_TESTUSER"))
	})

	t.Run("skips specs without env var prefix", func(t *testing.T) {
		secrets := &memorySecretStore{data: make(map[string]string)}
		reg := toolpkg.NewRegistry(&stubPackage{
			name: "beta",
			secrets: []toolpkg.SecretSpec{
				{StoreKey: "beta_key"}, // no EnvVarPrefix
			},
		})

		err := reg.SeedAllSecrets(context.Background(), "testuser", secrets)
		require.NoError(t, err)
		require.Empty(t, secrets.data["beta_key"])
	})
}

func TestRegistry_AllInfo(t *testing.T) {
	secrets := &memorySecretStore{data: map[string]string{"alpha_key": "set"}}
	reg := toolpkg.NewRegistry(
		&stubPackage{name: "alpha", secrets: []toolpkg.SecretSpec{
			{StoreKey: "alpha_key", Label: "Alpha Key", Required: true},
		}},
		&stubPackage{name: "beta"},
	)

	infos := reg.AllInfo(context.Background(), secrets)
	require.Len(t, infos, 2)
	require.Equal(t, "alpha", infos[0].Name)
	require.Equal(t, "beta", infos[1].Name)
}

func TestRegistry_BuildGroupTools(t *testing.T) {
	reg := toolpkg.NewRegistry(
		&stubPackage{name: "alpha", group: toolgroup.GroupPersonalServices},
		&stubPackage{name: "beta", group: toolgroup.GroupPersonalServices},
	)

	groupMap := reg.BuildGroupTools()
	tools := groupMap[toolgroup.GroupPersonalServices]
	require.Len(t, tools, 2)
}

func TestCheckCredentialStatus(t *testing.T) {
	secrets := &memorySecretStore{data: map[string]string{
		"present_key": "has-value",
	}}

	specs := []toolpkg.SecretSpec{
		{StoreKey: "present_key", Label: "Present", Required: true},
		{StoreKey: "missing_key", Label: "Missing", Required: false},
	}

	statuses := toolpkg.CheckCredentialStatus(context.Background(), secrets, specs)
	require.Len(t, statuses, 2)
	require.True(t, statuses[0].Configured)
	require.Equal(t, "Present", statuses[0].Label)
	require.False(t, statuses[1].Configured)
	require.Equal(t, "Missing", statuses[1].Label)
}

// --- helpers ---

type stubPackage struct {
	name    string
	group   toolgroup.ToolGroup
	secrets []toolpkg.SecretSpec
}

func (s *stubPackage) Name() string                          { return s.name }
func (s *stubPackage) Description() string                   { return "Stub " + s.name + " tools" }
func (s *stubPackage) Group() toolgroup.ToolGroup            { return s.group }
func (s *stubPackage) RequiredSecrets() []toolpkg.SecretSpec { return s.secrets }

func (s *stubPackage) GroupTools() map[toolgroup.ToolGroup][]claudecli.Tool {
	if s.group == "" {
		return nil
	}
	return map[toolgroup.ToolGroup][]claudecli.Tool{
		s.group: {claudecli.Tool("mcp__tclaw__" + s.name + "_*")},
	}
}

func (s *stubPackage) Info(ctx context.Context, store secret.Store) (*toolpkg.PackageInfo, error) {
	// Find group info.
	var gi toolgroup.GroupInfo
	for _, g := range toolgroup.AllGroups() {
		if g.Group == s.group {
			gi = g
			break
		}
	}
	return &toolpkg.PackageInfo{
		Name:        s.name,
		Description: s.Description(),
		Group:       s.group,
		GroupInfo:   gi,
		Credentials: toolpkg.CheckCredentialStatus(ctx, store, s.secrets),
		Tools:       []string{s.name + "_do_thing"},
	}, nil
}

func (s *stubPackage) Register(handler *mcp.Handler, ctx toolpkg.RegistrationContext) error {
	handler.Register(mcp.ToolDef{
		Name:        s.name + "_do_thing",
		Description: "Stub tool for " + s.name,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	})
	return nil
}

func toolNames(defs []mcp.ToolDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
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

// Ensure stubPackage implements Package at compile time.
var _ toolpkg.Package = (*stubPackage)(nil)

// Suppress unused import warnings.
var _ secret.Store = (*memorySecretStore)(nil)
