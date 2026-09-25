package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"
)

func TestBuildArgs_Permissions(t *testing.T) {
	t.Run("a turn the user started asks through the prompt tool, which the model may not call", func(t *testing.T) {
		args := buildArgs(buildArgsParams{MaxTurns: 10, Prompt: "hello", PermissionPromptTool: "mcp__tclaw__permission_prompt"})

		require.Equal(t, "mcp__tclaw__permission_prompt", flagValue(t, args, "--permission-prompt-tool"))
		require.Equal(t, "mcp__tclaw__permission_prompt", flagValue(t, args, "--disallowedTools"))
	})

	t.Run("an unattended turn never asks, even with a prompt tool", func(t *testing.T) {
		args := buildArgs(buildArgsParams{MaxTurns: 10, Prompt: "hello", Unattended: true, PermissionPromptTool: "mcp__tclaw__permission_prompt"})

		require.NotContains(t, args, "--permission-prompt-tool")
		require.Equal(t, "none", flagValue(t, args, "--permission-prompts"))
	})

	t.Run("an unattended turn refuses whatever would prompt", func(t *testing.T) {
		args := buildArgs(buildArgsParams{MaxTurns: 10, Prompt: "hello", Unattended: true})

		require.Equal(t, "none", flagValue(t, args, "--permission-prompts"))
	})

	t.Run("a turn the user started leaves prompting to the CLI", func(t *testing.T) {
		args := buildArgs(buildArgsParams{MaxTurns: 10, Prompt: "hello"})

		require.NotContains(t, args, "--permission-prompts")
	})
}

func TestPermissionPromptToolFor(t *testing.T) {
	prompter := &promptingChannel{}
	opts := Options{PermissionPromptTool: "mcp__tclaw__permission_prompt", PermissionMode: claudecli.PermissionAuto}
	tests := []struct {
		name       string
		opts       Options
		ch         channel.Channel
		unattended bool
		want       claudecli.Tool
	}{
		{name: "a user turn on a channel with buttons asks", opts: opts, ch: prompter, want: "mcp__tclaw__permission_prompt"},
		{name: "a channel without buttons cannot be answered mid-turn", opts: opts, ch: &mockChannel{}, want: ""},
		{name: "a turn nobody started never asks", opts: opts, ch: prompter, unattended: true, want: ""},
		{name: "dontAsk mode never consults it", opts: Options{PermissionPromptTool: opts.PermissionPromptTool, PermissionMode: claudecli.PermissionDontAsk}, ch: prompter, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, permissionPromptToolFor(tt.opts, tt.ch, tt.unattended))
		})
	}
}

// --- helpers ---

// promptingChannel is a channel that can show buttons.
type promptingChannel struct {
	mockChannel
}

func (p *promptingChannel) SendPrompt(context.Context, channel.SendPromptParams) (channel.MessageID, error) {
	return "1", nil
}
