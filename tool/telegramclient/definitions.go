package telegramclient

import (
	"encoding/json"

	"tclaw/mcp"
)

var toolDefs = []mcp.ToolDef{
	{
		Name: "telegram_client_setup",
		Description: "Store Telegram Client API credentials (API ID and API hash). " +
			"Register at my.telegram.org → API Development Tools to get these. " +
			"These are stored encrypted and never leave this device.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"api_id": {
					"type": "integer",
					"description": "Telegram API ID (numeric) from my.telegram.org."
				},
				"api_hash": {
					"type": "string",
					"description": "Telegram API hash (hexadecimal string) from my.telegram.org."
				}
			},
			"required": ["api_id", "api_hash"]
		}`),
	},
	{
		Name: "telegram_client_auth",
		Description: "Start Telegram authentication by sending an OTP code to the given phone number. " +
			"After calling this, ask the user for the code they received and call telegram_client_verify.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"phone": {
					"type": "string",
					"description": "Phone number in international format (e.g. +447123456789)."
				}
			},
			"required": ["phone"]
		}`),
	},
	{
		Name: "telegram_client_verify",
		Description: "Complete Telegram authentication with the OTP code the user received. " +
			"If 2FA is enabled, the response will indicate that — call telegram_client_2fa next.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"code": {
					"type": "string",
					"description": "The verification code the user received via Telegram."
				}
			},
			"required": ["code"]
		}`),
	},
	{
		Name: "telegram_client_2fa",
		Description: "Provide 2FA password to complete Telegram authentication. " +
			"Only needed if telegram_client_verify returned needs_2fa: true.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"password": {
					"type": "string",
					"description": "Two-factor authentication password."
				}
			},
			"required": ["password"]
		}`),
	},
	{
		Name: "telegram_client_status",
		Description: "Check Telegram Client API authentication status. " +
			"Returns whether credentials are stored and whether the client is connected.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	},
	{
		Name: "telegram_client_create_bot",
		Description: "Create a new Telegram bot via BotFather. The bot gets a randomized, " +
			"non-searchable username (tclaw_<random>_bot) and is automatically configured " +
			"with privacy mode ON and join groups OFF. Returns the bot token — pass it to " +
			"channel_create to set up a new tclaw channel.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"purpose": {
					"type": "string",
					"description": "Short label for the bot's purpose (e.g. 'assistant', 'admin'). Used in the display name only."
				}
			},
			"required": ["purpose"]
		}`),
	},
	{
		Name:        "telegram_client_delete_bot",
		Description: "Delete a Telegram bot via BotFather. This is permanent and cannot be undone.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"username": {
					"type": "string",
					"description": "Bot username (e.g. tclaw_a3f7b21e_bot)."
				}
			},
			"required": ["username"]
		}`),
	},
	{
		Name: "telegram_client_configure_bot",
		Description: "Configure a Telegram bot via BotFather. All parameters are optional — " +
			"only the provided ones are updated. Each setting runs a separate BotFather command internally.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"username": {
					"type": "string",
					"description": "Bot username to configure."
				},
				"description": {
					"type": "string",
					"description": "Bot description (shown on the bot's profile)."
				},
				"about": {
					"type": "string",
					"description": "Bot about text (shown in the bot info)."
				},
				"privacy": {
					"type": "boolean",
					"description": "Privacy mode. true = bot only sees commands, false = bot sees all messages in groups."
				},
				"join_groups": {
					"type": "boolean",
					"description": "Whether the bot can be added to groups by other users."
				}
			},
			"required": ["username"]
		}`),
	},
	{
		Name:        "telegram_client_create_group",
		Description: "Create a new Telegram group and optionally add users/bots to it.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title": {
					"type": "string",
					"description": "Group title."
				},
				"users": {
					"type": "array",
					"items": {"type": "string"},
					"description": "Usernames to add to the group (including bot usernames)."
				}
			},
			"required": ["title"]
		}`),
	},
	{
		Name:        "telegram_client_list_chats",
		Description: "List the authenticated user's Telegram chats and groups.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"limit": {
					"type": "integer",
					"description": "Maximum number of chats to return. Defaults to 20.",
					"default": 20
				}
			}
		}`),
	},
	{
		Name:        "telegram_client_get_history",
		Description: "Get message history from a Telegram chat.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"chat_id": {
					"type": "integer",
					"description": "Telegram chat ID."
				},
				"limit": {
					"type": "integer",
					"description": "Maximum number of messages to return. Defaults to 50.",
					"default": 50
				}
			},
			"required": ["chat_id"]
		}`),
	},
	{
		Name:        "telegram_client_search",
		Description: "Search messages across Telegram chats.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {
					"type": "string",
					"description": "Search query."
				},
				"chat_id": {
					"type": "integer",
					"description": "Limit search to a specific chat. Omit for global search."
				}
			},
			"required": ["query"]
		}`),
	},
}
