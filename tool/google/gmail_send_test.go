package google

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildRFC2822Message(t *testing.T) {
	t.Run("simple send", func(t *testing.T) {
		msg := buildRFC2822Message(gmailSendArgs{
			To:      "alice@example.com",
			Subject: "Hello",
			Body:    "Hi Alice!",
		})

		require.Contains(t, msg, "To: alice@example.com\r\n")
		require.Contains(t, msg, "Subject: Hello\r\n")
		require.Contains(t, msg, "MIME-Version: 1.0\r\n")
		require.Contains(t, msg, "Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		require.Contains(t, msg, "\r\n\r\nHi Alice!")

		// Should not contain reply headers.
		require.NotContains(t, msg, "In-Reply-To")
		require.NotContains(t, msg, "References")
		require.NotContains(t, msg, "Cc:")
		require.NotContains(t, msg, "Bcc:")
	})

	t.Run("reply with all fields", func(t *testing.T) {
		msg := buildRFC2822Message(gmailSendArgs{
			To:         "bob@example.com",
			Subject:    "Re: Meeting",
			Body:       "Sounds good!",
			CC:         "carol@example.com",
			BCC:        "dave@example.com",
			InReplyTo:  "<original-id@mail.gmail.com>",
			References: "<original-id@mail.gmail.com>",
		})

		require.Contains(t, msg, "To: bob@example.com\r\n")
		require.Contains(t, msg, "Subject: Re: Meeting\r\n")
		require.Contains(t, msg, "Cc: carol@example.com\r\n")
		require.Contains(t, msg, "Bcc: dave@example.com\r\n")
		require.Contains(t, msg, "In-Reply-To: <original-id@mail.gmail.com>\r\n")
		require.Contains(t, msg, "References: <original-id@mail.gmail.com>\r\n")
		require.Contains(t, msg, "\r\n\r\nSounds good!")
	})

	t.Run("uses CRLF line endings", func(t *testing.T) {
		msg := buildRFC2822Message(gmailSendArgs{
			To:      "test@example.com",
			Subject: "Test",
			Body:    "Body",
		})

		// Every line should end with \r\n, not just \n.
		lines := strings.Split(msg, "\r\n")
		require.Greater(t, len(lines), 3, "should have multiple CRLF-delimited lines")

		// No bare \n should appear (except potentially in the body).
		headerSection := strings.SplitN(msg, "\r\n\r\n", 2)[0]
		require.NotContains(t, strings.ReplaceAll(headerSection, "\r\n", ""), "\n",
			"headers should only use CRLF, not bare LF")
	})

	t.Run("base64url round-trip", func(t *testing.T) {
		msg := buildRFC2822Message(gmailSendArgs{
			To:      "test@example.com",
			Subject: "Round trip",
			Body:    "Hello world!",
		})

		encoded := base64.URLEncoding.EncodeToString([]byte(msg))
		decoded, err := base64.URLEncoding.DecodeString(encoded)
		require.NoError(t, err)
		require.Equal(t, msg, string(decoded))
	})
}
