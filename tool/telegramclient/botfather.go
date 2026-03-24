package telegramclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

const (
	// botFatherUsername is BotFather's Telegram username.
	botFatherUsername = "BotFather"

	// stepTimeout is how long to wait for a BotFather response per step.
	stepTimeout = 30 * time.Second

	// pollInterval is how often to check for new BotFather messages.
	pollInterval = 2 * time.Second

	// maxUsernameRetries is how many times to retry with a new random username
	// if BotFather rejects the chosen one (already taken).
	maxUsernameRetries = 3
)

// tokenRegex matches Telegram bot tokens in BotFather responses.
var tokenRegex = regexp.MustCompile(`\d{8,12}:[A-Za-z0-9_-]{30,}`)

// BotFather drives conversations with @BotFather programmatically.
// All interaction is deterministic — the caller provides parameters and gets
// back a finished result. No agent involvement in the BotFather conversation.
type BotFather struct {
	client *Client
	peer   *tg.InputPeerUser

	// lastSeenMsgID tracks the most recent message ID in the BotFather chat.
	// waitForResponse only accepts messages with IDs strictly greater than this,
	// preventing stale or concurrent messages from being picked up.
	lastSeenMsgID int
}

// NewBotFather creates a BotFather session for the given client.
func NewBotFather(client *Client) *BotFather {
	return &BotFather{client: client}
}

// CreateBotResult is returned by CreateBot on success.
type CreateBotResult struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Message     string `json:"message"`
}

// ConfigureBotParams controls which BotFather settings to update.
type ConfigureBotParams struct {
	Username    string
	Description string
	About       string
	Privacy     *bool
	JoinGroups  *bool
}

// CreateBot mints a new bot via BotFather with a randomized, non-searchable
// username and automatically configures privacy ON and join groups OFF.
func (bf *BotFather) CreateBot(ctx context.Context, purpose string) (*CreateBotResult, error) {
	if err := bf.resolvePeer(ctx); err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := range maxUsernameRetries {
		_ = attempt
		username, displayName, err := generateBotNames(purpose)
		if err != nil {
			return nil, fmt.Errorf("generate bot names: %w", err)
		}

		result, err := bf.tryCreateBot(ctx, username, displayName)
		if err != nil {
			// If the username is taken, retry with a new random one.
			if strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "taken") || strings.Contains(err.Error(), "occupied") {
				lastErr = err
				continue
			}
			return nil, err
		}

		// Auto-configure the new bot: privacy ON, join groups OFF.
		if err := bf.configurePrivacy(ctx, username, true); err != nil {
			// Non-fatal — the bot is created, just not fully configured.
			result.Message += fmt.Sprintf(" (warning: failed to set privacy: %v)", err)
		}
		if err := bf.configureJoinGroups(ctx, username, false); err != nil {
			result.Message += fmt.Sprintf(" (warning: failed to disable join groups: %v)", err)
		}

		return result, nil
	}

	return nil, fmt.Errorf("failed to create bot after %d attempts (username collisions): %w", maxUsernameRetries, lastErr)
}

// DeleteBot permanently deletes a bot via BotFather.
func (bf *BotFather) DeleteBot(ctx context.Context, username string) error {
	if err := bf.resolvePeer(ctx); err != nil {
		return err
	}

	// Send /deletebot command.
	if err := bf.sendMessage(ctx, "/deletebot"); err != nil {
		return fmt.Errorf("send /deletebot: %w", err)
	}

	// Wait for bot selection prompt.
	_, err := bf.waitForResponse(ctx, "choose")
	if err != nil {
		return fmt.Errorf("waiting for bot selection: %w", err)
	}

	// Select the bot.
	atUsername := username
	if !strings.HasPrefix(atUsername, "@") {
		atUsername = "@" + atUsername
	}
	if err := bf.sendMessage(ctx, atUsername); err != nil {
		return fmt.Errorf("send bot selection: %w", err)
	}

	// Wait for confirmation prompt.
	_, err = bf.waitForResponse(ctx, "sure")
	if err != nil {
		return fmt.Errorf("waiting for confirmation: %w", err)
	}

	// Confirm deletion.
	if err := bf.sendMessage(ctx, "Yes, I am totally sure."); err != nil {
		return fmt.Errorf("send confirmation: %w", err)
	}

	// Wait for "Done" response.
	resp, err := bf.waitForResponse(ctx, "done")
	if err != nil {
		return fmt.Errorf("waiting for deletion confirmation: %w", err)
	}
	if containsError(resp) {
		return fmt.Errorf("BotFather error: %s", resp)
	}

	return nil
}

