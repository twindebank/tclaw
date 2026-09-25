package telegram

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

	// maxBotListPages bounds how many /mybots keyboard pages ListBots walks
	// through, so a malformed or endlessly-cycling navigation keyboard can never
	// loop forever.
	maxBotListPages = 25

	// botListPageEditTimeout is how long to wait for BotFather to edit the
	// /mybots message with the next page after the navigation button is pressed.
	botListPageEditTimeout = 15 * time.Second
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
	Name        string
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
		username, displayName, err := GenerateBotNames(purpose)
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

	if params.Name != "" {
		// /setname changes the bot's display name — the title shown in the chat
		// list. BotFather sets it once at creation and only /setname changes it
		// afterwards, so this is the only way to rename an existing channel's bot.
		if runes := len([]rune(params.Name)); runes > MaxBotDisplayNameLength {
			return fmt.Errorf("name too long: %d runes, max %d (BotFather display name limit)", runes, MaxBotDisplayNameLength)
		}
		if err := bf.runBotFatherCommand(ctx, "/setname", params.Username, params.Name); err != nil {
			return fmt.Errorf("set name: %w", err)
		}
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

// ListBots returns the usernames (without the @) of every bot the account owns,
// by asking BotFather for /mybots and reading the bot list off the inline
// keyboard it replies with. When the account owns more bots than fit on one
// keyboard, BotFather paginates: a navigation button advances the SAME message
// to the next page. ListBots follows that button and re-reads until every page
// has been collected, so the returned list is complete rather than just the
// first page.
func (bf *BotFather) ListBots(ctx context.Context) ([]string, error) {
	if err := bf.resolvePeer(ctx); err != nil {
		return nil, err
	}

	if err := bf.sendMessage(ctx, "/mybots"); err != nil {
		return nil, fmt.Errorf("send /mybots: %w", err)
	}

	// BotFather replies "Choose a bot from the list below:" with one inline
	// button per bot, each labelled with the bot's @username.
	msg, err := bf.waitForMessageMatching(ctx, "choose")
	if err != nil {
		return nil, fmt.Errorf("waiting for bot list: %w", err)
	}

	seen := make(map[string]bool)
	var usernames []string
	for page := 0; page < maxBotListPages; page++ {
		before := len(usernames)
		appendNewUsernames(&usernames, seen, parseBotUsernames(msg))

		if page > 0 && len(usernames) == before {
			// Pressing the navigation button produced no bots we hadn't already
			// seen — the keyboard is cycling rather than advancing, so stop here
			// rather than loop until the page cap.
			break
		}

		data, ok := findNextPageButton(msg)
		if !ok {
			// No forward-navigation button: this is the last (or only) page.
			break
		}

		next, err := bf.pressPageButtonAndReread(ctx, msg, data)
		if err != nil {
			return nil, fmt.Errorf("advance to next bot list page: %w", err)
		}
		msg = next

		if page == maxBotListPages-1 {
			slog.Warn("botfather: hit max /mybots pages, list may be truncated", "max_pages", maxBotListPages)
		}
	}

	if len(usernames) == 0 {
		// Either the account has no bots or the reply wasn't the expected
		// keyboard — surface it rather than returning a silent empty list.
		return nil, fmt.Errorf("no bot usernames found in BotFather's /mybots reply")
	}
	return usernames, nil
}

// pressPageButtonAndReread presses an inline navigation button on the /mybots
// message and returns the edited message once BotFather has loaded the next
// page. BotFather edits the SAME message in place, so we poll its ID until the
// keyboard changes (or the timeout elapses).
func (bf *BotFather) pressPageButtonAndReread(ctx context.Context, msg *tg.Message, data []byte) (*tg.Message, error) {
	req := &tg.MessagesGetBotCallbackAnswerRequest{
		Peer:  bf.peer,
		MsgID: msg.ID,
	}
	req.SetData(data)
	if _, err := bf.client.API().MessagesGetBotCallbackAnswer(ctx, req); err != nil {
		return nil, fmt.Errorf("press navigation button: %w", err)
	}

	before := keyboardSignature(msg)
	deadline := time.Now().Add(botListPageEditTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		time.Sleep(pollInterval)

		updated, err := bf.getMessageByID(ctx, msg.ID)
		if err != nil {
			return nil, err
		}
		if updated != nil && keyboardSignature(updated) != before {
			return updated, nil
		}
	}
	return nil, fmt.Errorf("timeout waiting for BotFather to load the next /mybots page")
}

// getMessageByID fetches a single message from the BotFather chat by its ID,
// returning nil if it is no longer present. Used to re-read the /mybots message
// after BotFather edits it to the next page.
func (bf *BotFather) getMessageByID(ctx context.Context, msgID int) (*tg.Message, error) {
	res, err := bf.client.API().MessagesGetMessages(ctx, []tg.InputMessageClass{
		&tg.InputMessageID{ID: msgID},
	})
	if err != nil {
		return nil, fmt.Errorf("get message %d: %w", msgID, err)
	}

	var messages []tg.MessageClass
	switch m := res.(type) {
	case *tg.MessagesMessages:
		messages = m.Messages
	case *tg.MessagesMessagesSlice:
		messages = m.Messages
	case *tg.MessagesChannelMessages:
		messages = m.Messages
	}

	for _, raw := range messages {
		if msg, ok := raw.(*tg.Message); ok && msg.ID == msgID {
			return msg, nil
		}
	}
	return nil, nil
}

// StartBot sends /start to a bot as the authenticated user via MTProto.
func (bf *BotFather) StartBot(ctx context.Context, botUsername string) error {
	resolved, err := bf.client.API().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: botUsername,
	})
	if err != nil {
		return fmt.Errorf("resolve bot @%s: %w", botUsername, err)
	}
	if len(resolved.Users) == 0 {
		return fmt.Errorf("bot @%s not found", botUsername)
	}

	u, ok := resolved.Users[0].(*tg.User)
	if !ok {
		return fmt.Errorf("unexpected user type for @%s", botUsername)
	}

	peer := &tg.InputPeerUser{
		UserID:     u.ID,
		AccessHash: u.AccessHash,
	}

	_, err = bf.client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  "/start",
		RandomID: GenerateRandomID(),
	})
	if err != nil {
		return fmt.Errorf("send /start to @%s: %w", botUsername, err)
	}

	slog.Info("auto-started bot conversation", "bot", botUsername)
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
	slog.Debug("botfather: sending message", "text", truncate(text, 40), "last_seen_id", bf.lastSeenMsgID)

	_, err := bf.client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     bf.peer,
		Message:  text,
		RandomID: GenerateRandomID(),
	})
	if err != nil {
		slog.Error("botfather: send failed", "err", err)
	}
	return err
}

