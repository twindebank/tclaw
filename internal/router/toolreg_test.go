package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/mcp"
	"tclaw/internal/tool/bankingtools"
	"tclaw/internal/tool/channeltools"
	"tclaw/internal/tool/credentialtools"
	"tclaw/internal/tool/devtools"
	googletools "tclaw/internal/tool/google"
	"tclaw/internal/tool/modeltools"
	monzotools "tclaw/internal/tool/monzo"
	"tclaw/internal/tool/notificationtools"
	"tclaw/internal/tool/onboardingtools"
	"tclaw/internal/tool/remotemcp"
	"tclaw/internal/tool/repotools"
	"tclaw/internal/tool/restauranttools"
	"tclaw/internal/tool/scheduletools"
	"tclaw/internal/tool/secretform"
	"tclaw/internal/tool/telegramclient"
	tfltools "tclaw/internal/tool/tfl"
	"tclaw/internal/tool/toolpkg"
)

func TestAllPackages_ImplementInterface(t *testing.T) {
	// Verify every tool package implements toolpkg.Package at compile time.
	packages := allPackages()
	require.Len(t, packages, 16, "expected 16 tool packages")

	seen := make(map[string]bool)
	for _, pkg := range packages {
		name := pkg.Name()
		require.NotEmpty(t, name, "package name must not be empty")
		require.False(t, seen[name], "duplicate package name: %s", name)
		seen[name] = true

		require.NotEmpty(t, pkg.Description(), "package %s must have a description", name)
		require.NotEmpty(t, pkg.GroupTools(), "package %s must have group tools", name)
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
	handler := mcp.NewHandler()
	secrets := &memorySecretStore{data: make(map[string]string)}

	// Test with packages that don't require external infrastructure like
	// stores, schedulers, etc. Packages that read SecretStore during
	// Register() need it set on the struct.
	simplePkgs := []toolpkg.Package{
		&modeltools.Package{},
		&tfltools.Package{SecretStore: secrets},
		&restauranttools.Package{SecretStore: secrets},
		&bankingtools.Package{SecretStore: secrets},
		&monzotools.Package{},
		&googletools.Package{},
	}
	reg := toolpkg.NewRegistry(simplePkgs...)

	err := reg.RegisterAll(handler, toolpkg.RegistrationContext{
		SecretStore: secrets,
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
		&notificationtools.Package{},
	}
}
