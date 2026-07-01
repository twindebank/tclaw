package claudecli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidModel(t *testing.T) {
	t.Run("accepts the latest opus", func(t *testing.T) {
		require.True(t, ValidModel(ModelOpus48))
		require.Equal(t, Model("claude-opus-4-8"), ModelOpus48)
	})

	t.Run("rejects an unknown model", func(t *testing.T) {
		require.False(t, ValidModel("claude-opus-9-9"))
	})

	t.Run("opus 4.8 has a short name", func(t *testing.T) {
		require.Equal(t, "opus-4.8", ModelOpus48.ShortName())
	})
}

func TestValidPermissionMode(t *testing.T) {
	t.Run("accepts auto mode", func(t *testing.T) {
		require.True(t, ValidPermissionMode(PermissionAuto))
		require.Equal(t, PermissionMode("auto"), PermissionAuto)
	})

	t.Run("still accepts existing modes", func(t *testing.T) {
		require.True(t, ValidPermissionMode(PermissionDontAsk))
		require.True(t, ValidPermissionMode(PermissionBypass))
	})

	t.Run("rejects an unknown mode", func(t *testing.T) {
		require.False(t, ValidPermissionMode("yolo"))
	})
}