// ConfigureBot updates one or more bot settings via BotFather.
func (bf *BotFather) ConfigureBot(ctx context.Context, params ConfigureBotParams) error {
	if err := bf.resolvePeer(ctx); err != nil {
		return err
	}

	if params.Description != "" {
		if err := bf.runBotFatherCommand(ctx, "/setdescription", params.Username, params.Description); err != nil {
			return fmt.Errorf("set description: %w", err)
		}
	}

	if params.About != "" {
		if err := bf.runBotFatherCommand(ctx, "/setabouttext", params.Username, params.About); err != nil {
			return fmt.Errorf("set about: %w", err)
		}
	}

	if params.Privacy != nil {
		if err := bf.configurePrivacy(ctx, params.Username, *params.Privacy); err != nil {
			return fmt.Errorf("set privacy: %w", err)
		}
	}

	if params.JoinGroups != nil {
		if err := bf.configureJoinGroups(ctx, params.Username, *params.JoinGroups); err != nil {
			return fmt.Errorf("set join groups: %w", err)
		}
	}

	return nil
}

// --- internal methods ---

func (bf *BotFather) tryCreateBot(ctx context.Context, username, displayName string) (*CreateBotResult, error) {
	// Send /newbot command.
	if err := bf.sendMessage(ctx, "/newbot"); err != nil {
		return nil, fmt.Errorf("send /newbot: %w", err)
	}

	// Wait for "choose a name" prompt.
	_, err := bf.waitForResponse(ctx, "name")
	if err != nil {
		return nil, fmt.Errorf("waiting for name prompt: %w", err)
	}

	// Send the display name.
	if err := bf.sendMessage(ctx, displayName); err != nil {
		return nil, fmt.Errorf("send display name: %w", err)
	}

	// Wait for "choose a username" prompt.
	_, err = bf.waitForResponse(ctx, "username")
	if err != nil {
		return nil, fmt.Errorf("waiting for username prompt: %w", err)
	}

	// Send the username.
	if err := bf.sendMessage(ctx, username); err != nil {
		return nil, fmt.Errorf("send username: %w", err)
	}

	// Wait for the response — either a token or an error.
	resp, err := bf.waitForResponse(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("waiting for bot creation result: %w", err)
	}

	if containsError(resp) {
		return nil, fmt.Errorf("BotFather: %s", resp)
	}

	// Extract the bot token.
	token := tokenRegex.FindString(resp)
	if token == "" {
		return nil, fmt.Errorf("no token found in BotFather response: %s", resp)
	}

	return &CreateBotResult{
		Token:       token,
		Username:    username,
		DisplayName: displayName,
		Message:     fmt.Sprintf("Bot @%s created. Token can be used with channel_create.", username),
	}, nil
}

func (bf *BotFather) configurePrivacy(ctx context.Context, username string, enabled bool) error {
	value := "Disable"
	if enabled {
		value = "Enable"
	}
	return bf.runBotFatherCommand(ctx, "/setprivacy", username, value)
}

func (bf *BotFather) configureJoinGroups(ctx context.Context, username string, enabled bool) error {
	value := "Disable"
	if enabled {
		value = "Enable"
	}
	return bf.runBotFatherCommand(ctx, "/setjoingroups", username, value)
}

// runBotFatherCommand executes a BotFather command that follows the pattern:
// send command → wait for bot selection → select bot → wait for value prompt → send value.
func (bf *BotFather) runBotFatherCommand(ctx context.Context, command, username, value string) error {
	if err := bf.sendMessage(ctx, command); err != nil {
		return fmt.Errorf("send %s: %w", command, err)
	}

	// Wait for bot selection prompt.
	if _, err := bf.waitForResponse(ctx, ""); err != nil {
		return fmt.Errorf("waiting for bot selection after %s: %w", command, err)
	}

	// Select the bot.
	atUsername := username
	if !strings.HasPrefix(atUsername, "@") {
		atUsername = "@" + atUsername
	}
	if err := bf.sendMessage(ctx, atUsername); err != nil {
		return fmt.Errorf("send bot selection for %s: %w", command, err)
	}

	// Wait for the value prompt.
	if _, err := bf.waitForResponse(ctx, ""); err != nil {
		return fmt.Errorf("waiting for value prompt after %s: %w", command, err)
	}

	// Send the value.
	if err := bf.sendMessage(ctx, value); err != nil {
		return fmt.Errorf("send value for %s: %w", command, err)
	}

	// Wait for confirmation.
	resp, err := bf.waitForResponse(ctx, "")
	if err != nil {
		return fmt.Errorf("waiting for confirmation after %s: %w", command, err)
	}
	if containsError(resp) {
		return fmt.Errorf("BotFather error on %s: %s", command, resp)
	}

	return nil
}

// resolvePeer resolves BotFather's username to an InputPeerUser. Cached after first call.
func (bf *BotFather) resolvePeer(ctx context.Context) error {
	if bf.peer != nil {
		return nil
	}

	resolved, err := bf.client.API().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: botFatherUsername,
	})
	if err != nil {
		return fmt.Errorf("resolve @%s: %w", botFatherUsername, err)
	}
	if len(resolved.Users) == 0 {
		return fmt.Errorf("@%s not found", botFatherUsername)
	}

	u, ok := resolved.Users[0].(*tg.User)
	if !ok {
		return fmt.Errorf("unexpected user type for @%s", botFatherUsername)
	}

	bf.peer = &tg.InputPeerUser{
		UserID:     u.ID,
		AccessHash: u.AccessHash,
	}

	// Snapshot the latest message ID so waitForResponse ignores anything
	// already in the chat before we start our conversation.
	bf.lastSeenMsgID = bf.latestMessageID(ctx)

	return nil
}

