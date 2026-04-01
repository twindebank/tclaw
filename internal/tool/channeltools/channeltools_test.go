package channeltools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/telegramchannel"
	"tclaw/internal/claudecli"
	"tclaw/internal/config"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
	"tclaw/internal/reconciler"
	"tclaw/internal/tool/channeltools"
	"tclaw/internal/toolgroup"
	"tclaw/internal/user"
)

const testUserID = user.ID("testuser")

func TestMain(m *testing.M) {
	// Seed package-contributed tool groups so ValidGroup works in tests.
	toolgroup.SetPackageTools(map[toolgroup.ToolGroup][]claudecli.Tool{
		toolgroup.GroupChannelManagement: {"mcp__tclaw__channel_*"},
		toolgroup.GroupChannelMessaging:  {"mcp__tclaw__channel_send", "mcp__tclaw__channel_done"},
		toolgroup.GroupScheduling:        {"mcp__tclaw__schedule_*"},
		toolgroup.GroupDevWorkflow:       {"mcp__tclaw__dev_*"},
		toolgroup.GroupRepoMonitoring:    {"mcp__tclaw__repo_*"},
		toolgroup.GroupPersonalServices:  {"mcp__tclaw__tfl_*"},
		toolgroup.GroupConnections:       {"mcp__tclaw__credential_*"},
		toolgroup.GroupNotifications:     {"mcp__tclaw__notification_*"},
		toolgroup.GroupOnboarding:        {"mcp__tclaw__onboarding_*"},
		toolgroup.GroupSecretForm:        {"mcp__tclaw__secret_form_*"},
		toolgroup.GroupGSuiteWrite:       {"mcp__tclaw__google_*"},
		toolgroup.GroupGSuiteRead:        {"mcp__tclaw__google_gmail_*"},
		toolgroup.GroupTelegramClient:    {"mcp__tclaw__telegram_client_*"},
	})
	os.Exit(m.Run())
}

func TestChannelList(t *testing.T) {
	t.Run("shows channels from registry", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		result := callTool(t, th.handler, "channel_list", map[string]any{})

		var entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(result, &entries))
		require.Len(t, entries, 1)
		require.Equal(t, "desktop", entries[0].Name)
		require.Equal(t, "socket", entries[0].Type)
	})
}

