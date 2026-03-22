package channel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/require"
)

func TestMediaFileInfo(t *testing.T) {
	t.Run("photo picks largest size", func(t *testing.T) {
		msg := &models.Message{
			Photo: []models.PhotoSize{
				{FileID: "small", Width: 90, Height: 90},
				{FileID: "medium", Width: 320, Height: 320},
				{FileID: "large", Width: 800, Height: 800},
			},
		}
		fileID, ext := mediaFileInfo(msg)
		require.Equal(t, "large", fileID)
		require.Equal(t, ".jpg", ext)
	})

	t.Run("voice message", func(t *testing.T) {
		msg := &models.Message{
			Voice: &models.Voice{FileID: "voice123"},
		}
		fileID, ext := mediaFileInfo(msg)
		require.Equal(t, "voice123", fileID)
		require.Equal(t, ".ogg", ext)
	})

	t.Run("audio with filename", func(t *testing.T) {
		msg := &models.Message{
			Audio: &models.Audio{FileID: "audio456", FileName: "song.m4a"},
		}
		fileID, ext := mediaFileInfo(msg)
		require.Equal(t, "audio456", fileID)
		require.Equal(t, ".m4a", ext)
	})

	t.Run("audio without filename defaults to mp3", func(t *testing.T) {
		msg := &models.Message{
			Audio: &models.Audio{FileID: "audio789"},
		}
		fileID, ext := mediaFileInfo(msg)
		require.Equal(t, "audio789", fileID)
		require.Equal(t, ".mp3", ext)
	})

	t.Run("no media returns empty", func(t *testing.T) {
		msg := &models.Message{Text: "just text"}
		fileID, ext := mediaFileInfo(msg)
		require.Empty(t, fileID)
		require.Empty(t, ext)
	})

	t.Run("photo takes priority over voice", func(t *testing.T) {
		msg := &models.Message{
			Photo: []models.PhotoSize{{FileID: "photo1"}},
			Voice: &models.Voice{FileID: "voice1"},
		}
		fileID, _ := mediaFileInfo(msg)
		require.Equal(t, "photo1", fileID)
	})
}

func TestMediaFilename(t *testing.T) {
	t.Run("photo filename", func(t *testing.T) {
		msg := &models.Message{
			ID:    42,
			Photo: []models.PhotoSize{{FileID: "x"}},
		}
		name := mediaFilename(msg, ".jpg")
		require.Contains(t, name, "photo_")
		require.Contains(t, name, "_42.jpg")
	})

	t.Run("voice filename", func(t *testing.T) {
		msg := &models.Message{
			ID:    99,
			Voice: &models.Voice{FileID: "x"},
		}
		name := mediaFilename(msg, ".ogg")
		require.Contains(t, name, "voice_")
		require.Contains(t, name, "_99.ogg")
	})

	t.Run("audio filename", func(t *testing.T) {
		msg := &models.Message{
			ID:    7,
			Audio: &models.Audio{FileID: "x"},
		}
		name := mediaFilename(msg, ".mp3")
		require.Contains(t, name, "audio_")
		require.Contains(t, name, "_7.mp3")
	})
}

func TestFormatMediaMessage(t *testing.T) {
	t.Run("image with caption", func(t *testing.T) {
		result := formatMediaMessage("What is this?", "media/photo_123_42.jpg")
		require.Equal(t, "[Attached image: media/photo_123_42.jpg — view it with the Read tool]\nWhat is this?", result)
	})

	t.Run("image without caption", func(t *testing.T) {
		result := formatMediaMessage("", "media/photo_123_42.jpg")
		require.Equal(t, "[Attached image: media/photo_123_42.jpg — view it with the Read tool]", result)
	})

	t.Run("audio file", func(t *testing.T) {
		result := formatMediaMessage("", "media/voice_123_42.ogg")
		require.Equal(t, "[Attached audio: media/voice_123_42.ogg — view it with the Read tool]", result)
	})

	t.Run("unknown extension treated as file", func(t *testing.T) {
		result := formatMediaMessage("", "media/doc_123_42.pdf")
		require.Contains(t, result, "[Attached file:")
	})
}

func TestCleanupOldMedia(t *testing.T) {
	dir := t.TempDir()

	// Create an "old" file by writing and then backdating its mod time.
	oldFile := filepath.Join(dir, "photo_old.jpg")
	require.NoError(t, os.WriteFile(oldFile, []byte("old"), 0o644))
	oldTime := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(oldFile, oldTime, oldTime))

	// Create a "new" file that should be kept.
	newFile := filepath.Join(dir, "photo_new.jpg")
	require.NoError(t, os.WriteFile(newFile, []byte("new"), 0o644))

	cleanupOldMedia(dir)

	_, err := os.Stat(oldFile)
	require.True(t, os.IsNotExist(err), "old file should have been deleted")

	_, err = os.Stat(newFile)
	require.NoError(t, err, "new file should still exist")
}
