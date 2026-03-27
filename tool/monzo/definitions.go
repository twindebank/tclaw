package monzo

import (
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/connection"
	"tclaw/mcp"
)

// setCredentialsDef is always registered so the agent can discover Monzo and
// set up the OAuth client credentials at runtime.
var setCredentialsDef = mcp.ToolDef{
	Name: "monzo_set_credentials",
	Description: "Store Monzo API OAuth client credentials. After storing, use connection_add " +
		"with provider 'monzo' to start the OAuth flow.\n\n" +
		"To get credentials: create an API client at developers.monzo.com (personal use only), " +
		"set the redirect URI to your tclaw callback URL (shown in the response). " +
		"Once connected, Monzo tools let you: list accounts, check balances, view pots, and browse transactions.\n\n" +
		"Monzo requires Strong Customer Authentication — after browser auth, the user must also approve access in the Monzo app.",
	InputSchema: json.RawMessage(`{
		"type": "object",
		"properties": {
			"client_id": {
				"type": "string",
				"description": "Monzo OAuth client ID from developers.monzo.com."
			},
			"client_secret": {
				"type": "string",
				"description": "Monzo OAuth client secret from developers.monzo.com."
			}
		},
		"required": ["client_id", "client_secret"]
	}`),
}

// ToolDefs returns the MCP tool definitions for Monzo.
// connIDs lists all active connections — used to build the connection enum.
func ToolDefs(connIDs []connection.ConnectionID) []mcp.ToolDef {
	connEnum := make([]string, len(connIDs))
	for i, id := range connIDs {
		connEnum[i] = fmt.Sprintf("%q", id)
	}
	enumJSON := "[" + strings.Join(connEnum, ", ") + "]"
	connDescription := fmt.Sprintf("Connection ID to use. Available: %s", strings.Join(connEnum, ", "))

	return []mcp.ToolDef{
		{
			Name: "monzo_list_accounts",
			Description: "List Monzo bank accounts. Returns account IDs, types (uk_retail, uk_retail_joint, uk_monzo_flex), " +
				"descriptions, and creation dates. Use the account ID from results in other Monzo tools.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"account_type": {
						"type": "string",
						"description": "Filter by account type. Omit to list all accounts.",
						"enum": ["uk_retail", "uk_retail_joint", "uk_monzo_flex"]
					}
				},
				"required": ["connection"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: "monzo_get_balance",
			Description: "Get the balance for a Monzo account. Returns balance (current), total_balance (including pots), " +
				"currency, and spend_today. All amounts are in minor units (pence for GBP).",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"account_id": {
						"type": "string",
						"description": "The account ID from monzo_list_accounts."
					}
				},
				"required": ["connection", "account_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: "monzo_list_pots",
			Description: "List Monzo pots (savings goals) for an account. Returns pot IDs, names, balances, " +
				"currency, and whether each pot is deleted or locked. Amounts in minor units (pence for GBP).",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"account_id": {
						"type": "string",
						"description": "The account ID from monzo_list_accounts."
					}
				},
				"required": ["connection", "account_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: "monzo_list_transactions",
			Description: "List recent transactions for a Monzo account. Returns transaction IDs, amounts, descriptions, " +
				"merchant info, categories, and timestamps. Amounts in minor units (pence for GBP — negative = debit, positive = credit). " +
				"Note: after 5 minutes post-authentication, only the last 90 days of transactions are accessible. " +
				"Max 100 transactions per request.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"account_id": {
						"type": "string",
						"description": "The account ID from monzo_list_accounts."
					},
					"since": {
						"type": "string",
						"description": "Only return transactions after this time. RFC3339 format (e.g. '2025-01-01T00:00:00Z') or a transaction ID to paginate from."
					},
					"before": {
						"type": "string",
						"description": "Only return transactions before this time. RFC3339 format."
					},
					"limit": {
						"type": "integer",
						"description": "Number of transactions to return. Default 25, max 100."
					}
				},
				"required": ["connection", "account_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: "monzo_get_transaction",
			Description: "Get details of a single Monzo transaction, including expanded merchant information " +
				"(name, address, logo, category, online status). Amounts in minor units (pence for GBP).",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"connection": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"transaction_id": {
						"type": "string",
						"description": "The transaction ID from monzo_list_transactions."
					}
				},
				"required": ["connection", "transaction_id"]
			}`, connDescription, enumJSON)),
		},
	}
}
