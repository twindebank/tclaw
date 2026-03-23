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
		require.True(t, Monitor.Valid())
	})

	t.Run("unknown roles are invalid", func(t *testing.T) {
		require.False(t, Role("admin").Valid())
		require.False(t, Role("").Valid())
		require.False(t, Role("SUPERUSER").Valid())
	})
}

func TestValidRoles(t *testing.T) {
	roles := ValidRoles()
	require.Len(t, roles, 4)
	require.Contains(t, roles, Superuser)
	require.Contains(t, roles, Developer)
	require.Contains(t, roles, Assistant)
	require.Contains(t, roles, Monitor)
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

	t.Run("monitor", func(t *testing.T) {
		allowed, disallowed := Resolve(Monitor, emptyCtx)
		require.NotNil(t, allowed)
		require.Nil(t, disallowed)
		// Monitor has channel_ops (including create) and scheduling.
		require.Contains(t, allowed, claudecli.Tool("mcp__tclaw__channel_create"))
		require.Contains(t, allowed, MCPToolScheduleAll)
		// Monitor does NOT have dev tools.
		require.NotContains(t, allowed, MCPToolDevAll)
	})

	t.Run("superuser includes all builtins", func(t *testing.T) {
		allowed, _ := Resolve(Superuser, emptyCtx)
		for _, b := range GroupTools(GroupBuiltins) {
			require.Contains(t, allowed, b)
		}
	})

	t.Run("developer includes all builtins", func(t *testing.T) {
		allowed, _ := Resolve(Developer, emptyCtx)
		for _, b := range GroupTools(GroupBuiltins) {
			require.Contains(t, allowed, b)
		}
	})

	t.Run("assistant includes basic builtins only", func(t *testing.T) {
		allowed, _ := Resolve(Assistant, emptyCtx)
		for _, b := range GroupTools(GroupBuiltinsBasic) {
			require.Contains(t, allowed, b)
		}
		// Full reset and login/auth should not be in assistant.
		require.NotContains(t, allowed, claudecli.BuiltinReset)
		require.NotContains(t, allowed, claudecli.BuiltinLogin)
		require.NotContains(t, allowed, claudecli.BuiltinAuth)
	})

	t.Run("assistant cannot create channels", func(t *testing.T) {
		allowed, _ := Resolve(Assistant, emptyCtx)
		require.NotContains(t, allowed, claudecli.Tool("mcp__tclaw__channel_create"))
	})

	t.Run("developer cannot create channels", func(t *testing.T) {
		allowed, _ := Resolve(Developer, emptyCtx)
		require.NotContains(t, allowed, claudecli.Tool("mcp__tclaw__channel_create"))
	})
}

func TestDefaultCreatableRoles(t *testing.T) {
	t.Run("superuser can create anything", func(t *testing.T) {
		roles := DefaultCreatableRoles(Superuser)
		require.Contains(t, roles, Superuser)
		require.Contains(t, roles, Developer)
		require.Contains(t, roles, Assistant)
		require.Contains(t, roles, Monitor)
	})

	t.Run("monitor can create developer and assistant", func(t *testing.T) {
		roles := DefaultCreatableRoles(Monitor)
		require.Contains(t, roles, Developer)
		require.Contains(t, roles, Assistant)
		require.NotContains(t, roles, Superuser)
	})

	t.Run("assistant cannot create anything", func(t *testing.T) {
		roles := DefaultCreatableRoles(Assistant)
		require.Nil(t, roles)
	})

	t.Run("developer cannot create anything", func(t *testing.T) {
		roles := DefaultCreatableRoles(Developer)
		require.Nil(t, roles)
	})
}

func TestToolGroups(t *testing.T) {
	t.Run("all groups are valid", func(t *testing.T) {
		for _, info := range AllGroups() {
			require.True(t, ValidGroup(info.Group), "group %q should be valid", info.Group)
			require.NotEmpty(t, GroupTools(info.Group), "group %q should have tools", info.Group)
		}
	})

	t.Run("base group has bash and file tools", func(t *testing.T) {
		tools := GroupTools(GroupBase)
		require.Contains(t, tools, claudecli.ToolBash)
		require.Contains(t, tools, claudecli.ToolRead)
		require.Contains(t, tools, claudecli.ToolWrite)
	})

	t.Run("channel_ops has create but channel_send does not", func(t *testing.T) {
		ops := GroupTools(GroupChannelOps)
		send := GroupTools(GroupChannelSend)
		require.Contains(t, ops, claudecli.Tool("mcp__tclaw__channel_create"))
		require.NotContains(t, send, claudecli.Tool("mcp__tclaw__channel_create"))
	})

	t.Run("gsuite_read and gsuite_write are separate", func(t *testing.T) {
		read := GroupTools(GroupGSuiteRead)
		write := GroupTools(GroupGSuiteWrite)
		require.Contains(t, read, claudecli.Tool("mcp__tclaw__google_gmail_list"))
		// gsuite_write uses the wildcard that covers everything.
		require.Contains(t, write, MCPToolGoogleAll)
	})

	t.Run("ResolveGroups deduplicates", func(t *testing.T) {
		// channel_ops and channel_send both include channel_done.
		tools := ResolveGroups([]ToolGroup{GroupChannelOps, GroupChannelSend})
		count := 0
		for _, t := range tools {
			if t == MCPToolChannelDone {
				count++
			}
		}
		require.Equal(t, 1, count, "channel_done should appear exactly once")
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
		for _, tool := range allowed {
			if tool == MCPToolAll {
				continue
			}
			require.NotRegexp(t, `^mcp__[^t].*__\*$`, string(tool),
				"unexpected remote MCP pattern: %s", tool)
		}
	})
}
