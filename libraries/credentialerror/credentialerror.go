// Package credentialerror provides a standard way for MCP tools to signal that
// credentials are missing. The error text uses a well-known format that the
// agent's system prompt teaches it to detect and handle via secret_form_request.
//
// Usage in a tool handler:
//
//	if apiKey == "" {
//	    return nil, credentialerror.New(
//	        "GitHub Configuration",
//	        "A Personal Access Token with repo scope is needed",
//	        credentialerror.Field{Key: "github_token", Label: "GitHub PAT"},
//	    )
//	}
package credentialerror

import (
	"encoding/json"
	"fmt"
)

// Field describes a single credential the user needs to provide.
// These map directly to secret_form_request field definitions.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// New returns an error with the CREDENTIALS_NEEDED marker. The agent's system
// prompt instructs it to detect this marker and automatically invoke
// secret_form_request with the provided title, description, and fields.
func New(title string, description string, fields ...Field) error {
	fieldsJSON, _ := json.Marshal(fields)
	return fmt.Errorf("CREDENTIALS_NEEDED\ntitle: %s\ndescription: %s\nfields: %s",
		title, description, string(fieldsJSON))
}
