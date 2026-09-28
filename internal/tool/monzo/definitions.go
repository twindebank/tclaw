package monzo

import (
	"encoding/json"
	"fmt"
	"strings"

	"tclaw/internal/credential"
	"tclaw/internal/mcp"
)

const (
	ToolWhoAmI               = "monzo_whoami"
	ToolListAccounts         = "monzo_list_accounts"
	ToolGetBalance           = "monzo_get_balance"
	ToolListPots             = "monzo_list_pots"
	ToolDepositIntoPot       = "monzo_deposit_into_pot"
	ToolWithdrawFromPot      = "monzo_withdraw_from_pot"
	ToolListTransactions     = "monzo_list_transactions"
	ToolGetTransaction       = "monzo_get_transaction"
	ToolAnnotateTransaction  = "monzo_annotate_transaction"
	ToolCreateFeedItem       = "monzo_create_feed_item"
	ToolRegisterAttachment   = "monzo_register_attachment"
	ToolDeregisterAttachment = "monzo_deregister_attachment"
	ToolGetReceipt           = "monzo_get_receipt"
	ToolSetReceipt           = "monzo_set_receipt"
	ToolDeleteReceipt        = "monzo_delete_receipt"
	ToolListWebhooks         = "monzo_list_webhooks"
	ToolRegisterWebhook      = "monzo_register_webhook"
	ToolDeleteWebhook        = "monzo_delete_webhook"
)

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{
		ToolWhoAmI, ToolListAccounts, ToolGetBalance,
		ToolListPots, ToolDepositIntoPot, ToolWithdrawFromPot,
		ToolListTransactions, ToolGetTransaction, ToolAnnotateTransaction,
		ToolCreateFeedItem, ToolRegisterAttachment, ToolDeregisterAttachment,
		ToolGetReceipt, ToolSetReceipt, ToolDeleteReceipt,
		ToolListWebhooks, ToolRegisterWebhook, ToolDeleteWebhook,
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
			Name: ToolWhoAmI,
			Description: "Check that the Monzo connection works. Returns whether the access token is valid, " +
				"plus the client and user IDs it belongs to.",
			InputSchema: connSchema(connDescription, enumJSON, ""),
		},
		{
			Name: ToolListAccounts,
			Description: "List Monzo bank accounts. Returns account IDs, types (uk_retail, uk_retail_joint, uk_monzo_flex), " +
				"descriptions, and creation dates. " +
				"Always call this first before using any other Monzo tool — the response contains both the credential_set ID " +
				"and account IDs required by all other Monzo calls.",
			InputSchema: connSchema(connDescription, enumJSON, fmt.Sprintf(`
				"account_type": {
					"type": "string",
					"description": "Filter by account type. Omit to list all accounts.",
					"enum": [%q, %q, %q]
				}`, accountTypeRetail, accountTypeRetailJoint, accountTypeFlex)),
		},
		{
			Name: ToolGetBalance,
			Description: "Get the balance for a Monzo account. Returns balance (current), total_balance (including pots), " +
				"currency, and spend_today. All amounts are in minor units (pence for GBP).",
			InputSchema: connSchema(connDescription, enumJSON, `
				"account_id": {"type": "string", "description": "The account ID from monzo_list_accounts."}`,
				"account_id"),
		},
		{
			Name: ToolListPots,
			Description: "List Monzo pots (savings goals) for an account. Returns pot IDs, names, balances, " +
				"currency, and whether each pot is deleted or locked. Amounts in minor units (pence for GBP). " +
				"Only the given account's pots are returned: pass the joint account's ID to see joint pots.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"account_id": {"type": "string", "description": "The account ID from monzo_list_accounts."}`,
				"account_id"),
		},
		{
			Name: ToolListTransactions,
			Description: "List recent transactions for a Monzo account. Returns transaction IDs, amounts, descriptions, " +
				"merchant info, categories, and timestamps. Amounts in minor units (pence for GBP — negative = debit, positive = credit). " +
				"IMPORTANT: only the last 90 days of transactions are accessible — requests beyond 90 days will fail with an SCA error requiring in-app verification. " +
				"Always keep `since` within the last 90 days. Default window is 30 days. " +
				"Max 100 transactions per request.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"account_id": {"type": "string", "description": "The account ID from monzo_list_accounts."},
				"since": {
					"type": "string",
					"description": "Only return transactions after this time. RFC3339 format (e.g. '2025-01-01T00:00:00Z') or a transaction ID to paginate from. Defaults to 30 days ago. Maximum 90 days — older requests require in-app SCA verification."
				},
				"before": {"type": "string", "description": "Only return transactions before this time. RFC3339 format."},
				"limit": {"type": "integer", "description": "Number of transactions to return. Default 25, max 100."}`,
				"account_id"),
		},
		{
			Name: ToolGetTransaction,
			Description: "Get details of a single Monzo transaction, including expanded merchant information " +
				"(name, address, logo, category, online status). Amounts in minor units (pence for GBP).",
			InputSchema: connSchema(connDescription, enumJSON, `
				"transaction_id": {"type": "string", "description": "The transaction ID from monzo_list_transactions."}`,
				"transaction_id"),
		},
		{
			Name: ToolDepositIntoPot,
			Description: "Move money from a Monzo account into one of its pots. This moves real money: only call it when the user " +
				"has asked for this exact transfer. The account must be the pot's own account (the joint account for a joint pot). " +
				"Returns the updated pot.",
			InputSchema: potTransferSchema(connDescription, enumJSON, "The account the money comes from. Must be the pot's own account."),
		},
		{
			Name: ToolWithdrawFromPot,
			Description: "Move money out of a Monzo pot back into its account. This moves real money: only call it when the user " +
				"has asked for this exact transfer. Fails for pots with added security turned on; those can only be " +
				"withdrawn from in the Monzo app. Returns the updated pot.",
			InputSchema: potTransferSchema(connDescription, enumJSON, "The account the money goes to. Must be the pot's own account."),
		},
		{
			Name: ToolAnnotateTransaction,
			Description: "Set or clear metadata on a Monzo transaction. The \"notes\" key sets the note shown on the " +
				"transaction in the Monzo app; other keys are private to this app. An empty value deletes a key.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"transaction_id": {"type": "string", "description": "The transaction ID from monzo_list_transactions."},
				"metadata": {
					"type": "object",
					"description": "Keys to set. Use \"notes\" for the visible note. An empty string deletes the key.",
					"additionalProperties": {"type": "string"}
				}`, "transaction_id", "metadata"),
		},
		{
			Name: ToolCreateFeedItem,
			Description: "Post an item into the Monzo app's feed for an account, such as a reminder or a summary. " +
				"Tapping it opens url, if given. Colours are hex values like #FF0000.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"account_id": {"type": "string", "description": "The account ID from monzo_list_accounts."},
				"title": {"type": "string", "description": "The title to show."},
				"image_url": {"type": "string", "description": "URL of the image to show next to the item."},
				"body": {"type": "string", "description": "Optional body text."},
				"url": {"type": "string", "description": "Optional URL to open when the item is tapped."},
				"background_color": {"type": "string", "description": "Optional hex background colour."},
				"title_color": {"type": "string", "description": "Optional hex title colour."},
				"body_color": {"type": "string", "description": "Optional hex body colour."}`,
				"account_id", "title", "image_url"),
		},
		{
			Name:        ToolRegisterAttachment,
			Description: "Attach an image (such as a photo of a receipt) to a Monzo transaction, from a public URL.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"transaction_id": {"type": "string", "description": "The transaction ID from monzo_list_transactions."},
				"file_url": {"type": "string", "description": "Public URL of the image."},
				"file_type": {"type": "string", "description": "MIME type of the image, e.g. image/png."}`,
				"transaction_id", "file_url", "file_type"),
		},
		{
			Name:        ToolDeregisterAttachment,
			Description: "Remove an attachment from a Monzo transaction.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"attachment_id": {"type": "string", "description": "The attachment ID, from the transaction's attachments or monzo_register_attachment."}`,
				"attachment_id"),
		},
		{
			Name:        ToolGetReceipt,
			Description: "Get a receipt this app previously attached to a Monzo transaction, by the external ID it was saved with.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"external_id": {"type": "string", "description": "The external_id the receipt was saved with."}`,
				"external_id"),
		},
		{
			Name: ToolSetReceipt,
			Description: "Create or replace an itemised receipt on a Monzo transaction. Saving again with the same " +
				"external_id replaces it. Amounts are in pence and must add up to the transaction amount.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"receipt": {
					"type": "object",
					"description": "The receipt. Required: transaction_id, external_id (your own unique ID), total (pence), currency (e.g. GBP), and items (each with description, amount in pence, currency, and optionally quantity and unit). Optional: taxes, payments, merchant.",
					"required": ["transaction_id", "external_id", "total", "currency", "items"]
				}`, "receipt"),
		},
		{
			Name:        ToolDeleteReceipt,
			Description: "Delete a receipt this app previously attached to a Monzo transaction.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"external_id": {"type": "string", "description": "The external_id the receipt was saved with."}`,
				"external_id"),
		},
		{
			Name:        ToolListWebhooks,
			Description: "List the webhooks registered on a Monzo account.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"account_id": {"type": "string", "description": "The account ID from monzo_list_accounts."}`,
				"account_id"),
		},
		{
			Name: ToolRegisterWebhook,
			Description: "Register a URL that Monzo will POST to each time a transaction is created on the account. " +
				"Only register a URL the user has asked for: every transaction's details will be sent there.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"account_id": {"type": "string", "description": "The account ID from monzo_list_accounts."},
				"url": {"type": "string", "description": "The HTTPS URL Monzo should send transaction events to."}`,
				"account_id", "url"),
		},
		{
			Name:        ToolDeleteWebhook,
			Description: "Delete a webhook from a Monzo account.",
			InputSchema: connSchema(connDescription, enumJSON, `
				"webhook_id": {"type": "string", "description": "The webhook ID from monzo_list_webhooks."}`,
				"webhook_id"),
		},
	}
}

func potTransferSchema(connDescription, enumJSON, accountDescription string) json.RawMessage {
	return connSchema(connDescription, enumJSON, fmt.Sprintf(`
		"pot_id": {"type": "string", "description": "The pot ID from monzo_list_pots."},
		"account_id": {"type": "string", "description": %q},
		"amount": {"type": "integer", "description": "Amount in pence. Must be positive.", "minimum": 1},
		"dedupe_id": {
			"type": "string",
			"description": "A new unique string for this transfer. Reuse the same value only when retrying the same transfer, so it is not made twice."
		}`, accountDescription), "pot_id", "account_id", "amount", "dedupe_id")
}

// connSchema builds a tool's input schema: the credential_set property plus props, a comma-separated list of
// JSON properties. credential_set is always required.
func connSchema(connDescription, enumJSON, props string, required ...string) json.RawMessage {
	quoted := []string{`"credential_set"`}
	for _, r := range required {
		quoted = append(quoted, fmt.Sprintf("%q", r))
	}
	requiredJSON := "[" + strings.Join(quoted, ", ") + "]"
	if props != "" {
		props = "," + props
	}
	return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"credential_set": {"type": "string", "description": %q, "enum": %s}%s
		},
		"required": %s
	}`, connDescription, enumJSON, props, requiredJSON))
}
