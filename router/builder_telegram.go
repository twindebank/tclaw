package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"

	"tclaw/channel"
)

// TelegramBuilder builds Telegram bot channel transports.
type TelegramBuilder struct{}

func (TelegramBuilder) Build(ctx context.Context, params ChannelBuildParams) (channel.Channel, error) {
	var opts channel.TelegramOptions
	if params.PublicURL != "" && params.RegisterHandler != nil {
		webhookSecret := make([]byte, 16)
		if _, err := rand.Read(webhookSecret); err != nil {
			return nil, fmt.Errorf("generate webhook secret: %w", err)
		}
		webhookPath := "/telegram/" + hex.EncodeToString(webhookSecret)
		opts.WebhookURL = params.PublicURL + webhookPath
		opts.WebhookPath = webhookPath
		opts.RegisterHandler = params.RegisterHandler
	}
	opts.ChatID = loadChatID(ctx, params.StateStore, params.ChannelCfg.Name)
	opts.OnChatID = saveChatIDFunc(params.StateStore, params.ChannelCfg.Name)
	opts.MediaDir = filepath.Join(params.BaseDir, string(params.UserID), "memory", "media")

	// Token: from channel config (inline or resolved secret ref) or secret store (provisioned).
	var token string
	if params.ChannelCfg.Telegram != nil && params.ChannelCfg.Telegram.Token != "" {
		token = params.ChannelCfg.Telegram.Token
	} else {
		var err error
		token, err = params.SecretStore.Get(ctx, channel.ChannelSecretKey(params.ChannelCfg.Name))
		if err != nil {
			return nil, fmt.Errorf("read token from secret store: %w", err)
		}
		if token == "" {
			return nil, fmt.Errorf("no bot token found (config or secret store)")
		}
	}

	// User ID from user-level config.
	var allowedUserIDs []int64
	if params.UserCfg.Telegram != nil && params.UserCfg.Telegram.UserID != "" {
		uid, err := strconv.ParseInt(params.UserCfg.Telegram.UserID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid telegram user_id %q: %w", params.UserCfg.Telegram.UserID, err)
		}
		allowedUserIDs = []int64{uid}
	}

	return channel.NewTelegram(token, params.ChannelCfg.Name, params.ChannelCfg.Description, allowedUserIDs, opts), nil
}