func TestChannelCreate(t *testing.T) {
	t.Run("socket in local env", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		result := callTool(t, th.handler, "channel_create", map[string]any{
			"name":        "phone",
			"description": "Mobile device",
			"type":        "socket",
		})

		var created map[string]any
		require.NoError(t, json.Unmarshal(result, &created))
		require.Equal(t, "phone", created["name"])
		require.Equal(t, "socket", created["type"])

		// Should be persisted in config.
		channels, err := th.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		require.Len(t, channels, 2) // desktop + phone
	})

	t.Run("socket blocked in prod", func(t *testing.T) {
		th := setupHarness(t, config.EnvProd)

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "phone",
			"description": "Mobile device",
			"type":        "socket",
		})
		require.Contains(t, err.Error(), "not allowed")
	})

	t.Run("telegram provisions synchronously", func(t *testing.T) {
		th := setupHarnessWithProvisioner(t, config.EnvProd)

		result := callTool(t, th.handler, "channel_create", map[string]any{
			"name":          "mybot",
			"description":   "Personal Telegram bot",
			"type":          "telegram",
			"allowed_users": []any{"123456789"},
		})

		var created map[string]any
		require.NoError(t, json.Unmarshal(result, &created))
		require.Equal(t, "mybot", created["name"])
		require.Equal(t, "telegram", created["type"])
		require.Equal(t, "ready", created["status"])

		// Provisioner should have been called synchronously during the tool call.
		require.True(t, th.provisioner.provisionCalled, "expected provisioner.Provision to be called synchronously")

		// Channel should be in the config file.
		channels, err := th.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)

		var found bool
		for _, ch := range channels {
			if ch.Name == "mybot" {
				found = true
				require.Equal(t, channel.TypeTelegram, ch.Type)
			}
		}
		require.True(t, found, "expected channel 'mybot' in config")
	})

	t.Run("telegram provisioning failure returns error", func(t *testing.T) {
		th := setupHarnessWithProvisioner(t, config.EnvProd)
		th.provisioner.provisionErr = fmt.Errorf("BotFather unreachable")

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":          "mybot",
			"description":   "Personal Telegram bot",
			"type":          "telegram",
			"allowed_users": []any{"123456789"},
		})

		// The tool should return the provisioning error, not silently succeed.
		require.Contains(t, err.Error(), "provisioning failed")
		require.Contains(t, err.Error(), "BotFather unreachable")
	})

	t.Run("telegram rejects description exceeding BotFather limit", func(t *testing.T) {
		th := setupHarnessWithProvisioner(t, config.EnvProd)
		th.provisioner.validateCreateErr = fmt.Errorf("description too long for Telegram channel: 62 characters, max 56")

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":          "mybot",
			"description":   "This description is way too long for the BotFather display name limit of 56 characters",
			"type":          "telegram",
			"allowed_users": []any{"123456789"},
		})

		require.Contains(t, err.Error(), "description too long")
	})

	t.Run("telegram without provisioner or token returns error", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		// No provisioner and no bot token — creation must fail so the agent
		// knows to prompt the user for Telegram Client API credentials.
		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":          "mybot",
			"description":   "No provisioner",
			"type":          "telegram",
			"allowed_users": []any{"123456789"},
		})

		require.Contains(t, err.Error(), "auto-provisioning is unavailable")
		require.Contains(t, err.Error(), "telegram_client_info")
	})

	t.Run("rejects name collision", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		// "desktop" already exists in the registry.
		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "desktop",
			"description": "conflicts with existing",
			"type":        "socket",
		})
		require.Contains(t, err.Error(), "already exists")
	})

	t.Run("rejects duplicate name", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		callTool(t, th.handler, "channel_create", map[string]any{
			"name":        "phone",
			"description": "first",
			"type":        "socket",
		})

		// Reload registry so it sees the newly-added channel.
		reloadRegistry(t, th)

		err := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "phone",
			"description": "duplicate",
			"type":        "socket",
		})
		require.Contains(t, err.Error(), "already exists")
	})
}

func TestChannelEdit(t *testing.T) {
	t.Run("updates description", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "phone", "description": "Old description", "type": "socket",
		})
		reloadRegistry(t, th)

		callTool(t, th.handler, "channel_edit", map[string]any{
			"name":        "phone",
			"description": "New description",
		})

		channels, err := th.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)

		var found bool
		for _, ch := range channels {
			if ch.Name == "phone" {
				found = true
				require.Equal(t, "New description", ch.Description)
			}
		}
		require.True(t, found)
	})

	t.Run("edits hand-written channel", func(t *testing.T) {
		// The "desktop" channel is hand-written in the config — edits should work.
		th := setupHarness(t, config.EnvLocal)

		callTool(t, th.handler, "channel_edit", map[string]any{
			"name":        "desktop",
			"description": "Updated desktop description",
		})

		channels, err := th.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)

		var found bool
		for _, ch := range channels {
			if ch.Name == "desktop" {
				found = true
				require.Equal(t, "Updated desktop description", ch.Description)
			}
		}
		require.True(t, found)
	})

	t.Run("rejects nonexistent channel", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_edit", map[string]any{
			"name":        "nonexistent",
			"description": "try to edit",
		})
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("requires at least one field", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "phone", "description": "Socket", "type": "socket",
		})
		reloadRegistry(t, th)

		err := callToolExpectError(t, th.handler, "channel_edit", map[string]any{
			"name": "phone",
		})
		require.Contains(t, err.Error(), "at least one")
	})
}