// waitForResponse polls for the next matching BotFather message and returns its
// text. See waitForMessageMatching for the matching rules.
func (bf *BotFather) waitForResponse(ctx context.Context, substring string) (string, error) {
	msg, err := bf.waitForMessageMatching(ctx, substring)
	if err != nil {
		return "", err
	}
	return msg.Message, nil
}

// waitForMessageMatching polls BotFather's chat for a new message with an ID
// strictly greater than lastSeenMsgID whose text contains substring (case-
// insensitive; an empty substring matches the next message). It returns the
// full message so callers can also read structured fields such as the inline
// keyboard. Polling by ID prevents picking up stale messages or responses from
// concurrent BotFather conversations.
func (bf *BotFather) waitForMessageMatching(ctx context.Context, substring string) (*tg.Message, error) {
	deadline := time.Now().Add(stepTimeout)
	substring = strings.ToLower(substring)

	// Small initial delay to let BotFather process the message.
	time.Sleep(pollInterval)

	slog.Debug("botfather: waiting for response", "substring", substring, "last_seen_id", bf.lastSeenMsgID)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		history, err := bf.client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  bf.peer,
			Limit: 5,
		})
		if err != nil {
			return nil, fmt.Errorf("get BotFather history: %w", err)
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
			bf.lastSeenMsgID = msg.ID

			// Fail fast on BotFather error messages (rate limits, invalid input, etc.)
			// even when they don't match the expected substring. Without this, we'd
			// silently wait until stepTimeout before surfacing the error.
			if substring != "" && !strings.Contains(strings.ToLower(text), substring) && containsError(text) {
				slog.Error("botfather: received error response", "msg_id", msg.ID, "text", truncate(text, 120))
				return nil, fmt.Errorf("BotFather error: %s", text)
			}

			if substring == "" || strings.Contains(strings.ToLower(text), substring) {
				slog.Info("botfather: got response", "msg_id", msg.ID, "text_prefix", truncate(text, 80))
				return msg, nil
			}
			slog.Debug("botfather: skipping message (no substring match)", "msg_id", msg.ID, "text_prefix", truncate(text, 80), "want", substring)
		}

		time.Sleep(pollInterval)
	}

	slog.Error("botfather: timeout waiting for response", "substring", substring, "last_seen_id", bf.lastSeenMsgID)
	return nil, fmt.Errorf("timeout waiting for BotFather response (expected %q)", substring)
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

const (
	// MaxBotDisplayNameLength is BotFather's hard limit on bot display name length in runes.
	// Exported so callers (e.g. the configure_bot tool) can validate a new name before invoking BotFather.
	MaxBotDisplayNameLength = 64

	// botDisplayNamePrefix is prepended to the purpose to form the display name.
	// Rune length: t,c,l,a,w,space,·,space = 8.
	botDisplayNamePrefix = "tclaw · "

	// MaxBotPurposeRunes is the maximum rune length allowed for the purpose string.
	// = MaxBotDisplayNameLength (64) - len([]rune(botDisplayNamePrefix)) (8).
	// Exported so callers can validate before invoking BotFather.
	MaxBotPurposeRunes = 56
)

