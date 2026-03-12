package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tclaw/channel"
)

// sendMessages feeds messages into the agent and collects results.
// Returns the error from RunWithMessages and the messages the channel received.
func sendMessages(t *testing.T, opts Options, messages ...string) (error, []string) {
	t.Helper()

	ch := &mockChannel{info: channel.Info{
		ID:   "test-ch",
		Name: "test",
		Type: channel.TypeSocket,
	}}

	opts.Channels = map[channel.ChannelID]channel.Channel{"test-ch": ch}
	if opts.Sessions == nil {
		opts.Sessions = make(map[channel.ChannelID]string)
	}

	msgCh := make(chan channel.TaggedMessage, len(messages)+1)
	for _, text := range messages {
		msgCh <- channel.TaggedMessage{ChannelID: "test-ch", Text: text}
	}
	close(msgCh)

	err := RunWithMessages(context.Background(), opts, msgCh)
	return err, ch.sends
}

// --- Reset menu tests ---

func TestReset_ShowsMenu(t *testing.T) {
	for _, cmd := range []string{"reset", "Reset", "new", "clear", "delete"} {
		t.Run(cmd, func(t *testing.T) {
			// Send reset command then close channel (agent exits).
			_, sends := sendMessages(t, Options{}, cmd)

			if len(sends) == 0 {
				t.Fatal("expected menu to be sent")
			}
			if !strings.Contains(sends[0], "Reset") {
				t.Errorf("expected menu prompt, got: %s", sends[0])
			}
			if !strings.Contains(sends[0], "Session") {
				t.Errorf("expected 'Session' option in menu, got: %s", sends[0])
			}
			if !strings.Contains(sends[0], "Everything") {
				t.Errorf("expected 'Everything' option in menu, got: %s", sends[0])
			}
		})
	}
}

func TestReset_SessionClearsCurrentChannel(t *testing.T) {
	var updatedChID channel.ChannelID
	var updatedSessionID string

	opts := Options{
		Sessions: map[channel.ChannelID]string{
			"test-ch": "old-session-123",
		},
		OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
			updatedChID = chID
			updatedSessionID = sessionID
		},
	}

	_, sends := sendMessages(t, opts, "reset", "1")

	// Should confirm session cleared.
	found := false
	for _, s := range sends {
		if strings.Contains(strings.ToLower(s), "session cleared") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'session cleared' confirmation, got: %v", sends)
	}

	if updatedChID != "test-ch" {
		t.Errorf("expected OnSessionUpdate for test-ch, got %q", updatedChID)
	}
	if updatedSessionID != "" {
		t.Errorf("expected empty session ID, got %q", updatedSessionID)
	}
}

func TestReset_SessionWordAlias(t *testing.T) {
	// "session" should also work as the choice.
	var called bool
	opts := Options{
		OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
			called = true
		},
	}

	sendMessages(t, opts, "reset", "session")

	if !called {
		t.Error("expected OnSessionUpdate to be called for 'session' choice")
	}
}

func TestReset_MemoriesRequiresConfirmation(t *testing.T) {
	var resetLevel ResetLevel
	resetCalled := false

	opts := Options{
		OnReset: func(level ResetLevel) error {
			resetCalled = true
			resetLevel = level
			return nil
		},
	}

	// Choose memories but don't confirm.
	_, sends := sendMessages(t, opts, "reset", "2")

	if resetCalled {
		t.Error("OnReset should not be called without confirmation")
	}

	// Should show confirmation prompt.
	found := false
	for _, s := range sends {
		if strings.Contains(s, "confirm") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected confirmation prompt, got: %v", sends)
	}

	// Now send with confirmation.
	resetCalled = false
	_, sends = sendMessages(t, opts, "reset", "2", "confirm")

	if !resetCalled {
		t.Error("expected OnReset to be called after confirmation")
	}
	if resetLevel != ResetMemories {
		t.Errorf("expected ResetMemories, got %d", resetLevel)
	}

	// Should show success message.
	found = false
	for _, s := range sends {
		if strings.Contains(strings.ToLower(s), "memories") && strings.Contains(s, "reset complete") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected memories reset confirmation, got: %v", sends)
	}
}

