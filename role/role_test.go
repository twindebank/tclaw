package role

import (
	"testing"

	"tclaw/claudecli"

	"github.com/stretchr/testify/require"
)

func TestValid(t *testing.T) {
	t.Run("known roles are valid", func(t *testing.T) {
		require.True(t, Superuser.Valid())
		require.True(t, Developer.Valid())
		require.True(t, Assistant.Valid())
	})

	t.Run("unknown roles are invalid", func(t *testing.T) {
		require.False(t, Role("admin").Valid())
		require.False(t, Role("").Valid())
		require.False(t, Role("SUPERUSER").Valid())
	})
}

func TestValidRoles(t *testing.T) {
	roles := ValidRoles()
	require.Len(t, roles, 3)
	require.Contains(t, roles, Superuser)
	require.Contains(t, roles, Developer)
	require.Contains(t, roles, Assistant)
}

func TestResolve(t *testing.T) {
	emptyCtx := ChannelContext{}

	t.Run("unknown role returns nil", func(t *testing.T) {
		allowed, disallowed := Resolve(Role("bogus"), emptyCtx)
		require.Nil(t, allowed)
		require.Nil(t, disallowed)
	})

	t.Run("superuser", func(t *testing.T) {
		allowed, disallowed := Resolve(Superuser, emptyCtx)
		require.NotNil(t, allowed)
		require.Nil(t, disallowed)
		require.Contains(t, allowed, MCPToolAll)
	})

	t.Run("developer", func(t *testing.T) {
		allowed, disallowed := Resolve(Developer, emptyCtx)
		require.NotNil(t, allowed)
		require.Nil(t, disallowed)
		require.Contains(t, allowed, MCPToolDevAll)
		require.Contains(t, allowed, MCPToolDeploy)
		require.Contains(t, allowed, MCPToolScheduleAll)
	})

	t.Run("assistant", func(t *testing.T) {
		allowed, disallowed := Resolve(Assistant, emptyCtx)
		require.NotNil(t, allowed)
		require.Nil(t, disallowed)
		require.Contains(t, allowed, MCPToolConnectionAll)
		require.Contains(t, allowed, MCPToolRemoteMCPAll)
		require.Contains(t, allowed, MCPToolScheduleAll)
		require.Contains(t, allowed, MCPToolTflAll)
	})

	t.Run("superuser includes all builtins", func(t *testing.T) {
		allowed, _ := Resolve(Superuser, emptyCtx)
		for _, b := range allBuiltins {
			require.Contains(t, allowed, b)
		}
	})

	t.Run("developer includes all builtins", func(t *testing.T) {
		allowed, _ := Resolve(Developer, emptyCtx)
		for _, b := range allBuiltins {
			require.Contains(t, allowed, b)
		}
	})

	t.Run("assistant includes basic builtins only", func(t *testing.T) {
		allowed, _ := Resolve(Assistant, emptyCtx)
		for _, b := range basicBuiltins {
			require.Contains(t, allowed, b)
		}
		// Full reset and login/auth should not be in assistant.
		require.NotContains(t, allowed, claudecli.BuiltinReset)
		require.NotContains(t, allowed, claudecli.BuiltinLogin)
		require.NotContains(t, allowed, claudecli.BuiltinAuth)
	})
}

func TestResolveProviderToolPatterns(t *testing.T) {
	t.Run("google provider adds google tool pattern", func(t *testing.T) {
		ctx := ChannelContext{ProviderIDs: []string{"google"}}
		allowed, _ := Resolve(Superuser, ctx)
		require.Contains(t, allowed, MCPToolGoogleAll)
	})

	t.Run("monzo provider adds monzo tool pattern", func(t *testing.T) {
		ctx := ChannelContext{ProviderIDs: []string{"monzo"}}
		allowed, _ := Resolve(Superuser, ctx)
		require.Contains(t, allowed, MCPToolMonzoAll)
	})

	t.Run("multiple providers add all patterns", func(t *testing.T) {
		ctx := ChannelContext{ProviderIDs: []string{"google", "monzo"}}
		allowed, _ := Resolve(Assistant, ctx)
		require.Contains(t, allowed, MCPToolGoogleAll)
		require.Contains(t, allowed, MCPToolMonzoAll)
	})

	t.Run("unknown provider adds nothing", func(t *testing.T) {
		ctx := ChannelContext{ProviderIDs: []string{"unknown"}}
		allowed, _ := Resolve(Superuser, ctx)
		require.NotContains(t, allowed, claudecli.Tool("mcp__tclaw__unknown_*"))
	})

	t.Run("empty providers adds no provider patterns", func(t *testing.T) {
		ctx := ChannelContext{}
		allowed, _ := Resolve(Superuser, ctx)
		require.NotContains(t, allowed, MCPToolGoogleAll)
		require.NotContains(t, allowed, MCPToolMonzoAll)
	})
}

func TestResolveRemoteMCPToolPatterns(t *testing.T) {
	t.Run("remote MCP names generate tool patterns", func(t *testing.T) {
		ctx := ChannelContext{RemoteMCPNames: []string{"linear"}}
		allowed, _ := Resolve(Superuser, ctx)
		require.Contains(t, allowed, claudecli.Tool("mcp__linear__*"))
	})

	t.Run("multiple remote MCPs", func(t *testing.T) {
		ctx := ChannelContext{RemoteMCPNames: []string{"linear", "notion"}}
		allowed, _ := Resolve(Assistant, ctx)
		require.Contains(t, allowed, claudecli.Tool("mcp__linear__*"))
		require.Contains(t, allowed, claudecli.Tool("mcp__notion__*"))
	})

	t.Run("empty remote MCPs adds no patterns", func(t *testing.T) {
		ctx := ChannelContext{}
		allowed, _ := Resolve(Superuser, ctx)
		// Only MCPToolAll should be present, no specific remote MCP patterns.
		for _, tool := range allowed {
			if tool == MCPToolAll {
				continue
			}
			require.NotRegexp(t, `^mcp__[^t].*__\*$`, string(tool),
				"unexpected remote MCP pattern: %s", tool)
		}
	})
}
