package telegramchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tclaw/internal/channel"
	tgsdk "tclaw/internal/telegram"
)

// promptCallbackPrefix starts a prompt button's callback data: "p:<prompt id>:<reply>".
const promptCallbackPrefix = "p:"

// promptButtons is how each answer is labelled and coloured.
var promptButtons = map[channel.PromptReply]struct {
	Label string
	Style string
}{
	channel.ReplyYes: {Label: "✅ Yes", Style: "success"},
	channel.ReplyNo:  {Label: "✖️ No", Style: "danger"},
}

// SendPrompt sends a confirmation with a button per answer. A press arrives on Messages as the
// user's answer to this prompt.
func (t *Telegram) SendPrompt(ctx context.Context, p channel.SendPromptParams) (channel.MessageID, error) {
	t.mu.Lock()
	chatID := t.currentChatID
	b := t.bot
	t.mu.Unlock()
	if chatID == 0 || b == nil {
		return "", fmt.Errorf("telegram prompt: channel %q has no chat to ask in yet", t.name)
	}

	keyboard, err := promptKeyboard(p.PromptID, p.Replies)
	if err != nil {
		return "", fmt.Errorf("telegram prompt: %w", err)
	}
	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        tgsdk.SanitizeHTML(tgsdk.MarkdownToHTML(p.Text)),
		ParseMode:   models.ParseModeHTML,
		ReplyMarkup: keyboard,
	})
	if err != nil {
		return "", fmt.Errorf("telegram prompt: %w", err)
	}
	return channel.MessageID(strconv.Itoa(msg.ID)), nil
}

// promptKeyboard is one row with a button per reply, each carrying the prompt's id.
func promptKeyboard(promptID string, replies []channel.PromptReply) (models.InlineKeyboardMarkup, error) {
	var row []models.InlineKeyboardButton
	for _, reply := range replies {
		button, ok := promptButtons[reply]
		if !ok {
			return models.InlineKeyboardMarkup{}, fmt.Errorf("no button for reply %q", reply)
		}
		row = append(row, models.InlineKeyboardButton{
			Text:         button.Label,
			Style:        button.Style,
			CallbackData: promptCallbackPrefix + promptID + ":" + string(reply),
		})
	}
	return models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}, nil
}

// PromptKeyboardJSON is the reply_markup for a yes/no prompt, for a message sent outside this
// transport. Its presses arrive on the channel's Messages like any other prompt's.
func PromptKeyboardJSON(promptID string) (string, error) {
	keyboard, err := promptKeyboard(promptID, []channel.PromptReply{channel.ReplyYes, channel.ReplyNo})
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(keyboard)
	if err != nil {
		return "", fmt.Errorf("encode prompt keyboard: %w", err)
	}
	return string(encoded), nil
}

// handleCallbackQuery turns a press on a prompt's button into the user's answer. A press from
// anyone but an allowed user answers nothing, and with no allowlist nobody's press counts.
func (t *Telegram) handleCallbackQuery(ctx context.Context, b *bot.Bot, query *models.CallbackQuery, out chan<- string) {
	press := parsePromptCallback(query.Data)
	switch {
	case len(t.allowedUsers) == 0 || !t.userAllowed(query.From.ID):
		slog.Warn("telegram button press from a user who may not answer prompts", "from_id", query.From.ID, "channel", t.name)
		t.answerCallback(ctx, b, query.ID, "Not allowed")
		return
	case press == nil:
		slog.Warn("telegram button press with unknown data", "channel", t.name)
		t.answerCallback(ctx, b, query.ID, "This button does nothing")
		return
	}

	t.answerCallback(ctx, b, query.ID, "")
	if msg := query.Message.Message; msg != nil {
		// The buttons go once pressed, leaving the answer given in their place.
		t.replaceButtons(ctx, b, msg, press.Reply)
	}

	select {
	case out <- channel.ButtonPressText(*press):
	case <-ctx.Done():
	case <-time.After(30 * time.Second):
		slog.Warn("telegram button press dropped, pipeline blocked", "channel", t.name)
	}
}

// parsePromptCallback reads a prompt button's callback data. Nil means it is not one.
func parsePromptCallback(data string) *channel.ButtonPress {
	rest, ok := strings.CutPrefix(data, promptCallbackPrefix)
	if !ok {
		return nil
	}
	id, reply, ok := strings.Cut(rest, ":")
	if !ok || id == "" {
		return nil
	}
	if _, known := promptButtons[channel.PromptReply(reply)]; !known {
		return nil
	}
	return &channel.ButtonPress{PromptID: id, Reply: channel.PromptReply(reply)}
}

// answerCallback stops the button's loading spinner, showing text briefly when there is any.
func (t *Telegram) answerCallback(ctx context.Context, b *bot.Bot, queryID, text string) {
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: queryID, Text: text}); err != nil {
		slog.Warn("failed to answer telegram button press", "channel", t.name, "err", err)
	}
}

// replaceButtons swaps a prompt's buttons for one disabled button naming the answer given.
func (t *Telegram) replaceButtons(ctx context.Context, b *bot.Bot, msg *models.Message, reply channel.PromptReply) {
	_, err := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
		ChatID:    msg.Chat.ID,
		MessageID: msg.ID,
		ReplyMarkup: models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: promptButtons[reply].Label, Disabled: &models.DisabledButton{}},
		}}},
	})
	if err != nil {
		// The answer still counts; the old buttons just stay visible.
		slog.Warn("failed to remove telegram prompt buttons", "channel", t.name, "err", err)
	}
}
