package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateBotNames(t *testing.T) {
	t.Run("generates valid names", func(t *testing.T) {
		username, displayName, err := GenerateBotNames("assistant")
		require.NoError(t, err)

		// Username: tclaw_<8hex>_bot
		require.True(t, strings.HasPrefix(username, "tclaw_"), "username should start with tclaw_: %s", username)
		require.True(t, strings.HasSuffix(username, "_bot"), "username should end with _bot: %s", username)
		require.Len(t, username, len("tclaw_12345678_bot"))

		// Display name: tclaw · <purpose>
		require.Equal(t, "tclaw · assistant", displayName)
	})

	t.Run("rejects purpose exceeding max rune length", func(t *testing.T) {
		longPurpose := strings.Repeat("x", MaxBotPurposeRunes+1)
		_, _, err := GenerateBotNames(longPurpose)
		require.Error(t, err)
		require.Contains(t, err.Error(), "too long")
	})

	t.Run("accepts purpose at exactly max rune length", func(t *testing.T) {
		exactPurpose := strings.Repeat("x", MaxBotPurposeRunes)
		_, _, err := GenerateBotNames(exactPurpose)
		require.NoError(t, err)
	})

	t.Run("generates unique usernames", func(t *testing.T) {
		u1, _, err := GenerateBotNames("test")
		require.NoError(t, err)
		u2, _, err := GenerateBotNames("test")
		require.NoError(t, err)

		// Extremely unlikely to collide with 4 random bytes.
		require.NotEqual(t, u1, u2)
	})
}

func TestTokenRegex(t *testing.T) {
	t.Run("extracts token from BotFather response", func(t *testing.T) {
		response := `Done! Congratulations on your new bot. You will find it at t.me/tclaw_a3f7b21e_bot.

Use this token to access the HTTP API:
7123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw

Keep your token secure and store it safely, it can be used by anyone to control your bot.`

		token := tokenRegex.FindString(response)
		require.Equal(t, "7123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", token)
	})

	t.Run("returns empty for no token", func(t *testing.T) {
		token := tokenRegex.FindString("Sorry, this username is already taken.")
		require.Empty(t, token)
	})
}

func TestContainsError(t *testing.T) {
	t.Run("detects sorry", func(t *testing.T) {
		require.True(t, containsError("Sorry, this username is already taken."))
	})

	t.Run("detects error", func(t *testing.T) {
		require.True(t, containsError("An error occurred while processing your request."))
	})

	t.Run("detects invalid", func(t *testing.T) {
		require.True(t, containsError("Invalid username format."))
	})

	t.Run("detects can't", func(t *testing.T) {
		require.True(t, containsError("I can't find that bot."))
	})

	t.Run("passes normal response", func(t *testing.T) {
		require.False(t, containsError("Done! Congratulations on your new bot."))
	})

	t.Run("passes token response", func(t *testing.T) {
		require.False(t, containsError("Use this token to access the HTTP API:\n7123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"))
	})
}