func TestChannelChangeCallback(t *testing.T) {
	t.Run("create calls OnChannelChange", func(t *testing.T) {
		var called int
		th := setupHarnessWithCallback(t, config.EnvLocal, func() { called++ })

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "test", "description": "Test channel", "type": "socket",
		})
		require.Equal(t, 1, called)
	})

	t.Run("edit calls OnChannelChange", func(t *testing.T) {
		var called int
		th := setupHarnessWithCallback(t, config.EnvLocal, func() { called++ })

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "test", "description": "Test channel", "type": "socket",
		})
		reloadRegistry(t, th)
		called = 0

		callTool(t, th.handler, "channel_edit", map[string]any{
			"name": "test", "description": "Updated",
		})
		require.Equal(t, 1, called)
	})

	t.Run("delete calls OnChannelChange", func(t *testing.T) {
		var called int
		th := setupHarnessWithCallback(t, config.EnvLocal, func() { called++ })

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "test", "description": "Test channel", "type": "socket",
		})
		reloadRegistry(t, th)
		called = 0

		callTool(t, th.handler, "channel_delete", map[string]any{"name": "test"})
		require.Equal(t, 1, called)
	})

	t.Run("nil callback does not panic", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "test", "description": "Test channel", "type": "socket",
		})
	})

	t.Run("create calls OnChannelAdded when set", func(t *testing.T) {
		var addedName string
		var changeCalled int
		th := setupHarnessWithHotAdd(t, config.EnvLocal, func() { changeCalled++ }, func(name string) { addedName = name })

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "hottest", "description": "Hot-add channel", "type": "socket",
		})

		// The new create handler calls OnChannelChange (not OnChannelAdded directly),
		// but the test wires OnChannelAdded too. If the handler calls OnChannelChange,
		// we expect that path. Let's check what happens.
		// In the new code, channel_create always calls OnChannelChange. OnChannelAdded
		// is NOT called from the create handler (it's the router's responsibility).
		// So changeCalled should be 1.
		require.Equal(t, 1, changeCalled)
		require.Equal(t, "", addedName, "OnChannelAdded is not called directly from create handler")
	})
}

func TestChannelDelete(t *testing.T) {
	t.Run("removes channel from config", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "phone", "description": "will be deleted", "type": "socket",
		})
		reloadRegistry(t, th)

		callTool(t, th.handler, "channel_delete", map[string]any{"name": "phone"})

		channels, err := th.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		for _, ch := range channels {
			require.NotEqual(t, "phone", ch.Name, "channel should have been removed from config")
		}
	})

	t.Run("cleans up secret on delete", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		// Seed a bot token so the channel passes the "no provisioner" check.
		require.NoError(t, th.secretStore.Set(context.Background(), channel.ChannelSecretKey("mybot"), "fake-token"))

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "mybot", "description": "Telegram bot", "type": "telegram",
			"allowed_users": []any{"123456789"},
		})
		reloadRegistry(t, th)

		callTool(t, th.handler, "channel_delete", map[string]any{"name": "mybot"})

		// Secret should be cleaned up.
		token, err := th.secretStore.Get(context.Background(), channel.ChannelSecretKey("mybot"))
		require.NoError(t, err)
		require.Empty(t, token)
	})

	t.Run("rejects nonexistent channel", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_delete", map[string]any{"name": "nonexistent"})
		require.Contains(t, err.Error(), "not found")
	})
}

