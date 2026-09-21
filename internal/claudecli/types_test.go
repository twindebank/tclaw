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

	t.Run("accepts the Claude 5 family", func(t *testing.T) {
		require.True(t, ValidModel(ModelFable51))
		require.Equal(t, Model("claude-fable-5-1"), ModelFable51)
		require.True(t, ValidModel(ModelOpus5))
		require.True(t, ValidModel(ModelSonnet5))
	})

	t.Run("fable 5.1 has a short name", func(t *testing.T) {
		require.Equal(t, "fable-5.1", ModelFable51.ShortName())
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
