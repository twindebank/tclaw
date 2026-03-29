package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/mcp"
	"tclaw/tool/bankingtools"
	"tclaw/tool/channeltools"
	"tclaw/tool/credentialtools"
	"tclaw/tool/devtools"
	googletools "tclaw/tool/google"
	"tclaw/tool/modeltools"
	monzotools "tclaw/tool/monzo"
	"tclaw/tool/onboardingtools"
	"tclaw/tool/remotemcp"
	"tclaw/tool/repotools"
	"tclaw/tool/restauranttools"
	"tclaw/tool/scheduletools"
	"tclaw/tool/secretform"
	"tclaw/tool/telegramclient"
	tfltools "tclaw/tool/tfl"
	"tclaw/tool/toolpkg"
)

func TestAllPackages_ImplementInterface(t *testing.T) {
	// Verify every tool package implements toolpkg.Package at compile time.
	packages := allPackages()
	require.Len(t, packages, 15, "expected 15 tool packages")

	seen := make(map[string]bool)
	for _, pkg := range packages {
		name := pkg.Name()
		require.NotEmpty(t, name, "package name must not be empty")
		require.False(t, seen[name], "duplicate package name: %s", name)
		seen[name] = true

		require.NotEmpty(t, pkg.Description(), "package %s must have a description", name)
		require.NotEmpty(t, pkg.ToolPatterns(), "package %s must have tool patterns", name)
	}
}

func TestAllPackages_InfoReturnsValidData(t *testing.T) {
	secrets := &memorySecretStore{data: make(map[string]string)}
	ctx := context.Background()

	for _, pkg := range allPackages() {
		t.Run(pkg.Name(), func(t *testing.T) {
			info, err := pkg.Info(ctx, secrets)
			require.NoError(t, err)
			require.NotNil(t, info)
			require.Equal(t, pkg.Name(), info.Name)
			require.NotEmpty(t, info.Description)
			require.NotEmpty(t, info.Tools, "package %s must list its tools", pkg.Name())

			// Credential count should match RequiredSecrets.
			require.Len(t, info.Credentials, len(pkg.RequiredSecrets()),
				"package %s credential count mismatch", pkg.Name())
		})
	}
}

func TestRegistry_RegistersInfoTools(t *testing.T) {
	// Test with packages that don't require Extra deps (they register
	// without external infrastructure like stores, schedulers, etc.).
	simplePkgs := []toolpkg.Package{
		&modeltools.Package{},
		&tfltools.Package{},
		&restauranttools.Package{},
		&bankingtools.Package{},
		&monzotools.Package{},
		&googletools.Package{},
	}

	handler := mcp.NewHandler()
	secrets := &memorySecretStore{data: make(map[string]string)}
	reg := toolpkg.NewRegistry(simplePkgs...)

	err := reg.RegisterAll(handler, toolpkg.RegistrationContext{
		SecretStore: secrets,
		Extra:       make(map[string]any),
	})
	require.NoError(t, err)

	tools := handler.ListTools()
	names := make(map[string]bool, len(tools))
	for _, td := range tools {
		names[td.Name] = true
	}

	// Every package should have an auto-generated <name>_info tool.
	for _, pkg := range simplePkgs {
		infoName := pkg.Name() + "_info"
		require.True(t, names[infoName], "missing info tool %s", infoName)
	}

	// Verify actual tools were registered too.
	require.True(t, names["model_list"], "model_list should be registered")
	require.True(t, names["tfl_line_status"], "tfl_line_status should be registered")
}

// --- helpers ---

// allPackages returns one instance of every tool package.
func allPackages() []toolpkg.Package {
	return []toolpkg.Package{
		&modeltools.Package{},
		&scheduletools.Package{},
		&onboardingtools.Package{},
		&tfltools.Package{},
		&repotools.Package{},
		&devtools.Package{},
		&restauranttools.Package{},
		&bankingtools.Package{},
		&monzotools.Package{},
		&telegramclient.Package{},
		&secretform.Package{},
		&credentialtools.Package{},
		&remotemcp.Package{},
		&googletools.Package{},
		&channeltools.Package{},
	}
}