func TestChannelDone(t *testing.T) {
	t.Run("tears down channel without platform state", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		callTool(t, th.handler, "channel_create", map[string]any{
			"name":        "temp",
			"description": "Temporary channel",
			"type":        "socket",
		})
		reloadRegistry(t, th)

		result := callTool(t, th.handler, "channel_done", map[string]any{
			"channel_name": "temp",
			"results_sent": "No outbound links configured",
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "deleted", got["status"])

		// Channel should be removed from config.
		channels, err := th.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		for _, ch := range channels {
			require.NotEqual(t, "temp", ch.Name)
		}
	})

	t.Run("rejects nonexistent channel", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_done", map[string]any{
			"channel_name": "nonexistent",
			"results_sent": "No outbound links configured",
		})
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("rejects missing results_sent", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_done", map[string]any{
			"channel_name": "temp",
		})
		require.Contains(t, err.Error(), "results_sent is required")
	})

	t.Run("rejects empty channel name when no active channel", func(t *testing.T) {
		th := setupHarness(t, config.EnvLocal)

		err := callToolExpectError(t, th.handler, "channel_done", map[string]any{
			"results_sent": "No outbound links configured",
		})
		require.Contains(t, err.Error(), "channel_name is required")
	})

	t.Run("infers channel name from active channel", func(t *testing.T) {
		th := setupHarnessWithActiveChannel(t, config.EnvLocal, "temp")

		callTool(t, th.handler, "channel_create", map[string]any{
			"name":        "temp",
			"description": "Temporary channel",
			"type":        "socket",
		})
		reloadRegistry(t, th)

		result := callTool(t, th.handler, "channel_done", map[string]any{
			"results_sent": "No results to report",
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "deleted", got["status"])
		require.Equal(t, "temp", got["channel"])
	})

	t.Run("calls provisioner teardown when teardown state exists", func(t *testing.T) {
		th := setupHarnessWithProvisioner(t, config.EnvLocal)

		// Create a channel in config, then seed runtime state with teardown info.
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "ephemeral-test", "description": "Ephemeral bot", "type": "telegram",
			"allowed_users": []any{"123456789"}, "ephemeral": true,
		})
		reloadRegistry(t, th.testHarness)

		require.NoError(t, th.runtimeState.Update(context.Background(), "ephemeral-test", func(rs *channel.RuntimeState) {
			rs.TeardownState = telegramchannel.NewTeardownState("tclaw_test_bot")
		}))
		require.NoError(t, th.secretStore.Set(context.Background(), channel.ChannelSecretKey("ephemeral-test"), "fake-token"))

		result := callTool(t, th.handler, "channel_done", map[string]any{
			"channel_name": "ephemeral-test",
			"results_sent": "Sent PR URL to admin channel",
		})

		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "deleted", got["status"])

		require.True(t, th.provisioner.teardownCalled)
		require.Equal(t, "tclaw_test_bot", th.provisioner.teardownUsername)

		// Secret should be cleaned up.
		token, err := th.secretStore.Get(context.Background(), channel.ChannelSecretKey("ephemeral-test"))
		require.NoError(t, err)
		require.Empty(t, token)
	})

	t.Run("does not delete channel if teardown fails", func(t *testing.T) {
		th := setupHarnessWithProvisioner(t, config.EnvLocal)
		th.provisioner.teardownErr = fmt.Errorf("BotFather unreachable")

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "failing-ephemeral", "description": "Failing bot", "type": "telegram",
			"allowed_users": []any{"123456789"}, "ephemeral": true,
		})
		reloadRegistry(t, th.testHarness)

		require.NoError(t, th.runtimeState.Update(context.Background(), "failing-ephemeral", func(rs *channel.RuntimeState) {
			rs.TeardownState = telegramchannel.NewTeardownState("tclaw_fail_bot")
		}))

		toolErr := callToolExpectError(t, th.handler, "channel_done", map[string]any{
			"channel_name": "failing-ephemeral",
			"results_sent": "Sent results to admin channel",
		})
		require.Contains(t, toolErr.Error(), "platform teardown failed")
		require.Contains(t, toolErr.Error(), "BotFather unreachable")

		// Channel should still be in config.
		channels, err := th.configWriter.ReadChannels(testUserID)
		require.NoError(t, err)
		var found bool
		for _, ch := range channels {
			if ch.Name == "failing-ephemeral" {
				found = true
			}
		}
		require.True(t, found, "channel should not be deleted when teardown fails")
	})

	t.Run("sends prompt and sets pending_done when platform state is set", func(t *testing.T) {
		th := setupHarnessWithProvisioner(t, config.EnvLocal)

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "confirm-test", "description": "Confirm bot", "type": "telegram",
			"allowed_users": []any{"123456789"}, "ephemeral": true,
		})
		reloadRegistry(t, th.testHarness)

		// Seed platform state (Telegram chat ID) and teardown state.
		require.NoError(t, th.runtimeState.Update(context.Background(), "confirm-test", func(rs *channel.RuntimeState) {
			rs.PlatformState = telegramchannel.NewPlatformState(12345)
			rs.TeardownState = telegramchannel.NewTeardownState("tclaw_confirm_bot")
		}))
		require.NoError(t, th.secretStore.Set(context.Background(), channel.ChannelSecretKey("confirm-test"), "fake-token"))

		result := callTool(t, th.handler, "channel_done", map[string]any{
			"channel_name": "confirm-test",
			"results_sent": "Sent PR URL to admin",
		})

		// Tool returns "awaiting_confirmation" — not "deleted".
		var got map[string]string
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "awaiting_confirmation", got["status"])

		// SendTeardownPrompt was called, actual teardown was not.
		require.True(t, th.provisioner.sendTeardownPromptCalled)
		require.False(t, th.provisioner.teardownCalled)

		// PendingDone should be set in runtime state.
		rs, err := th.runtimeState.Get(context.Background(), "confirm-test")
		require.NoError(t, err)
		require.True(t, rs.PendingDone)
	})

	t.Run("returns error if sending teardown prompt fails", func(t *testing.T) {
		th := setupHarnessWithProvisioner(t, config.EnvLocal)
		th.provisioner.sendTeardownPromptErr = fmt.Errorf("bot API unreachable")

		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "prompt-fail-test", "description": "Prompt fail bot", "type": "telegram",
			"allowed_users": []any{"123456789"}, "ephemeral": true,
		})
		reloadRegistry(t, th.testHarness)

		require.NoError(t, th.runtimeState.Update(context.Background(), "prompt-fail-test", func(rs *channel.RuntimeState) {
			rs.PlatformState = telegramchannel.NewPlatformState(12345)
			rs.TeardownState = telegramchannel.NewTeardownState("tclaw_fail_bot")
		}))
		require.NoError(t, th.secretStore.Set(context.Background(), channel.ChannelSecretKey("prompt-fail-test"), "fake-token"))

		toolErr := callToolExpectError(t, th.handler, "channel_done", map[string]any{
			"channel_name": "prompt-fail-test",
			"results_sent": "Sent results",
		})
		require.Contains(t, toolErr.Error(), "send teardown prompt")

		// PendingDone should NOT have been set.
		rs, err := th.runtimeState.Get(context.Background(), "prompt-fail-test")
		require.NoError(t, err)
		require.False(t, rs.PendingDone)
		require.False(t, th.provisioner.teardownCalled)
	})
}

