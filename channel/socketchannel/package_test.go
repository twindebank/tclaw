package socketchannel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/channel"
	"tclaw/channel/channelpkg"
	"tclaw/channel/socketchannel"
	"tclaw/config"
)

func TestPackage_Type(t *testing.T) {
	pkg := socketchannel.Package{}
	require.Equal(t, channel.TypeSocket, pkg.Type())
}

func TestPackage_Build(t *testing.T) {
	t.Run("local env succeeds", func(t *testing.T) {
		pkg := socketchannel.Package{}
		ch, err := pkg.Build(context.Background(), channelpkg.BuildParams{
			ChannelCfg: config.Channel{Name: "test", Description: "test socket"},
			Env:        config.EnvLocal,
			BaseDir:    t.TempDir(),
			UserID:     "testuser",
		})
		require.NoError(t, err)
		require.Equal(t, channel.TypeSocket, ch.Info().Type)
		require.Equal(t, "test", ch.Info().Name)
	})

	t.Run("non-local env returns error", func(t *testing.T) {
		pkg := socketchannel.Package{}
		_, err := pkg.Build(context.Background(), channelpkg.BuildParams{
			ChannelCfg: config.Channel{Name: "test"},
			Env:        config.EnvProd,
			BaseDir:    t.TempDir(),
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not allowed")
	})
}

func TestPackage_Provisioner(t *testing.T) {
	pkg := socketchannel.Package{}
	require.Nil(t, pkg.Provisioner())
}
