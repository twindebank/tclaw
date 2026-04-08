package telegramchannel

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"

	"tclaw/internal/channel"
	"tclaw/internal/channel/channelpkg"
	"tclaw/internal/libraries/store"
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
	if opts.ChatID == 0 {
		// Provisioning stores the chatID in PlatformState but not in the chatid_
		// store key that loadChatID reads. Bridge the gap so the transport can
		// send messages immediately after provisioning (e.g. initial_message).
		chatID, err := loadChatIDFromPlatformState(ctx, params.StateStore, params.ChannelCfg.Name)
		if err != nil {
			slog.Error("failed to load chat ID from platform state", "channel", params.ChannelCfg.Name, "err", err)
		}
		if chatID != 0 {
			opts.ChatID = chatID
			saveChatIDFunc(params.StateStore, params.ChannelCfg.Name)(chatID)
		}
	}
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

	return NewTelegram(token, params.ChannelCfg.Name, params.ChannelCfg.Description, params.ChannelCfg.Purpose, allowedUserIDs, opts), nil
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

// loadChatIDFromPlatformState reads the chat ID from the RuntimeState's PlatformState.
// This is the fallback path: after provisioning, the chatID is stored in PlatformState
// but not yet in the chatid_ key. Returns (0, nil) when no PlatformState exists (normal
// for channels that haven't been provisioned yet).
func loadChatIDFromPlatformState(ctx context.Context, s store.Store, channelName string) (int64, error) {
	data, err := s.Get(ctx, "channel_runtime/"+channelName)
	if err != nil {
		return 0, fmt.Errorf("read runtime state: %w", err)
	}
	if len(data) == 0 {
		return 0, nil
	}

	// Inline parsing to avoid importing the channel package (circular).
	var rs struct {
		PlatformState struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"platform_state"`
	}
	if err := json.Unmarshal(data, &rs); err != nil {
		return 0, fmt.Errorf("parse runtime state: %w", err)
	}
	if channel.PlatformType(rs.PlatformState.Type) != channel.PlatformTelegram {
		return 0, nil
	}
	if len(rs.PlatformState.Data) == 0 {
		return 0, fmt.Errorf("telegram platform state present but data is empty")
	}

	var tgState TelegramPlatformState
	if err := json.Unmarshal(rs.PlatformState.Data, &tgState); err != nil {
		return 0, fmt.Errorf("parse telegram platform state: %w", err)
	}
	return tgState.ChatID, nil
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
