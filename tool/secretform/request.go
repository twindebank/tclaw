package secretform

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"tclaw/mcp"
)

func secretFormRequestDef() mcp.ToolDef {
	return mcp.ToolDef{
		Name:        "secret_form_request",
		Description: "Create a secure web form for the user to enter sensitive information (API keys, tokens, credentials). Returns a URL and a verification code. Send BOTH to the user — the code must be entered on the form to prove identity. Values are stored securely and never visible to the agent. Call secret_form_wait after sending the URL.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title": {
					"type": "string",
					"description": "Form title shown to the user (e.g. 'GitHub Configuration')"
				},
				"description": {
					"type": "string",
					"description": "Optional explanatory text shown above the form fields"
				},
				"fields": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"key": {
								"type": "string",
								"description": "Secret store key (lowercase alphanumeric + underscores only, e.g. 'github_token')"
							},
							"label": {
								"type": "string",
								"description": "Human-readable label shown on the form (e.g. 'GitHub Personal Access Token')"
							},
							"description": {
								"type": "string",
								"description": "Help text shown below the field"
							},
							"secret": {
								"type": "boolean",
								"description": "If true, the field is rendered as a password input (masked). Defaults to true."
							},
							"required": {
								"type": "boolean",
								"description": "If true, the field must be filled before submission. Defaults to true."
							}
						},
						"required": ["key", "label"]
					},
					"description": "Fields to collect from the user"
				}
			},
			"required": ["title", "fields"]
		}`),
	}
}

type secretFormRequestArgs struct {
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Fields      []FormField `json:"fields"`
}

func secretFormRequestHandler(deps Deps, pending *sync.Map) mcp.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		var a secretFormRequestArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}

		if a.Title == "" {
			return nil, fmt.Errorf("title is required")
		}
		if len(a.Title) > maxTitleLen {
			return nil, fmt.Errorf("title exceeds %d characters", maxTitleLen)
		}
		if len(a.Description) > maxDescLen {
			return nil, fmt.Errorf("description exceeds %d characters", maxDescLen)
		}
		if len(a.Fields) == 0 {
			return nil, fmt.Errorf("at least one field is required")
		}
		if len(a.Fields) > maxFields {
			return nil, fmt.Errorf("too many fields (max %d)", maxFields)
		}
		for i, f := range a.Fields {
			if err := validateKey(f.Key, i); err != nil {
				return nil, err
			}
			if f.Label == "" {
				return nil, fmt.Errorf("field %d: label is required", i)
			}
			if len(f.Label) > maxLabelLen {
				return nil, fmt.Errorf("field %d: label exceeds %d characters", i, maxLabelLen)
			}
			if len(f.Description) > maxDescLen {
				return nil, fmt.Errorf("field %d: description exceeds %d characters", i, maxDescLen)
			}
		}

		if deps.BaseURL == "" {
			return nil, fmt.Errorf("HTTP server not configured — cannot serve forms")
		}

		id, err := generateRequestID()
		if err != nil {
			return nil, err
		}

		verifyCode, err := generateVerifyCode()
		if err != nil {
			return nil, err
		}

		req := &PendingRequest{
			ID:          id,
			Title:       a.Title,
			Description: a.Description,
			Fields:      a.Fields,
			CreatedAt:   time.Now(),
			VerifyCode:  verifyCode,
			Done:        make(chan struct{}),
		}
		pending.Store(id, req)

		result := map[string]string{
			"request_id":  id,
			"url":         deps.BaseURL + "/secret-form/" + id,
			"verify_code": verifyCode,
			"message":     "Send the URL AND the verification code to the user. They must enter the code on the form to submit it. Then call secret_form_wait with the request_id.",
		}
		return json.Marshal(result)
	}
}