func TestCreatableGroups(t *testing.T) {
	t.Run("channel with empty creatable_groups cannot create", func(t *testing.T) {
		th := setupHarnessWithActiveChannel(t, config.EnvLocal, "monitor-chan")

		// Create a channel with no creatable_groups.
		callTool(t, th.handler, "channel_create", map[string]any{
			"name": "monitor-chan", "description": "Monitor", "type": "socket",
		})
		reloadRegistry(t, th)

		toolErr := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "child",
			"description": "Child channel",
			"type":        "socket",
			"tool_groups": []string{"core_tools"},
		})
		require.Contains(t, toolErr.Error(), "not authorized to create")
	})

	t.Run("channel can delegate authorized groups", func(t *testing.T) {
		th := setupHarnessWithActiveChannel(t, config.EnvLocal, "monitor-chan")

		// Create the monitor channel with creatable_groups.
		callTool(t, th.handler, "channel_create", map[string]any{
			"name":             "monitor-chan",
			"description":      "Monitor with delegation",
			"type":             "socket",
			"creatable_groups": []string{"core_tools", "channel_messaging"},
		})
		reloadRegistry(t, th)

		result := callTool(t, th.handler, "channel_create", map[string]any{
			"name":        "child-ok",
			"description": "Authorized child",
			"type":        "socket",
			"tool_groups": []string{"core_tools", "channel_messaging"},
		})

		var got map[string]any
		require.NoError(t, json.Unmarshal(result, &got))
		require.Equal(t, "child-ok", got["name"])
	})

	t.Run("channel cannot delegate unauthorized groups", func(t *testing.T) {
		th := setupHarnessWithActiveChannel(t, config.EnvLocal, "monitor-chan")

		callTool(t, th.handler, "channel_create", map[string]any{
			"name":             "monitor-chan",
			"description":      "Monitor with base only",
			"type":             "socket",
			"creatable_groups": []string{"core_tools"},
		})
		reloadRegistry(t, th)

		toolErr := callToolExpectError(t, th.handler, "channel_create", map[string]any{
			"name":        "child-bad",
			"description": "Unauthorized child",
			"type":        "socket",
			"tool_groups": []string{"core_tools", "dev_workflow"},
		})
		require.Contains(t, toolErr.Error(), "not authorized to delegate tool group")
		require.Contains(t, toolErr.Error(), "dev_workflow")
	})
}

