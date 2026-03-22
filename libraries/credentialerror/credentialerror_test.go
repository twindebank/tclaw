package credentialerror_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/libraries/credentialerror"
)

func TestNew(t *testing.T) {
	t.Run("contains marker and fields", func(t *testing.T) {
		err := credentialerror.New(
			"Resy Configuration",
			"Get credentials from resy.com",
			credentialerror.Field{Key: "resy_api_key", Label: "API Key", Description: "Authorization header"},
			credentialerror.Field{Key: "resy_auth_token", Label: "Auth Token"},
		)

		msg := err.Error()
		require.True(t, strings.HasPrefix(msg, "CREDENTIALS_NEEDED"))
		require.Contains(t, msg, "title: Resy Configuration")
		require.Contains(t, msg, "description: Get credentials from resy.com")

		// Extract and validate the fields JSON.
		idx := strings.Index(msg, "fields: ")
		require.Greater(t, idx, 0)
		fieldsJSON := msg[idx+len("fields: "):]

		var fields []credentialerror.Field
		require.NoError(t, json.Unmarshal([]byte(fieldsJSON), &fields))
		require.Len(t, fields, 2)
		require.Equal(t, "resy_api_key", fields[0].Key)
		require.Equal(t, "API Key", fields[0].Label)
		require.Equal(t, "Authorization header", fields[0].Description)
		require.Equal(t, "resy_auth_token", fields[1].Key)
	})

	t.Run("single field", func(t *testing.T) {
		err := credentialerror.New(
			"GitHub",
			"PAT required",
			credentialerror.Field{Key: "github_token", Label: "Token"},
		)

		msg := err.Error()
		require.Contains(t, msg, "CREDENTIALS_NEEDED")
		require.Contains(t, msg, `"github_token"`)
	})

	t.Run("error is wrappable", func(t *testing.T) {
		inner := credentialerror.New("Test", "desc", credentialerror.Field{Key: "k", Label: "l"})
		wrapped := fmt.Errorf("context: %w", inner)

		// The CREDENTIALS_NEEDED marker survives wrapping.
		require.Contains(t, wrapped.Error(), "CREDENTIALS_NEEDED")
	})
}
