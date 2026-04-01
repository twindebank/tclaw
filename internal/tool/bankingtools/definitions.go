package bankingtools

import (
	"encoding/json"

	"tclaw/internal/mcp"
)

const (
	ToolSetCredentials  = "banking_set_credentials"
	ToolListBanks       = "banking_list_banks"
	ToolConnect         = "banking_connect"
	ToolAuthWait        = "banking_auth_wait"
	ToolListAccounts    = "banking_list_accounts"
	ToolGetBalance      = "banking_get_balance"
	ToolGetTransactions = "banking_get_transactions"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolSetCredentials, ToolListBanks, ToolConnect,
		ToolAuthWait, ToolListAccounts, ToolGetBalance,
		ToolGetTransactions,
	}
}

// infoToolDefs are always registered — they let the agent learn about the
// provider and set up credentials.
var infoToolDefs = []mcp.ToolDef{
	{
		Name: ToolSetCredentials,
		Description: "Set up Enable Banking credentials (application ID and RSA private key). " +
			"Call with no parameters to trigger the secure credential collection flow " +
			"(returns CREDENTIALS_NEEDED if not yet stored). Call with application_id and " +
			"private_key_pem to store them directly.\n\n" +
			"Register for free at enablebanking.com: create an application, generate a self-signed " +
			"certificate with OpenSSL, upload the public cert, and provide the app ID and private key PEM.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"application_id": {
					"type": "string",
					"description": "Enable Banking application ID from the control panel."
				},
				"private_key_pem": {
					"type": "string",
					"description": "RSA private key in PEM format (the full -----BEGIN PRIVATE KEY----- block)."
				}
			}
		}`),
	},
}

// operationalToolDefs are only registered when credentials are configured.
var operationalToolDefs = []mcp.ToolDef{
	{
		Name: ToolListBanks,
		Description: "List available banks that support Open Banking connections. " +
			"Returns bank names and ASPSP IDs needed for banking_connect.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"country": {
					"type": "string",
					"description": "ISO 3166-1 alpha-2 country code. Defaults to GB (United Kingdom).",
					"default": "GB"
				}
			}
		}`),
	},
	{
		Name: ToolConnect,
		Description: "Start bank authorization. Returns an authorization URL the user must visit " +
			"to log into their bank. After sending the URL to the user, call banking_auth_wait " +
			"to wait for completion.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"aspsp_name": {
					"type": "string",
					"description": "Bank name exactly as returned by banking_list_banks (e.g. 'Monzo Bank')."
				},
				"aspsp_country": {
					"type": "string",
					"description": "Country code for the bank. Defaults to GB.",
					"default": "GB"
				}
			},
			"required": ["aspsp_name"]
		}`),
	},
	{
		Name: ToolAuthWait,
		Description: "Wait for a pending bank authorization to complete (up to 5 minutes). " +
			"Call this immediately after sending the banking_connect auth URL to the user.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"aspsp_name": {
					"type": "string",
					"description": "Bank name used in banking_connect."
				}
			},
			"required": ["aspsp_name"]
		}`),
	},
	{
		Name: ToolListAccounts,
		Description: "List all connected bank accounts across all banks. Shows account names, " +
			"IBANs, and which bank they belong to. Also flags expired sessions that need re-authorization.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	},
	{
		Name: ToolGetBalance,
		Description: "Get the current balance for a specific bank account. " +
			"Use the account UID from banking_list_accounts.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"account_id": {
					"type": "string",
					"description": "Account UID from banking_list_accounts."
				}
			},
			"required": ["account_id"]
		}`),
	},
	{
		Name: ToolGetTransactions,
		Description: "Get transaction history for a specific bank account. " +
			"Supports date range filtering. Use the account UID from banking_list_accounts.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"account_id": {
					"type": "string",
					"description": "Account UID from banking_list_accounts."
				},
				"date_from": {
					"type": "string",
					"description": "Start date in YYYY-MM-DD format (inclusive). Defaults to 30 days ago."
				},
				"date_to": {
					"type": "string",
					"description": "End date in YYYY-MM-DD format (inclusive). Defaults to today."
				},
				"continuation_key": {
					"type": "string",
					"description": "Pagination key from a previous response to fetch the next page."
				}
			},
			"required": ["account_id"]
		}`),
	},
}
