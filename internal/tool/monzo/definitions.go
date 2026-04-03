package monzo

import (
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
)

const (
	ToolListAccounts     = "monzo_list_accounts"
	ToolGetBalance       = "monzo_get_balance"
	ToolListPots         = "monzo_list_pots"
	ToolListTransactions = "monzo_list_transactions"
	ToolGetTransaction   = "monzo_get_transaction"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolListAccounts, ToolGetBalance,
		ToolListPots, ToolListTransactions, ToolGetTransaction,
	}
}

// ToolDefs returns the MCP tool definitions for Monzo.
// connIDs lists all active connections — used to build the connection enum.
func ToolDefs(connIDs []credential.CredentialSetID) []mcp.ToolDef {
	connEnum := make([]string, len(connIDs))
	for i, id := range connIDs {
		connEnum[i] = fmt.Sprintf("%q", id)
	}
	enumJSON := "[" + strings.Join(connEnum, ", ") + "]"
	// All Monzo tools require a credential_set ID. The agent must call monzo_list_accounts first
	// to discover valid IDs — guessing will produce "unknown credential set" errors.
	connDescription := fmt.Sprintf("Connection ID to use. Must be a valid ID from monzo_list_accounts — do not guess. Available: %s", strings.Join(connEnum, ", "))

	return []mcp.ToolDef{
		{
			Name: ToolListAccounts,
			Description: "List Monzo bank accounts. Returns account IDs, types (uk_retail, uk_retail_joint, uk_monzo_flex), " +
				"descriptions, and creation dates. " +
				"Always call this first before using any other Monzo tool — the response contains both the credential_set ID " +
				"and account IDs required by all other Monzo calls.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
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
				"required": ["credential_set"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolGetBalance,
			Description: "Get the balance for a Monzo account. Returns balance (current), total_balance (including pots), " +
				"currency, and spend_today. All amounts are in minor units (pence for GBP).",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"account_id": {
						"type": "string",
						"description": "The account ID from monzo_list_accounts."
					}
				},
				"required": ["credential_set", "account_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolListPots,
			Description: "List Monzo pots (savings goals) for an account. Returns pot IDs, names, balances, " +
				"currency, and whether each pot is deleted or locked. Amounts in minor units (pence for GBP).",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"account_id": {
						"type": "string",
						"description": "The account ID from monzo_list_accounts."
					}
				},
				"required": ["credential_set", "account_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolListTransactions,
			Description: "List recent transactions for a Monzo account. Returns transaction IDs, amounts, descriptions, " +
				"merchant info, categories, and timestamps. Amounts in minor units (pence for GBP — negative = debit, positive = credit). " +
				"IMPORTANT: only the last 90 days of transactions are accessible — requests beyond 90 days will fail with an SCA error requiring in-app verification. " +
				"Always keep `since` within the last 90 days. Default window is 30 days. " +
				"Max 100 transactions per request.",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
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
						"description": "Only return transactions after this time. RFC3339 format (e.g. '2025-01-01T00:00:00Z') or a transaction ID to paginate from. Defaults to 30 days ago. Maximum 90 days — older requests require in-app SCA verification."
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
				"required": ["credential_set", "account_id"]
			}`, connDescription, enumJSON)),
		},
		{
			Name: ToolGetTransaction,
			Description: "Get details of a single Monzo transaction, including expanded merchant information " +
				"(name, address, logo, category, online status). Amounts in minor units (pence for GBP).",
			InputSchema: json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"credential_set": {
						"type": "string",
						"description": %q,
						"enum": %s
					},
					"transaction_id": {
						"type": "string",
						"description": "The transaction ID from monzo_list_transactions."
					}
				},
				"required": ["credential_set", "transaction_id"]
			}`, connDescription, enumJSON)),
		},
	}
}
