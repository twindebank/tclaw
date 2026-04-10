package gws

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshalMapOrRaw(t *testing.T) {
	t.Run("raw string value passed through", func(t *testing.T) {
		m := map[string]any{"__raw": `{"key": "value"}`}
		got, err := marshalMapOrRaw(m)
		require.NoError(t, err)
		require.Equal(t, `{"key": "value"}`, string(got))
	})

	t.Run("non-string raw value returns error", func(t *testing.T) {
		m := map[string]any{"__raw": 42}
		_, err := marshalMapOrRaw(m)
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be a string")
	})

	t.Run("nil raw value returns error", func(t *testing.T) {
		m := map[string]any{"__raw": nil}
		_, err := marshalMapOrRaw(m)
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be a string")
	})

	t.Run("regular map marshals to JSON", func(t *testing.T) {
		m := map[string]any{"userId": "me", "maxResults": float64(10)}
		got, err := marshalMapOrRaw(m)
		require.NoError(t, err)
		require.Contains(t, string(got), `"userId":"me"`)
	})

	t.Run("map with __raw and other keys marshals normally", func(t *testing.T) {
		// __raw only takes effect when it's the sole key.
		m := map[string]any{"__raw": "ignored", "other": "value"}
		got, err := marshalMapOrRaw(m)
		require.NoError(t, err)
		require.Contains(t, string(got), `"other":"value"`)
	})
}
