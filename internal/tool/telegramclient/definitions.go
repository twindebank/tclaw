package telegramclient

import (
	"encoding/json"

	"tclaw/internal/mcp"
)

const (
	ToolSetup        = "telegram_client_setup"
	ToolAuth         = "telegram_client_auth"
	ToolVerify       = "telegram_client_verify"
	Tool2FA          = "telegram_client_2fa"
	ToolStatus       = "telegram_client_status"
	ToolConfigureBot = "telegram_client_configure_bot"
	ToolCreateGroup  = "telegram_client_create_group"
	ToolListChats    = "telegram_client_list_chats"
	ToolGetHistory   = "telegram_client_get_history"
	ToolSearch       = "telegram_client_search"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolSetup, ToolAuth, ToolVerify, Tool2FA, ToolStatus,
		ToolConfigureBot, ToolCreateGroup, ToolListChats, ToolGetHistory, ToolSearch,
	}
}

var toolDefs = []mcp.ToolDef{
	{
		Name: ToolSetup,
		Description: "Store Telegram Client API credentials (API ID and API hash). " +
			"Register at my.telegram.org → API Development Tools to get these. " +
			"Preferred flow: collect credentials via secret_form_request using keys " +
			"\"telegram_client_api_id\" (integer) and \"telegram_client_api_hash\" (string), " +
			"then call this tool with no arguments — it reads from the secret store automatically. " +
			"Alternatively, pass api_id and api_hash directly (stored encrypted).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"api_id": {
					"type": "integer",
					"description": "Telegram API ID (numeric). Omit if already stored via secret_form_request."
				},
				"api_hash": {
					"type": "string",
					"description": "Telegram API hash (hex string). Omit if already stored via secret_form_request."
				}
			}
		}`),
	},
	{
		Name: ToolAuth,
		Description: "Start Telegram authentication by sending an OTP code to the given phone number. " +
			"After calling this, IMMEDIATELY call secret_form_request with key \"telegram_otp_code\" to collect " +
			"the Telegram OTP via a secure web form — do NOT ask for it in chat. " +
			"Sharing the code directly in Telegram chat triggers a security block on the account.",
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
		Name: ToolVerify,
		Description: "Complete Telegram authentication with the OTP the user received. " +
			"Call with NO arguments — reads the code from the secret store automatically " +
			"(put there by secret_form_request with key \"telegram_otp_code\"). " +
			"If 2FA is enabled, the response will indicate that — call telegram_client_2fa next.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"code": {
					"type": "string",
					"description": "Omit to read from secret store (preferred). Only pass directly if secret_form was not used."
				}
			}
		}`),
	},
	{
		Name: Tool2FA,
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
		Name: ToolStatus,
		Description: "Check Telegram Client API authentication status. " +
			"Returns whether credentials are stored and whether the client is connected.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	},
	{
		Name: ToolConfigureBot,
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
		Name:        ToolCreateGroup,
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
		Name:        ToolListChats,
		Description: "List the authenticated user's Telegram chats and groups. Returns a has_more flag — pass the last chat's ID as offset_id to paginate.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"limit": {
					"type": "integer",
					"description": "Maximum number of chats to return. Defaults to 20.",
					"default": 20
				},
				"offset_id": {
					"type": "integer",
					"description": "Message ID offset for pagination. Pass the last message ID from a previous response to fetch the next page of chats."
				}
			}
		}`),
	},
	{
		Name:        ToolGetHistory,
		Description: "Get message history from a Telegram chat. Returns messages newest-first. Pass the last message's ID as offset_id to fetch older messages.",
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
				},
				"offset_id": {
					"type": "integer",
					"description": "Message ID offset for pagination. Pass the last (oldest) message ID from a previous response to fetch older messages."
				}
			},
			"required": ["chat_id"]
		}`),
	},
	{
		Name:        ToolSearch,
		Description: "Search messages across Telegram chats. Pass the last message's ID as offset_id to paginate through results.",
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
				},
				"limit": {
					"type": "integer",
					"description": "Maximum number of results to return. Defaults to 50.",
					"default": 50
				},
				"offset_id": {
					"type": "integer",
					"description": "Message ID offset for pagination. Pass the last message ID from a previous response to fetch the next page of results."
				}
			},
			"required": ["query"]
		}`),
	},
}