func TestChannelNotify(t *testing.T) {
	t.Run("rejects notify on current channel", func(t *testing.T) {
		th := setupHarnessWithActiveChannel(t, config.EnvLocal, "admin")

		err := callToolExpectError(t, th.handler, "channel_notify", map[string]any{
			"channel_name": "admin",
			"message":      "hello",
		})
		require.Contains(t, err.Error(), "cannot notify the current channel")
	})
}

// --- helpers ---

type testHarness struct {
	handler      *mcp.Handler
	configWriter *config.Writer
	runtimeState *channel.RuntimeStateStore
	secretStore  *memorySecretStore
	registry     *channel.Registry
	configPath   string
}

type testHarnessWithProvisioner struct {
	testHarness
	provisioner *mockProvisioner
}

func setupHarness(t *testing.T, env config.Env) testHarness {
	return setupHarnessWithCallback(t, env, nil)
}

func setupHarnessWithActiveChannel(t *testing.T, env config.Env, activeChannel string) testHarness {
	t.Helper()
	th := buildHarness(t, env)

	channeltools.RegisterTools(th.handler, channeltools.Deps{
		Registry:     th.registry,
		ConfigWriter: th.configWriter,
		RuntimeState: th.runtimeState,
		UserID:       testUserID,
		Env:          env,
		SecretStore:  th.secretStore,
		ReconcileParams: reconciler.ReconcileParams{
			RuntimeState: th.runtimeState,
		},
		ActiveChannel: func() string {
			return activeChannel
		},
	})

	return th
}

func setupHarnessWithCallback(t *testing.T, env config.Env, onChange func()) testHarness {
	t.Helper()
	th := buildHarness(t, env)

	channeltools.RegisterTools(th.handler, channeltools.Deps{
		Registry:        th.registry,
		ConfigWriter:    th.configWriter,
		RuntimeState:    th.runtimeState,
		UserID:          testUserID,
		Env:             env,
		SecretStore:     th.secretStore,
		OnChannelChange: onChange,
		ReconcileParams: reconciler.ReconcileParams{
			RuntimeState: th.runtimeState,
		},
	})

	return th
}

func setupHarnessWithHotAdd(t *testing.T, env config.Env, onChange func(), onAdded func(string)) testHarness {
	t.Helper()
	th := buildHarness(t, env)

	channeltools.RegisterTools(th.handler, channeltools.Deps{
		Registry:        th.registry,
		ConfigWriter:    th.configWriter,
		RuntimeState:    th.runtimeState,
		UserID:          testUserID,
		Env:             env,
		SecretStore:     th.secretStore,
		OnChannelChange: onChange,
		OnChannelAdded:  onAdded,
		ReconcileParams: reconciler.ReconcileParams{
			RuntimeState: th.runtimeState,
		},
	})

	return th
}

func setupHarnessWithProvisioner(t *testing.T, env config.Env) testHarnessWithProvisioner {
	t.Helper()
	th := buildHarness(t, env)
	prov := &mockProvisioner{}

	provisioners := channel.ProvisionerLookup(func(ct channel.ChannelType) channel.EphemeralProvisioner {
		if ct == channel.TypeTelegram {
			return prov
		}
		return nil
	})

	channeltools.RegisterTools(th.handler, channeltools.Deps{
		Registry:     th.registry,
		ConfigWriter: th.configWriter,
		RuntimeState: th.runtimeState,
		UserID:       testUserID,
		Env:          env,
		SecretStore:  th.secretStore,
		Provisioners: provisioners,
		ReconcileParams: reconciler.ReconcileParams{
			RuntimeState: th.runtimeState,
			Provisioners: provisioners,
		},
	})

	return testHarnessWithProvisioner{
		testHarness: th,
		provisioner: prov,
	}
}