// autoProvisionedBotUsernameRegex matches usernames minted by GenerateBotNames:
// "tclaw_" + 8 lowercase hex chars + "_bot". tclaw only ever auto-creates bots
// in this exact shape, so a bot matching it that no channel claims is a leftover
// that is safe to reap, while a custom or statically-configured bot never
// matches and is never mistaken for one.
var autoProvisionedBotUsernameRegex = regexp.MustCompile(`^tclaw_[0-9a-f]{8}_bot$`)

// IsAutoProvisionedBotUsername reports whether username follows the naming
// convention GenerateBotNames uses for every bot tclaw creates via BotFather.
// The @ prefix is tolerated and the check is case-insensitive, since BotFather
// lowercases usernames.
func IsAutoProvisionedBotUsername(username string) bool {
	normalized := strings.ToLower(strings.TrimPrefix(username, "@"))
	return autoProvisionedBotUsernameRegex.MatchString(normalized)
}

// GenerateBotNames creates a randomized username and a human-readable display name.
// The username has a random hex suffix for non-discoverability. Returns an error if
// the purpose exceeds MaxBotPurposeRunes — callers should validate upfront.
func GenerateBotNames(purpose string) (username, displayName string, err error) {
	if len([]rune(purpose)) > MaxBotPurposeRunes {
		return "", "", fmt.Errorf("purpose too long: %d runes, max %d (BotFather display name limit)", len([]rune(purpose)), MaxBotPurposeRunes)
	}

	randomBytes := make([]byte, 4)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	randomHex := hex.EncodeToString(randomBytes)

	username = fmt.Sprintf("tclaw_%s_bot", randomHex)
	displayName = botDisplayNamePrefix + purpose

	return username, displayName, nil
}

// GenerateRandomID creates a random int64 for Telegram message deduplication.
func GenerateRandomID() int64 {
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

// containsError checks if a BotFather response indicates an error or rate limit.
func containsError(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "sorry") ||
		strings.Contains(lower, "error") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "can't") ||
		strings.Contains(lower, "too many")
}

// parseBotUsernames pulls bot usernames (without the leading @) out of the
// inline keyboard on BotFather's /mybots reply, where each button is labelled
// with a bot's @username. Non-bot control buttons and duplicates are skipped.
func parseBotUsernames(msg *tg.Message) []string {
	markup, ok := msg.ReplyMarkup.(*tg.ReplyInlineMarkup)
	if !ok {
		return nil
	}

	var usernames []string
	seen := make(map[string]bool)
	for _, row := range markup.Rows {
		for _, button := range row.Buttons {
			name := strings.TrimPrefix(strings.TrimSpace(button.Text), "@")
			// BotFather labels each bot button with its @username, which always
			// ends in "bot"; anything else is a control button, so skip it.
			if !strings.HasSuffix(strings.ToLower(name), "bot") || seen[name] {
				continue
			}
			seen[name] = true
			usernames = append(usernames, name)
		}
	}
	return usernames
}

// appendNewUsernames appends the usernames not already present in seen to dst,
// preserving order and marking each as seen. Used to accumulate bots across the
// paginated /mybots keyboards without duplicating a bot that appears on more
// than one page.
func appendNewUsernames(dst *[]string, seen map[string]bool, usernames []string) {
	for _, username := range usernames {
		if seen[username] {
			continue
		}
		seen[username] = true
		*dst = append(*dst, username)
	}
}

// findNextPageButton returns the callback data of the forward-navigation button
// on a paginated /mybots keyboard, if one is present. BotFather labels forward
// navigation with a » (right guillemet); the previous-page button uses « and is
// deliberately ignored so paging only ever moves forward.
func findNextPageButton(msg *tg.Message) ([]byte, bool) {
	markup, ok := msg.ReplyMarkup.(*tg.ReplyInlineMarkup)
	if !ok {
		return nil, false
	}
	for _, row := range markup.Rows {
		for _, button := range row.Buttons {
			callback, ok := button.Type.(*tg.InlineButtonTypeCallback)
			if !ok {
				continue
			}
			text := strings.TrimSpace(button.Text)
			if strings.Contains(text, "»") && !strings.Contains(text, "«") {
				return callback.Data, true
			}
		}
	}
	return nil, false
}

// keyboardSignature returns a stable fingerprint of a message's inline keyboard
// button labels. It lets ListBots tell when BotFather has finished editing the
// /mybots message to a different page (the signature changes) versus not yet
// (the signature is unchanged).
func keyboardSignature(msg *tg.Message) string {
	markup, ok := msg.ReplyMarkup.(*tg.ReplyInlineMarkup)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, row := range markup.Rows {
		for _, button := range row.Buttons {
			b.WriteString(button.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
