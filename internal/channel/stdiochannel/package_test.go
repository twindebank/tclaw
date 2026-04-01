package stdiochannel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"tclaw/internal/channel"
	"tclaw/internal/channel/channelpkg"
	"tclaw/internal/channel/stdiochannel"
	"tclaw/internal/config"
)

func TestPackage_Type(t *testing.T) {
	pkg := stdiochannel.Package{}
	require.Equal(t, channel.TypeStdio, pkg.Type())
}

func TestPackage_Build(t *testing.T) {
	t.Run("local env succeeds", func(t *testing.T) {
		pkg := stdiochannel.Package{}
		ch, err := pkg.Build(context.Background(), channelpkg.BuildParams{
			Env: config.EnvLocal,
		})
		require.NoError(t, err)
		require.Equal(t, channel.TypeStdio, ch.Info().Type)
	})

	t.Run("non-local env returns error", func(t *testing.T) {
		pkg := stdiochannel.Package{}
		_, err := pkg.Build(context.Background(), channelpkg.BuildParams{
			Env: config.EnvProd,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "not allowed")
	})
}

func TestPackage_Provisioner(t *testing.T) {
	pkg := stdiochannel.Package{}
	require.Nil(t, pkg.Provisioner())
}
