package telegramchannel

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"

	"tclaw/channel"
	"tclaw/channel/channelpkg"
	"tclaw/libraries/store"
)

// Package implements channelpkg.Package for Telegram bot channels.
type Package struct {
	provisioner channel.EphemeralProvisioner
}

// NewPackage creates a telegram channel package. The provisioner is optional —
// pass nil if Telegram Client API provisioning is not available.
func NewPackage(provisioner channel.EphemeralProvisioner) *Package {
	return &Package{provisioner: provisioner}
}

func (p *Package) Type() channel.ChannelType { return channel.TypeTelegram }

func (p *Package) Build(ctx context.Context, params channelpkg.BuildParams) (channel.Channel, error) {
	var opts TelegramOptions
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

	return NewTelegram(token, params.ChannelCfg.Name, params.ChannelCfg.Description, allowedUserIDs, opts), nil
}

func (p *Package) Provisioner() channel.EphemeralProvisioner { return p.provisioner }

// --- helpers ---

// loadChatID returns the persisted Telegram chat ID for a channel, or 0 if none.
func loadChatID(ctx context.Context, s store.Store, channelName string) int64 {
	data, err := s.Get(ctx, "chatid_"+channelName)
	if err != nil {
		slog.Error("failed to load chat ID", "channel", channelName, "err", err)
		return 0
	}
	if len(data) != 8 {
		return 0
	}
	return int64(binary.LittleEndian.Uint64(data))
}

// saveChatIDFunc returns a callback that persists a Telegram chat ID to the store.
func saveChatIDFunc(s store.Store, channelName string) func(int64) {
	return func(chatID int64) {
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(chatID))
		if err := s.Set(context.Background(), "chatid_"+channelName, buf); err != nil {
			slog.Error("failed to persist chat ID", "channel", channelName, "err", err)
		}
	}
}