// sendMessage sends a text message to BotFather and updates lastSeenMsgID
// so that waitForResponse only picks up messages newer than what we sent.
func (bf *BotFather) sendMessage(ctx context.Context, text string) error {
	if id := bf.latestMessageID(ctx); id > bf.lastSeenMsgID {
		bf.lastSeenMsgID = id
	}
	slog.Info("botfather: sending message", "text", truncate(text, 40), "last_seen_id", bf.lastSeenMsgID)

	_, err := bf.client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     bf.peer,
		Message:  text,
		RandomID: generateRandomID(),
	})
	if err != nil {
		slog.Error("botfather: send failed", "err", err)
	}
	return err
}

// waitForResponse polls BotFather's chat for a new response with a message ID
// strictly greater than lastSeenMsgID. This prevents picking up stale messages
// or responses from concurrent BotFather conversations.
func (bf *BotFather) waitForResponse(ctx context.Context, substring string) (string, error) {
	deadline := time.Now().Add(stepTimeout)
	substring = strings.ToLower(substring)

	// Small initial delay to let BotFather process the message.
	time.Sleep(pollInterval)

	slog.Info("botfather: waiting for response", "substring", substring, "last_seen_id", bf.lastSeenMsgID)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		history, err := bf.client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  bf.peer,
			Limit: 5,
		})
		if err != nil {
			return "", fmt.Errorf("get BotFather history: %w", err)
		}

		var messages []tg.MessageClass
		switch h := history.(type) {
		case *tg.MessagesMessages:
			messages = h.Messages
		case *tg.MessagesMessagesSlice:
			messages = h.Messages
		}

		for _, raw := range messages {
			msg, ok := raw.(*tg.Message)
			if !ok {
				continue
			}
			slog.Info("botfather: poll saw message",
				"msg_id", msg.ID,
				"last_seen_id", bf.lastSeenMsgID,
				"from_id", msg.FromID,
				"text_prefix", truncate(msg.Message, 60))
			if msg.ID <= bf.lastSeenMsgID {
				continue
			}
			// Skip our own messages. In private chats with BotFather, bot
			// replies may have FromID=nil (MTProto uses peer context). Our
			// own sent messages have FromID set to our user ID. So: skip
			// messages with a known non-BotFather FromID, accept everything else.
			if from, ok := msg.FromID.(*tg.PeerUser); ok && from.UserID != bf.peer.UserID {
				// Message from us (or another user), not BotFather — skip.
				continue
			}

			text := msg.Message
			if substring == "" || strings.Contains(strings.ToLower(text), substring) {
				bf.lastSeenMsgID = msg.ID
				slog.Info("botfather: got response", "msg_id", msg.ID, "text_prefix", truncate(text, 80))
				return text, nil
			}
			slog.Debug("botfather: skipping message (no substring match)", "msg_id", msg.ID, "text_prefix", truncate(text, 80), "want", substring)
		}

		time.Sleep(pollInterval)
	}

	slog.Error("botfather: timeout waiting for response", "substring", substring, "last_seen_id", bf.lastSeenMsgID)
	return "", fmt.Errorf("timeout waiting for BotFather response (expected %q)", substring)
}

// latestMessageID returns the ID of the most recent message in the BotFather
// chat, or 0 if the chat is empty or unreadable.
func (bf *BotFather) latestMessageID(ctx context.Context) int {
	history, err := bf.client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  bf.peer,
		Limit: 1,
	})
	if err != nil {
		return 0
	}

	var messages []tg.MessageClass
	switch h := history.(type) {
	case *tg.MessagesMessages:
		messages = h.Messages
	case *tg.MessagesMessagesSlice:
		messages = h.Messages
	}

	if len(messages) > 0 {
		if msg, ok := messages[0].(*tg.Message); ok {
			return msg.ID
		}
	}
	return 0
}

// --- helpers ---

// generateBotNames creates a randomized username and clean display name.
// The username has a random suffix for non-discoverability; the display
// name is clean and human-readable (no random parts).
func generateBotNames(purpose string) (username, displayName string, err error) {
	randomBytes := make([]byte, 4)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	randomHex := hex.EncodeToString(randomBytes)

	username = fmt.Sprintf("tclaw_%s_bot", randomHex)
	displayName = fmt.Sprintf("tclaw · %s", purpose)

	return username, displayName, nil
}

// generateRandomID creates a random int64 for Telegram message deduplication.
func generateRandomID() int64 {
	b := make([]byte, 8)
	rand.Read(b)
	var id int64
	for i := range 8 {
		id |= int64(b[i]) << (i * 8)
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// containsError checks if a BotFather response indicates an error.
func containsError(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "sorry") ||
		strings.Contains(lower, "error") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "can't")
}
