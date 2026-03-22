package secretform

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"sync"
	"time"

	"tclaw/libraries/secret"
	"tclaw/mcp"
)

const (
	requestTTL = 10 * time.Minute

	// Limits to prevent abuse via malformed tool calls.
	maxFields     = 20
	maxKeyLen     = 128
	maxLabelLen   = 256
	maxDescLen    = 1024
	maxTitleLen   = 256
	verifyCodeLen = 6
)

// keyPattern restricts secret store keys to safe characters: lowercase
// alphanumeric plus underscores. No slashes, dots, or path separators.
var keyPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// reservedKeys cannot be overwritten via the form — they're managed by
// dedicated auth/seeding flows and overwriting them could lock the user out.
var reservedKeys = map[string]bool{
	"anthropic_api_key":  true,
	"claude_setup_token": true,
}

// FormField describes a single field to collect from the user.
type FormField struct {
	// Key is the secret store key where the value will be saved.
	Key string `json:"key"`

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

// Deps holds the dependencies for secret form tools.
type Deps struct {
	SecretStore secret.Store
	BaseURL     string // externally-reachable base URL (e.g. "https://your-app.fly.dev")

	// RegisterHandler adds HTTP routes to the callback server.
	RegisterHandler func(pattern string, handler http.Handler)
}

// RegisterTools adds the secret form tools to the MCP handler and registers
// the HTTP endpoint for serving forms.
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
