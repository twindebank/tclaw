package credential

import (
	"context"
	"fmt"
)

// SetField stores a single credential field value.
func (m *Manager) SetField(ctx context.Context, id CredentialSetID, field string, value string) error {
	if err := m.secrets.Set(ctx, fieldKey(id, field), value); err != nil {
		return fmt.Errorf("store field %q for %s: %w", field, id, err)
	}
	return nil
}

// GetField retrieves a single credential field value. Returns empty string if
// the field is not set.
func (m *Manager) GetField(ctx context.Context, id CredentialSetID, field string) (string, error) {
	val, err := m.secrets.Get(ctx, fieldKey(id, field))
	if err != nil {
		return "", fmt.Errorf("read field %q for %s: %w", field, id, err)
	}
	return val, nil
}

// GetAllFields returns all stored fields for a credential set by checking each
// field key in the provided list.
func (m *Manager) GetAllFields(ctx context.Context, id CredentialSetID, fieldKeys []string) (map[string]string, error) {
	result := make(map[string]string, len(fieldKeys))
	for _, key := range fieldKeys {
		val, err := m.GetField(ctx, id, key)
		if err != nil {
			return nil, err
		}
		if val != "" {
			result[key] = val
		}
	}
	return result, nil
}

// IsReady checks whether a credential set has all required fields populated.
// For OAuth sets, it also checks that OAuth tokens are present.
func (m *Manager) IsReady(ctx context.Context, id CredentialSetID, requiredFields []string, needsOAuth bool) (bool, error) {
	for _, field := range requiredFields {
		val, err := m.GetField(ctx, id, field)
		if err != nil {
			return false, err
		}
		if val == "" {
			return false, nil
		}
	}

	if needsOAuth {
		tokens, err := m.GetOAuthTokens(ctx, id)
		if err != nil {
			return false, err
		}
		if tokens == nil || tokens.AccessToken == "" {
			return false, nil
		}
	}

	return true, nil
}
