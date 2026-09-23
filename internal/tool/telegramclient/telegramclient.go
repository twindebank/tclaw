package telegramclient

import (
	"tclaw/internal/channel"
	"tclaw/internal/libraries/secret"
	"tclaw/internal/libraries/store"
	"tclaw/internal/mcp"
)

const (
	// APIIDStoreKey is the secret store key for the Telegram API ID from my.telegram.org.
	APIIDStoreKey = "telegram_client_api_id"

	// APIHashStoreKey is the secret store key for the Telegram API hash from my.telegram.org.
	APIHashStoreKey = "telegram_client_api_hash"

	// PhoneStoreKey is the secret store key for the authenticated phone number.
	PhoneStoreKey = "telegram_client_phone"

	// SessionStoreKey is the secret store key for the persisted MTProto session (base64-encoded).
	SessionStoreKey = "telegram_client_session"

	// OTPStoreKey is the secret store key used to receive the Telegram OTP via
	// secret_form_request. The agent never sees the value — it reads it here.
	OTPStoreKey = "telegram_otp_code"

	// pendingPhoneStoreKey and pendingCodeHashStoreKey persist the in-progress
	// auth flow across agent restarts. Without this, a restart between
	// telegram_client_auth and telegram_client_verify loses the code hash and
	// requires re-requesting the OTP (which Telegram may block as suspicious).
	pendingPhoneStoreKey    = "telegram_client_pending_phone"
	pendingCodeHashStoreKey = "telegram_client_pending_code_hash"
)

// Deps holds dependencies for Telegram Client API tools.
type Deps struct {
	SecretStore secret.Store
	StateStore  store.Store

	// ChannelRegistry and RuntimeState let telegram_client_list_bots map each
	// BotFather-owned bot back to the channel that owns it, so leftover bots from
	// deleted channels can be told apart from live ones. Both may be nil (e.g. in
	// tests that don't exercise the mapping).
	ChannelRegistry *channel.Registry
	RuntimeState    *channel.RuntimeStateStore
}

// RegisterTools registers all Telegram Client API tools on the handler.
// Each call creates a new handlerState, so per-user state is isolated.
// Returns a Provisioner that can create/delete bots using the same client
// state — pass it to channel tools for auto-provisioning.
func RegisterTools(handler *mcp.Handler, deps Deps) *Provisioner {
	state := &handlerState{
		deps: deps,
	}
	for _, def := range toolDefs {
		handler.Register(def, makeHandler(def.Name, state))
	}
	return &Provisioner{state: state}
}

// UnregisterTools removes all Telegram Client API tools from the handler.
func UnregisterTools(handler *mcp.Handler) {
	for _, def := range toolDefs {
		handler.Unregister(def.Name)
	}
}