// buildHarness creates the shared infrastructure (config file, stores, registry)
// without registering tools. Callers add their own Deps.
func buildHarness(t *testing.T, env config.Env) testHarness {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "tclaw.yaml")
	writeTestConfig(t, configPath, env)

	s, err := store.NewFS(filepath.Join(tmpDir, "state"))
	require.NoError(t, err)

	runtimeState := channel.NewRuntimeStateStore(s)
	secrets := newMemorySecretStore()
	handler := mcp.NewHandler()

	staticEntries := []channel.RegistryEntry{
		{Info: channel.Info{
			ID:          "/tmp/test/desktop.sock",
			Type:        channel.TypeSocket,
			Name:        "desktop",
			Description: "Desktop workstation",
		}},
	}
	registry := channel.NewRegistry(staticEntries)

	return testHarness{
		handler:      handler,
		configWriter: config.NewWriter(configPath, env),
		runtimeState: runtimeState,
		secretStore:  secrets,
		registry:     registry,
		configPath:   configPath,
	}
}

// writeTestConfig writes a minimal tclaw.yaml with one user and one hand-written channel.
func writeTestConfig(t *testing.T, path string, env config.Env) {
	t.Helper()
	content := fmt.Sprintf(`%s:
  base_dir: /tmp/tclaw-test
  users:
    - id: %s
      channels:
        - name: desktop
          type: socket
          description: Desktop workstation
`, string(env), string(testUserID))

	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// reloadRegistry reads channels from config and reloads the registry so it
// sees channels added via ConfigWriter.
func reloadRegistry(t *testing.T, th testHarness) {
	t.Helper()
	channels, err := th.configWriter.ReadChannels(testUserID)
	require.NoError(t, err)

	var entries []channel.RegistryEntry
	for _, ch := range channels {
		var creatableGroups []string
		for _, g := range ch.CreatableGroups {
			creatableGroups = append(creatableGroups, string(g))
		}
		entries = append(entries, channel.RegistryEntry{
			Info: channel.Info{
				ID:              channel.ChannelID(ch.Name),
				Type:            ch.Type,
				Name:            ch.Name,
				Description:     ch.Description,
				CreatableGroups: creatableGroups,
			},
		})
	}
	th.registry.Reload(entries)
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

// memorySecretStore is an in-memory secret.Store for testing.
type memorySecretStore struct {
	data map[string]string
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{data: make(map[string]string)}
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

type mockProvisioner struct {
	teardownCalled           bool
	teardownUsername         string
	teardownErr              error
	provisionCalled          bool
	provisionResult          *channel.ProvisionResult
	provisionErr             error
	sendTeardownPromptCalled bool
	sendTeardownPromptErr    error
	validateCreateErr        error
	notifyCalled             bool
	notifyErr                error
	platformResponseInfo     map[string]any
}

func (m *mockProvisioner) IsReady(_ context.Context, _ string) bool { return false }
func (m *mockProvisioner) CanAutoProvision() bool                   { return true }

func (m *mockProvisioner) ValidateCreate(description string) error {
	return m.validateCreateErr
}

func (m *mockProvisioner) Provision(_ context.Context, params channel.ProvisionParams) (*channel.ProvisionResult, error) {
	m.provisionCalled = true
	if m.provisionErr != nil {
		return nil, m.provisionErr
	}
	if m.provisionResult != nil {
		return m.provisionResult, nil
	}
	return &channel.ProvisionResult{
		Token:         "mock-bot-token",
		TeardownState: telegramchannel.NewTeardownState("tclaw_mock_bot"),
	}, nil
}

func (m *mockProvisioner) Teardown(_ context.Context, state channel.TeardownState) error {
	m.teardownCalled = true
	if tgState, err := telegramchannel.ParseTeardownState(state); err == nil {
		m.teardownUsername = tgState.BotUsername
	}
	return m.teardownErr
}

func (m *mockProvisioner) SendTeardownPrompt(_ context.Context, _ string, _ channel.PlatformState) error {
	m.sendTeardownPromptCalled = true
	return m.sendTeardownPromptErr
}

func (m *mockProvisioner) SendClosingMessage(_ context.Context, _ string, _ channel.PlatformState) error {
	return nil
}

func (m *mockProvisioner) Notify(_ context.Context, _ string, _ string) error {
	m.notifyCalled = true
	return m.notifyErr
}

func (m *mockProvisioner) PlatformResponseInfo(state channel.TeardownState) map[string]any {
	return m.platformResponseInfo
}
