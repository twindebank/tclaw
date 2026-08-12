// Package credentialerror provides a standard way for MCP tools to signal that
// credentials are missing. The error text uses a well-known format that the
// agent's system prompt teaches it to detect and handle via secret_form_request.
//
// Usage in a tool handler:
//
//	if apiKey == "" {
//	    return nil, credentialerror.New(
//	        "Resy Configuration",
//	        "An API key is needed",
//	        credentialerror.Field{Key: "resy_api_key", Label: "Resy API key"},
//	    )
//	}
//
// For a credential held in a declared slot — anything under cred/ — use
// SlotField instead of Key, since a form cannot address a slot by bare key:
//
//	credentialerror.SlotField("git", "default", "token", "GitHub PAT")
package credentialerror

import (
	"encoding/json"
	"fmt"
)

// Field describes a single credential the user needs to provide.
// These map directly to secret_form_request field definitions: either a bare
// store key, or a credential slot target.
type Field struct {
	Key         string           `json:"key,omitempty"`
	Credential  *CredentialField `json:"credential,omitempty"`
	Label       string           `json:"label"`
	Description string           `json:"description,omitempty"`
}

// CredentialField addresses a field of a declared credential slot, mirroring
// the credential target a secret form accepts.
type CredentialField struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	Field string `json:"field"`
}

// SlotField builds a Field targeting a declared credential slot.
func SlotField(slotType, slotLabel, field, label string) Field {
	return Field{
		Credential: &CredentialField{Type: slotType, Label: slotLabel, Field: field},
		Label:      label,
	}
}

// New returns an error with the CREDENTIALS_NEEDED marker. The agent's system
// prompt instructs it to detect this marker and automatically invoke
// secret_form_request with the provided title, description, and fields.
func New(title string, description string, fields ...Field) error {
	fieldsJSON, _ := json.Marshal(fields)
	return fmt.Errorf("CREDENTIALS_NEEDED\ntitle: %s\ndescription: %s\nfields: %s",
		title, description, string(fieldsJSON))
}