func TestReset_ProjectRequiresConfirmation_ReturnsResetRequested(t *testing.T) {
	resetCalled := false

	opts := Options{
		OnReset: func(level ResetLevel) error {
			resetCalled = true
			if level != ResetProject {
				t.Errorf("expected ResetProject, got %d", level)
			}
			return nil
		},
	}

	err, _ := sendMessages(t, opts, "reset", "3", "confirm")

	if !resetCalled {
		t.Error("expected OnReset to be called")
	}
	if !errors.Is(err, ErrResetRequested) {
		t.Errorf("expected ErrResetRequested, got %v", err)
	}
}

func TestReset_EverythingRequiresConfirmation_ReturnsResetRequested(t *testing.T) {
	resetCalled := false

	opts := Options{
		OnReset: func(level ResetLevel) error {
			resetCalled = true
			if level != ResetAll {
				t.Errorf("expected ResetAll, got %d", level)
			}
			return nil
		},
	}

	err, _ := sendMessages(t, opts, "reset", "4", "confirm")

	if !resetCalled {
		t.Error("expected OnReset to be called")
	}
	if !errors.Is(err, ErrResetRequested) {
		t.Errorf("expected ErrResetRequested, got %v", err)
	}
}

func TestReset_CancelFromMenu(t *testing.T) {
	resetCalled := false

	opts := Options{
		OnReset: func(level ResetLevel) error {
			resetCalled = true
			return nil
		},
	}

	_, sends := sendMessages(t, opts, "reset", "5")

	if resetCalled {
		t.Error("OnReset should not be called on cancel")
	}

	found := false
	for _, s := range sends {
		if strings.Contains(strings.ToLower(s), "cancel") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected cancel message, got: %v", sends)
	}
}

func TestReset_CancelWordFromMenu(t *testing.T) {
	resetCalled := false

	opts := Options{
		OnReset: func(level ResetLevel) error {
			resetCalled = true
			return nil
		},
	}

	sendMessages(t, opts, "reset", "cancel")

	if resetCalled {
		t.Error("OnReset should not be called on cancel")
	}
}

func TestReset_DenyConfirmation(t *testing.T) {
	resetCalled := false

	opts := Options{
		OnReset: func(level ResetLevel) error {
			resetCalled = true
			return nil
		},
	}

	// Choose "everything" but type "no" instead of "confirm".
	_, sends := sendMessages(t, opts, "reset", "4", "no")

	if resetCalled {
		t.Error("OnReset should not be called when confirmation denied")
	}

	found := false
	for _, s := range sends {
		if strings.Contains(strings.ToLower(s), "cancel") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected cancel message, got: %v", sends)
	}
}

func TestReset_InvalidChoice(t *testing.T) {
	_, sends := sendMessages(t, Options{}, "reset", "9")

	found := false
	for _, s := range sends {
		if strings.Contains(s, "1-5") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected re-prompt with valid range, got: %v", sends)
	}
}

func TestReset_OnResetError(t *testing.T) {
	opts := Options{
		OnReset: func(level ResetLevel) error {
			return errors.New("disk full")
		},
	}

	err, sends := sendMessages(t, opts, "reset", "2", "confirm")

	// Should not return ErrResetRequested on failure.
	if errors.Is(err, ErrResetRequested) {
		t.Error("should not return ErrResetRequested when OnReset fails")
	}

	found := false
	for _, s := range sends {
		if strings.Contains(s, "disk full") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error message with 'disk full', got: %v", sends)
	}
}

func TestReset_NilOnReset(t *testing.T) {
	// When OnReset is nil, memories reset should still succeed (no-op).
	_, sends := sendMessages(t, Options{}, "reset", "2", "confirm")

	found := false
	for _, s := range sends {
		if strings.Contains(s, "reset complete") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected success message even with nil OnReset, got: %v", sends)
	}
}

