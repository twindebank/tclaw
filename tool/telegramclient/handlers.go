package telegramclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"tclaw/mcp"
	tgsdk "tclaw/telegram"
)

const (
	// connectTimeout is how long to wait for the MTProto client to connect.
	connectTimeout = 30 * time.Second
)

// handlerState holds per-user state shared across all tool handlers.
// Created once per RegisterTools call, so each user gets their own instance.
type handlerState struct {
	deps Deps

	// client is lazily initialized on the first tool call that needs it.
	client *tgsdk.Client

	// botFatherMu serializes BotFather conversations. BotFather is a sequential
	// chat — interleaving two /newbot flows corrupts both. Any tool that talks
	// to BotFather must hold this lock for the duration of the conversation.
	botFatherMu sync.Mutex

	// pendingPhone and pendingCodeHash track the in-progress auth flow.
	pendingPhone    string
	pendingCodeHash string
}

func makeHandler(name string, state *handlerState) mcp.ToolHandler {
	switch name {
	case ToolSetup:
		return setupHandler(state)
	case ToolAuth:
		return authHandler(state)
	case ToolVerify:
		return verifyHandler(state)
	case Tool2FA:
		return twoFAHandler(state)
	case ToolStatus:
		return statusHandler(state)
	case "telegram_client_create_bot":
		return createBotHandler(state)
	case "telegram_client_delete_bot":
		return deleteBotHandler(state)
	case ToolConfigureBot:
		return configureBotHandler(state)
	case ToolCreateGroup:
		return createGroupHandler(state)
	case ToolListChats:
		return listChatsHandler(state)
	case ToolGetHistory:
		return getHistoryHandler(state)
	case ToolSearch:
		return searchHandler(state)
	default:
		return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("unknown telegram client tool: %s", name)
		}
	}
}

// ensureConnected lazily initializes and connects the MTProto client.
// Reads API credentials from the secret store on first call.
func ensureConnected(ctx context.Context, s *handlerState) error {
	if s.client != nil && s.client.IsReady() {
		return nil
	}

	apiIDStr, err := s.deps.SecretStore.Get(ctx, APIIDStoreKey)
	if err != nil {
		return fmt.Errorf("read API ID: %w", err)
	}
	if apiIDStr == "" {
		return fmt.Errorf("Telegram Client API credentials not configured — call telegram_client_setup first (register at my.telegram.org)")
	}

	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		return fmt.Errorf("invalid API ID (expected integer): %w", err)
	}

	apiHash, err := s.deps.SecretStore.Get(ctx, APIHashStoreKey)
	if err != nil {
		return fmt.Errorf("read API hash: %w", err)
	}
	if apiHash == "" {
		return fmt.Errorf("Telegram Client API hash not configured — call telegram_client_setup first")
	}

	// Close any existing client before creating a new one.
	if s.client != nil {
		s.client.Close()
	}

	s.client = tgsdk.NewClient(apiID, apiHash, s.deps.SecretStore, SessionStoreKey)
	if err := s.client.Connect(); err != nil {
		return fmt.Errorf("connect to Telegram: %w", err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := s.client.WaitReady(connectCtx); err != nil {
		return fmt.Errorf("waiting for Telegram connection: %w", err)
	}

	return nil
}

func setupHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			APIID   int    `json:"api_id"`
			APIHash string `json:"api_hash"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.APIID != 0 || a.APIHash != "" {
			// Credentials provided directly — store them.
			if a.APIID == 0 {
				return nil, fmt.Errorf("api_id is required when providing credentials directly")
			}
			if a.APIHash == "" {
				return nil, fmt.Errorf("api_hash is required when providing credentials directly")
			}
			if err := s.deps.SecretStore.Set(ctx, APIIDStoreKey, strconv.Itoa(a.APIID)); err != nil {
				return nil, fmt.Errorf("store API ID: %w", err)
			}
			if err := s.deps.SecretStore.Set(ctx, APIHashStoreKey, a.APIHash); err != nil {
				return nil, fmt.Errorf("store API hash: %w", err)
			}
		} else {
			// No params — verify credentials already exist in the secret store
			// (put there via secret_form_request with keys telegram_client_api_id / telegram_client_api_hash).
			apiIDStr, err := s.deps.SecretStore.Get(ctx, APIIDStoreKey)
			if err != nil {
				return nil, fmt.Errorf("read API ID from secret store: %w", err)
			}
			if apiIDStr == "" {
				return nil, fmt.Errorf("no credentials found — collect them via secret_form_request " +
					"using keys \"telegram_client_api_id\" and \"telegram_client_api_hash\", then retry")
			}
			apiHash, err := s.deps.SecretStore.Get(ctx, APIHashStoreKey)
			if err != nil {
				return nil, fmt.Errorf("read API hash from secret store: %w", err)
			}
			if apiHash == "" {
				return nil, fmt.Errorf("API hash not found in secret store — re-run secret_form_request to collect credentials")
			}
		}

		return json.Marshal(map[string]string{
			"status":  "stored",
			"message": "Telegram Client API credentials saved. Call telegram_client_auth with your phone number to authenticate.",
		})
	}
}

func authHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Phone string `json:"phone"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Phone == "" {
			return nil, fmt.Errorf("phone is required")
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		sentCode, err := s.client.Auth().SendCode(ctx, a.Phone, auth.SendCodeOptions{})
		if err != nil {
			return nil, fmt.Errorf("send auth code: %w", err)
		}

		// Extract the code hash from the response.
		sc, ok := sentCode.(*tg.AuthSentCode)
		if !ok {
			return nil, fmt.Errorf("unexpected sent code type: %T", sentCode)
		}

		s.pendingPhone = a.Phone
		s.pendingCodeHash = sc.PhoneCodeHash

		// Persist the phone number for status checks.
		if err := s.deps.SecretStore.Set(ctx, PhoneStoreKey, a.Phone); err != nil {
			return nil, fmt.Errorf("store phone: %w", err)
		}

		// Persist pending auth state so it survives agent restarts — without this
		// a restart between auth and verify loses the code hash and forces a new
		// OTP request, which Telegram may block as a replay attack.
		if err := s.deps.StateStore.Set(ctx, pendingPhoneStoreKey, []byte(a.Phone)); err != nil {
			return nil, fmt.Errorf("persist pending phone: %w", err)
		}
		if err := s.deps.StateStore.Set(ctx, pendingCodeHashStoreKey, []byte(sc.PhoneCodeHash)); err != nil {
			return nil, fmt.Errorf("persist pending code hash: %w", err)
		}

		return json.Marshal(map[string]string{
			"status": "code_sent",
			"message": "Verification code sent to " + a.Phone + ". " +
				"IMPORTANT: IMMEDIATELY call secret_form_request with key \"telegram_otp_code\" to collect the code via secure form. " +
				"WARNING: do NOT ask the user to type the code in chat — sharing it directly in Telegram chat triggers a security block. " +
				"NOTE: there are TWO codes: (1) the tclaw form verification code shown in the form URL, and (2) the actual Telegram OTP the user receives in their Telegram app/another chat — they enter (2) into the form field. " +
				"After secret_form_wait completes, call telegram_client_verify with NO arguments.",
		})
	}
}

func verifyHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		// If no code provided, read from secret store (placed there by secret_form_request).
		if a.Code == "" {
			otp, err := s.deps.SecretStore.Get(ctx, OTPStoreKey)
			if err != nil {
				return nil, fmt.Errorf("read OTP from secret store: %w", err)
			}
			if otp == "" {
				return nil, fmt.Errorf("no code provided and no OTP found in secret store — " +
					"collect the code via secret_form_request with key \"telegram_otp_code\" first")
			}
			a.Code = otp
		}

		// Reload pending state from the store if an agent restart wiped it from memory.
		if s.pendingPhone == "" || s.pendingCodeHash == "" {
			phone, err := s.deps.StateStore.Get(ctx, pendingPhoneStoreKey)
			if err != nil {
				return nil, fmt.Errorf("read pending phone: %w", err)
			}
			hash, err := s.deps.StateStore.Get(ctx, pendingCodeHashStoreKey)
			if err != nil {
				return nil, fmt.Errorf("read pending code hash: %w", err)
			}
			s.pendingPhone = string(phone)
			s.pendingCodeHash = string(hash)
		}

		if s.pendingPhone == "" || s.pendingCodeHash == "" {
			return nil, fmt.Errorf("no pending auth flow — call telegram_client_auth first")
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		_, err := s.client.Auth().SignIn(ctx, s.pendingPhone, a.Code, s.pendingCodeHash)
		if err != nil {
			if err == auth.ErrPasswordAuthNeeded {
				return json.Marshal(map[string]any{
					"status":    "needs_2fa",
					"message":   "Two-factor authentication is enabled. Ask the user for their 2FA password and call telegram_client_2fa.",
					"needs_2fa": true,
				})
			}
			return nil, fmt.Errorf("sign in: %w", err)
		}

		// Auth complete — clear in-memory and persisted pending state.
		s.pendingPhone = ""
		s.pendingCodeHash = ""
		if err := s.deps.StateStore.Set(ctx, pendingPhoneStoreKey, nil); err != nil {
			return nil, fmt.Errorf("clear pending phone: %w", err)
		}
		if err := s.deps.StateStore.Set(ctx, pendingCodeHashStoreKey, nil); err != nil {
			return nil, fmt.Errorf("clear pending code hash: %w", err)
		}

		return json.Marshal(map[string]string{
			"status":  "authenticated",
			"message": "Telegram authentication complete. Client API tools are now available.",
		})
	}
}

func twoFAHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Password string `json:"password"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Password == "" {
			return nil, fmt.Errorf("password is required")
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		_, err := s.client.Auth().Password(ctx, a.Password)
		if err != nil {
			if err == auth.ErrPasswordInvalid {
				return nil, fmt.Errorf("invalid 2FA password — check for whitespace and try again")
			}
			return nil, fmt.Errorf("2FA authentication: %w", err)
		}

		// Auth complete — clear pending state.
		s.pendingPhone = ""
		s.pendingCodeHash = ""

		return json.Marshal(map[string]string{
			"status":  "authenticated",
			"message": "Two-factor authentication complete. Client API tools are now available.",
		})
	}
}

func statusHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		apiIDStr, _ := s.deps.SecretStore.Get(ctx, APIIDStoreKey)
		phone, _ := s.deps.SecretStore.Get(ctx, PhoneStoreKey)

		result := map[string]any{
			"credentials_stored": apiIDStr != "",
			"phone":              phone,
			"connected":          s.client != nil && s.client.IsReady(),
		}

		return json.Marshal(result)
	}
}

// Bot management handlers — delegate to botfather.go

func createBotHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Purpose string `json:"purpose"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Purpose == "" {
			return nil, fmt.Errorf("purpose is required")
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		// Serialize BotFather conversations — only one at a time.
		s.botFatherMu.Lock()
		defer s.botFatherMu.Unlock()

		bf := tgsdk.NewBotFather(s.client)
		result, err := bf.CreateBot(ctx, a.Purpose)
		if err != nil {
			return nil, fmt.Errorf("create bot: %w", err)
		}

		// Store the token server-side so it can be used by channel_create later.
		// Key format matches what channel_create expects.
		tokenKey := "bot/" + result.Username + "/token"
		if storeErr := s.deps.SecretStore.Set(ctx, tokenKey, result.Token); storeErr != nil {
			return nil, fmt.Errorf("store bot token: %w", storeErr)
		}

		// Return only non-sensitive fields — the token must never appear in
		// tool call output or chat history.
		return json.Marshal(map[string]string{
			"username":     result.Username,
			"display_name": result.DisplayName,
			"message":      result.Message,
		})
	}
}

func deleteBotHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Username string `json:"username"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Username == "" {
			return nil, fmt.Errorf("username is required")
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		s.botFatherMu.Lock()
		defer s.botFatherMu.Unlock()

		bf := tgsdk.NewBotFather(s.client)
		if err := bf.DeleteBot(ctx, a.Username); err != nil {
			return nil, fmt.Errorf("delete bot: %w", err)
		}

		return json.Marshal(map[string]string{
			"status":  "deleted",
			"message": fmt.Sprintf("Bot @%s has been permanently deleted.", a.Username),
		})
	}
}

func configureBotHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Username    string `json:"username"`
			Description string `json:"description"`
			About       string `json:"about"`
			Privacy     *bool  `json:"privacy"`
			JoinGroups  *bool  `json:"join_groups"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Username == "" {
			return nil, fmt.Errorf("username is required")
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		s.botFatherMu.Lock()
		defer s.botFatherMu.Unlock()

		bf := tgsdk.NewBotFather(s.client)
		if err := bf.ConfigureBot(ctx, tgsdk.ConfigureBotParams{
			Username:    a.Username,
			Description: a.Description,
			About:       a.About,
			Privacy:     a.Privacy,
			JoinGroups:  a.JoinGroups,
		}); err != nil {
			return nil, fmt.Errorf("configure bot: %w", err)
		}

		return json.Marshal(map[string]string{
			"status":  "configured",
			"message": fmt.Sprintf("Bot @%s has been configured.", a.Username),
		})
	}
}

// Chat management handlers — use raw tg.Client API

func createGroupHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Title string   `json:"title"`
			Users []string `json:"users"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Title == "" {
			return nil, fmt.Errorf("title is required")
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		// Resolve usernames to input users.
		var users []tg.InputUserClass
		for _, username := range a.Users {
			resolved, err := s.client.API().ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
				Username: username,
			})
			if err != nil {
				return nil, fmt.Errorf("resolve username %q: %w", username, err)
			}
			if len(resolved.Users) == 0 {
				return nil, fmt.Errorf("user %q not found", username)
			}
			u, ok := resolved.Users[0].(*tg.User)
			if !ok {
				return nil, fmt.Errorf("unexpected user type for %q", username)
			}
			users = append(users, &tg.InputUser{
				UserID:     u.ID,
				AccessHash: u.AccessHash,
			})
		}

		// Create the group. MessagesCreateChat creates a basic group.
		invited, err := s.client.API().MessagesCreateChat(ctx, &tg.MessagesCreateChatRequest{
			Title: a.Title,
			Users: users,
		})
		if err != nil {
			return nil, fmt.Errorf("create group: %w", err)
		}

		// Extract chat ID from the updates response.
		result := map[string]any{
			"status":  "created",
			"title":   a.Title,
			"message": fmt.Sprintf("Group %q created.", a.Title),
		}
		if chatID := extractChatIDFromInvited(invited); chatID != 0 {
			result["chat_id"] = chatID
		}

		return json.Marshal(result)
	}
}

func listChatsHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Limit int `json:"limit"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Limit <= 0 {
			a.Limit = 20
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		dialogs, err := s.client.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			Limit:      a.Limit,
			OffsetPeer: &tg.InputPeerEmpty{},
		})
		if err != nil {
			return nil, fmt.Errorf("get dialogs: %w", err)
		}

		var chats []chatInfo
		switch d := dialogs.(type) {
		case *tg.MessagesDialogs:
			chats = extractChats(d.Chats)
		case *tg.MessagesDialogsSlice:
			chats = extractChats(d.Chats)
		}

		return json.Marshal(map[string]any{
			"chats": chats,
			"count": len(chats),
		})
	}
}

func getHistoryHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			ChatID int64 `json:"chat_id"`
			Limit  int   `json:"limit"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.ChatID == 0 {
			return nil, fmt.Errorf("chat_id is required")
		}
		if a.Limit <= 0 {
			a.Limit = 50
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		messages, err := s.client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  &tg.InputPeerChat{ChatID: a.ChatID},
			Limit: a.Limit,
		})
		if err != nil {
			return nil, fmt.Errorf("get history: %w", err)
		}

		return json.Marshal(extractMessages(messages))
	}
}

func searchHandler(s *handlerState) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a struct {
			Query  string `json:"query"`
			ChatID int64  `json:"chat_id"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if a.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		if err := ensureConnected(ctx, s); err != nil {
			return nil, err
		}

		var peer tg.InputPeerClass
		if a.ChatID != 0 {
			peer = &tg.InputPeerChat{ChatID: a.ChatID}
		} else {
			peer = &tg.InputPeerEmpty{}
		}

		results, err := s.client.API().MessagesSearch(ctx, &tg.MessagesSearchRequest{
			Peer:   peer,
			Q:      a.Query,
			Filter: &tg.InputMessagesFilterEmpty{},
			Limit:  50,
		})
		if err != nil {
			return nil, fmt.Errorf("search messages: %w", err)
		}

		return json.Marshal(extractMessages(results))
	}
}

// --- helpers ---

// extractChatIDFromInvited pulls the chat ID from a MessagesCreateChat response.
func extractChatIDFromInvited(invited *tg.MessagesInvitedUsers) int64 {
	if invited == nil || invited.Updates == nil {
		return 0
	}
	switch u := invited.Updates.(type) {
	case *tg.Updates:
		for _, chat := range u.Chats {
			if c, ok := chat.(*tg.Chat); ok {
				return c.ID
			}
		}
	}
	return 0
}

type chatInfo struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

func extractChats(chats []tg.ChatClass) []chatInfo {
	var result []chatInfo
	for _, c := range chats {
		switch chat := c.(type) {
		case *tg.Chat:
			result = append(result, chatInfo{ID: chat.ID, Title: chat.Title, Type: "group"})
		case *tg.Channel:
			t := "channel"
			if chat.Megagroup {
				t = "supergroup"
			}
			result = append(result, chatInfo{ID: chat.ID, Title: chat.Title, Type: t})
		}
	}
	return result
}

type messageInfo struct {
	ID     int    `json:"id"`
	Date   int    `json:"date"`
	Text   string `json:"text,omitempty"`
	FromID int64  `json:"from_id,omitempty"`
}

func extractMessages(messages tg.MessagesMessagesClass) map[string]any {
	var result []messageInfo

	var msgList []tg.MessageClass
	switch m := messages.(type) {
	case *tg.MessagesMessages:
		msgList = m.Messages
	case *tg.MessagesMessagesSlice:
		msgList = m.Messages
	case *tg.MessagesChannelMessages:
		msgList = m.Messages
	}

	for _, msg := range msgList {
		if m, ok := msg.(*tg.Message); ok {
			info := messageInfo{
				ID:   m.ID,
				Date: m.Date,
				Text: m.Message,
			}
			if from, ok := m.FromID.(*tg.PeerUser); ok {
				info.FromID = from.UserID
			}
			result = append(result, info)
		}
	}

	return map[string]any{
		"messages": result,
		"count":    len(result),
	}
}
