package secretform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"sync"
	"time"

	"tclaw/internal/libraries/secret"
	"tclaw/internal/mcp"
)

const (
	ToolRequest = "secret_form_request"
	ToolWait    = "secret_form_wait"

	requestTTL = 10 * time.Minute

	// Limits to prevent abuse via malformed tool calls.
	maxFields     = 20
	maxKeyLen     = 128
	maxLabelLen   = 256
	maxDescLen    = 1024
	maxTitleLen   = 256
	verifyCodeLen = 6
)

// maxWaitPerCall caps how long a single secret_form_wait call blocks. The
// Claude CLI cancels MCP tool calls at ~60s by default; a longer wait gets
// aborted with "wait cancelled" and the user loses their form-fill before
// they could submit. Callers that want to wait longer must loop with the
// same request_id — the tool returns status "still_waiting" when this
// window elapses without a submission.
//
// Not a const so tests can drop it to sub-second via SetMaxWaitPerCall.
var maxWaitPerCall = 45 * time.Second

// ToolNames returns all tool name constants in this package.
func ToolNames() []string {
	return []string{ToolRequest, ToolWait}
}

// keyPattern restricts secret store keys to safe characters: lowercase
// alphanumeric plus underscores. No slashes, dots, or path separators.
var keyPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// reservedKeys cannot be overwritten via the form — they're managed by
// dedicated auth/seeding flows and overwriting them could lock the user out.
var reservedKeys = map[string]bool{
	"anthropic_api_key":  true,
	"claude_setup_token": true,
}

// CredentialTarget addresses a field of a credential slot declared in config.
// Using this instead of Key is how an operator credential — a GitHub PAT, say —
// gets filled from a phone: bare keys are validated against a pattern with no
// slash in it, so they can never reach the cred/ namespace.
type CredentialTarget struct {
	// Type is the slot's type (a tool package name, or "git").
	Type string `json:"type"`

	// Label is the slot's label (e.g. "default", "homeassistant").
	Label string `json:"label"`

	// Field is the field within the slot (e.g. "token").
	Field string `json:"field"`
}

// FormField describes a single field to collect from the user.
// Exactly one of Key or Credential must be set.
type FormField struct {
	// Key is the secret store key where the value will be saved. Bare keys
	// hold values a tool package declares via RequiredSecrets, plus the ad-hoc
	// values the agent names for itself.
	Key string `json:"key,omitempty"`

	// Credential targets a declared credential slot instead of a bare key.
	Credential *CredentialTarget `json:"credential,omitempty"`

	// Label is the human-readable label shown on the form.
	Label string `json:"label"`

	// Description is optional help text shown below the field.
	Description string `json:"description,omitempty"`

	// Secret controls whether the field is rendered as a password input.
	// Defaults to true when nil.
	Secret *bool `json:"secret,omitempty"`

	// Required controls whether the field must be filled before submission.
	// Defaults to true when nil.
	Required *bool `json:"required,omitempty"`

	// StoreKey is the resolved destination in the secret store, filled in when
	// the request is created. Not part of the tool's input schema.
	StoreKey string `json:"-"`

	// InputName is the HTML form input name. Derived rather than reusing
	// StoreKey because slot keys contain slashes.
	InputName string `json:"-"`

	// AlreadySet marks a destination that already holds a value, so the form
	// can warn that submitting replaces it rather than doing so silently.
	AlreadySet bool `json:"-"`
}

// IsSecret returns whether this field should be masked. Defaults to true.
func (f FormField) IsSecret() bool {
	if f.Secret == nil {
		return true
	}
	return *f.Secret
}

// IsRequired returns whether this field must be filled. Defaults to true.
func (f FormField) IsRequired() bool {
	if f.Required == nil {
		return true
	}
	return *f.Required
}

// PendingRequest tracks an in-progress form request.
type PendingRequest struct {
	ID          string
	Title       string
	Description string
	Fields      []FormField
	CreatedAt   time.Time

	// VerifyCode is a short numeric code the user must enter on the form to
	// prove they're the same person who received the URL in chat.
	VerifyCode string

	// Done is closed when the user submits the form.
	Done chan struct{}
}

