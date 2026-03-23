package toolgroup

import (
	"testing"

	"tclaw/claudecli"

	"github.com/stretchr/testify/require"
)

func TestToolGroups(t *testing.T) {
	t.Run("all groups are valid", func(t *testing.T) {
		for _, info := range AllGroups() {
			require.True(t, ValidGroup(info.Group), "group %q should be valid", info.Group)
			require.NotEmpty(t, GroupTools(info.Group), "group %q should have tools", info.Group)
		}
	})

	t.Run("base group has bash and file tools", func(t *testing.T) {
		tools := GroupTools(GroupCoreTools)
		require.Contains(t, tools, claudecli.ToolBash)
		require.Contains(t, tools, claudecli.ToolRead)
		require.Contains(t, tools, claudecli.ToolWrite)
	})

	t.Run("channel_ops has create but channel_send does not", func(t *testing.T) {
		ops := GroupTools(GroupChannelManagement)
		send := GroupTools(GroupChannelMessaging)
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
		tools := ResolveGroups([]ToolGroup{GroupChannelManagement, GroupChannelMessaging})
		count := 0
		for _, t := range tools {
			if t == MCPToolChannelDone {
				count++
			}
		}
		require.Equal(t, 1, count, "channel_done should appear exactly once")
	})
}

func TestProviderToolPatterns(t *testing.T) {
	t.Run("google provider adds google tool pattern", func(t *testing.T) {
		ctx := ChannelContext{ProviderIDs: []string{"google"}}
		tools := ProviderToolPatterns(ctx)
		require.Contains(t, tools, MCPToolGoogleAll)
	})

	t.Run("monzo provider adds monzo tool pattern", func(t *testing.T) {
		ctx := ChannelContext{ProviderIDs: []string{"monzo"}}
		tools := ProviderToolPatterns(ctx)
		require.Contains(t, tools, MCPToolMonzoAll)
	})

	t.Run("multiple providers add all patterns", func(t *testing.T) {
		ctx := ChannelContext{ProviderIDs: []string{"google", "monzo"}}
		tools := ProviderToolPatterns(ctx)
		require.Contains(t, tools, MCPToolGoogleAll)
		require.Contains(t, tools, MCPToolMonzoAll)
	})

	t.Run("unknown provider adds nothing", func(t *testing.T) {
		ctx := ChannelContext{ProviderIDs: []string{"unknown"}}
		tools := ProviderToolPatterns(ctx)
		require.Empty(t, tools)
	})

	t.Run("empty providers adds no patterns", func(t *testing.T) {
		ctx := ChannelContext{}
		tools := ProviderToolPatterns(ctx)
		require.Empty(t, tools)
	})
}

func TestRemoteMCPToolPatterns(t *testing.T) {
	t.Run("remote MCP names generate tool patterns", func(t *testing.T) {
		ctx := ChannelContext{RemoteMCPNames: []string{"linear"}}
		tools := RemoteMCPToolPatterns(ctx)
		require.Contains(t, tools, claudecli.Tool("mcp__linear__*"))
	})

	t.Run("multiple remote MCPs", func(t *testing.T) {
		ctx := ChannelContext{RemoteMCPNames: []string{"linear", "notion"}}
		tools := RemoteMCPToolPatterns(ctx)
		require.Contains(t, tools, claudecli.Tool("mcp__linear__*"))
		require.Contains(t, tools, claudecli.Tool("mcp__notion__*"))
	})

	t.Run("empty remote MCPs adds no patterns", func(t *testing.T) {
		ctx := ChannelContext{}
		tools := RemoteMCPToolPatterns(ctx)
		require.Empty(t, tools)
	})
}
