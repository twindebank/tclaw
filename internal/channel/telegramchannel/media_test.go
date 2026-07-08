package telegramchannel

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
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "large", att.FileID)
		require.Equal(t, ".jpg", att.Ext)
		require.Equal(t, "photo", att.Prefix)
	})

	t.Run("voice message", func(t *testing.T) {
		msg := &models.Message{
			Voice: &models.Voice{FileID: "voice123"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "voice123", att.FileID)
		require.Equal(t, ".ogg", att.Ext)
		require.Equal(t, "voice", att.Prefix)
	})

	t.Run("audio with filename", func(t *testing.T) {
		msg := &models.Message{
			Audio: &models.Audio{FileID: "audio456", FileName: "song.m4a"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "audio456", att.FileID)
		require.Equal(t, ".m4a", att.Ext)
	})

	t.Run("audio without filename defaults to mp3", func(t *testing.T) {
		msg := &models.Message{
			Audio: &models.Audio{FileID: "audio789"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "audio789", att.FileID)
		require.Equal(t, ".mp3", att.Ext)
	})

	t.Run("document keeps its filename extension", func(t *testing.T) {
		msg := &models.Message{
			Document: &models.Document{FileID: "doc1", FileName: "report.pdf"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "doc1", att.FileID)
		require.Equal(t, ".pdf", att.Ext)
		require.Equal(t, "document", att.Prefix)
	})

	t.Run("document without filename falls back to .bin", func(t *testing.T) {
		msg := &models.Message{
			Document: &models.Document{FileID: "doc2"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, ".bin", att.Ext)
	})

	t.Run("video with filename", func(t *testing.T) {
		msg := &models.Message{
			Video: &models.Video{FileID: "vid1", FileName: "clip.mov"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "vid1", att.FileID)
		require.Equal(t, ".mov", att.Ext)
		require.Equal(t, "video", att.Prefix)
	})

	t.Run("video without filename defaults to mp4", func(t *testing.T) {
		msg := &models.Message{
			Video: &models.Video{FileID: "vid2"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, ".mp4", att.Ext)
	})

	t.Run("video note is always mp4", func(t *testing.T) {
		msg := &models.Message{
			VideoNote: &models.VideoNote{FileID: "note1"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "note1", att.FileID)
		require.Equal(t, ".mp4", att.Ext)
		require.Equal(t, "videonote", att.Prefix)
	})

	t.Run("animation defaults to mp4", func(t *testing.T) {
		msg := &models.Message{
			Animation: &models.Animation{FileID: "anim1"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "anim1", att.FileID)
		require.Equal(t, ".mp4", att.Ext)
		require.Equal(t, "animation", att.Prefix)
	})

	t.Run("static sticker is webp", func(t *testing.T) {
		msg := &models.Message{
			Sticker: &models.Sticker{FileID: "st1"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "st1", att.FileID)
		require.Equal(t, ".webp", att.Ext)
		require.Equal(t, "sticker", att.Prefix)
	})

	t.Run("video sticker is webm", func(t *testing.T) {
		msg := &models.Message{
			Sticker: &models.Sticker{FileID: "st2", IsVideo: true},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, ".webm", att.Ext)
	})

	t.Run("animated sticker is tgs", func(t *testing.T) {
		msg := &models.Message{
			Sticker: &models.Sticker{FileID: "st3", IsAnimated: true},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, ".tgs", att.Ext)
	})

	t.Run("no media returns false", func(t *testing.T) {
		msg := &models.Message{Text: "just text"}
		_, ok := mediaFileInfo(msg)
		require.False(t, ok)
	})

	t.Run("photo takes priority over voice", func(t *testing.T) {
		msg := &models.Message{
			Photo: []models.PhotoSize{{FileID: "photo1"}},
			Voice: &models.Voice{FileID: "voice1"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "photo1", att.FileID)
	})

	t.Run("animation takes priority over document for GIFs", func(t *testing.T) {
		// Telegram sets both fields for a GIF; Animation must win so the file
		// keeps its ".mp4" rather than the document fallback.
		msg := &models.Message{
			Animation: &models.Animation{FileID: "anim1"},
			Document:  &models.Document{FileID: "doc1", FileName: "giphy.gif"},
		}
		att, ok := mediaFileInfo(msg)
		require.True(t, ok)
		require.Equal(t, "anim1", att.FileID)
		require.Equal(t, "animation", att.Prefix)
	})
}

func TestMediaFilename(t *testing.T) {
	t.Run("photo filename", func(t *testing.T) {
		name := mediaFilename(mediaAttachment{Prefix: "photo", Ext: ".jpg"}, 42)
		require.Contains(t, name, "photo_")
		require.Contains(t, name, "_42.jpg")
	})

	t.Run("voice filename", func(t *testing.T) {
		name := mediaFilename(mediaAttachment{Prefix: "voice", Ext: ".ogg"}, 99)
		require.Contains(t, name, "voice_")
		require.Contains(t, name, "_99.ogg")
	})

	t.Run("document filename", func(t *testing.T) {
		name := mediaFilename(mediaAttachment{Prefix: "document", Ext: ".pdf"}, 7)
		require.Contains(t, name, "document_")
		require.Contains(t, name, "_7.pdf")
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

	t.Run("pdf uses the Read tool", func(t *testing.T) {
		result := formatMediaMessage("", "media/document_123_42.pdf")
		require.Contains(t, result, "[Attached PDF:")
		require.Contains(t, result, "view it with the Read tool")
	})

	t.Run("video is not steered toward the Read tool", func(t *testing.T) {
		result := formatMediaMessage("", "media/video_123_42.mp4")
		require.Contains(t, result, "[Attached video:")
		require.Contains(t, result, "available on disk")
	})

	t.Run("unknown extension treated as file", func(t *testing.T) {
		result := formatMediaMessage("", "media/document_123_42.xyz")
		require.Contains(t, result, "[Attached file:")
		require.Contains(t, result, "available on disk")
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
