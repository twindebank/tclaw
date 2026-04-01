package telegram

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// BotSend sends a message via the Telegram Bot HTTP API and returns the message ID.
func BotSend(token string, chatID int64, text string) (int, error) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	resp, err := http.PostForm(apiURL, url.Values{
		"chat_id":    {strconv.FormatInt(chatID, 10)},
		"text":       {text},
		"parse_mode": {"HTML"},
	})
	if err != nil {
		return 0, fmt.Errorf("telegram sendMessage: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode sendMessage response: %w", err)
	}
	if !result.OK {
		return 0, fmt.Errorf("telegram sendMessage failed: %s", result.Description)
	}
	return result.Result.MessageID, nil
}

// BotGetUpdates polls for new messages via the Telegram Bot HTTP API.
func BotGetUpdates(token string, offset, timeout int) ([]Update, error) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", token)
	resp, err := http.PostForm(apiURL, url.Values{
		"offset":  {strconv.Itoa(offset)},
		"timeout": {strconv.Itoa(timeout)},
	})
	if err != nil {
		return nil, fmt.Errorf("telegram getUpdates: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode getUpdates response: %w", err)
	}
	return result.Result, nil
}

// Update represents a Telegram Bot API update.
type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message"`
}

// Message represents a Telegram Bot API message.
type Message struct {
	MessageID      int      `json:"message_id"`
	Text           string   `json:"text"`
	ReplyToMessage *Message `json:"reply_to_message"`
}

// ParseAllowedUserIDs converts string user IDs to int64 for Telegram API calls.
func ParseAllowedUserIDs(users []string) ([]int64, error) {
	ids := make([]int64, 0, len(users))
	for _, s := range users {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid Telegram user ID %q: %w", s, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