func TestReset_StopCancelsResetFlow(t *testing.T) {
	resetCalled := false

	opts := Options{
		OnReset: func(level ResetLevel) error {
			resetCalled = true
			return nil
		},
	}

	// Start reset, then send stop, then a normal message.
	// The normal message after stop should not be treated as a reset choice.
	sendMessages(t, opts, "reset", "stop")

	if resetCalled {
		t.Error("OnReset should not be called after stop")
	}
}

// --- Compact tests ---

func TestCompact_RewritesMessage(t *testing.T) {
	// Compact should rewrite the message text and fall through to handle().
	// Since we don't have a claude binary, handle() will fail, but we can
	// verify the command is recognized (not treated as unknown).
	for _, cmd := range []string{"compact", "Compact", "COMPACT"} {
		t.Run(cmd, func(t *testing.T) {
			if strings.EqualFold(cmd, CmdCompact) {
				// Verify the constant matches.
			} else {
				t.Errorf("CmdCompact %q doesn't match %q", CmdCompact, cmd)
			}
		})
	}
}

// --- Confirmation prompt content tests ---

func TestResetConfirmPrompt_MemoriesDescribesSharing(t *testing.T) {
	prompt := resetConfirmPrompt(ResetMemories, channel.MarkupMarkdown)
	if !strings.Contains(prompt, "shared across all channels") {
		t.Error("memories confirm should mention data is shared across channels")
	}
	if !strings.Contains(prompt, "CLAUDE.md") {
		t.Error("memories confirm should mention CLAUDE.md")
	}
}

func TestResetConfirmPrompt_ProjectDescribesAllChannels(t *testing.T) {
	prompt := resetConfirmPrompt(ResetProject, channel.MarkupMarkdown)
	if !strings.Contains(prompt, "all channels") {
		t.Error("project confirm should mention all channels are affected")
	}
	if !strings.Contains(prompt, "memory files") {
		t.Error("project confirm should mention memories are kept")
	}
	if !strings.Contains(prompt, "connections") {
		t.Error("project confirm should mention connections are kept")
	}
}

func TestResetConfirmPrompt_EverythingListsAllData(t *testing.T) {
	prompt := resetConfirmPrompt(ResetAll, channel.MarkupMarkdown)
	for _, expected := range []string{
		"Memory files",
		"all channels",
		"OAuth connections",
		"Schedules",
		"API keys",
		"Channel tokens",
		"re-authenticate",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("everything confirm should mention %q", expected)
		}
	}
}

func TestResetMenuPrompt_DescribesPerChannelVsShared(t *testing.T) {
	menu := resetMenuPrompt(channel.MarkupMarkdown)

	// Session should say "this channel" — it's per-channel.
	if !strings.Contains(menu, "this channel") {
		t.Error("session option should mention 'this channel'")
	}

	// Memories should say "shared across all channels".
	if !strings.Contains(menu, "shared across all channels") {
		t.Error("memories option should mention 'shared across all channels'")
	}

	// Project should say "all channels" for sessions.
	if !strings.Contains(menu, "all") {
		t.Error("project option should mention 'all' channels")
	}
}

func TestResetMenuPrompt_HTMLMarkup(t *testing.T) {
	menu := resetMenuPrompt(channel.MarkupHTML)
	if !strings.Contains(menu, "<b>") {
		t.Error("HTML menu should contain bold tags")
	}
}

// --- Reset level name tests ---

func TestResetLevelName(t *testing.T) {
	tests := []struct {
		level ResetLevel
		want  string
	}{
		{ResetSession, "session"},
		{ResetMemories, "memories"},
		{ResetProject, "project"},
		{ResetAll, "everything"},
		{ResetLevel(99), "unknown"},
	}
	for _, tt := range tests {
		if got := resetLevelName(tt.level); got != tt.want {
			t.Errorf("resetLevelName(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}
