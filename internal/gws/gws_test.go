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

func TestSplitShellArgs(t *testing.T) {
	t.Run("simple command", func(t *testing.T) {
		require.Equal(t, []string{"gmail", "users", "messages", "list"}, splitShellArgs("gmail users messages list"))
	})

	t.Run("double quoted body preserved", func(t *testing.T) {
		got := splitShellArgs(`gmail +reply --message-id abc --body "Hi Renae, thanks for getting back"`)
		require.Equal(t, []string{"gmail", "+reply", "--message-id", "abc", "--body", "Hi Renae, thanks for getting back"}, got)
	})

	t.Run("single quoted body preserved", func(t *testing.T) {
		got := splitShellArgs(`gmail +reply --body 'Hello world, this is a test'`)
		require.Equal(t, []string{"gmail", "+reply", "--body", "Hello world, this is a test"}, got)
	})

	t.Run("attachment flags", func(t *testing.T) {
		got := splitShellArgs(`gmail +reply --message-id abc --body "hello" --attachment /tmp/file.jpg`)
		require.Equal(t, []string{"gmail", "+reply", "--message-id", "abc", "--body", "hello", "--attachment", "/tmp/file.jpg"}, got)
	})

	t.Run("empty string", func(t *testing.T) {
		require.Empty(t, splitShellArgs(""))
	})

	t.Run("extra whitespace collapsed", func(t *testing.T) {
		require.Equal(t, []string{"a", "b", "c"}, splitShellArgs("  a   b   c  "))
	})
}