// ResolveSlotField maps a declared credential slot field to the store key it is
// written to, erroring when the slot is not declared. Nil disables credential
// targets — a form may then only write bare keys.
type ResolveSlotField func(ctx context.Context, target CredentialTarget) (string, error)

// Deps holds the dependencies for secret form tools.
type Deps struct {
	SecretStore secret.Store
	BaseURL     string // externally-reachable base URL (e.g. "https://your-app.fly.dev")

	// RegisterHandler adds HTTP routes to the callback server.
	RegisterHandler func(pattern string, handler http.Handler)

	// ResolveSlotField resolves credential targets. Nil when the credential
	// system isn't wired.
	ResolveSlotField ResolveSlotField
}

// RegisterTools adds the secret form tools to the MCP handler and registers
// the HTTP endpoint for serving forms.
//
// TODO: persist pending forms to disk so they survive tclaw restarts.
// Currently a restart between secret_form_request and submission drops the
// form — the URL 404s and the user has to redo the flow. See TODO.md
// "Persist pending secret forms across restarts" for the design.
func RegisterTools(handler *mcp.Handler, deps Deps) {
	pending := &sync.Map{}

	if deps.RegisterHandler != nil {
		deps.RegisterHandler("/secret-form/", newFormHTTPHandler(deps.SecretStore, pending))
	}

	handler.Register(secretFormRequestDef(), secretFormRequestHandler(deps, pending))
	handler.Register(secretFormWaitDef(), secretFormWaitHandler(pending))
}

func generateRequestID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate request ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// generateVerifyCode produces a cryptographically random 6-digit numeric code.
func generateVerifyCode() (string, error) {
	// 6 digits: 000000–999999.
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate verify code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// validateKey checks that a secret store key is safe and not reserved.
func validateKey(key string, idx int) error {
	if key == "" {
		return fmt.Errorf("field %d: key is required", idx)
	}
	if len(key) > maxKeyLen {
		return fmt.Errorf("field %d: key exceeds %d characters", idx, maxKeyLen)
	}
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("field %d: key %q contains invalid characters (only lowercase alphanumeric and underscores allowed)", idx, key)
	}
	if reservedKeys[key] {
		return fmt.Errorf("field %d: key %q is reserved and cannot be set via form", idx, key)
	}
	return nil
}

// resolveTarget determines where a field's value will be stored and whether
// something is already there. Exactly one of Key or Credential must be set:
// a bare key addresses the flat namespace, a credential target addresses a
// declared slot.
func resolveTarget(ctx context.Context, deps Deps, field FormField, idx int) (FormField, error) {
	switch {
	case field.Key != "" && field.Credential != nil:
		return FormField{}, fmt.Errorf("field %d: set only one of key or credential", idx)

	case field.Credential != nil:
		if deps.ResolveSlotField == nil {
			return FormField{}, fmt.Errorf("field %d: credential targets are unavailable — no credential system is wired", idx)
		}
		target := *field.Credential
		if target.Type == "" || target.Label == "" || target.Field == "" {
			return FormField{}, fmt.Errorf("field %d: credential needs type, label and field", idx)
		}
		storeKey, err := deps.ResolveSlotField(ctx, target)
		if err != nil {
			return FormField{}, fmt.Errorf("field %d: %w", idx, err)
		}
		field.StoreKey = storeKey
		field.InputName = fmt.Sprintf("cred_%s_%s_%s", target.Type, target.Label, target.Field)

	default:
		if err := validateKey(field.Key, idx); err != nil {
			return FormField{}, err
		}
		field.StoreKey = field.Key
		field.InputName = field.Key
	}

	// A form that silently replaces a working credential is the worst outcome
	// here, so surface it on the page instead.
	existing, err := deps.SecretStore.Get(ctx, field.StoreKey)
	if err != nil {
		return FormField{}, fmt.Errorf("field %d: check existing value: %w", idx, err)
	}
	field.AlreadySet = existing != ""

	return field, nil
}
