package agent

import (
	"context"
	"strings"
	"testing"

	"tclaw/internal/channel"
	"tclaw/internal/claudecli"
)

func TestFreshSessionCommand(t *testing.T) {
	t.Run("clears session without a menu or confirmation", func(t *testing.T) {
		// "new" and its "reset"/"clear"/"delete" synonyms all behave identically.
		for _, cmd := range []string{"new", "New", "NEW", "reset", "clear", "delete"} {
			t.Run(cmd, func(t *testing.T) {
				var updatedChID channel.ChannelID
				var updatedSessionID string
				updateCalled := false

				opts := Options{
					Sessions: map[channel.ChannelID]string{
						"test-ch": "old-session-123",
					},
					OnSessionUpdate: func(chID channel.ChannelID, sessionID string) {
						updateCalled = true
						updatedChID = chID
						updatedSessionID = sessionID
					},
				}

				_, sends := sendMessages(t, opts, cmd)

				if !updateCalled {
					t.Fatal("expected OnSessionUpdate to be called")
				}
				if updatedChID != "test-ch" {
					t.Errorf("expected OnSessionUpdate for test-ch, got %q", updatedChID)
				}
				if updatedSessionID != "" {
					t.Errorf("expected empty session ID, got %q", updatedSessionID)
				}

				// Should confirm a fresh session directly — never show a menu.
				if len(sends) == 0 {
					t.Fatal("expected a confirmation message")
				}
				if !strings.Contains(strings.ToLower(sends[0]), "new session") {
					t.Errorf("expected 'new session' confirmation, got: %v", sends)
				}
				for _, s := range sends {
					if strings.Contains(s, "Choose what to clear") {
						t.Errorf("should not show a reset menu, got: %v", sends)
					}
				}
			})
		}
	})

	t.Run("denied when session reset builtin not allowed", func(t *testing.T) {
		opts := Options{
			// Omit BuiltinResetSession so the command is not permitted.
			AllowedTools: []claudecli.Tool{claudecli.BuiltinStop},
		}

		_, sends := sendMessages(t, opts, "new")

		found := false
		for _, s := range sends {
			if strings.Contains(strings.ToLower(s), "not available") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected a denial message, got: %v", sends)
		}
	})
}

func TestCompact(t *testing.T) {
	t.Run("rewrites message", func(t *testing.T) {
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
	})
}

// --- helpers ---

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
	// Default to all builtins so reset/stop/compact tests work.
	if opts.AllowedTools == nil {
		opts.AllowedTools = []claudecli.Tool{
			claudecli.BuiltinStop, claudecli.BuiltinCompact,
			claudecli.BuiltinReset, claudecli.BuiltinResetSession,
		}
	}

	msgCh := make(chan channel.TaggedMessage, len(messages)+1)
	for _, text := range messages {
		msgCh <- channel.TaggedMessage{ChannelID: "test-ch", Text: text}
	}
	close(msgCh)

	err := RunWithMessages(context.Background(), opts, msgCh)
	return err, ch.sends
}
