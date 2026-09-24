package telegramchannel

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// menuCommands are the agent's built-in keywords, listed in Telegram's "/" menu. Picking one
// sends "/stop", which normalizeCommand turns back into the plain keyword the agent matches.
var menuCommands = []models.BotCommand{
	{Command: "stop", Description: "Stop the reply in progress"},
	{Command: "new", Description: "Start a fresh conversation"},
	{Command: "compact", Description: "Shrink the conversation so far"},
	{Command: "help", Description: "List these commands"},
	{Command: "login", Description: "Sign in to Claude"},
	{Command: "auth", Description: "Show sign-in status"},
}

func registerCommands(ctx context.Context, b *bot.Bot) error {
	if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: menuCommands}); err != nil {
		return fmt.Errorf("set bot commands: %w", err)
	}
	return nil
}

// normalizeCommand turns "/stop" or "/stop@some_bot" into "stop" for a menu command, and leaves
// any other text alone.
func normalizeCommand(text string) string {
	if !strings.HasPrefix(text, "/") {
		return text
	}
	word := strings.TrimPrefix(strings.TrimSpace(text), "/")
	word, _, _ = strings.Cut(word, "@")
	for _, c := range menuCommands {
		if strings.EqualFold(word, c.Command) {
			return c.Command
		}
	}
	return text
}
